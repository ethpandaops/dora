package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/bellatrix"
	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
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
		"slot/deposits.html",
		"slot/withdrawals.html",
		"slot/voluntary_exits.html",
		"slot/slashings.html",
		"slot/blobs.html",
		"slot/deposit_requests.html",
		"slot/withdrawal_requests.html",
		"slot/consolidation_requests.html",
		"slot/bids.html",
		"slot/ptc_votes.html",
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
	if err != nil || blockData == nil || blockData.Block == nil {
		http.Error(w, "Block not found", http.StatusNotFound)
		return
	}

	commitments, err := blockData.Block.BlobKZGCommitments()
	if err != nil || int(blobIndex) >= len(commitments) {
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
			epochStatsValues = epochStats.GetOrLoadValues(beaconIndexer, true, false)
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
	slotBlocks, slotBlockProposers := getSlotBlocks(slot, blockRoot, blockData)
	pageData.SlotBlocks = slotBlocks

	if blockData == nil {
		pageData.Status = uint16(models.SlotStatusMissed)
		pageData.Proposer = math.MaxInt64
		if epochStatsValues != nil {
			if slotIndex := int(chainState.SlotToSlotIndex(slot)); slotIndex < len(epochStatsValues.ProposerDuties) {
				pageData.Proposer = uint64(epochStatsValues.ProposerDuties[slotIndex])
			}
		}
		if pageData.Proposer == math.MaxInt64 {
			pageData.Proposer = db.GetSlotAssignment(uint64(slot))
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
		} else if dbBlock := db.GetBlockHeadByRoot(blockData.Root[:]); dbBlock != nil {
			blockUid = dbBlock.BlockUid
		}
		pageData.Block = getSlotPageBlockData(blockData, epochStatsValues, blockUid)

		// check mev block
		if pageData.Block.ExecutionData != nil {
			mevBlock := db.GetMevBlockByBlockHash(pageData.Block.ExecutionData.BlockHash)
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

	return pageData, cacheTimeout
}

// getSlotBlocks retrieves all blocks for a given slot and builds the SlotBlocks slice
// for the multi-block display. Uses GetDbBlocksByFilter which handles both cache and database.
// Also returns a list of proposers from orphaned blocks (used as fallback when proposer is unknown).
func getSlotBlocks(slot phase0.Slot, currentBlockRoot []byte, currentBlockData *services.CombinedBlockResponse) ([]*models.SlotPageSlotBlock, []uint64) {
	slotBlocks := make([]*models.SlotPageSlotBlock, 0)
	orphanedProposers := make([]uint64, 0)
	hasCanonicalOrMissed := false

	// Get all blocks for the slot (from cache and database)
	slotNum := uint64(slot)
	dbBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(&dbtypes.BlockFilter{
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

func getSlotPageBlockData(blockData *services.CombinedBlockResponse, epochStatsValues *beacon.EpochStatsValues, blockUid uint64) *models.SlotPageBlockData {
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	graffiti, _ := blockData.Block.Graffiti()
	randaoReveal, _ := blockData.Block.RandaoReveal()
	eth1Data, _ := blockData.Block.ETH1Data()
	attestations, _ := blockData.Block.Attestations()
	deposits, _ := blockData.Block.Deposits()
	voluntaryExits, _ := blockData.Block.VoluntaryExits()
	attesterSlashings, _ := blockData.Block.AttesterSlashings()
	proposerSlashings, _ := blockData.Block.ProposerSlashings()
	blsToExecChanges, _ := blockData.Block.BLSToExecutionChanges()
	syncAggregate, _ := blockData.Block.SyncAggregate()
	executionWithdrawals, _ := blockData.Block.Withdrawals()
	blobKzgCommitments, _ := blockData.Block.BlobKZGCommitments()
	//consolidations, _ := blockData.Block.Consolidations()

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
		ValidatorNames:         make(map[uint64]string),
		SpecValues:             make(map[string]interface{}),
		ProposerSlashingsCount: uint64(len(proposerSlashings)),
		AttesterSlashingsCount: uint64(len(attesterSlashings)),
		AttestationsCount:      uint64(len(attestations)),
		DepositsCount:          uint64(len(deposits)),
		VoluntaryExitsCount:    uint64(len(voluntaryExits)),
		SlashingsCount:         uint64(len(proposerSlashings)) + uint64(len(attesterSlashings)),
	}

	pageData.SpecValues["max_committees_per_slot"] = specs.MaxCommitteesPerSlot
	pageData.SpecValues["target_committee_size"] = specs.TargetCommitteeSize
	pageData.SpecValues["slots_per_epoch"] = specs.SlotsPerEpoch

	epoch := chainState.EpochOfSlot(blockData.Header.Message.Slot)
	assignmentsMap := make(map[phase0.Epoch]*beacon.EpochStatsValues)
	assignmentsLoaded := make(map[phase0.Epoch]bool)
	dbEpochMap := make(map[phase0.Epoch]*dbtypes.Epoch)
	dbEpochLoaded := make(map[phase0.Epoch]bool)
	assignmentsMap[epoch] = epochStatsValues
	assignmentsLoaded[epoch] = true

	attHeadBlocks := make(map[phase0.Root]phase0.Slot)

	pageData.Attestations = make([]*models.SlotPageAttestation, pageData.AttestationsCount)
	for i, attVersioned := range attestations {
		attData, _ := attVersioned.Data()
		if attData == nil {
			continue
		}

		attSignature, err := attVersioned.Signature()
		if err != nil {
			continue
		}

		attAggregationBits, err := attVersioned.AggregationBits()
		if err != nil {
			continue
		}

		totalActiveValidators := uint64(0)

		attEpoch := chainState.EpochOfSlot(attData.Slot)
		if !assignmentsLoaded[attEpoch] { // get epoch duties from cache
			assignmentsLoaded[attEpoch] = true
			beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
			if epochStats := beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
				epochStatsValues := epochStats.GetOrLoadValues(beaconIndexer, true, false)

				assignmentsMap[attEpoch] = epochStatsValues
			}
		}

		if assignmentsMap[attEpoch] != nil {
			totalActiveValidators = assignmentsMap[attEpoch].ActiveValidators
		} else {
			if !dbEpochLoaded[attEpoch] {
				dbEpochLoaded[attEpoch] = true
				dbEpochs := db.GetEpochs(uint64(attEpoch), 1)
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
			beaconBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(&dbtypes.BlockFilter{
				BlockRoot: attData.BeaconBlockRoot[:],
			}, 0, 1, 0)
			if len(beaconBlocks) > 0 {
				slot := phase0.Slot(beaconBlocks[0].Slot)
				attHeadBlocks[attData.BeaconBlockRoot] = slot
				attPageData.BeaconBlockSlot = uint64(slot)
			}
		}

		var attAssignments []uint64
		includedValidators := []uint64{}
		attEpochStatsValues := assignmentsMap[attEpoch]

		if attVersioned.Version >= spec.DataVersionElectra {
			// EIP-7549 attestation
			attAssignments = []uint64{}
			attPageData.CommitteeIndex = []uint64{}

			committeeBits, err := attVersioned.CommitteeBits()
			if err != nil {
				continue
			}

			attBitsOffset := uint64(0)
			for _, committee := range committeeBits.BitIndices() {
				if uint64(committee) >= specs.MaxCommitteesPerSlot {
					continue
				}

				attPageData.CommitteeIndex = append(attPageData.CommitteeIndex, uint64(committee))
				if attEpochStatsValues != nil {
					slotIndex := int(chainState.SlotToSlotIndex(attData.Slot))
					committeeAssignments := attEpochStatsValues.AttesterDuties[slotIndex][uint64(committee)]
					if len(committeeAssignments) == 0 {
						break
					}

					committeeAssignmentsInt := make([]uint64, 0)
					for j := 0; j < len(committeeAssignments); j++ {
						validatorIndex := attEpochStatsValues.ActiveIndices[committeeAssignments[j]]
						if attAggregationBits.BitAt(attBitsOffset + uint64(j)) {
							includedValidators = append(includedValidators, uint64(validatorIndex))
						}
						committeeAssignmentsInt = append(committeeAssignmentsInt, uint64(validatorIndex))
					}

					attBitsOffset += uint64(len(committeeAssignments))
					attAssignments = append(attAssignments, committeeAssignmentsInt...)
				}
			}
		} else {
			// pre-electra attestation
			if attEpochStatsValues != nil {
				slotIndex := int(chainState.SlotToSlotIndex(attData.Slot))
				committeeAssignments := attEpochStatsValues.AttesterDuties[slotIndex][uint64(attData.Index)]
				committeeAssignmentsInt := make([]uint64, 0)
				for j := 0; j < len(committeeAssignments); j++ {
					validatorIndex := attEpochStatsValues.ActiveIndices[committeeAssignments[j]]
					if attAggregationBits.BitAt(uint64(j)) {
						includedValidators = append(includedValidators, uint64(validatorIndex))
					}
					committeeAssignmentsInt = append(committeeAssignmentsInt, uint64(validatorIndex))
				}

				attAssignments = committeeAssignmentsInt
			} else {
				attAssignments = []uint64{}
			}

			attPageData.CommitteeIndex = []uint64{uint64(attData.Index)}
		}

		attPageData.Validators = attAssignments
		for j := 0; j < len(attAssignments); j++ {
			if _, found := pageData.ValidatorNames[attAssignments[j]]; !found {
				pageData.ValidatorNames[attAssignments[j]] = services.GlobalBeaconService.GetValidatorName(attAssignments[j])
			}
		}

		attPageData.IncludedValidators = includedValidators
		for j := 0; j < len(includedValidators); j++ {
			if _, found := pageData.ValidatorNames[includedValidators[j]]; !found {
				pageData.ValidatorNames[includedValidators[j]] = services.GlobalBeaconService.GetValidatorName(includedValidators[j])
			}
		}

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
		att1, _ := slashing.Attestation1()
		att2, _ := slashing.Attestation2()
		if att1 == nil || att2 == nil {
			continue
		}

		att1AttestingIndices, _ := att1.AttestingIndices()
		att2AttestingIndices, _ := att2.AttestingIndices()
		if att1AttestingIndices == nil || att2AttestingIndices == nil {
			continue
		}

		att1Signature, err1 := att1.Signature()
		att2Signature, err2 := att2.Signature()
		if err1 != nil || err2 != nil {
			continue
		}

		att1Data, err1 := att1.Data()
		att2Data, err2 := att2.Data()
		if err1 != nil || err2 != nil {
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
			syncAssignments = db.GetSyncAssignmentsForPeriod(syncPeriod)
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

	if payloadBid, err := blockData.Block.SignedExecutionPayloadBid(); err == nil {
		pageData.PayloadHeader = &models.SlotPagePayloadHeader{
			PayloadStatus:          uint16(0),
			ParentBlockHash:        payloadBid.Message.ParentBlockHash[:],
			ParentBlockRoot:        payloadBid.Message.ParentBlockRoot[:],
			BlockHash:              payloadBid.Message.BlockHash[:],
			GasLimit:               uint64(payloadBid.Message.GasLimit),
			BuilderIndex:           uint64(payloadBid.Message.BuilderIndex),
			BuilderName:            services.GlobalBeaconService.GetValidatorName(uint64(payloadBid.Message.BuilderIndex) | services.BuilderIndexFlag),
			Slot:                   uint64(payloadBid.Message.Slot),
			Value:                  uint64(payloadBid.Message.Value),
			BlobKzgCommitmentsRoot: payloadBid.Message.BlobKZGCommitmentsRoot[:],
			Signature:              payloadBid.Signature[:],
		}
	}

	var executionPayload *spec.VersionedExecutionPayload
	if blockData.Block.Version >= spec.DataVersionGloas && blockData.Payload != nil {
		blobKzgCommitments = blockData.Payload.Message.BlobKZGCommitments
		executionPayload = &spec.VersionedExecutionPayload{
			Version: spec.DataVersionGloas,
			Gloas:   blockData.Payload.Message.Payload,
		}

		pageData.PayloadHeader.PayloadStatus = uint16(1)
	} else {
		executionPayload, _ = blockData.Block.ExecutionPayload()
	}

	if executionPayload != nil {
		pageData.ExecutionData = &models.SlotPageExecutionData{}

		if parentHash, err := executionPayload.ParentHash(); err == nil {
			pageData.ExecutionData.ParentHash = parentHash[:]
		}

		if feeRecipient, err := executionPayload.FeeRecipient(); err == nil {
			pageData.ExecutionData.FeeRecipient = feeRecipient[:]
		}

		if stateRoot, err := executionPayload.StateRoot(); err == nil {
			pageData.ExecutionData.StateRoot = stateRoot[:]
		}

		if receiptsRoot, err := executionPayload.ReceiptsRoot(); err == nil {
			pageData.ExecutionData.ReceiptsRoot = receiptsRoot[:]
		}

		if logsBloom, err := executionPayload.LogsBloom(); err == nil {
			pageData.ExecutionData.LogsBloom = logsBloom[:]
		}

		if random, err := executionPayload.PrevRandao(); err == nil {
			pageData.ExecutionData.Random = random[:]
		}

		if gasLimit, err := executionPayload.GasLimit(); err == nil {
			pageData.ExecutionData.GasLimit = uint64(gasLimit)
		}

		if gasUsed, err := executionPayload.GasUsed(); err == nil {
			pageData.ExecutionData.GasUsed = uint64(gasUsed)
		}

		if timestamp, err := executionPayload.Timestamp(); err == nil {
			pageData.ExecutionData.Timestamp = uint64(timestamp)
			pageData.ExecutionData.Time = time.Unix(int64(timestamp), 0)
		}

		if extraData, err := executionPayload.ExtraData(); err == nil {
			pageData.ExecutionData.ExtraData = extraData
		}

		if baseFeePerGas, err := executionPayload.BaseFeePerGas(); err == nil {
			pageData.ExecutionData.BaseFeePerGas = baseFeePerGas.Uint64()
		}

		if blockHash, err := executionPayload.BlockHash(); err == nil {
			pageData.ExecutionData.BlockHash = blockHash[:]
		}

		if blockNumber, err := executionPayload.BlockNumber(); err == nil {
			pageData.ExecutionData.BlockNumber = uint64(blockNumber)
		}

		if excessBlobGas, err := executionPayload.ExcessBlobGas(); err == nil {
			pageData.ExecutionData.ExcessBlobGas = &excessBlobGas
		}

		if blobGasUsed, err := executionPayload.BlobGasUsed(); err == nil {
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

		if transactions, err := executionPayload.Transactions(); err == nil {
			getSlotPageTransactions(pageData, transactions, blockUid)
		}

		// Check if execution data exists in blockdb for receipt downloads
		if blockdb.GlobalBlockDb != nil && blockdb.GlobalBlockDb.SupportsExecData() {
			hasExecData, _ := blockdb.GlobalBlockDb.HasExecData(
				context.Background(),
				uint64(blockData.Header.Message.Slot),
				blockData.Root[:],
			)
			pageData.ExecutionData.HasExecData = hasExecData
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
		for i, withdrawal := range executionWithdrawals {
			pageData.Withdrawals[i] = &models.SlotPageWithdrawal{
				Index:          uint64(withdrawal.Index),
				ValidatorIndex: uint64(withdrawal.ValidatorIndex),
				ValidatorName:  services.GlobalBeaconService.GetValidatorName(uint64(withdrawal.ValidatorIndex)),
				Address:        withdrawal.Address[:],
				Amount:         uint64(withdrawal.Amount),
			}
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
		var requests *electra.ExecutionRequests
		if blockData.Block.Version >= spec.DataVersionGloas {
			if blockData.Payload != nil {
				requests = blockData.Payload.Message.ExecutionRequests
			}
		} else {
			requests, _ = blockData.Block.ExecutionRequests()
		}

		if requests != nil {
			getSlotPageDepositRequests(pageData, requests.Deposits)
			getSlotPageWithdrawalRequests(pageData, requests.Withdrawals)
			getSlotPageConsolidationRequests(pageData, requests.Consolidations)
		}
	}

	// Load execution payload bids for ePBS (gloas+) blocks
	if blockData.Block.Version >= spec.DataVersionGloas {
		getSlotPageBids(pageData)
		getSlotPagePtcVotes(pageData, blockData, blockData.Header.Message.Slot)
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

func getSlotPageTransactions(pageData *models.SlotPageBlockData, transactions []bellatrix.Transaction, blockUid uint64) {
	pageData.Transactions = make([]*models.SlotPageTransaction, 0)
	sigLookupBytes := []types.TxSignatureBytes{}
	sigLookupMap := map[types.TxSignatureBytes][]*models.SlotPageTransaction{}

	// Build a map of tx hash to tx data for EL enrichment
	txHashMap := make(map[string]*models.SlotPageTransaction, len(transactions))

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
		if txData.DataLen >= 4 {
			sigBytes := types.TxSignatureBytes(txData.Data[0:4])
			if sigLookupMap[sigBytes] == nil {
				sigLookupMap[sigBytes] = []*models.SlotPageTransaction{
					txData,
				}
				sigLookupBytes = append(sigLookupBytes, sigBytes)
			} else {
				sigLookupMap[sigBytes] = append(sigLookupMap[sigBytes], txData)
			}
		} else {
			txData.FuncSigStatus = 10
			txData.FuncName = "transfer"
		}
	}
	pageData.TransactionsCount = uint64(len(transactions))

	if len(sigLookupBytes) > 0 {
		sigLookups := services.GlobalTxSignaturesService.LookupSignatures(sigLookupBytes)
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
		elTxs, err := db.GetElTransactionsByBlockUid(blockUid)
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
			receiptData.ValidatorIndex = uint64(validatorIdx)
			receiptData.ValidatorName = services.GlobalBeaconService.GetValidatorName(receiptData.ValidatorIndex)
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
			requestData.ValidatorIndex = uint64(validatorIdx)
			requestData.ValidatorName = services.GlobalBeaconService.GetValidatorName(requestData.ValidatorIndex)
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
			requestData.SourceIndex = uint64(sourceValidatorIdx)
			requestData.SourceName = services.GlobalBeaconService.GetValidatorName(requestData.SourceIndex)
		}

		if targetValidatorIdx, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(consolidationRequest.TargetPubkey)); found {
			requestData.TargetFound = true
			requestData.TargetIndex = uint64(targetValidatorIdx)
			requestData.TargetName = services.GlobalBeaconService.GetValidatorName(requestData.TargetIndex)
		}

		pageData.ConsolidationRequests = append(pageData.ConsolidationRequests, requestData)
	}

	pageData.ConsolidationRequestsCount = uint64(len(pageData.ConsolidationRequests))
}

func getSlotPageBids(pageData *models.SlotPageBlockData) {
	beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
	bids := beaconIndexer.GetBlockBids(phase0.Root(pageData.ParentRoot))

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
			BuilderIndex: bid.BuilderIndex,
			BuilderName:  services.GlobalBeaconService.GetValidatorName(bid.BuilderIndex),
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
	if blockData.Block.Version < spec.DataVersionGloas || blockData.Block.Gloas == nil {
		return
	}

	payloadAttestations := blockData.Block.Gloas.Message.Body.PayloadAttestations
	if len(payloadAttestations) == 0 {
		return
	}

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()

	// PTC votes are for the previous slot
	votedSlot := blockSlot - 1
	votedEpoch := chainState.EpochOfSlot(votedSlot)

	// Get epoch stats for the voted slot to retrieve PTC duties
	var ptcDuties []phase0.ValidatorIndex
	beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
	epochStats := beaconIndexer.GetEpochStatsByEpoch(votedEpoch)
	for _, es := range epochStats {
		values := es.GetValues(true)
		if values != nil && values.PtcDuties != nil {
			slotInEpoch := uint64(votedSlot) % specs.SlotsPerEpoch
			if slotInEpoch < uint64(len(values.PtcDuties)) && values.PtcDuties[slotInEpoch] != nil {
				// Convert from active indice indices to validator indices
				ptcDuties = make([]phase0.ValidatorIndex, len(values.PtcDuties[slotInEpoch]))
				for i, activeIdx := range values.PtcDuties[slotInEpoch] {
					if int(activeIdx) < len(values.ActiveIndices) {
						ptcDuties[i] = values.ActiveIndices[activeIdx]
					}
				}
				break
			}
		}
	}

	// Build PTC votes structure
	ptcVotes := &models.SlotPagePtcVotes{
		VotedSlot:    uint64(votedSlot),
		TotalPtcSize: specs.PtcSize,
		Aggregates:   make([]*models.SlotPagePtcAggregate, 0, len(payloadAttestations)),
	}

	// Track participating validators across all aggregates
	participatingValidators := make(map[uint64]bool)
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
			Validators:        make([]uint64, 0),
		}

		// Map aggregation bits to validator indices
		if len(ptcDuties) > 0 {
			for i := 0; i < len(ptcDuties) && i < len(pa.AggregationBits)*8; i++ {
				byteIdx := i / 8
				bitIdx := i % 8
				if byteIdx < len(pa.AggregationBits) && (pa.AggregationBits[byteIdx]>>bitIdx)&1 == 1 {
					validatorIdx := uint64(ptcDuties[i])
					aggregate.Validators = append(aggregate.Validators, validatorIdx)
					participatingValidators[validatorIdx] = true
				}
			}
		}

		aggregate.VoteCount = uint64(len(aggregate.Validators))
		totalVotes += aggregate.VoteCount

		ptcVotes.Aggregates = append(ptcVotes.Aggregates, aggregate)
	}

	// Build PTC committee list
	ptcVotes.PtcCommittee = make([]types.NamedValidator, len(ptcDuties))
	for i, vidx := range ptcDuties {
		ptcVotes.PtcCommittee[i] = types.NamedValidator{
			Index: uint64(vidx),
			Name:  services.GlobalBeaconService.GetValidatorName(uint64(vidx)),
		}

		// Add to validator names map
		if pageData.ValidatorNames == nil {
			pageData.ValidatorNames = make(map[uint64]string)
		}
		pageData.ValidatorNames[uint64(vidx)] = ptcVotes.PtcCommittee[i].Name
	}

	// Calculate participation rate
	if specs.PtcSize > 0 {
		ptcVotes.Participation = float64(len(participatingValidators)) / float64(specs.PtcSize)
	}

	pageData.PtcVotes = ptcVotes
	pageData.PtcVotesCount = totalVotes
}
