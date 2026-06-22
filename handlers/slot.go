package handlers

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/bellatrix"
	"github.com/ethpandaops/go-eth2-client/spec/capella"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/blockdb"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// Index will return the main "index" page using a go template
func Slot(w http.ResponseWriter, r *http.Request) {
	var slotTemplateFiles = append(layoutTemplateFiles,
		"slot/slot.html",
		"slot/overview.html",
		"slot/transactions.html",
		"slot/attestations.html",
		"slot/proofs.html",
		"slot/deposits.html",
		"slot/withdrawals.html",
		"slot/voluntary_exits.html",
		"slot/slashings.html",
		"slot/blobs.html",
		"slot/deposit_requests.html",
		"slot/withdrawal_requests.html",
		"slot/consolidation_requests.html",
		"slot/builder_deposit_requests.html",
		"slot/builder_exit_requests.html",
		"slot/bids.html",
		"slot/ptc_votes.html",
		"slot/inclusion_lists.html",
		"slot/block_access_list.html",
	)
	var notfoundTemplateFiles = append(layoutTemplateFiles,
		"slot/notfound.html",
	)

	vars := mux.Vars(r)
	slotOrHash := strings.Replace(vars["slotOrHash"], "0x", "", -1)
	blockSlot := int64(-1)
	blockRootHash, err := hex.DecodeString(slotOrHash)
	if err != nil || len(slotOrHash) != 64 {
		blockRootHash = []byte{}
		blockSlot, err = strconv.ParseInt(vars["slotOrHash"], 10, 64)
		if err != nil || blockSlot >= 2147483648 { // block slot must be lower then max int4
			data := InitPageData(w, r, "blockchain", "/slots", fmt.Sprintf("Slot %v", slotOrHash), notfoundTemplateFiles)
			w.Header().Set("Content-Type", "text/html")
			if handleTemplateError(w, r, "slot.go", "Slot", "blockSlot", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
				return // an error has occurred and was processed
			}
			return
		}
	}

	if pageError := services.GlobalCallRateLimiter.CheckCallLimit(r, 1); pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	urlArgs := r.URL.Query()
	if urlArgs.Has("download") {
		if err := handleSlotDownload(r.Context(), w, blockSlot, blockRootHash, urlArgs.Get("download")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		return
	}

	if urlArgs.Get("action") == "parse-access-list" {
		if err := handleSlotParseAccessList(w, r); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		return
	}

	pageData, pageError := getSlotPageData(blockSlot, blockRootHash)
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	if pageData == nil {
		data := InitPageData(w, r, "blockchain", "/slots", fmt.Sprintf("Slot %v", slotOrHash), notfoundTemplateFiles)
		data.Data = "slot"
		w.Header().Set("Content-Type", "text/html")
		if handleTemplateError(w, r, "slot.go", "Slot", "notFound", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
			return // an error has occurred and was processed
		}
		return
	}

	if urlArgs.Has("blob") && pageData.Block != nil {
		blobIndex, err1 := strconv.ParseUint(urlArgs.Get("blob"), 10, 64)
		blobData, err2 := services.GlobalBeaconService.GetBlockBlob(r.Context(), phase0.Root(pageData.Block.BlockRoot), blobIndex)
		if err1 == nil && err2 == nil && blobData != nil {
			if int(blobIndex) < len(pageData.Block.Blobs) {
				blobModel := pageData.Block.Blobs[blobIndex]
				blobModel.HaveData = true
				blobModel.Blob = blobData[:]
				if len(blobModel.Blob) > 512 {
					blobModel.BlobShort = blobModel.Blob[0:512]
					blobModel.IsShort = true
				} else {
					blobModel.BlobShort = blobModel.Blob
				}
			}
		}
	}

	template := templates.GetTemplate(slotTemplateFiles...)
	data := InitPageData(w, r, "blockchain", "/slots", fmt.Sprintf("Slot %v", slotOrHash), slotTemplateFiles)
	data.Data = pageData
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "index.go", "Slot", "", template.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

// SlotBlob handles responses for the block blobs tab
func SlotBlob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	blobIndex, err := strconv.ParseUint(vars["index"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid blob index", http.StatusBadRequest)
		return
	}

	blockRoot, err := hex.DecodeString(strings.Replace(vars["root"], "0x", "", -1))
	if err != nil || len(blockRoot) != 32 {
		http.Error(w, "Invalid block root", http.StatusBadRequest)
		return
	}

	// Get the block to retrieve the KZG commitment
	blockData, err := services.GlobalBeaconService.GetSlotDetailsByBlockroot(r.Context(), phase0.Root(blockRoot))
	if err != nil || blockData == nil || blockData.Block == nil || blockData.Block.Message == nil || blockData.Block.Message.Body == nil {
		http.Error(w, "Block not found", http.StatusNotFound)
		return
	}

	commitments := utils.BlockBodyBlobCommitments(blockData.Block.Message.Body)
	if int(blobIndex) >= len(commitments) {
		http.Error(w, "Blob index out of range", http.StatusBadRequest)
		return
	}

	blobData, err := services.GlobalBeaconService.GetBlockBlob(r.Context(), phase0.Root(blockRoot), blobIndex)
	if err != nil {
		logrus.WithError(err).Error("error loading blob data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
	if blobData == nil {
		http.Error(w, "Blob not found", http.StatusNotFound)
		return
	}

	result := &models.SlotPageBlobDetails{
		Index:         blobIndex,
		KzgCommitment: fmt.Sprintf("%x", commitments[blobIndex][:]),
		Blob:          fmt.Sprintf("%x", blobData[:]),
	}
	err = json.NewEncoder(w).Encode(result)
	if err != nil {
		logrus.WithError(err).Error("error encoding blob data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

func getSlotPageData(blockSlot int64, blockRoot []byte) (*models.SlotPageData, error) {
	pageData := &models.SlotPageData{}
	pageCacheKey := fmt.Sprintf("slot:%v:%x", blockSlot, blockRoot)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildSlotPageData(pageCall.CallCtx, blockSlot, blockRoot)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.SlotPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildSlotPageData(ctx context.Context, blockSlot int64, blockRoot []byte) (*models.SlotPageData, time.Duration) {
	chainState := services.GlobalBeaconService.GetChainState()
	currentSlot := chainState.CurrentSlot()
	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	var blockData *services.CombinedBlockResponse
	var err error
	if blockSlot > -1 {
		if phase0.Slot(blockSlot) <= currentSlot {
			blockData, err = services.GlobalBeaconService.GetSlotDetailsBySlot(ctx, phase0.Slot(blockSlot))
		}
	} else {
		blockData, err = services.GlobalBeaconService.GetSlotDetailsByBlockroot(ctx, phase0.Root(blockRoot))
	}

	if err != nil {
		return nil, -1
	}

	var slot phase0.Slot
	if blockData != nil {
		slot = blockData.Header.Message.Slot
	} else if blockSlot > -1 {
		slot = phase0.Slot(blockSlot)
	} else {
		return nil, -1
	}
	logrus.Debugf("slot page called: %v", slot)

	epoch := chainState.EpochOfSlot(slot)

	pageData := &models.SlotPageData{
		Slot:           uint64(slot),
		Epoch:          uint64(chainState.EpochOfSlot(slot)),
		Ts:             chainState.SlotToTime(slot),
		NextSlot:       uint64(slot + 1),
		PreviousSlot:   uint64(slot - 1),
		Future:         slot >= currentSlot,
		EpochFinalized: finalizedEpoch >= chainState.EpochOfSlot(slot),
		Badges:         []*models.SlotPageBlockBadge{},
		TracoorUrl:     utils.Config.Frontend.TracoorUrl,
	}

	var epochStatsValues *beacon.EpochStatsValues
	if chainState.EpochOfSlot(slot) >= finalizedEpoch {
		beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
		if epochStats := beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
			epochStatsValues = epochStats.GetOrLoadValues(ctx, beaconIndexer, true, false)
		}
	}

	var cacheTimeout time.Duration
	if pageData.Future {
		timeDiff := time.Until(pageData.Ts)
		if timeDiff > 10*time.Minute {
			cacheTimeout = 10 * time.Minute
		} else {
			cacheTimeout = timeDiff
		}
	} else if pageData.EpochFinalized {
		cacheTimeout = 30 * time.Minute
	} else if blockData != nil {
		cacheTimeout = 5 * time.Minute
	} else {
		cacheTimeout = 10 * time.Second
	}

	// Get all blocks for this slot (used for multi-block display and proposer fallback)
	slotBlocks, slotBlockProposers := getSlotBlocks(ctx, slot, blockRoot, blockData)
	pageData.SlotBlocks = slotBlocks

	if blockData == nil {
		pageData.Status = uint16(models.SlotStatusMissed)
		// A canonical block is indexed for this slot but its contents could not be
		// loaded from any client or the block db (e.g. all nodes checkpoint-synced
		// past it). Surface this as "data unavailable" rather than "missed".
		for _, sb := range slotBlocks {
			if sb.Status == uint16(models.SlotStatusFound) && len(sb.BlockRoot) == 32 {
				pageData.Status = uint16(models.SlotStatusDataUnavailable)
				break
			}
		}
		pageData.Proposer = math.MaxInt64
		if epochStatsValues != nil {
			if slotIndex := int(chainState.SlotToSlotIndex(slot)); slotIndex < len(epochStatsValues.ProposerDuties) {
				pageData.Proposer = uint64(epochStatsValues.ProposerDuties[slotIndex])
			}
		}
		if pageData.Proposer == math.MaxInt64 {
			pageData.Proposer = db.GetSlotAssignment(ctx, uint64(slot))
		}
		// If proposer is still unknown, check if there's exactly one orphaned block and use its proposer
		if pageData.Proposer == math.MaxInt64 && len(slotBlockProposers) == 1 {
			pageData.Proposer = slotBlockProposers[0]
		}
		pageData.ProposerName = services.GlobalBeaconService.GetValidatorName(pageData.Proposer)
	} else {
		if blockData.Orphaned {
			pageData.Status = uint16(models.SlotStatusOrphaned)
		} else {
			pageData.Status = uint16(models.SlotStatusFound)
		}
		pageData.Proposer = uint64(blockData.Header.Message.ProposerIndex)
		pageData.ProposerName = services.GlobalBeaconService.GetValidatorName(pageData.Proposer)

		blockUid := uint64(blockData.Header.Message.Slot)<<16 | 0xffff
		if cacheBlock := services.GlobalBeaconService.GetBeaconIndexer().GetBlockByRoot(blockData.Root); cacheBlock != nil {
			blockUid = cacheBlock.BlockUID
		} else if dbBlock := db.GetBlockHeadByRoot(ctx, blockData.Root[:]); dbBlock != nil {
			blockUid = dbBlock.BlockUid
		}
		pageData.Block = getSlotPageBlockData(ctx, blockData, epochStatsValues, blockUid)

		// Expose block transactions to the access-list UI via a stable field.
		if pageData.Block != nil && pageData.Block.Transactions != nil {
			pageData.TransactionDetails = pageData.Block.Transactions
		}

		// check mev block
		if pageData.Block.ExecutionData != nil {
			mevBlock := db.GetMevBlockByBlockHash(ctx, pageData.Block.ExecutionData.BlockHash)
			if mevBlock != nil {
				relays := []string{}
				for _, relay := range utils.Config.MevIndexer.Relays {
					relayFlag := uint64(1) << uint64(relay.Index)
					if mevBlock.SeenbyRelays&relayFlag > 0 {
						relays = append(relays, relay.Name)
					}
				}

				pageData.Badges = append(pageData.Badges, &models.SlotPageBlockBadge{
					Title:       "MEV Block",
					Icon:        "fa-money-bill",
					Description: fmt.Sprintf("Block proposed via Relay: %v", strings.Join(relays, ", ")),
					ClassName:   "text-bg-warning",
				})
			}
		}
	}

	// Collect system contract addresses for the access-list UI to label.
	if contracts := services.GlobalBeaconService.GetSystemContractAddresses(); len(contracts) > 0 {
		pageData.SystemContracts = make([]*types.SystemContract, 0, len(contracts))
		for addr, name := range contracts {
			pageData.SystemContracts = append(pageData.SystemContracts, &types.SystemContract{
				Address: addr.Hex(),
				Name:    name,
			})
		}
	}

	return pageData, cacheTimeout
}

// getSlotBlocks retrieves all blocks for a given slot and builds the SlotBlocks slice
// for the multi-block display. Uses GetDbBlocksByFilter which handles both cache and database.
// Also returns a list of proposers from orphaned blocks (used as fallback when proposer is unknown).
func getSlotBlocks(ctx context.Context, slot phase0.Slot, currentBlockRoot []byte, currentBlockData *services.CombinedBlockResponse) ([]*models.SlotPageSlotBlock, []uint64) {
	slotBlocks := make([]*models.SlotPageSlotBlock, 0)
	orphanedProposers := make([]uint64, 0)
	hasCanonicalOrMissed := false

	// Get all blocks for the slot (from cache and database)
	slotNum := uint64(slot)
	dbBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, &dbtypes.BlockFilter{
		Slot:         &slotNum,
		WithOrphaned: 1, // include both canonical and orphaned
		WithMissing:  1, // include missing slots
	}, 0, 100, 0)

	for _, dbBlock := range dbBlocks {
		if dbBlock.Block == nil {
			// This is a missed slot row (canonical proposer info without a block)
			hasCanonicalOrMissed = true
			slotBlocks = append(slotBlocks, &models.SlotPageSlotBlock{
				BlockRoot: nil, // nil indicates missed
				Status:    uint16(models.SlotStatusMissed),
				IsCurrent: currentBlockData == nil,
			})
			continue
		}

		var blockRoot phase0.Root
		copy(blockRoot[:], dbBlock.Block.Root)

		isCanonical := dbBlock.Block.Status == dbtypes.Canonical
		if isCanonical {
			hasCanonicalOrMissed = true
		} else {
			// Track orphaned block proposers for fallback
			orphanedProposers = append(orphanedProposers, dbBlock.Block.Proposer)
		}

		isCurrent := false
		if currentBlockData != nil && blockRoot == currentBlockData.Root {
			isCurrent = true
		} else if len(currentBlockRoot) == 32 && blockRoot == phase0.Root(currentBlockRoot) {
			isCurrent = true
		}

		status := uint16(models.SlotStatusOrphaned)
		if isCanonical {
			status = uint16(models.SlotStatusFound)
		}

		slotBlocks = append(slotBlocks, &models.SlotPageSlotBlock{
			BlockRoot: blockRoot[:],
			Status:    status,
			IsCurrent: isCurrent,
		})
	}

	// If no canonical or missed block was returned but there are orphaned blocks,
	// add a "missed (canonical)" entry (fallback for edge cases)
	if !hasCanonicalOrMissed && len(slotBlocks) > 0 {
		missedBlock := &models.SlotPageSlotBlock{
			BlockRoot: nil, // nil indicates missed
			Status:    uint16(models.SlotStatusMissed),
			IsCurrent: currentBlockData == nil,
		}
		// Insert missed block at the beginning
		slotBlocks = append([]*models.SlotPageSlotBlock{missedBlock}, slotBlocks...)
	}

	return slotBlocks, orphanedProposers
}

func getSlotPageBlockData(ctx context.Context, blockData *services.CombinedBlockResponse, epochStatsValues *beacon.EpochStatsValues, blockUid uint64) *models.SlotPageBlockData {
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()

	if blockData.Block == nil || blockData.Block.Message == nil || blockData.Block.Message.Body == nil {
		return nil
	}

	body := blockData.Block.Message.Body
	graffiti := body.Graffiti
	randaoReveal := body.RANDAOReveal
	eth1Data := body.ETH1Data
	attestations := body.Attestations
	deposits := body.Deposits
	voluntaryExits := body.VoluntaryExits
	attesterSlashings := body.AttesterSlashings
	proposerSlashings := body.ProposerSlashings
	blsToExecChanges := body.BLSToExecutionChanges
	syncAggregate := body.SyncAggregate
	blobKzgCommitments := utils.BlockBodyBlobCommitments(body)
	var executionWithdrawals []*capella.Withdrawal
	if body.ExecutionPayload != nil {
		executionWithdrawals = body.ExecutionPayload.Withdrawals
	}

	pageData := &models.SlotPageBlockData{
		BlockRoot:              blockData.Root[:],
		ParentRoot:             blockData.Header.Message.ParentRoot[:],
		StateRoot:              blockData.Header.Message.StateRoot[:],
		BodyRoot:               blockData.Header.Message.BodyRoot[:],
		Signature:              blockData.Header.Signature[:],
		RandaoReveal:           randaoReveal[:],
		Graffiti:               graffiti[:],
		Eth1dataDepositroot:    eth1Data.DepositRoot[:],
		Eth1dataDepositcount:   eth1Data.DepositCount,
		Eth1dataBlockhash:      eth1Data.BlockHash,
		ValidatorNames:         make([]models.SlotPageValidatorName, 0),
		SlotsPerEpoch:          specs.SlotsPerEpoch,
		TargetCommitteeSize:    specs.TargetCommitteeSize,
		MaxCommitteesPerSlot:   specs.MaxCommitteesPerSlot,
		ProposerSlashingsCount: uint64(len(proposerSlashings)),
		AttesterSlashingsCount: uint64(len(attesterSlashings)),
		AttestationsCount:      uint64(len(attestations)),
		DepositsCount:          uint64(len(deposits)),
		VoluntaryExitsCount:    uint64(len(voluntaryExits)),
		SlashingsCount:         uint64(len(proposerSlashings)) + uint64(len(attesterSlashings)),
	}

	epoch := chainState.EpochOfSlot(blockData.Header.Message.Slot)
	assignmentsMap := make(map[phase0.Epoch]*beacon.EpochStatsValues)
	assignmentsLoaded := make(map[phase0.Epoch]bool)
	dbEpochMap := make(map[phase0.Epoch]*dbtypes.Epoch)
	dbEpochLoaded := make(map[phase0.Epoch]bool)
	assignmentsMap[epoch] = epochStatsValues
	assignmentsLoaded[epoch] = true

	attHeadBlocks := make(map[phase0.Root]phase0.Slot)

	// Committee membership is loaded on demand via /slot/{slot}/duties; only the
	// committee indices and aggregation bits are populated here.
	pageData.Attestations = make([]*models.SlotPageAttestation, pageData.AttestationsCount)
	for i, att := range attestations {
		if att == nil || att.Data == nil {
			continue
		}

		attData := att.Data
		attSignature := att.Signature
		attAggregationBits := att.AggregationBits

		totalActiveValidators := uint64(0)

		attEpoch := chainState.EpochOfSlot(attData.Slot)
		if !assignmentsLoaded[attEpoch] { // get epoch duties from cache
			assignmentsLoaded[attEpoch] = true
			beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
			if epochStats := beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
				epochStatsValues := epochStats.GetOrLoadValues(ctx, beaconIndexer, true, false)

				assignmentsMap[attEpoch] = epochStatsValues
			}
		}

		if assignmentsMap[attEpoch] != nil {
			totalActiveValidators = assignmentsMap[attEpoch].ActiveValidators
		} else {
			if !dbEpochLoaded[attEpoch] {
				dbEpochLoaded[attEpoch] = true
				dbEpochs := db.GetEpochs(ctx, uint64(attEpoch), 1)
				if len(dbEpochs) > 0 && dbEpochs[0].Epoch == uint64(attEpoch) {
					dbEpochMap[attEpoch] = dbEpochs[0]
				}
			}

			if dbEpochMap[attEpoch] != nil {
				totalActiveValidators = dbEpochMap[attEpoch].ValidatorCount
			}
		}

		attPageData := models.SlotPageAttestation{
			Slot:            uint64(attData.Slot),
			TotalActive:     totalActiveValidators,
			AggregationBits: attAggregationBits,
			Signature:       attSignature[:],
			BeaconBlockRoot: attData.BeaconBlockRoot[:],
			SourceEpoch:     uint64(attData.Source.Epoch),
			SourceRoot:      attData.Source.Root[:],
			TargetEpoch:     uint64(attData.Target.Epoch),
			TargetRoot:      attData.Target.Root[:],
		}

		if slot, ok := attHeadBlocks[attData.BeaconBlockRoot]; ok {
			attPageData.BeaconBlockSlot = uint64(slot)
		} else {
			beaconBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, &dbtypes.BlockFilter{
				BlockRoot: attData.BeaconBlockRoot[:],
			}, 0, 1, 0)
			if len(beaconBlocks) > 0 {
				slot := phase0.Slot(beaconBlocks[0].Slot)
				attHeadBlocks[attData.BeaconBlockRoot] = slot
				attPageData.BeaconBlockSlot = uint64(slot)
			}
		}

		if att.Version >= spec.DataVersionGloas {
			payloadStatus := uint64(attData.Index)
			attPageData.PayloadStatus = &payloadStatus
		}

		if att.Version >= spec.DataVersionElectra {
			// EIP-7549 attestation
			attPageData.CommitteeIndex = []uint64{}
			for _, committee := range att.CommitteeBits.BitIndices() {
				if uint64(committee) >= specs.MaxCommitteesPerSlot {
					continue
				}
				attPageData.CommitteeIndex = append(attPageData.CommitteeIndex, uint64(committee))
			}
		} else {
			attPageData.CommitteeIndex = []uint64{uint64(attData.Index)}
		}

		attPageData.Validators = []uint64{}
		attPageData.IncludedValidators = []uint64{}

		pageData.Attestations[i] = &attPageData
	}

	pageData.Deposits = make([]*models.SlotPageDeposit, pageData.DepositsCount)
	for i, deposit := range deposits {
		pageData.Deposits[i] = &models.SlotPageDeposit{
			PublicKey:             deposit.Data.PublicKey[:],
			Withdrawalcredentials: deposit.Data.WithdrawalCredentials,
			Amount:                uint64(deposit.Data.Amount),
			Signature:             deposit.Data.Signature[:],
		}
	}

	pageData.VoluntaryExits = make([]*models.SlotPageVoluntaryExit, pageData.VoluntaryExitsCount)
	for i, exit := range voluntaryExits {
		validatorIndex := uint64(exit.Message.ValidatorIndex)
		isBuilder := validatorIndex&services.BuilderIndexFlag != 0
		displayIndex := validatorIndex
		if isBuilder {
			displayIndex = validatorIndex &^ services.BuilderIndexFlag
		}
		pageData.VoluntaryExits[i] = &models.SlotPageVoluntaryExit{
			ValidatorIndex: displayIndex,
			ValidatorName:  services.GlobalBeaconService.GetValidatorName(validatorIndex),
			IsBuilder:      isBuilder,
			Epoch:          uint64(exit.Message.Epoch),
			Signature:      exit.Signature[:],
		}
	}

	pageData.AttesterSlashings = make([]*models.SlotPageAttesterSlashing, pageData.AttesterSlashingsCount)
	for i, slashing := range attesterSlashings {
		if slashing == nil || slashing.Attestation1 == nil || slashing.Attestation2 == nil {
			continue
		}

		att1, att2 := slashing.Attestation1, slashing.Attestation2

		att1AttestingIndices := att1.AttestingIndices
		att2AttestingIndices := att2.AttestingIndices
		if att1AttestingIndices == nil || att2AttestingIndices == nil {
			continue
		}

		att1Signature := att1.Signature
		att2Signature := att2.Signature

		att1Data := att1.Data
		att2Data := att2.Data
		if att1Data == nil || att2Data == nil {
			continue
		}

		slashingData := &models.SlotPageAttesterSlashing{
			Attestation1Indices:         make([]uint64, len(att1AttestingIndices)),
			Attestation1Signature:       att1Signature[:],
			Attestation1Slot:            uint64(att1Data.Slot),
			Attestation1Index:           uint64(att1Data.Index),
			Attestation1BeaconBlockRoot: att1Data.BeaconBlockRoot[:],
			Attestation1SourceEpoch:     uint64(att1Data.Source.Epoch),
			Attestation1SourceRoot:      att1Data.Source.Root[:],
			Attestation1TargetEpoch:     uint64(att1Data.Target.Epoch),
			Attestation1TargetRoot:      att1Data.Target.Root[:],
			Attestation2Indices:         make([]uint64, len(att2AttestingIndices)),
			Attestation2Signature:       att2Signature[:],
			Attestation2Slot:            uint64(att2Data.Slot),
			Attestation2Index:           uint64(att2Data.Index),
			Attestation2BeaconBlockRoot: att2Data.BeaconBlockRoot[:],
			Attestation2SourceEpoch:     uint64(att2Data.Source.Epoch),
			Attestation2SourceRoot:      att2Data.Source.Root[:],
			Attestation2TargetEpoch:     uint64(att2Data.Target.Epoch),
			Attestation2TargetRoot:      att2Data.Target.Root[:],
			SlashedValidators:           make([]types.NamedValidator, 0),
		}
		pageData.AttesterSlashings[i] = slashingData
		for j := range att1AttestingIndices {
			slashingData.Attestation1Indices[j] = uint64(att1AttestingIndices[j])
		}
		for j := range att2AttestingIndices {
			slashingData.Attestation2Indices[j] = uint64(att2AttestingIndices[j])
		}
		for _, valIdx := range utils.FindMatchingIndices(att1AttestingIndices, att2AttestingIndices) {
			slashingData.SlashedValidators = append(slashingData.SlashedValidators, types.NamedValidator{
				Index: valIdx,
				Name:  services.GlobalBeaconService.GetValidatorName(valIdx),
			})
		}
	}

	pageData.ProposerSlashings = make([]*models.SlotPageProposerSlashing, pageData.ProposerSlashingsCount)
	for i, slashing := range proposerSlashings {
		pageData.ProposerSlashings[i] = &models.SlotPageProposerSlashing{
			ProposerIndex:     uint64(slashing.SignedHeader1.Message.ProposerIndex),
			ProposerName:      services.GlobalBeaconService.GetValidatorName(uint64(slashing.SignedHeader1.Message.ProposerIndex)),
			Header1Slot:       uint64(slashing.SignedHeader1.Message.Slot),
			Header1ParentRoot: slashing.SignedHeader1.Message.ParentRoot[:],
			Header1StateRoot:  slashing.SignedHeader1.Message.StateRoot[:],
			Header1BodyRoot:   slashing.SignedHeader1.Message.BodyRoot[:],
			Header1Signature:  slashing.SignedHeader1.Signature[:],
			Header2Slot:       uint64(slashing.SignedHeader2.Message.Slot),
			Header2ParentRoot: slashing.SignedHeader2.Message.ParentRoot[:],
			Header2StateRoot:  slashing.SignedHeader2.Message.StateRoot[:],
			Header2BodyRoot:   slashing.SignedHeader2.Message.BodyRoot[:],
			Header2Signature:  slashing.SignedHeader2.Signature[:],
		}
	}

	if specs.AltairForkEpoch != nil && uint64(epoch) >= *specs.AltairForkEpoch && syncAggregate != nil {
		pageData.SyncAggregateBits = syncAggregate.SyncCommitteeBits
		pageData.SyncAggregateSignature = syncAggregate.SyncCommitteeSignature[:]
		var syncAssignments []uint64
		if epochStatsValues != nil {
			syncAssignmentsInt := make([]uint64, len(epochStatsValues.SyncCommitteeDuties))
			for j := 0; j < len(epochStatsValues.SyncCommitteeDuties); j++ {
				syncAssignmentsInt[j] = uint64(epochStatsValues.SyncCommitteeDuties[j])
			}
			syncAssignments = syncAssignmentsInt
		}
		if len(syncAssignments) == 0 {
			syncPeriod := uint64(epoch) / specs.EpochsPerSyncCommitteePeriod
			syncAssignments = db.GetSyncAssignmentsForPeriod(ctx, syncPeriod)
		}

		if len(syncAssignments) != 0 {
			pageData.SyncAggCommittee = make([]types.NamedValidator, len(syncAssignments))
			for idx, vidx := range syncAssignments {
				pageData.SyncAggCommittee[idx] = types.NamedValidator{
					Index: vidx,
					Name:  services.GlobalBeaconService.GetValidatorName(vidx),
				}
			}
		} else {
			pageData.SyncAggCommittee = []types.NamedValidator{}
		}
		pageData.SyncAggParticipation = utils.SyncCommitteeParticipation(pageData.SyncAggregateBits, specs.SyncCommitteeSize)
	}

	if signedBid := body.SignedExecutionPayloadBid; signedBid != nil && signedBid.Message != nil {
		bid := signedBid.Message
		blobKzgCommitments := bid.BlobKZGCommitments
		commitments := make([][]byte, len(blobKzgCommitments))
		for i := range blobKzgCommitments {
			commitments[i] = blobKzgCommitments[i][:]
		}

		pageData.PayloadHeader = &models.SlotPagePayloadHeader{
			PayloadStatus:      uint16(0),
			ParentBlockHash:    bid.ParentBlockHash[:],
			ParentBlockRoot:    bid.ParentBlockRoot[:],
			BlockHash:          bid.BlockHash[:],
			GasLimit:           bid.GasLimit,
			BuilderIndex:       uint64(bid.BuilderIndex),
			BuilderName:        services.GlobalBeaconService.GetValidatorName(uint64(bid.BuilderIndex) | services.BuilderIndexFlag),
			BuilderURL:         services.GlobalBeaconService.GetBuilderURL(uint64(bid.BuilderIndex)),
			Slot:               uint64(bid.Slot),
			Value:              uint64(bid.Value),
			BlobKZGCommitments: commitments,
			Signature:          signedBid.Signature[:],
		}
	}

	// Source the execution payload either from the in-block payload (pre-EIP-7732)
	// or from the separately-delivered envelope (Gloas+). Both share the same
	// fork-specific fields exposed via the agnostic ExecutionPayload struct.
	var executionPayload *all.ExecutionPayload
	if blockData.Block.Version >= spec.DataVersionGloas {
		havePayload := blockData.Payload != nil && blockData.Payload.Message != nil
		if havePayload {
			executionPayload = blockData.Payload.Message.Payload
		}

		// The payload is canonical if a canonical successor builds on top of it -
		// i.e. some canonical slot has its execution parent hash set to this block's
		// committed block hash. A single indexed, canonical-only lookup answers that.
		payloadIncluded := false
		if pageData.PayloadHeader != nil {
			builtOn := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, &dbtypes.BlockFilter{
				EthBlockParentHash: pageData.PayloadHeader.BlockHash,
				WithOrphaned:       0, // canonical successors only
			}, 0, 1, 0)
			payloadIncluded = len(builtOn) > 0
		}

		// Distinguish a genuinely orphaned payload from the chain tip: a canonical
		// block at the head has no successor yet that could reference its payload.
		hasCanonicalChild := false
		if !payloadIncluded && !blockData.Orphaned {
			for _, child := range services.GlobalBeaconService.GetDbBlocksByParentRoot(ctx, blockData.Root) {
				if child.Status == dbtypes.Canonical {
					hasCanonicalChild = true
					break
				}
			}
		}

		switch {
		case payloadIncluded:
			// A canonical successor builds on this payload.
			pageData.PayloadHeader.PayloadStatus = uint16(dbtypes.PayloadStatusCanonical)

			if !havePayload {
				// It existed and is part of the chain, we just don't have the envelope.
				pageData.PayloadDataUnavailable = true
			}
		case havePayload && !blockData.Orphaned && !hasCanonicalChild:
			// Chain tip: no canonical successor exists yet - treat the payload as
			// canonical (pending) instead of orphaned.
			pageData.PayloadHeader.PayloadStatus = uint16(dbtypes.PayloadStatusCanonical)
		case havePayload:
			// We hold the payload but a canonical successor builds on something else
			// (or the block itself is orphaned) -> orphaned.
			pageData.PayloadHeader.PayloadStatus = uint16(dbtypes.PayloadStatusOrphaned)
		default:
			// No payload and nothing builds on it -> missing (status stays 0).
		}
	} else {
		executionPayload = body.ExecutionPayload
	}

	if executionPayload != nil {
		pageData.ExecutionData = &models.SlotPageExecutionData{
			ParentHash:   executionPayload.ParentHash[:],
			FeeRecipient: executionPayload.FeeRecipient[:],
			StateRoot:    executionPayload.StateRoot[:],
			ReceiptsRoot: executionPayload.ReceiptsRoot[:],
			LogsBloom:    executionPayload.LogsBloom[:],
			Random:       executionPayload.PrevRandao[:],
			GasLimit:     executionPayload.GasLimit,
			GasUsed:      executionPayload.GasUsed,
			Timestamp:    executionPayload.Timestamp,
			Time:         time.Unix(int64(executionPayload.Timestamp), 0),
			ExtraData:    executionPayload.ExtraData,
			BlockHash:    executionPayload.BlockHash[:],
			BlockNumber:  executionPayload.BlockNumber,
		}

		// BaseFeePerGas: post-Deneb is *uint256.Int, Bellatrix/Capella store the
		// little-endian byte representation in BaseFeePerGasLE.
		if executionPayload.BaseFeePerGas != nil {
			pageData.ExecutionData.BaseFeePerGas = executionPayload.BaseFeePerGas.Uint64()
		} else {
			pageData.ExecutionData.BaseFeePerGas = utils.GetBaseFeeAsUint64(executionPayload.BaseFeePerGasLE)
		}

		if executionPayload.Version >= spec.DataVersionDeneb {
			excessBlobGas := executionPayload.ExcessBlobGas
			pageData.ExecutionData.ExcessBlobGas = &excessBlobGas
			blobGasUsed := executionPayload.BlobGasUsed
			pageData.ExecutionData.BlobGasUsed = &blobGasUsed
		}

		executionChainState := services.GlobalBeaconService.GetExecutionChainState()
		blobSchedule := executionChainState.GetBlobScheduleForTimestamp(time.Unix(int64(pageData.ExecutionData.Timestamp), 0))
		if blobSchedule != nil {
			blobGasLimit := blobSchedule.Max * 131072
			pageData.ExecutionData.BlobGasLimit = &blobGasLimit
			pageData.ExecutionData.BlobLimit = &blobSchedule.Max
		}

		if pageData.ExecutionData.ExcessBlobGas != nil && blobSchedule != nil {
			blobBaseFee := executionChainState.CalcBaseFeePerBlobGas(*pageData.ExecutionData.ExcessBlobGas, blobSchedule.BaseFeeUpdateFraction)
			blobBaseFeeUint64 := blobBaseFee.Uint64()
			pageData.ExecutionData.BlobBaseFee = &blobBaseFeeUint64

			// EIP-7918: Calculate adjusted blob base fee if reserve price mechanism is active
			eip7918BlobBaseFee := executionChainState.CalculateEIP7918BlobBaseFee(pageData.ExecutionData.BaseFeePerGas, blobBaseFeeUint64)
			if eip7918BlobBaseFee > blobBaseFeeUint64 {
				// Store the original blob base fee in BlobBaseFee
				// Store the EIP-7918 adjusted fee in BlobBaseFeeEIP7918
				pageData.ExecutionData.BlobBaseFeeEIP7918 = &eip7918BlobBaseFee
				pageData.ExecutionData.IsEIP7918Active = true
			}
		}

		if len(executionPayload.Transactions) > 0 {
			getSlotPageTransactions(ctx, pageData, executionPayload.Transactions, blockUid)
		}

		// EIP-7778: compute gas refund delta = |block.gasUsed - sum(receipt.gasUsed)|.
		// In Amsterdam these two counters ALWAYS diverge (they were equal pre-Amsterdam):
		//   block.gasUsed  = gp.Used()           = max(sum_regular, sum_state)
		//   sum(receipts)  = gp.CumulativeUsed() = sum(max(pre_refund-refund, floor))
		// Only computed when EL indexing has enriched ALL tx receipts in this block.
		if len(pageData.Transactions) > 0 {
			var sumTxGas uint64
			elDataCount := 0
			for _, tx := range pageData.Transactions {
				if tx.HasElData {
					sumTxGas += tx.GasUsed
					elDataCount++
				}
			}
			if elDataCount == len(pageData.Transactions) {
				blockGas := executionPayload.GasUsed
				pageData.ExecutionData.SumTxGasUsed = &sumTxGas
				if blockGas >= sumTxGas {
					delta := blockGas - sumTxGas
					pageData.ExecutionData.GasRefundDelta = &delta
					pageData.ExecutionData.GasRefundDeltaExcess = true
				} else {
					delta := sumTxGas - blockGas
					pageData.ExecutionData.GasRefundDelta = &delta
					pageData.ExecutionData.GasRefundDeltaExcess = false
				}
			}
		}

		// EIP-7928 Block Access List — the chainservice sources the bytes
		// either from the envelope (fresh from the node) or from blockdb
		// (preserved after the node pruned it).
		if len(blockData.BlockAccessList) > 0 {
			accesses, err := utils.DecodeBlockAccessList(blockData.BlockAccessList)
			if err != nil {
				logrus.Errorf("error decoding block access list for slot %v: %v", blockData.Header.Message.Slot, err)
			} else {
				pageData.ExecutionData.BlockAccessList = convertBALToModel(accesses)
				pageData.ExecutionData.BALSummary = computeBALSummary(pageData.ExecutionData.BlockAccessList)
			}
		}

		// Check if execution data exists in blockdb for receipt downloads
		if blockdb.GlobalBlockDb != nil && blockdb.GlobalBlockDb.SupportsExecData() {
			hasExecData, _ := blockdb.GlobalBlockDb.HasExecData(
				ctx,
				uint64(blockData.Header.Message.Slot),
				blockData.Root[:],
			)
			pageData.ExecutionData.HasExecData = hasExecData
		}
	}

	if specs.DenebForkEpoch != nil && uint64(epoch) >= *specs.DenebForkEpoch {
		pageData.BlobsCount = uint64(len(blobKzgCommitments))
		pageData.Blobs = make([]*models.SlotPageBlob, pageData.BlobsCount)
		for i := range blobKzgCommitments {
			blobData := &models.SlotPageBlob{
				Index:         uint64(i),
				KzgCommitment: blobKzgCommitments[i][:],
			}
			pageData.Blobs[i] = blobData
		}
	}

	if specs.ElectraForkEpoch != nil && uint64(epoch) >= *specs.ElectraForkEpoch {
		var requests *all.ExecutionRequests
		if blockData.Block.Version >= spec.DataVersionGloas {
			// In Gloas the execution requests carried by a payload are processed in the next
			// block (as parent_execution_requests), so they are displayed there — consistent
			// with the deposits table and the slot/epoch deposit counts. The withdrawal sweep
			// remains part of this block's own payload.
			requests = body.ParentExecutionRequests
			pageData.RequestsFromParentPayload = true
			if blockData.Payload != nil {
				executionWithdrawals = blockData.Payload.Message.Payload.Withdrawals
			}
		} else {
			requests = body.ExecutionRequests
		}

		if requests != nil {
			getSlotPageDepositRequests(pageData, requests.Deposits)
			getSlotPageWithdrawalRequests(pageData, requests.Withdrawals)
			getSlotPageConsolidationRequests(pageData, requests.Consolidations)
			getSlotPageBuilderDeposits(ctx, pageData, requests.BuilderDeposits)
			getSlotPageBuilderExits(ctx, pageData, requests.BuilderExits)
		}
	}

	if specs.CapellaForkEpoch != nil && uint64(epoch) >= *specs.CapellaForkEpoch {
		pageData.BLSChangesCount = uint64(len(blsToExecChanges))
		pageData.BLSChanges = make([]*models.SlotPageBLSChange, pageData.BLSChangesCount)
		for i, blschange := range blsToExecChanges {
			pageData.BLSChanges[i] = &models.SlotPageBLSChange{
				ValidatorIndex: uint64(blschange.Message.ValidatorIndex),
				ValidatorName:  services.GlobalBeaconService.GetValidatorName(uint64(blschange.Message.ValidatorIndex)),
				BlsPubkey:      []byte(blschange.Message.FromBLSPubkey[:]),
				Address:        []byte(blschange.Message.ToExecutionAddress[:]),
				Signature:      []byte(blschange.Signature[:]),
			}
		}

		pageData.WithdrawalsCount = uint64(len(executionWithdrawals))
		pageData.Withdrawals = make([]*models.SlotPageWithdrawal, pageData.WithdrawalsCount)

		// Try to get enriched withdrawal data (type + ref slot) from the chain service.
		// This works for both cached (unfinalized) and DB (finalized) blocks.
		var enrichedWithdrawals []*dbtypes.Withdrawal
		if cacheBlock := services.GlobalBeaconService.GetBeaconIndexer().GetBlockByRoot(blockData.Root); cacheBlock != nil {
			isCanonical := slices.Contains(services.GlobalBeaconService.GetCanonicalForkKeys(), cacheBlock.GetForkId())
			enrichedWithdrawals = cacheBlock.GetDbWithdrawals(services.GlobalBeaconService.GetBeaconIndexer(), isCanonical)
		}
		if len(enrichedWithdrawals) == 0 {
			dbWithdrawals, _ := db.GetWithdrawalsByBlockUid(ctx, blockUid)
			enrichedWithdrawals = dbWithdrawals
		}

		// Build a lookup map by block index for enrichment
		enrichedMap := make(map[int16]*dbtypes.Withdrawal, len(enrichedWithdrawals))
		for _, ew := range enrichedWithdrawals {
			enrichedMap[ew.BlockIdx] = ew
		}

		// Batch resolve ref slot block roots
		refBlockUids := make([]uint64, 0)
		refBlockUidSet := make(map[uint64]bool)
		for _, ew := range enrichedWithdrawals {
			if ew.RefSlot != nil && !refBlockUidSet[*ew.RefSlot] {
				refBlockUidSet[*ew.RefSlot] = true
				refBlockUids = append(refBlockUids, *ew.RefSlot)
			}
		}
		refBlockMap := make(map[uint64]*dbtypes.AssignedSlot, len(refBlockUids))
		if len(refBlockUids) > 0 {
			refFilter := &dbtypes.BlockFilter{
				BlockUids:    refBlockUids,
				WithOrphaned: 1,
			}
			refBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, refFilter, 0, uint32(len(refBlockUids)), 0)
			for _, b := range refBlocks {
				if b.Block != nil {
					refBlockMap[b.Block.BlockUid] = b
				}
			}
		}

		for i, withdrawal := range executionWithdrawals {
			validatorIndex := uint64(withdrawal.ValidatorIndex)
			isBuilder := validatorIndex&services.BuilderIndexFlag != 0
			displayIndex := validatorIndex
			if isBuilder {
				displayIndex = validatorIndex &^ services.BuilderIndexFlag
			}
			wd := &models.SlotPageWithdrawal{
				Index:          uint64(withdrawal.Index),
				ValidatorIndex: displayIndex,
				ValidatorName:  services.GlobalBeaconService.GetValidatorName(validatorIndex),
				IsBuilder:      isBuilder,
				Address:        withdrawal.Address[:],
				Amount:         uint64(withdrawal.Amount),
			}

			// Enrich with type and ref slot from chain service data
			if enriched, ok := enrichedMap[int16(i)]; ok {
				wd.Type = enriched.Type
				if enriched.RefSlot != nil {
					wd.RefSlot = *enriched.RefSlot >> 16
					if refBlock, ok := refBlockMap[*enriched.RefSlot]; ok && refBlock.Block != nil {
						wd.RefSlotRoot = refBlock.Block.Root
					}
				}
			}

			pageData.Withdrawals[i] = wd
		}
	}

	// Fetch execution proofs for this block
	getSlotPageExecutionProofs(pageData, blockData.Root, uint64(blockData.Header.Message.Slot))

	// Load execution payload bids for ePBS (gloas+) blocks
	if blockData.Block.Version >= spec.DataVersionGloas {
		getSlotPageBids(pageData, blockData.Header.Message.Slot)
		getSlotPagePtcVotes(pageData, blockData, blockData.Header.Message.Slot)
	}

	// Load inclusion lists for EIP-7805 (heze+) slots
	if services.GlobalBeaconService.GetChainState().IsEip7805Enabled(epoch) {
		getSlotPageInclusionLists(pageData, blockData.Header.Message.Slot)
	}

	return pageData
}

// Transaction type names for display
var slotTxTypeNames = map[uint8]string{
	0: "Legacy",
	1: "EIP-2930",
	2: "EIP-1559",
	3: "Blob",
	4: "EIP-7702",
}

func getSlotPageTransactions(ctx context.Context, pageData *models.SlotPageBlockData, transactions []bellatrix.Transaction, blockUid uint64) {
	pageData.Transactions = make([]*models.SlotPageTransaction, 0)
	sigLookupBytes := []types.TxSignatureBytes{}
	sigLookupMap := map[types.TxSignatureBytes][]*models.SlotPageTransaction{}

	// Build a map of tx hash to tx data for EL enrichment
	txHashMap := make(map[string]*models.SlotPageTransaction, len(transactions))

	sysContracts := services.GlobalBeaconService.GetSystemContractAddresses()

	for idx, txBytes := range transactions {
		var tx ethtypes.Transaction

		err := tx.UnmarshalBinary(txBytes)
		if err != nil {
			logrus.Warnf("error decoding transaction 0x%x.%v: %v\n", pageData.BlockRoot, idx, err)
			continue
		}

		txHash := tx.Hash()
		txBigFloat := new(big.Float).SetInt(tx.Value())
		txBigFloat.Quo(txBigFloat, new(big.Float).SetInt(utils.ETH))
		txValue, _ := txBigFloat.Float64()

		txType := uint8(tx.Type())
		typeName := slotTxTypeNames[txType]
		if typeName == "" {
			typeName = fmt.Sprintf("Type %d", txType)
		}

		txData := &models.SlotPageTransaction{
			Index:    uint64(idx),
			Hash:     txHash[:],
			Value:    txValue,
			Data:     tx.Data(),
			Type:     uint64(txType),
			TypeName: typeName,
			GasLimit: tx.Gas(),
		}
		txData.DataLen = uint64(len(txData.Data))

		chainId := tx.ChainId()
		if chainId != nil && chainId.Cmp(big.NewInt(0)) == 0 {
			chainId = nil
		}
		txFrom, err := ethtypes.Sender(ethtypes.LatestSignerForChainID(chainId), &tx)
		if err != nil {
			logrus.Warnf("error decoding transaction sender 0x%x.%v: %v\n", pageData.BlockRoot, idx, err)
		} else {
			txData.From = txFrom.Bytes()
		}
		txTo := tx.To()
		if txTo != nil {
			txData.To = txTo.Bytes()
		}

		pageData.Transactions = append(pageData.Transactions, txData)
		txHashMap[string(txHash[:])] = txData

		// check call fn signature
		isCreate := txTo == nil
		if txData.DataLen >= 4 {
			// Skip fn signature lookup for deployments, precompiles, and system contracts
			if skip, altName := utils.ShouldSkipSignatureLookup(txData.To, isCreate, sysContracts); skip {
				txData.FuncSigStatus = 10
				txData.FuncName = altName
			} else {
				sigBytes := types.TxSignatureBytes(txData.Data[0:4])
				if sigLookupMap[sigBytes] == nil {
					sigLookupMap[sigBytes] = []*models.SlotPageTransaction{
						txData,
					}
					sigLookupBytes = append(sigLookupBytes, sigBytes)
				} else {
					sigLookupMap[sigBytes] = append(sigLookupMap[sigBytes], txData)
				}
			}
		} else {
			txData.FuncSigStatus = 10
			txData.FuncName = "transfer"
		}
	}
	pageData.TransactionsCount = uint64(len(transactions))

	if len(sigLookupBytes) > 0 {
		sigLookups := services.GlobalTxSignaturesService.LookupSignatures(ctx, sigLookupBytes)
		for _, sigLookup := range sigLookups {
			for _, txData := range sigLookupMap[sigLookup.Bytes] {
				txData.FuncSigStatus = uint64(sigLookup.Status)
				txData.FuncBytes = fmt.Sprintf("0x%x", sigLookup.Bytes[:])
				if sigLookup.Status == types.TxSigStatusFound {
					txData.FuncSig = sigLookup.Signature
					txData.FuncName = sigLookup.Name
				} else {
					txData.FuncName = "call?"
				}
			}
		}
	}

	// Enrich with EL data if execution indexer is enabled
	if utils.Config.ExecutionIndexer.Enabled && len(pageData.Transactions) > 0 {
		elTxs, err := db.GetElTransactionsByBlockUid(ctx, blockUid)
		if err == nil && len(elTxs) > 0 {
			for _, elTx := range elTxs {
				txData := txHashMap[string(elTx.TxHash)]
				if txData == nil {
					continue
				}

				txData.HasElData = true
				txData.Reverted = elTx.Reverted
				txData.GasUsed = elTx.GasUsed
				txData.EffGasPrice = elTx.EffGasPrice

				// Calculate tx fee in ETH: gas_used * eff_gas_price (Gwei) / 1e9
				if elTx.GasUsed > 0 && elTx.EffGasPrice > 0 {
					txData.TxFee = float64(elTx.GasUsed) * elTx.EffGasPrice / 1e9
				}
			}
		}
	}
}

func getSlotPageDepositRequests(pageData *models.SlotPageBlockData, depositRequests []*electra.DepositRequest) {
	pageData.DepositRequests = make([]*models.SlotPageDepositRequest, 0)

	for _, depositRequest := range depositRequests {
		receiptData := &models.SlotPageDepositRequest{
			PublicKey:       depositRequest.Pubkey[:],
			WithdrawalCreds: depositRequest.WithdrawalCredentials[:],
			Amount:          uint64(depositRequest.Amount),
			Signature:       depositRequest.Signature[:],
			Index:           depositRequest.Index,
		}

		if validatorIdx, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(depositRequest.Pubkey)); found {
			receiptData.Exists = true
			rawIndex := uint64(validatorIdx)
			if rawIndex&services.BuilderIndexFlag != 0 {
				receiptData.IsBuilder = true
				receiptData.ValidatorIndex = rawIndex &^ services.BuilderIndexFlag
			} else {
				receiptData.ValidatorIndex = rawIndex
			}
			receiptData.ValidatorName = services.GlobalBeaconService.GetValidatorName(rawIndex)
		}

		pageData.DepositRequests = append(pageData.DepositRequests, receiptData)
	}

	pageData.DepositRequestsCount = uint64(len(pageData.DepositRequests))
}

func getSlotPageWithdrawalRequests(pageData *models.SlotPageBlockData, withdrawalRequests []*electra.WithdrawalRequest) {
	pageData.WithdrawalRequests = make([]*models.SlotPageWithdrawalRequest, 0)

	for _, withdrawalRequest := range withdrawalRequests {
		requestData := &models.SlotPageWithdrawalRequest{
			Address:   withdrawalRequest.SourceAddress[:],
			PublicKey: withdrawalRequest.ValidatorPubkey[:],
			Amount:    uint64(withdrawalRequest.Amount),
		}

		if validatorIdx, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(withdrawalRequest.ValidatorPubkey)); found {
			requestData.Exists = true
			fullIndex := uint64(validatorIdx)
			if fullIndex&services.BuilderIndexFlag != 0 {
				requestData.IsBuilder = true
				requestData.ValidatorIndex = fullIndex &^ services.BuilderIndexFlag
			} else {
				requestData.ValidatorIndex = fullIndex
			}
			requestData.ValidatorName = services.GlobalBeaconService.GetValidatorName(fullIndex)
		}

		pageData.WithdrawalRequests = append(pageData.WithdrawalRequests, requestData)
	}

	pageData.WithdrawalRequestsCount = uint64(len(pageData.WithdrawalRequests))
}

