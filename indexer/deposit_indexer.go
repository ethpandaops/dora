package indexer

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	blsu "github.com/protolambda/bls12-381-util"
	zrnt_common "github.com/protolambda/zrnt/eth2/beacon/common"
	"github.com/protolambda/ztyp/tree"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/utils"
)

type DepositIndexer struct {
	indexer            *Indexer
	state              *dbtypes.DepositIndexerState
	batchSize          int
	depositContract    common.Address
	depositContractAbi *abi.ABI
	depositEventTopic  []byte
	depositSigDomain   zrnt_common.BLSDomain
}

const depositContractAbi = `[{"inputs":[],"stateMutability":"nonpayable","type":"constructor"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"bytes","name":"pubkey","type":"bytes"},{"indexed":false,"internalType":"bytes","name":"withdrawal_credentials","type":"bytes"},{"indexed":false,"internalType":"bytes","name":"amount","type":"bytes"},{"indexed":false,"internalType":"bytes","name":"signature","type":"bytes"},{"indexed":false,"internalType":"bytes","name":"index","type":"bytes"}],"name":"DepositEvent","type":"event"},{"inputs":[{"internalType":"bytes","name":"pubkey","type":"bytes"},{"internalType":"bytes","name":"withdrawal_credentials","type":"bytes"},{"internalType":"bytes","name":"signature","type":"bytes"},{"internalType":"bytes32","name":"deposit_data_root","type":"bytes32"}],"name":"deposit","outputs":[],"stateMutability":"payable","type":"function"},{"inputs":[],"name":"get_deposit_count","outputs":[{"internalType":"bytes","name":"","type":"bytes"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"get_deposit_root","outputs":[{"internalType":"bytes32","name":"","type":"bytes32"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"bytes4","name":"interfaceId","type":"bytes4"}],"name":"supportsInterface","outputs":[{"internalType":"bool","name":"","type":"bool"}],"stateMutability":"pure","type":"function"}]`

func newDepositIndexer(indexer *Indexer) *DepositIndexer {
	batchSize := utils.Config.ExecutionApi.DepositLogBatchSize
	if batchSize == 0 {
		batchSize = 10000
	}

	contractAbi, err := abi.JSON(strings.NewReader(depositContractAbi))
	if err != nil {
		log.Fatal(err)
	}

	depositEventTopic := crypto.Keccak256Hash([]byte(contractAbi.Events["DepositEvent"].Sig))

	genesisForkVersion := common.FromHex(utils.Config.Chain.Config.GenesisForkVersion)
	depositSigDomain := zrnt_common.ComputeDomain(zrnt_common.DOMAIN_DEPOSIT, zrnt_common.Version(genesisForkVersion), zrnt_common.Root{})

	ds := &DepositIndexer{
		indexer:            indexer,
		batchSize:          batchSize,
		depositContract:    common.HexToAddress(utils.Config.Chain.Config.DepositContractAddress),
		depositContractAbi: &contractAbi,
		depositEventTopic:  depositEventTopic[:],
		depositSigDomain:   depositSigDomain,
	}

	go ds.runDepositIndexerLoop()

	return ds
}

func (ds *DepositIndexer) runDepositIndexerLoop() {
	defer utils.HandleSubroutinePanic("runCacheLoop")

	for {
		time.Sleep(60 * time.Second)
		logger.Debugf("run deposit indexer logic")

		err := ds.runDepositIndexer()
		if err != nil {
			logger.Errorf("deposit indexer error: %v", err)
		}
	}
}

func (ds *DepositIndexer) runDepositIndexer() error {
	// get indexer state
	if ds.state == nil {
		ds.loadState()
	}

	finalizedEpoch, finalizedRoot, _, _ := ds.indexer.GetFinalizationCheckpoints()
	if finalizedEpoch < 0 {
		return fmt.Errorf("no finalization checkpoint")
	}

	finalizedBlock := ds.indexer.GetCachedBlock(finalizedRoot)
	if finalizedBlock == nil {
		return fmt.Errorf("could not get finalized block from cache (0x%x)", finalizedRoot)
	}

	finalizedBlockBody := finalizedBlock.GetBlockBody()
	if finalizedBlockBody == nil {
		return fmt.Errorf("could not get finalized block body (0x%x)", finalizedRoot)
	}

	finalizedBlockNumber, err := finalizedBlockBody.ExecutionBlockNumber()
	if err != nil {
		return fmt.Errorf("could not get execution block number from block body (0x%x): %v", finalizedRoot, err)
	}

	if finalizedBlockNumber < ds.state.FinalBlock {
		return fmt.Errorf("finalized block number (%v) smaller than index state (%v)", finalizedBlockNumber, ds.state.FinalBlock)
	}

	if finalizedBlockNumber > ds.state.FinalBlock {
		err := ds.processFinalizedBlocks(finalizedBlockNumber)
		if err != nil {
			return err
		}
	}

	ds.processRecentBlocks()

	return nil
}

