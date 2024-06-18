package services

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
)

var logger_mev = logrus.StandardLogger().WithField("module", "mev_indexer")

type MevIndexer struct {
	updaterRunning      bool
	lastRefresh         time.Time
	mevBlockCacheMutex  sync.Mutex
	mevBlockCache       map[common.Hash]*mevIndexerBlockCache
	mevBlockCacheLoaded bool
	lastLoadedSlot      map[uint8]uint64
}

type mevIndexerBlockCache struct {
	updated bool
	block   *dbtypes.MevBlock
}

func NewMevIndexer() *MevIndexer {
	return &MevIndexer{
		mevBlockCache:  map[common.Hash]*mevIndexerBlockCache{},
		lastLoadedSlot: map[uint8]uint64{},
	}
}

func (mev *MevIndexer) StartUpdater(indexer *indexer.Indexer) {
	if utils.Config.Indexer.DisableIndexWriter || mev.updaterRunning {
		return
	}
	if utils.Config.MevIndexer.RefreshInterval == 0 {
		utils.Config.MevIndexer.RefreshInterval = 10 * time.Minute
	}

	mev.updaterRunning = true
	go mev.runUpdaterLoop(indexer)
}

func (mev *MevIndexer) runUpdaterLoop(indexer *indexer.Indexer) {
	defer utils.HandleSubroutinePanic("MevIndexer.runUpdaterLoop")

	for {
		time.Sleep(15 * time.Second)

		err := mev.runUpdater(indexer)
		if err != nil {
			logger_mev.Errorf("mev indexer update error: %v, retrying in 15 sec...", err)
		}
	}
}

func (mev *MevIndexer) runUpdater(indexer *indexer.Indexer) error {
	if time.Since(mev.lastRefresh) < utils.Config.MevIndexer.RefreshInterval {
		return nil
	}

	validatorSet := indexer.GetCachedValidatorPubkeyMap()
	if validatorSet == nil {
		return nil
	}

	if !mev.mevBlockCacheLoaded {
		// prefill cache
		_, finalizedEpoch, _, _ := indexer.GetCacheState()
		finalizedSlot := (uint64(finalizedEpoch+1) * utils.Config.Chain.Config.SlotsPerEpoch) - 1
		loadedCount := uint64(0)
		for {
			mevBlocks, totalCount, err := db.GetMevBlocksFiltered(0, 1000, &dbtypes.MevBlockFilter{
				MinSlot: finalizedSlot,
			})
			if err != nil {
				return fmt.Errorf("failed prefill mev blocks cache from db: %v", err)
			}

			for _, block := range mevBlocks {
				mev.mevBlockCache[common.Hash(block.BlockHash)] = &mevIndexerBlockCache{
					updated: false,
					block:   block,
				}
			}

			loadedCount += uint64(len(mevBlocks))
			if loadedCount >= totalCount || len(mevBlocks) == 0 {
				break
			}
		}

		for _, relay := range utils.Config.MevIndexer.Relays {
			lastSlot, err := db.GetHighestMevBlockSlotByRelay(relay.Index)
			if err != nil {
				continue
			}
			mev.lastLoadedSlot[relay.Index] = lastSlot
		}

		mev.mevBlockCacheLoaded = true
	}

	// load data from all relays
	wg := &sync.WaitGroup{}
	for idx := range utils.Config.MevIndexer.Relays {
		wg.Add(1)

		go func(idx int, relay *types.MevRelayConfig) {
			defer func() {
				wg.Done()
			}()

			err := mev.loadMevBlocksFromRelay(indexer, relay)
			if err != nil {
				logger_mev.Errorf("error loading mev blocks from relay %v (%v): %v", idx, relay.Name, err)
			}
		}(idx, &utils.Config.MevIndexer.Relays[idx])
	}
	wg.Wait()

	// save updated MevBlocks
	updatedMevBlocks := []*dbtypes.MevBlock{}
	for _, cachedMevBlock := range mev.mevBlockCache {
		if cachedMevBlock.updated {
			updatedMevBlocks = append(updatedMevBlocks, cachedMevBlock.block)
		}
	}
	err := mev.updateMevBlocks(updatedMevBlocks)
	if err != nil {
		return err
	}

	// reset updated flag
	for _, mevBlock := range updatedMevBlocks {
		mev.mevBlockCache[common.Hash(mevBlock.BlockHash)].updated = false
	}

	// cleanup cache (remove finalized mevBlocks)
	_, finalizedEpoch, _, processedEpoch := indexer.GetCacheState()
	finalizedSlot := uint64(0)
	if processedEpoch >= 0 {
		finalizedSlot = (uint64(processedEpoch+1) * utils.Config.Chain.Config.SlotsPerEpoch) - 1
	} else if finalizedEpoch >= 0 {
		finalizedSlot = (uint64(finalizedEpoch+1) * utils.Config.Chain.Config.SlotsPerEpoch) - 1
	}

	updatedMevBlocks = []*dbtypes.MevBlock{}
	for hash, cachedMevBlock := range mev.mevBlockCache {
		if cachedMevBlock.block.SlotNumber < finalizedSlot {
			proposed := mev.getMevBlockProposedStatus(indexer, cachedMevBlock.block, finalizedSlot)
			if cachedMevBlock.block.Proposed != proposed {
				cachedMevBlock.block.Proposed = proposed
				updatedMevBlocks = append(updatedMevBlocks, cachedMevBlock.block)
			}

			delete(mev.mevBlockCache, hash)
		}
	}
	err = mev.updateMevBlocks(updatedMevBlocks)
	if err != nil {
		return err
	}

	mev.lastRefresh = time.Now()
	return nil
}