func getSlotPageConsolidationRequests(pageData *models.SlotPageBlockData, consolidationRequests []*electra.ConsolidationRequest) {
	pageData.ConsolidationRequests = make([]*models.SlotPageConsolidationRequest, 0)

	for _, consolidationRequest := range consolidationRequests {
		requestData := &models.SlotPageConsolidationRequest{
			Address:      consolidationRequest.SourceAddress[:],
			SourcePubkey: consolidationRequest.SourcePubkey[:],
			TargetPubkey: consolidationRequest.TargetPubkey[:],
		}

		if sourceValidatorIdx, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(consolidationRequest.SourcePubkey)); found {
			requestData.SourceFound = true
			fullIndex := uint64(sourceValidatorIdx)
			if fullIndex&services.BuilderIndexFlag != 0 {
				requestData.SourceIsBuilder = true
				requestData.SourceIndex = fullIndex &^ services.BuilderIndexFlag
			} else {
				requestData.SourceIndex = fullIndex
			}
			requestData.SourceName = services.GlobalBeaconService.GetValidatorName(fullIndex)
		}

		if targetValidatorIdx, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(consolidationRequest.TargetPubkey)); found {
			requestData.TargetFound = true
			fullIndex := uint64(targetValidatorIdx)
			if fullIndex&services.BuilderIndexFlag != 0 {
				requestData.TargetIsBuilder = true
				requestData.TargetIndex = fullIndex &^ services.BuilderIndexFlag
			} else {
				requestData.TargetIndex = fullIndex
			}
			requestData.TargetName = services.GlobalBeaconService.GetValidatorName(fullIndex)
		}

		pageData.ConsolidationRequests = append(pageData.ConsolidationRequests, requestData)
	}

	pageData.ConsolidationRequestsCount = uint64(len(pageData.ConsolidationRequests))
}