func (ds *DepositIndexer) loadState() {
	syncState := dbtypes.DepositIndexerState{}
	db.GetExplorerState("indexer.depositstate", &syncState)
	ds.state = &syncState
}

func (ds *DepositIndexer) processFinalizedBlocks(finalizedBlockNumber uint64) error {
	client := ds.indexer.GetReadyElClient(false, nil, nil)
	if client == nil {
		return fmt.Errorf("no ready execution client found")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for ds.state.FinalBlock < finalizedBlockNumber {
		toBlock := ds.state.FinalBlock + uint64(ds.batchSize)
		if toBlock > finalizedBlockNumber {
			toBlock = finalizedBlockNumber
		}

		query := ethereum.FilterQuery{
			FromBlock: big.NewInt(0).SetUint64(ds.state.FinalBlock),
			ToBlock:   big.NewInt(0).SetUint64(toBlock),
			Addresses: []common.Address{
				ds.depositContract,
			},
		}

		logs, err := client.GetRpcClient().GetEthClient().FilterLogs(ctx, query)
		if err != nil {
			return fmt.Errorf("error fetching deposit contract logs: %v", err)
		}

		var txHash []byte
		var txDetails *types.Transaction

		depositTxs := []*dbtypes.DepositTx{}

		for _, log := range logs {
			if !bytes.Equal(log.Topics[0][:], ds.depositEventTopic) {
				continue
			}

			event, err := ds.depositContractAbi.Unpack("DepositEvent", log.Data)
			if err != nil {
				return fmt.Errorf("error decoding deposit event (%v): %v", log.TxHash, err)

			}

			if txHash == nil || !bytes.Equal(txHash, log.TxHash[:]) {
				txDetails, _, err = client.GetRpcClient().GetEthClient().TransactionByHash(ctx, log.TxHash)
				if err != nil {
					return fmt.Errorf("could not load tx details (%v): %v", log.TxHash, err)
				}

			}

			txFrom, err := types.Sender(types.LatestSignerForChainID(txDetails.ChainId()), txDetails)
			if err != nil {
				return fmt.Errorf("could not decode tx sender (%v): %v", log.TxHash, err)
			}
			txTo := *txDetails.To()

			depositTx := &dbtypes.DepositTx{
				Index:                 binary.LittleEndian.Uint64(event[4].([]byte)),
				BlockNumber:           log.BlockNumber,
				BlockRoot:             log.BlockHash[:],
				PublicKey:             event[0].([]byte),
				WithdrawalCredentials: event[1].([]byte),
				Amount:                binary.LittleEndian.Uint64(event[2].([]byte)),
				Signature:             event[3].([]byte),
				TxHash:                log.TxHash[:],
				TxSender:              txFrom[:],
				TxTarget:              txTo[:],
			}
			ds.checkDepositValidity(depositTx)
			depositTxs = append(depositTxs, depositTx)
		}

		if len(depositTxs) > 0 {
			logger.Infof("crawled deposits for block %v - %v: %v deposits", ds.state.FinalBlock, toBlock, len(depositTxs))

			depositCount := len(depositTxs)
			for depositIdx := 0; depositIdx < depositCount; depositIdx += 500 {
				endIdx := depositIdx + 500
				if endIdx > depositCount {
					endIdx = depositCount
				}

				err = ds.persistFinalizedDepositTxs(toBlock, depositTxs[depositIdx:endIdx])
				if err != nil {
					return fmt.Errorf("could not persist deposit txs: %v", err)
				}
			}

			time.Sleep(1 * time.Second)
		}
	}
	return nil
}

func (ds *DepositIndexer) processRecentBlocks() error {
	insertMap := map[string]bool{}

	headForks := ds.indexer.GetHeadForks(true)
	for _, headFork := range headForks {
		client := ds.indexer.GetReadyElClient(false, headFork.Root, nil)
		if client == nil {
			return fmt.Errorf("no ready execution client found")
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		query := ethereum.FilterQuery{
			FromBlock: big.NewInt(0).SetUint64(ds.state.FinalBlock),
			Addresses: []common.Address{
				ds.depositContract,
			},
		}

		logs, err := client.GetRpcClient().GetEthClient().FilterLogs(ctx, query)
		if err != nil {
			return fmt.Errorf("error fetching deposit contract logs: %v", err)
		}

		var txHash []byte
		var txDetails *types.Transaction

		depositTxs := []*dbtypes.DepositTx{}

		for _, log := range logs {
			if !bytes.Equal(log.Topics[0][:], ds.depositEventTopic) {
				continue
			}

			event, err := ds.depositContractAbi.Unpack("DepositEvent", log.Data)
			if err != nil {
				return fmt.Errorf("error decoding deposit event (%v): %v", log.TxHash, err)

			}

			if txHash == nil || !bytes.Equal(txHash, log.TxHash[:]) {
				txDetails, _, err = client.GetRpcClient().GetEthClient().TransactionByHash(ctx, log.TxHash)
				if err != nil {
					return fmt.Errorf("could not load tx details (%v): %v", log.TxHash, err)
				}

			}

			txFrom, err := types.Sender(types.LatestSignerForChainID(txDetails.ChainId()), txDetails)
			if err != nil {
				return fmt.Errorf("could not decode tx sender (%v): %v", log.TxHash, err)
			}
			txTo := *txDetails.To()

			depositTx := &dbtypes.DepositTx{
				Index:                 binary.LittleEndian.Uint64(event[4].([]byte)),
				BlockNumber:           log.BlockNumber,
				BlockRoot:             log.BlockHash[:],
				PublicKey:             event[0].([]byte),
				WithdrawalCredentials: event[1].([]byte),
				Amount:                binary.LittleEndian.Uint64(event[2].([]byte)),
				Signature:             event[3].([]byte),
				Orphaned:              true,
				TxHash:                log.TxHash[:],
				TxSender:              txFrom[:],
				TxTarget:              txTo[:],
			}

			depositKey := fmt.Sprintf("%v-%x", depositTx.Index, depositTx.BlockRoot)
			if insertMap[depositKey] {
				break
			}
			insertMap[depositKey] = true

			ds.checkDepositValidity(depositTx)
			depositTxs = append(depositTxs, depositTx)
		}

		if len(depositTxs) > 0 {
			logger.Infof("crawled recent deposits since block %v: %v deposits", ds.state.FinalBlock, len(depositTxs))

			depositCount := len(depositTxs)
			for depositIdx := 0; depositIdx < depositCount; depositIdx += 500 {
				endIdx := depositIdx + 500
				if endIdx > depositCount {
					endIdx = depositCount
				}

				err = ds.persistRecentDepositTxs(depositTxs[depositIdx:endIdx])
				if err != nil {
					return fmt.Errorf("could not persist deposit txs: %v", err)
				}
			}

			time.Sleep(1 * time.Second)
		}
	}
	return nil
}

func (ds *DepositIndexer) checkDepositValidity(depositTx *dbtypes.DepositTx) {
	depositMsg := &zrnt_common.DepositMessage{
		Pubkey:                zrnt_common.BLSPubkey(depositTx.PublicKey),
		WithdrawalCredentials: tree.Root(depositTx.WithdrawalCredentials),
		Amount:                zrnt_common.Gwei(depositTx.Amount),
	}
	depositRoot := depositMsg.HashTreeRoot(tree.GetHashFn())
	signingRoot := zrnt_common.ComputeSigningRoot(
		depositRoot,
		ds.depositSigDomain,
	)

	pubkey, err := depositMsg.Pubkey.Pubkey()
	sigData := zrnt_common.BLSSignature(depositTx.Signature)
	sig, err2 := sigData.Signature()
	if err == nil && err2 == nil && blsu.Verify(pubkey, signingRoot[:], sig) {
		depositTx.ValidSignature = true
	}
}

func (ds *DepositIndexer) persistFinalizedDepositTxs(toBlockNumber uint64, deposits []*dbtypes.DepositTx) error {
	tx, err := db.WriterDb.Beginx()
	if err != nil {
		return fmt.Errorf("error starting db transactions: %v", err)
	}
	defer tx.Rollback()

	err = db.InsertDepositTxs(deposits, tx)
	if err != nil {
		return fmt.Errorf("error while inserting deposit txs: %v", err)
	}

	ds.state.FinalBlock = toBlockNumber
	if toBlockNumber > ds.state.HeadBlock {
		ds.state.HeadBlock = toBlockNumber
	}
	err = db.SetExplorerState("indexer.depositstate", ds.state, tx)
	if err != nil {
		return fmt.Errorf("error while updating deposit state: %v", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing db transaction: %v", err)
	}
	return nil
}

func (ds *DepositIndexer) persistRecentDepositTxs(deposits []*dbtypes.DepositTx) error {
	tx, err := db.WriterDb.Beginx()
	if err != nil {
		return fmt.Errorf("error starting db transactions: %v", err)
	}
	defer tx.Rollback()

	err = db.InsertDepositTxs(deposits, tx)
	if err != nil {
		return fmt.Errorf("error while inserting deposit txs: %v", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing db transaction: %v", err)
	}
	return nil
}