type mevIndexerRelayBlockResponse struct {
	Slot                 string `json:"slot"`
	ParentHash           string `json:"parent_hash"`
	BlockHash            string `json:"block_hash"`
	BuilderPubkey        string `json:"builder_pubkey"`
	ProposerPubkey       string `json:"proposer_pubkey"`
	ProposerFeeRecipient string `json:"proposer_fee_recipient"`
	GasLimit             string `json:"gas_limit"`
	GasUsed              string `json:"gas_used"`
	Value                string `json:"value"`
	NumTx                string `json:"num_tx"`
	BlockNumber          string `json:"block_number"`
}

func (mev *MevIndexer) loadMevBlocksFromRelay(indexer *indexer.Indexer, relay *types.MevRelayConfig) error {
	relayUrl, err := url.Parse(relay.Url)
	if err != nil {
		return fmt.Errorf("invalid relay url: %v", err)
	}

	relayUrl.Path = path.Join(relayUrl.Path, "/relay/v1/data/bidtraces/proposer_payload_delivered")
	blockLimit := relay.BlockLimit
	if blockLimit == 0 {
		blockLimit = 200
	}
	apiUrl := fmt.Sprintf("%v?limit=%v", relayUrl.String(), blockLimit)

	logger_mev.Debugf("Loading mev blocks from relay %v: %v", relay.Name, utils.GetRedactedUrl(apiUrl))

	client := &http.Client{Timeout: time.Second * 120}
	resp, err := client.Get(apiUrl)
	if err != nil {
		return fmt.Errorf("could not fetch mev blocks (%v): %v", utils.GetRedactedUrl(apiUrl), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("could not fetch mev blocks (%v): not found", utils.GetRedactedUrl(apiUrl))
		}
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("url: %v, error-response: %s", utils.GetRedactedUrl(apiUrl), data)
	}
	blocksResponse := []*mevIndexerRelayBlockResponse{}
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&blocksResponse)
	if err != nil {
		return fmt.Errorf("error parsing mev blocks response: %v", err)
	}

	if len(blocksResponse) == 0 {
		return nil
	}

	// parse blocks
	mev.mevBlockCacheMutex.Lock()
	defer mev.mevBlockCacheMutex.Unlock()
	validatorSet := indexer.GetCachedValidatorPubkeyMap()
	highestSlot, finalizedEpoch, _, processedEpoch := indexer.GetCacheState()
	finalizedSlot := uint64(0)
	if processedEpoch >= 0 {
		finalizedSlot = (uint64(processedEpoch+1) * utils.Config.Chain.Config.SlotsPerEpoch) - 1
	} else if finalizedEpoch >= 0 {
		finalizedSlot = (uint64(finalizedEpoch+1) * utils.Config.Chain.Config.SlotsPerEpoch) - 1
	}
	relayFlag := uint64(1) << relay.Index
	latestLoadedBlock := uint64(0)

	for idx, blockData := range blocksResponse {
		blockHash := common.HexToHash(blockData.BlockHash)
		cachedBlock := mev.mevBlockCache[blockHash]

		var slot uint64

		if cachedBlock == nil {
			slot, err = strconv.ParseUint(blockData.Slot, 10, 64)
			if err != nil {
				logger_mev.Warnf("failed parsing mev block %v.Slot: %v", idx, err)
				continue
			}

			if int64(slot) > highestSlot {
				// skip for now, process in next refresh
				continue
			}

			if slot <= mev.lastLoadedSlot[relay.Index] {
				continue
			}

			if slot < finalizedSlot {
				// try load from db
				mevBlock := db.GetMevBlockByBlockHash(blockHash[:])
				if mevBlock != nil {
					cachedBlock = &mevIndexerBlockCache{
						block: mevBlock,
					}
				}
			}
		}

		if cachedBlock == nil {
			blockNumber, err := strconv.ParseUint(blockData.BlockNumber, 10, 64)
			if err != nil {
				logger_mev.Warnf("failed parsing mev block %v.BlockNumber: %v", idx, err)
				continue
			}

			txCount, err := strconv.ParseUint(blockData.NumTx, 10, 64)
			if err != nil {
				logger_mev.Warnf("failed parsing mev block %v.NumTx: %v", idx, err)
				continue
			}

			gasUsed, err := strconv.ParseUint(blockData.GasUsed, 10, 64)
			if err != nil {
				logger_mev.Warnf("failed parsing mev block %v.GasUsed: %v", idx, err)
				continue
			}

			blockValue := big.NewInt(0)
			blockValue, ok := blockValue.SetString(blockData.Value, 10)
			if !ok {
				logger_mev.Warnf("failed parsing mev block %v.Value: big.Int.SetString failed", idx)
				continue
			}
			blockValueBytes := blockValue.Bytes()
			blockValueGwei := big.NewInt(0).Div(blockValue, utils.GWEI)

			validatorPubkey := phase0.BLSPubKey(common.FromHex(blockData.ProposerPubkey))
			validator := validatorSet[validatorPubkey]
			if validator == nil {
				logger_mev.Warnf("failed parsing mev block %v: ProposerPubkey (%v) not found in validator set", idx, validatorPubkey.String())
				continue
			}

			mevBlock := &dbtypes.MevBlock{
				SlotNumber:     slot,
				BlockHash:      blockHash[:],
				BlockNumber:    blockNumber,
				BuilderPubkey:  common.FromHex(blockData.BuilderPubkey),
				ProposerIndex:  uint64(validator.Index),
				SeenbyRelays:   relayFlag,
				FeeRecipient:   common.FromHex(blockData.ProposerFeeRecipient),
				TxCount:        txCount,
				GasUsed:        gasUsed,
				BlockValue:     blockValueBytes,
				BlockValueGwei: blockValueGwei.Uint64(),
			}
			mevBlock.Proposed = mev.getMevBlockProposedStatus(indexer, mevBlock, finalizedSlot)

			cachedBlock = &mevIndexerBlockCache{
				updated: true,
				block:   mevBlock,
			}
			mev.mevBlockCache[blockHash] = cachedBlock
		} else {
			if cachedBlock.block.SeenbyRelays&relayFlag > 0 {
				continue
			}

			cachedBlock.block.SeenbyRelays |= relayFlag
			cachedBlock.updated = true
			mev.mevBlockCache[blockHash] = cachedBlock
		}

		if mev.mevBlockCache[blockHash].block.SlotNumber > latestLoadedBlock {
			latestLoadedBlock = mev.mevBlockCache[blockHash].block.SlotNumber
		}
	}

	if latestLoadedBlock > mev.lastLoadedSlot[relay.Index] {
		mev.lastLoadedSlot[relay.Index] = latestLoadedBlock
	}

	return nil
}

func (mev *MevIndexer) getMevBlockProposedStatus(indexer *indexer.Indexer, mevBlock *dbtypes.MevBlock, finalizedSlot uint64) uint8 {
	proposed := uint8(0)
	if mevBlock.SlotNumber > finalizedSlot {
		for _, block := range indexer.GetCachedBlocksByExecutionBlockHash(mevBlock.BlockHash) {
			if proposed != 1 && block.IsCanonical(indexer, nil) {
				proposed = 1
			} else {
				proposed = 2
			}
		}
	} else {
		for _, block := range db.GetSlotsByBlockHash(mevBlock.BlockHash) {
			if proposed != 1 && block.Status == dbtypes.Canonical {
				proposed = 1
			} else {
				proposed = 2
			}
		}
	}

	return proposed
}

func (mev *MevIndexer) updateMevBlocks(updatedMevBlocks []*dbtypes.MevBlock) error {
	if len(updatedMevBlocks) == 0 {
		return nil
	}

	err := db.RunDBTransaction(func(tx *sqlx.Tx) error {
		return db.InsertMevBlocks(updatedMevBlocks, tx)
	})
	if err != nil {
		return fmt.Errorf("error saving mev blocks to db: %v", err)
	}

	return nil
}