func getSlotPageBuilderDeposits(ctx context.Context, pageData *models.SlotPageBlockData, builderDeposits []*gloas.BuilderDepositRequest) {
	pageData.BuilderDepositRequests = make([]*models.SlotPageBuilderDepositRequest, 0, len(builderDeposits))

	// resolve pubkeys -> builder indexes first, then batch-load the builders for those indexes to
	// tell whether each pubkey still owns its (reusable) index or was superseded.
	resolvedIdx := make([]gloas.BuilderIndex, len(builderDeposits))
	resolvedOk := make([]bool, len(builderDeposits))
	indexes := make([]gloas.BuilderIndex, 0, len(builderDeposits))
	for i, builderDeposit := range builderDeposits {
		if builderIdx, found := services.GlobalBeaconService.GetBuilderIndexByPubkey(builderDeposit.Pubkey); found {
			resolvedIdx[i] = builderIdx
			resolvedOk[i] = true
			indexes = append(indexes, builderIdx)
		}
	}
	builders := services.GlobalBeaconService.GetActiveBuildersByIndexes(ctx, indexes)

	for i, builderDeposit := range builderDeposits {
		requestData := &models.SlotPageBuilderDepositRequest{
			PublicKey:       builderDeposit.Pubkey[:],
			WithdrawalCreds: builderDeposit.WithdrawalCredentials,
			Amount:          uint64(builderDeposit.Amount),
			Signature:       builderDeposit.Signature[:],
		}

		if resolvedOk[i] {
			if b := builders[resolvedIdx[i]]; b != nil && bytes.Equal(b.PublicKey[:], builderDeposit.Pubkey[:]) {
				requestData.HasBuilderIndex = true
				requestData.BuilderIndex = uint64(resolvedIdx[i])
			} else {
				requestData.IsInactiveBuilder = true
			}
		}

		pageData.BuilderDepositRequests = append(pageData.BuilderDepositRequests, requestData)
	}

	pageData.BuilderDepositRequestsCount = uint64(len(pageData.BuilderDepositRequests))
}

