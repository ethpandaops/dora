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
	"strconv"
	"strings"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/bellatrix"
	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/gorilla/mux"
	"github.com/juliangruber/go-intersect"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
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
		commitment, err1 := hex.DecodeString(strings.Replace(urlArgs.Get("blob"), "0x", "", -1))
		blobData, err2 := services.GlobalBeaconService.GetBlockBlob(r.Context(), phase0.Root(pageData.Block.BlockRoot), deneb.KZGCommitment(commitment))
		if err1 == nil && err2 == nil && blobData != nil {
			var blobModel *models.SlotPageBlob
			for _, blob := range pageData.Block.Blobs {
				if bytes.Equal(blob.KzgCommitment, commitment) {
					blobModel = blob
					break
				}
			}
			if blobModel != nil {
				blobModel.KzgProof = blobData.KZGProof[:]
				blobModel.HaveData = true
				blobModel.Blob = blobData.Blob[:]
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
	commitment, err := hex.DecodeString(strings.Replace(vars["commitment"], "0x", "", -1))
	if err != nil || len(commitment) != 48 {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	blockRoot, err := hex.DecodeString(strings.Replace(vars["root"], "0x", "", -1))
	if err != nil || len(blockRoot) != 32 {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	blobData, err := services.GlobalBeaconService.GetBlockBlob(r.Context(), phase0.Root(blockRoot), deneb.KZGCommitment(commitment))
	if err != nil {
		logrus.WithError(err).Error("error loading blob data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
	result := &models.SlotPageBlobDetails{
		KzgCommitment: fmt.Sprintf("%x", blobData.KZGCommitment),
		KzgProof:      fmt.Sprintf("%x", blobData.KZGProof),
		Blob:          fmt.Sprintf("%x", blobData.Blob),
	}
	err = json.NewEncoder(w).Encode(result)
	if err != nil {
		logrus.WithError(err).Error("error encoding blob sidecar")
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
		EpochFinalized: finalizedEpoch > chainState.EpochOfSlot(slot),
		Badges:         []*models.SlotPageBlockBadge{},
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
		pageData.ProposerName = services.GlobalBeaconService.GetValidatorName(pageData.Proposer)
	} else {
		if blockData.Orphaned {
			pageData.Status = uint16(models.SlotStatusOrphaned)
		} else {
			pageData.Status = uint16(models.SlotStatusFound)
		}
		pageData.Proposer = uint64(blockData.Header.Message.ProposerIndex)
		pageData.ProposerName = services.GlobalBeaconService.GetValidatorName(pageData.Proposer)
		pageData.Block = getSlotPageBlockData(blockData, epochStatsValues)

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

func getSlotPageBlockData(blockData *services.CombinedBlockResponse, epochStatsValues *beacon.EpochStatsValues) *models.SlotPageBlockData {
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
		Signature:              blockData.Header.Signature[:],
		RandaoReveal:           randaoReveal[:],
		Graffiti:               graffiti[:],
		Eth1dataDepositroot:    eth1Data.DepositRoot[:],
		Eth1dataDepositcount:   eth1Data.DepositCount,
		Eth1dataBlockhash:      eth1Data.BlockHash,
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
	assignmentsMap[epoch] = epochStatsValues
	assignmentsLoaded[epoch] = true

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

		attEpoch := chainState.EpochOfSlot(attData.Slot)
		if !assignmentsLoaded[attEpoch] { // get epoch duties from cache
			beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
			if epochStats := beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
				epochStatsValues := epochStats.GetOrLoadValues(beaconIndexer, true, false)

				assignmentsMap[attEpoch] = epochStatsValues
				assignmentsLoaded[attEpoch] = true
			}
		}

		attPageData := models.SlotPageAttestation{
			Slot:            uint64(attData.Slot),
			AggregationBits: attAggregationBits,
			Signature:       attSignature[:],
			BeaconBlockRoot: attData.BeaconBlockRoot[:],
			SourceEpoch:     uint64(attData.Source.Epoch),
			SourceRoot:      attData.Source.Root[:],
			TargetEpoch:     uint64(attData.Target.Epoch),
			TargetRoot:      attData.Target.Root[:],
		}

		var attAssignments []uint64
		includedValidators := []uint64{}

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
				if assignmentsMap[attEpoch] != nil {
					slotIndex := int(chainState.SlotToSlotIndex(attData.Slot))
					committeeAssignments := assignmentsMap[attEpoch].AttesterDuties[slotIndex][uint64(committee)]
					if len(committeeAssignments) == 0 {
						break
					}

					committeeAssignmentsInt := make([]uint64, 0)
					for j := 0; j < len(committeeAssignments); j++ {
						if attAggregationBits.BitAt(attBitsOffset + uint64(j)) {
							includedValidators = append(includedValidators, uint64(committeeAssignments[j]))
						}
						committeeAssignmentsInt = append(committeeAssignmentsInt, uint64(committeeAssignments[j]))
					}

					attBitsOffset += uint64(len(committeeAssignments))
					attAssignments = append(attAssignments, committeeAssignmentsInt...)
				}
			}
		} else {
			// pre-electra attestation
			if assignmentsMap[attEpoch] != nil {
				slotIndex := int(chainState.SlotToSlotIndex(attData.Slot))
				committeeAssignments := assignmentsMap[attEpoch].AttesterDuties[slotIndex][uint64(attData.Index)]
				committeeAssignmentsInt := make([]uint64, 0)
				for j := 0; j < len(committeeAssignments); j++ {
					if attAggregationBits.BitAt(uint64(j)) {
						includedValidators = append(includedValidators, uint64(committeeAssignments[j]))
					}
					committeeAssignmentsInt = append(committeeAssignmentsInt, uint64(committeeAssignments[j]))
				}

				attAssignments = committeeAssignmentsInt
			} else {
				attAssignments = []uint64{}
			}

			attPageData.CommitteeIndex = []uint64{uint64(attData.Index)}
		}

		attPageData.Validators = make([]types.NamedValidator, len(attAssignments))
		for j := 0; j < len(attAssignments); j++ {
			attPageData.Validators[j] = types.NamedValidator{
				Index: attAssignments[j],
				Name:  services.GlobalBeaconService.GetValidatorName(attAssignments[j]),
			}
		}

		attPageData.IncludedValidators = make([]types.NamedValidator, len(includedValidators))
		for j := 0; j < len(includedValidators); j++ {
			attPageData.IncludedValidators[j] = types.NamedValidator{
				Index: includedValidators[j],
				Name:  services.GlobalBeaconService.GetValidatorName(includedValidators[j]),
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
		pageData.VoluntaryExits[i] = &models.SlotPageVoluntaryExit{
			ValidatorIndex: uint64(exit.Message.ValidatorIndex),
			ValidatorName:  services.GlobalBeaconService.GetValidatorName(uint64(exit.Message.ValidatorIndex)),
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
		inter := intersect.Simple(att1AttestingIndices, att2AttestingIndices)
		for _, j := range inter {
			valIdx := j.(uint64)
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

	if specs.BellatrixForkEpoch != nil && uint64(epoch) >= *specs.BellatrixForkEpoch {
		switch blockData.Block.Version {
		case spec.DataVersionBellatrix:
			if blockData.Block.Bellatrix == nil {
				break
			}
			executionPayload := blockData.Block.Bellatrix.Message.Body.ExecutionPayload
			var baseFeePerGasBEBytes [32]byte
			for i := 0; i < 32; i++ {
				baseFeePerGasBEBytes[i] = executionPayload.BaseFeePerGas[32-1-i]
			}
			baseFeePerGas := new(big.Int).SetBytes(baseFeePerGasBEBytes[:])
			pageData.ExecutionData = &models.SlotPageExecutionData{
				ParentHash:    executionPayload.ParentHash[:],
				FeeRecipient:  executionPayload.FeeRecipient[:],
				StateRoot:     executionPayload.StateRoot[:],
				ReceiptsRoot:  executionPayload.ReceiptsRoot[:],
				LogsBloom:     executionPayload.LogsBloom[:],
				Random:        executionPayload.PrevRandao[:],
				GasLimit:      uint64(executionPayload.GasLimit),
				GasUsed:       uint64(executionPayload.GasUsed),
				Timestamp:     uint64(executionPayload.Timestamp),
				Time:          time.Unix(int64(executionPayload.Timestamp), 0),
				ExtraData:     executionPayload.ExtraData,
				BaseFeePerGas: baseFeePerGas.Uint64(),
				BlockHash:     executionPayload.BlockHash[:],
				BlockNumber:   uint64(executionPayload.BlockNumber),
			}
			getSlotPageTransactions(pageData, executionPayload.Transactions)
		case spec.DataVersionCapella:
			if blockData.Block.Capella == nil {
				break
			}
			executionPayload := blockData.Block.Capella.Message.Body.ExecutionPayload
			var baseFeePerGasBEBytes [32]byte
			for i := 0; i < 32; i++ {
				baseFeePerGasBEBytes[i] = executionPayload.BaseFeePerGas[32-1-i]
			}
			baseFeePerGas := new(big.Int).SetBytes(baseFeePerGasBEBytes[:])
			pageData.ExecutionData = &models.SlotPageExecutionData{
				ParentHash:    executionPayload.ParentHash[:],
				FeeRecipient:  executionPayload.FeeRecipient[:],
				StateRoot:     executionPayload.StateRoot[:],
				ReceiptsRoot:  executionPayload.ReceiptsRoot[:],
				LogsBloom:     executionPayload.LogsBloom[:],
				Random:        executionPayload.PrevRandao[:],
				GasLimit:      uint64(executionPayload.GasLimit),
				GasUsed:       uint64(executionPayload.GasUsed),
				Timestamp:     uint64(executionPayload.Timestamp),
				Time:          time.Unix(int64(executionPayload.Timestamp), 0),
				ExtraData:     executionPayload.ExtraData,
				BaseFeePerGas: baseFeePerGas.Uint64(),
				BlockHash:     executionPayload.BlockHash[:],
				BlockNumber:   uint64(executionPayload.BlockNumber),
			}
			getSlotPageTransactions(pageData, executionPayload.Transactions)
		case spec.DataVersionDeneb:
			if blockData.Block.Deneb == nil {
				break
			}
			executionPayload := blockData.Block.Deneb.Message.Body.ExecutionPayload
			pageData.ExecutionData = &models.SlotPageExecutionData{
				ParentHash:    executionPayload.ParentHash[:],
				FeeRecipient:  executionPayload.FeeRecipient[:],
				StateRoot:     executionPayload.StateRoot[:],
				ReceiptsRoot:  executionPayload.ReceiptsRoot[:],
				LogsBloom:     executionPayload.LogsBloom[:],
				Random:        executionPayload.PrevRandao[:],
				GasLimit:      uint64(executionPayload.GasLimit),
				GasUsed:       uint64(executionPayload.GasUsed),
				Timestamp:     uint64(executionPayload.Timestamp),
				Time:          time.Unix(int64(executionPayload.Timestamp), 0),
				ExtraData:     executionPayload.ExtraData,
				BaseFeePerGas: executionPayload.BaseFeePerGas.Uint64(),
				BlockHash:     executionPayload.BlockHash[:],
				BlockNumber:   uint64(executionPayload.BlockNumber),
			}
			getSlotPageTransactions(pageData, executionPayload.Transactions)
		case spec.DataVersionElectra:
			if blockData.Block.Electra == nil {
				break
			}
			executionPayload := blockData.Block.Electra.Message.Body.ExecutionPayload
			pageData.ExecutionData = &models.SlotPageExecutionData{
				ParentHash:    executionPayload.ParentHash[:],
				FeeRecipient:  executionPayload.FeeRecipient[:],
				StateRoot:     executionPayload.StateRoot[:],
				ReceiptsRoot:  executionPayload.ReceiptsRoot[:],
				LogsBloom:     executionPayload.LogsBloom[:],
				Random:        executionPayload.PrevRandao[:],
				GasLimit:      uint64(executionPayload.GasLimit),
				GasUsed:       uint64(executionPayload.GasUsed),
				Timestamp:     uint64(executionPayload.Timestamp),
				Time:          time.Unix(int64(executionPayload.Timestamp), 0),
				ExtraData:     executionPayload.ExtraData,
				BaseFeePerGas: executionPayload.BaseFeePerGas.Uint64(),
				BlockHash:     executionPayload.BlockHash[:],
				BlockNumber:   uint64(executionPayload.BlockNumber),
			}
			getSlotPageTransactions(pageData, executionPayload.Transactions)
		case spec.DataVersionEip7805:
			if blockData.Block.Eip7805 == nil {
				break
			}
			executionPayload := blockData.Block.Eip7805.Message.Body.ExecutionPayload
			pageData.ExecutionData = &models.SlotPageExecutionData{
				ParentHash:    executionPayload.ParentHash[:],
				FeeRecipient:  executionPayload.FeeRecipient[:],
				StateRoot:     executionPayload.StateRoot[:],
				ReceiptsRoot:  executionPayload.ReceiptsRoot[:],
				LogsBloom:     executionPayload.LogsBloom[:],
				Random:        executionPayload.PrevRandao[:],
				GasLimit:      uint64(executionPayload.GasLimit),
				GasUsed:       uint64(executionPayload.GasUsed),
				Timestamp:     uint64(executionPayload.Timestamp),
				Time:          time.Unix(int64(executionPayload.Timestamp), 0),
				ExtraData:     executionPayload.ExtraData,
				BaseFeePerGas: executionPayload.BaseFeePerGas.Uint64(),
				BlockHash:     executionPayload.BlockHash[:],
				BlockNumber:   uint64(executionPayload.BlockNumber),
			}
			getSlotPageTransactions(pageData, executionPayload.Transactions)
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
		requests, err := blockData.Block.ExecutionRequests()
		if err == nil && requests != nil {
			getSlotPageDepositRequests(pageData, requests.Deposits)
			getSlotPageWithdrawalRequests(pageData, requests.Withdrawals)
			getSlotPageConsolidationRequests(pageData, requests.Consolidations)
		}
	}

	return pageData
}

func getSlotPageTransactions(pageData *models.SlotPageBlockData, transactions []bellatrix.Transaction) {
	pageData.Transactions = make([]*models.SlotPageTransaction, 0)
	sigLookupBytes := []types.TxSignatureBytes{}
	sigLookupMap := map[types.TxSignatureBytes][]*models.SlotPageTransaction{}

	for idx, txBytes := range transactions {
		var tx ethtypes.Transaction

		err := tx.UnmarshalBinary(txBytes)
		if err != nil {
			logrus.Warnf("error decoding transaction 0x%x.%v: %v\n", pageData.BlockRoot, idx, err)
			continue
		}

		txHash := tx.Hash()
		txValue, _ := tx.Value().Float64()
		ethFloat, _ := utils.ETH.Float64()
		txValue = txValue / ethFloat

		txData := &models.SlotPageTransaction{
			Index: uint64(idx),
			Hash:  txHash[:],
			Value: txValue,
			Data:  tx.Data(),
			Type:  uint64(tx.Type()),
		}
		txData.DataLen = uint64(len(txData.Data))
		txFrom, err := ethtypes.Sender(ethtypes.NewPragueSigner(tx.ChainId()), &tx)
		if err != nil {
			txData.From = "unknown"
			logrus.Warnf("error decoding transaction sender 0x%x.%v: %v\n", pageData.BlockRoot, idx, err)
		} else {
			txData.From = txFrom.String()
		}
		txTo := tx.To()
		if txTo == nil {
			txData.To = "new contract"
		} else {
			txData.To = txTo.String()
		}

		pageData.Transactions = append(pageData.Transactions, txData)

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

func handleSlotDownload(ctx context.Context, w http.ResponseWriter, blockSlot int64, blockRoot []byte, downloadType string) error {
	chainState := services.GlobalBeaconService.GetChainState()
	currentSlot := chainState.CurrentSlot()
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
		return fmt.Errorf("error getting block data: %v", err)
	}

	if blockData == nil || blockData.Block == nil {
		return fmt.Errorf("block not found")
	}

	switch downloadType {
	case "block-ssz":
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=block-%d-%x.ssz", blockData.Header.Message.Slot, blockData.Root[:]))

		dynSsz := services.GlobalBeaconService.GetBeaconIndexer().GetDynSSZ()
		_, blockSSZ, err := beacon.MarshalVersionedSignedBeaconBlockSSZ(dynSsz, blockData.Block, false, true)
		if err != nil {
			return fmt.Errorf("error serializing block: %v", err)
		}
		w.Write(blockSSZ)
		return nil

	case "block-json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=block-%d-%x.json", blockData.Header.Message.Slot, blockData.Root[:]))

		_, jsonRes, err := beacon.MarshalVersionedSignedBeaconBlockJson(blockData.Block)
		if err != nil {
			return fmt.Errorf("error serializing block: %v", err)
		}
		w.Write(jsonRes)
		return nil

	case "header-ssz":
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=header-%d-%x.ssz", blockData.Header.Message.Slot, blockData.Root[:]))
		headerSSZ, err := blockData.Header.MarshalSSZ()
		if err != nil {
			return fmt.Errorf("error serializing header: %v", err)
		}
		w.Write(headerSSZ)
		return nil

	case "header-json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=header-%d-%x.json", blockData.Header.Message.Slot, blockData.Root[:]))
		jsonRes, err := blockData.Header.MarshalJSON()
		if err != nil {
			return fmt.Errorf("error serializing header: %v", err)
		}
		w.Write(jsonRes)
		return nil

	default:
		return fmt.Errorf("unknown download type: %s", downloadType)
	}
}