func getSlotPageBuilderExits(ctx context.Context, pageData *models.SlotPageBlockData, builderExits []*gloas.BuilderExitRequest) {
	pageData.BuilderExitRequests = make([]*models.SlotPageBuilderExitRequest, 0, len(builderExits))

	resolvedIdx := make([]gloas.BuilderIndex, len(builderExits))
	resolvedOk := make([]bool, len(builderExits))
	indexes := make([]gloas.BuilderIndex, 0, len(builderExits))
	for i, builderExit := range builderExits {
		if builderIdx, found := services.GlobalBeaconService.GetBuilderIndexByPubkey(builderExit.Pubkey); found {
			resolvedIdx[i] = builderIdx
			resolvedOk[i] = true
			indexes = append(indexes, builderIdx)
		}
	}
	builders := services.GlobalBeaconService.GetActiveBuildersByIndexes(ctx, indexes)

	for i, builderExit := range builderExits {
		requestData := &models.SlotPageBuilderExitRequest{
			SourceAddress: builderExit.SourceAddress[:],
			PublicKey:     builderExit.Pubkey[:],
		}

		if resolvedOk[i] {
			if b := builders[resolvedIdx[i]]; b != nil && bytes.Equal(b.PublicKey[:], builderExit.Pubkey[:]) {
				requestData.HasBuilderIndex = true
				requestData.BuilderIndex = uint64(resolvedIdx[i])
			} else {
				requestData.IsInactiveBuilder = true
			}
		}

		pageData.BuilderExitRequests = append(pageData.BuilderExitRequests, requestData)
	}

	pageData.BuilderExitRequestsCount = uint64(len(pageData.BuilderExitRequests))
}

func getSlotPageExecutionProofs(pageData *models.SlotPageBlockData, blockRoot phase0.Root, slot uint64) {
	// Get a ready beacon client to fetch execution proofs
	beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
	client := beaconIndexer.GetReadyClient(false)
	if client == nil {
		// No client available, return empty proofs
		pageData.ExecutionProofsCount = 0
		pageData.ExecutionProofs = []*models.SlotPageExecutionProof{}
		return
	}

	// Fetch execution proofs from the beacon node
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	proofsResponse, err := client.GetClient().GetRPCClient().GetExecutionProofsByBlockroot(ctx, blockRoot[:])
	if err != nil {
		// Error fetching proofs, return empty list
		logrus.WithError(err).WithField("blockRoot", fmt.Sprintf("0x%x", blockRoot)).Info("Error fetching execution proofs")
		pageData.ExecutionProofsCount = 0
		pageData.ExecutionProofs = []*models.SlotPageExecutionProof{}
		return
	}

	// Block hash is sourced from the execution payload on the page itself,
	// since the Lighthouse execution_proofs response does not include it.
	var blockHash []byte
	if pageData.ExecutionData != nil {
		blockHash = pageData.ExecutionData.BlockHash
	}

	pageData.ExecutionProofs = make([]*models.SlotPageExecutionProof, 0, len(proofsResponse.Data))
	for _, proof := range proofsResponse.Data {
		proofData, err := hex.DecodeString(strings.TrimPrefix(proof.Message.ProofData, "0x"))
		if err != nil {
			logrus.WithError(err).WithField("proofType", proof.Message.ProofType).Warn("Error decoding proof data")
			continue
		}

		pageData.ExecutionProofs = append(pageData.ExecutionProofs, &models.SlotPageExecutionProof{
			ProofId:   proof.Message.ProofType,
			Slot:      slot,
			BlockHash: blockHash,
			BlockRoot: blockRoot[:],
			ProofData: proofData,
		})
	}
	pageData.ExecutionProofsCount = uint64(len(pageData.ExecutionProofs))
}

func getSlotPageBids(pageData *models.SlotPageBlockData, blockSlot phase0.Slot) {
	beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
	bids := beaconIndexer.GetBlockBids(phase0.Root(pageData.ParentRoot), blockSlot)

	pageData.Bids = make([]*models.SlotPageBid, 0, len(bids))

	// Get the winning block hash for comparison
	var winningBlockHash []byte
	if pageData.ExecutionData != nil {
		winningBlockHash = pageData.ExecutionData.BlockHash
	}

	for _, bid := range bids {
		bidData := &models.SlotPageBid{
			ParentRoot:   bid.ParentRoot,
			ParentHash:   bid.ParentHash,
			BlockHash:    bid.BlockHash,
			FeeRecipient: bid.FeeRecipient,
			GasLimit:     bid.GasLimit,
			BuilderIndex: uint64(bid.BuilderIndex),
			BuilderName:  services.GlobalBeaconService.GetValidatorName(uint64(bid.BuilderIndex) | services.BuilderIndexFlag),
			BuilderURL:   services.GlobalBeaconService.GetBuilderURL(uint64(bid.BuilderIndex)),
			IsSelfBuilt:  bid.BuilderIndex < 0,
			Slot:         bid.Slot,
			Value:        bid.Value,
			ElPayment:    bid.ElPayment,
			TotalValue:   bid.Value + bid.ElPayment,
		}

		// Check if this is the winning bid
		if winningBlockHash != nil && len(bid.BlockHash) == len(winningBlockHash) {
			isWinning := true
			for i := range bid.BlockHash {
				if bid.BlockHash[i] != winningBlockHash[i] {
					isWinning = false
					break
				}
			}
			bidData.IsWinning = isWinning
		}

		pageData.Bids = append(pageData.Bids, bidData)
	}

	// Sort by total value (value + el_payment) descending
	for i := 0; i < len(pageData.Bids)-1; i++ {
		for j := i + 1; j < len(pageData.Bids); j++ {
			if pageData.Bids[j].TotalValue > pageData.Bids[i].TotalValue {
				pageData.Bids[i], pageData.Bids[j] = pageData.Bids[j], pageData.Bids[i]
			}
		}
	}

	pageData.BidsCount = uint64(len(pageData.Bids))
}

// getSlotPagePtcVotes extracts PTC (Payload Timeliness Committee) votes from a Gloas block.
// PTC votes are included in blocks as payload attestations for the PREVIOUS slot.
func getSlotPagePtcVotes(pageData *models.SlotPageBlockData, blockData *services.CombinedBlockResponse, blockSlot phase0.Slot) {
	// Only Gloas+ blocks have payload attestations
	if blockData.Block.Version < spec.DataVersionGloas {
		return
	}

	if blockData.Block.Message == nil || blockData.Block.Message.Body == nil {
		return
	}

	payloadAttestations := blockData.Block.Message.Body.PayloadAttestations
	if len(payloadAttestations) == 0 {
		return
	}

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()

	// PTC votes are for the previous slot. ptcDuties (resolved by the caller via
	// the chain service) holds the PTC members for votedSlot.
	votedSlot := blockSlot - 1

	// PTC_SIZE is a spec constant (512). The Bitvector is always PTC_SIZE bits.
	// On small validator sets, validators appear multiple times in PTC duties
	// via weighted selection, but voting is tracked by bit position.
	ptcSize := specs.PtcSize

	// Build PTC votes structure
	ptcVotes := &models.SlotPagePtcVotes{
		VotedSlot:    uint64(votedSlot),
		TotalPtcSize: ptcSize,
		Aggregates:   make([]*models.SlotPagePtcAggregate, 0, len(payloadAttestations)),
	}

	// Track voted bit positions across all aggregates
	votedPositions := make(map[uint64]bool, ptcSize)
	totalVotes := uint64(0)

	for _, pa := range payloadAttestations {
		if pa == nil || pa.Data == nil {
			continue
		}

		// Set voted block root from first attestation
		if ptcVotes.VotedBlockRoot == nil {
			ptcVotes.VotedBlockRoot = pa.Data.BeaconBlockRoot[:]
		}

		aggregate := &models.SlotPagePtcAggregate{
			PayloadPresent:    pa.Data.PayloadPresent,
			BlobDataAvailable: pa.Data.BlobDataAvailable,
			AggregationBits:   pa.AggregationBits,
			Signature:         pa.Signature[:],
			Validators:        []types.NamedValidator{},
		}

		// Vote count comes from the raw aggregation bits; the member list is
		// loaded on demand via /slot/{votedSlot}/duties?ptc=1.
		bitCount := min(uint64(len(pa.AggregationBits))*8, ptcSize)
		bitVoteCount := uint64(0)
		for i := uint64(0); i < bitCount; i++ {
			if (pa.AggregationBits[i/8]>>(i%8))&1 != 1 {
				continue
			}
			votedPositions[i] = true
			bitVoteCount++
		}

		aggregate.VoteCount = bitVoteCount
		totalVotes += aggregate.VoteCount

		ptcVotes.Aggregates = append(ptcVotes.Aggregates, aggregate)
	}

	// Participation is seat-based: voted positions over the full PTC size. On
	// small validator sets the same validator holds multiple seats, so the seat
	// count (not the unique-validator count) is the correct denominator and keeps
	// the percentage within 0-100%.
	if ptcSize > 0 {
		totalVoted := uint64(len(votedPositions))
		ptcVotes.NonVoterCount = ptcSize - totalVoted
		ptcVotes.Participation = float64(totalVoted) / float64(ptcSize)
		ptcVotes.NonVoterPercent = float64(ptcVotes.NonVoterCount) / float64(ptcSize) * 100

		for _, agg := range ptcVotes.Aggregates {
			agg.VotePercent = float64(agg.VoteCount) / float64(ptcSize) * 100
		}
	}

	pageData.PtcVotes = ptcVotes
	pageData.PtcVotesCount = totalVotes
}

// getSlotPageInclusionLists fetches cached inclusion lists for the slot and decodes their transactions.
func getSlotPageInclusionLists(pageData *models.SlotPageBlockData, slot phase0.Slot) {
	beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
	inclusionLists := beaconIndexer.GetInclusionListsBySlot(slot)
	if len(inclusionLists) == 0 {
		return
	}

	// Build a set of block transaction hashes for inclusion checking
	blockTxHashes := make(map[string]bool, len(pageData.Transactions))
	for _, tx := range pageData.Transactions {
		blockTxHashes[string(tx.Hash)] = true
	}

	sysContracts := services.GlobalBeaconService.GetSystemContractAddresses()

	pageData.InclusionLists = make([]*models.SlotPageInclusionList, 0, len(inclusionLists))
	for _, il := range inclusionLists {
		ilData := &models.SlotPageInclusionList{
			Validator: types.NamedValidator{
				Index: uint64(il.Message.ValidatorIndex),
				Name:  services.GlobalBeaconService.GetValidatorName(uint64(il.Message.ValidatorIndex)),
			},
			InclusionListCommitteeRoot: il.Message.InclusionListCommitteeRoot[:],
			Signature:                  il.Signature[:],
		}

		ilData.Transactions = decodeInclusionListTransactions(il, sysContracts)
		ilData.TransactionsCount = uint64(len(ilData.Transactions))

		// Check which IL transactions are included in the block
		ilData.TransactionsIncluded = make([]bool, len(ilData.Transactions))
		for i, tx := range ilData.Transactions {
			ilData.TransactionsIncluded[i] = blockTxHashes[string(tx.Hash)]
		}

		pageData.InclusionLists = append(pageData.InclusionLists, ilData)
	}

	pageData.InclusionListsCount = uint64(len(pageData.InclusionLists))
}

// decodeInclusionListTransactions decodes the raw transactions from an inclusion list.
func decodeInclusionListTransactions(il *v1.SignedInclusionList, sysContracts map[common.Address]string) []*models.SlotPageTransaction {
	txList := make([]*models.SlotPageTransaction, 0, len(il.Message.Transactions))

	for idx, txBytes := range il.Message.Transactions {
		var tx ethtypes.Transaction

		err := tx.UnmarshalBinary(txBytes)
		if err != nil {
			logrus.Warnf("error decoding inclusion list transaction %v.%v: %v", il.Message.ValidatorIndex, idx, err)
			continue
		}

		txHash := tx.Hash()
		txBigFloat := new(big.Float).SetInt(tx.Value())
		txBigFloat.Quo(txBigFloat, new(big.Float).SetInt(utils.ETH))
		txValue, _ := txBigFloat.Float64()

		txType := uint8(tx.Type())
		typeName := slotTxTypeNames[txType]
		if typeName == "" {
			typeName = fmt.Sprintf("Type %d", txType)
		}

		txData := &models.SlotPageTransaction{
			Index:    uint64(idx),
			Hash:     txHash[:],
			Value:    txValue,
			Data:     tx.Data(),
			Type:     uint64(txType),
			TypeName: typeName,
			GasLimit: tx.Gas(),
		}
		txData.DataLen = uint64(len(txData.Data))

		chainId := tx.ChainId()
		if chainId != nil && chainId.Cmp(big.NewInt(0)) == 0 {
			chainId = nil
		}
		txFrom, err := ethtypes.Sender(ethtypes.LatestSignerForChainID(chainId), &tx)
		if err == nil {
			txData.From = txFrom.Bytes()
		}
		txTo := tx.To()
		if txTo != nil {
			txData.To = txTo.Bytes()
		}

		// Check call fn signature
		isCreate := txTo == nil
		if txData.DataLen >= 4 {
			if skip, altName := utils.ShouldSkipSignatureLookup(txData.To, isCreate, sysContracts); skip {
				txData.FuncSigStatus = 10
				txData.FuncName = altName
			} else {
				txData.FuncBytes = fmt.Sprintf("0x%x", txData.Data[0:4])
				txData.FuncName = txData.FuncBytes
				txData.FuncSigStatus = 0
			}
		} else {
			txData.FuncSigStatus = 10
			txData.FuncName = "transfer"
		}

		txList = append(txList, txData)
	}

	return txList
}

// handleSlotParseAccessList accepts RLP-encoded BAL bytes and returns the
// decoded, UI-ready representation as JSON. Used by the BAL inspector UI
// to preview arbitrary access lists pasted by the user.
func handleSlotParseAccessList(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		return fmt.Errorf("method not allowed")
	}

	rlpHex := r.PostFormValue("rlp")
	if rlpHex == "" {
		return fmt.Errorf("missing rlp parameter")
	}

	rlpHex = strings.TrimPrefix(rlpHex, "0x")

	rlpBytes, err := hex.DecodeString(rlpHex)
	if err != nil {
		return fmt.Errorf("invalid hex: %v", err)
	}

	accesses, err := utils.DecodeBlockAccessList(rlpBytes)
	if err != nil {
		return fmt.Errorf("invalid access list RLP: %v", err)
	}

	return json.NewEncoder(w).Encode(convertBALToModel(accesses))
}

// convertBALToModel converts decoded EIP-7928 BAL entries to the UI model.
func convertBALToModel(accesses []utils.BALAccountAccess) []*models.SlotPageBlockAccessListEntry {
	result := make([]*models.SlotPageBlockAccessListEntry, len(accesses))
	for i, entry := range accesses {
		balEntry := &models.SlotPageBlockAccessListEntry{
			Address: entry.Address[:],
		}

		if len(entry.StorageWrites) > 0 {
			balEntry.StorageChanges = make([]*models.SlotPageBlockBALStorageChange, len(entry.StorageWrites))
			for j, storageChange := range entry.StorageWrites {
				changes := make([]*models.SlotPageBlockBALStorageSlotChange, len(storageChange.Accesses))
				for k, write := range storageChange.Accesses {
					changes[k] = &models.SlotPageBlockBALStorageSlotChange{
						BlockAccessIndex: write.TxIdx,
						Value:            write.ValueAfter,
					}
				}
				balEntry.StorageChanges[j] = &models.SlotPageBlockBALStorageChange{
					Slot:    storageChange.Slot,
					Changes: changes,
				}
			}
		}

		if len(entry.StorageReads) > 0 {
			balEntry.StorageReads = make([][]byte, len(entry.StorageReads))
			copy(balEntry.StorageReads, entry.StorageReads)
		}

		if len(entry.BalanceChanges) > 0 {
			balEntry.BalanceChanges = make([]*models.SlotPageBlockBALBalanceChange, len(entry.BalanceChanges))
			for j, balanceChange := range entry.BalanceChanges {
				balEntry.BalanceChanges[j] = &models.SlotPageBlockBALBalanceChange{
					BlockAccessIndex: balanceChange.TxIdx,
					Balance:          balanceChange.Balance,
				}
			}
		}

		if len(entry.NonceChanges) > 0 {
			balEntry.NonceChanges = make([]*models.SlotPageBlockBALNonceChange, len(entry.NonceChanges))
			for j, nonceChange := range entry.NonceChanges {
				balEntry.NonceChanges[j] = &models.SlotPageBlockBALNonceChange{
					BlockAccessIndex: nonceChange.TxIdx,
					Nonce:            nonceChange.Nonce,
				}
			}
		}

		if len(entry.CodeChanges) > 0 {
			balEntry.CodeChanges = make([]*models.SlotPageBlockBALCodeChange, len(entry.CodeChanges))
			for j, codeChange := range entry.CodeChanges {
				balEntry.CodeChanges[j] = &models.SlotPageBlockBALCodeChange{
					BlockAccessIndex: codeChange.TxIndex,
					Code:             codeChange.Code,
				}
			}
		}

		result[i] = balEntry
	}

	return result
}

// computeBALSummary aggregates block-level BAL statistics for EIP-8038 visibility.
func computeBALSummary(entries []*models.SlotPageBlockAccessListEntry) *models.SlotPageBALSummary {
	if len(entries) == 0 {
		return nil
	}
	s := &models.SlotPageBALSummary{
		UniqueAddresses: uint64(len(entries)),
	}
	for _, e := range entries {
		s.StorageSlotWrites += uint64(len(e.StorageChanges))
		for _, sc := range e.StorageChanges {
			s.StorageWriteOps += uint64(len(sc.Changes))
		}
		s.ColdStorageReads += uint64(len(e.StorageReads))
		s.BalanceChanges += uint64(len(e.BalanceChanges))
		s.CodeChanges += uint64(len(e.CodeChanges))
		s.NonceChanges += uint64(len(e.NonceChanges))
	}
	return s
}
