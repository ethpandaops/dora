package handlers

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/juliangruber/go-intersect"
	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/pk910/light-beaconchain-explorer/types"
	"github.com/pk910/light-beaconchain-explorer/types/models"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

// Index will return the main "index" page using a go template
func Slot(w http.ResponseWriter, r *http.Request) {
	var slotTemplateFiles = append(layoutTemplateFiles,
		"slot/slot.html",
		"slot/overview.html",
		"slot/attestations.html",
		"slot/deposits.html",
		"slot/withdrawals.html",
		"slot/voluntary_exits.html",
		"slot/slashings.html",
		"slot/blobs.html",
	)
	var notfoundTemplateFiles = append(layoutTemplateFiles,
		"slot/notfound.html",
	)
	w.Header().Set("Content-Type", "text/html")

	vars := mux.Vars(r)
	slotOrHash := strings.Replace(vars["slotOrHash"], "0x", "", -1)
	blockSlot := int64(-1)
	blockRootHash, err := hex.DecodeString(slotOrHash)
	if err != nil || len(slotOrHash) != 64 {
		blockRootHash = []byte{}
		blockSlot, err = strconv.ParseInt(vars["slotOrHash"], 10, 64)
		if err != nil || blockSlot >= 2147483648 { // block slot must be lower then max int4
			data := InitPageData(w, r, "blockchain", "/slots", fmt.Sprintf("Slot %v", slotOrHash), notfoundTemplateFiles)
			if handleTemplateError(w, r, "slot.go", "Slot", "blockSlot", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
				return // an error has occurred and was processed
			}
			return
		}
	}

	pageData := getSlotPageData(blockSlot, blockRootHash)
	if pageData == nil {
		data := InitPageData(w, r, "blockchain", "/slots", fmt.Sprintf("Slot %v", slotOrHash), notfoundTemplateFiles)
		data.Data = "slot"
		if handleTemplateError(w, r, "slot.go", "Slot", "notFound", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
			return // an error has occurred and was processed
		}
		return
	}

	urlArgs := r.URL.Query()
	if urlArgs.Has("blob") && pageData.Block != nil {
		blobData, err := services.GlobalBeaconService.GetBlobSidecarsByBlockRoot(pageData.Block.BlockRoot)
		if err == nil && blobData != nil {
			for blobIdx, blob := range blobData.Data {
				blobData := pageData.Block.Blobs[blobIdx]
				blobData.HaveData = true
				blobData.KzgProof = blob.KzgProof
				blobData.Blob = blob.Blob
				if len(blob.Blob) > 512 {
					blobData.BlobShort = blob.Blob[0:512]
					blobData.IsShort = true
				} else {
					blobData.BlobShort = blob.Blob
				}
			}
		}
	}

	template := templates.GetTemplate(slotTemplateFiles...)
	data := InitPageData(w, r, "blockchain", "/slots", fmt.Sprintf("Slot %v", slotOrHash), slotTemplateFiles)
	data.Data = pageData
	if handleTemplateError(w, r, "index.go", "Slot", "", template.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

// SlotBlob handles responses for the block blobs tab
func SlotBlob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	blockRoot, err := hex.DecodeString(strings.Replace(vars["hash"], "0x", "", -1))
	if err != nil || len(blockRoot) != 32 {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
	blobIdx, err := strconv.ParseUint(vars["blobIdx"], 10, 64)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
	blobData, err := services.GlobalBeaconService.GetBlobSidecarsByBlockRoot(blockRoot)
	if err != nil {
		logrus.WithError(err).Error("error loading blob sidecar")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
	var result interface{}
	if blobData != nil && blobIdx < uint64(len(blobData.Data)) {
		blob := blobData.Data[blobIdx]
		result = &models.SlotPageBlobDetails{
			Index:         blobIdx,
			KzgCommitment: blob.KzgCommitment.String(),
			KzgProof:      blob.KzgProof.String(),
			Blob:          blob.Blob.String(),
		}
	}
	err = json.NewEncoder(w).Encode(result)
	if err != nil {
		logrus.WithError(err).Error("error encoding blob sidecar")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

func getSlotPageData(blockSlot int64, blockRoot []byte) *models.SlotPageData {
	pageData := &models.SlotPageData{}
	pageCacheKey := fmt.Sprintf("slot:%v:%x", blockSlot, blockRoot)
	pageData = services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildSlotPageData(blockSlot, blockRoot)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	}).(*models.SlotPageData)
	return pageData
}

func buildSlotPageData(blockSlot int64, blockRoot []byte) (*models.SlotPageData, time.Duration) {
	currentSlot := utils.TimeToSlot(uint64(time.Now().Unix()))
	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	var blockData *rpctypes.CombinedBlockResponse
	var err error
	if blockSlot > -1 {
		blockData, err = services.GlobalBeaconService.GetSlotDetailsBySlot(uint64(blockSlot), false)
	} else {
		blockData, err = services.GlobalBeaconService.GetSlotDetailsByBlockroot(blockRoot, false)
	}

	if err == nil {
		if blockData == nil {
			// check for orphaned block
			if blockSlot > -1 {
				dbBlocks := services.GlobalBeaconService.GetDbBlocksForSlots(uint64(blockSlot), 0, true)
				if len(dbBlocks) > 0 {
					blockRoot = dbBlocks[0].Root
				}
			}
			if blockRoot != nil {
				blockData = services.GlobalBeaconService.GetOrphanedBlock(blockRoot)
			}
		} else {
			// check orphaned status
			blockData.Orphaned = services.GlobalBeaconService.CheckBlockOrphanedStatus(blockData.Root)
		}
	}

	var slot uint64
	if blockData != nil {
		slot = uint64(blockData.Header.Message.Slot)
	} else if blockSlot > -1 {
		slot = uint64(blockSlot)
	} else {
		return nil, -1
	}
	logrus.Printf("slot page called: %v", slot)

	pageData := &models.SlotPageData{
		Slot:           slot,
		Epoch:          utils.EpochOfSlot(slot),
		Ts:             utils.SlotToTime(slot),
		NextSlot:       slot + 1,
		PreviousSlot:   slot - 1,
		Future:         slot >= currentSlot,
		EpochFinalized: finalizedEpoch >= int64(utils.EpochOfSlot(slot)),
	}

	assignments, err := services.GlobalBeaconService.GetEpochAssignments(utils.EpochOfSlot(slot))
	if err != nil {
		logrus.Printf("assignments error: %v", err)
		// we can safely continue here. the UI is prepared to work without epoch duties, but fields related to the duties are not shown
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

		if assignments != nil {
			pageData.Proposer = assignments.ProposerAssignments[slot]
			pageData.ProposerName = services.GlobalBeaconService.GetValidatorName(pageData.Proposer)
		}
	} else {
		if blockData.Orphaned {
			pageData.Status = uint16(models.SlotStatusOrphaned)
		} else {
			pageData.Status = uint16(models.SlotStatusFound)
		}
		pageData.Proposer = uint64(blockData.Block.Message.ProposerIndex)
		pageData.ProposerName = services.GlobalBeaconService.GetValidatorName(pageData.Proposer)
		pageData.Block = getSlotPageBlockData(blockData, assignments)
	}

	return pageData, cacheTimeout
}

func getSlotPageBlockData(blockData *rpctypes.CombinedBlockResponse, assignments *rpctypes.EpochAssignments) *models.SlotPageBlockData {
	pageData := &models.SlotPageBlockData{
		BlockRoot:              blockData.Root,
		ParentRoot:             blockData.Header.Message.ParentRoot,
		StateRoot:              blockData.Header.Message.StateRoot,
		Signature:              blockData.Header.Signature,
		RandaoReveal:           blockData.Block.Message.Body.RandaoReveal,
		Graffiti:               blockData.Block.Message.Body.Graffiti,
		Eth1dataDepositroot:    blockData.Block.Message.Body.Eth1Data.DepositRoot,
		Eth1dataDepositcount:   uint64(blockData.Block.Message.Body.Eth1Data.DepositCount),
		Eth1dataBlockhash:      blockData.Block.Message.Body.Eth1Data.BlockHash,
		ProposerSlashingsCount: uint64(len(blockData.Block.Message.Body.ProposerSlashings)),
		AttesterSlashingsCount: uint64(len(blockData.Block.Message.Body.AttesterSlashings)),
		AttestationsCount:      uint64(len(blockData.Block.Message.Body.Attestations)),
		DepositsCount:          uint64(len(blockData.Block.Message.Body.Deposits)),
		VoluntaryExitsCount:    uint64(len(blockData.Block.Message.Body.VoluntaryExits)),
		SlashingsCount:         uint64(len(blockData.Block.Message.Body.ProposerSlashings)) + uint64(len(blockData.Block.Message.Body.AttesterSlashings)),
	}

	epoch := utils.EpochOfSlot(uint64(blockData.Header.Message.Slot))
	assignmentsMap := make(map[uint64]*rpctypes.EpochAssignments)
	assignmentsLoaded := make(map[uint64]bool)
	assignmentsMap[epoch] = assignments
	assignmentsLoaded[epoch] = true

	pageData.Attestations = make([]*models.SlotPageAttestation, pageData.AttestationsCount)
	for i := uint64(0); i < pageData.AttestationsCount; i++ {
		attestation := blockData.Block.Message.Body.Attestations[i]
		var attAssignments []uint64
		attEpoch := utils.EpochOfSlot(uint64(attestation.Data.Slot))
		if !assignmentsLoaded[attEpoch] { // load epoch duties if needed
			attEpochAssignments, _ := services.GlobalBeaconService.GetEpochAssignments(attEpoch)
			assignmentsMap[attEpoch] = attEpochAssignments
			assignmentsLoaded[attEpoch] = true
		}

		if assignmentsMap[attEpoch] != nil {
			attAssignments = assignmentsMap[attEpoch].AttestorAssignments[fmt.Sprintf("%v-%v", uint64(attestation.Data.Slot), uint64(attestation.Data.Index))]
		} else {
			attAssignments = []uint64{}
		}
		attPageData := models.SlotPageAttestation{
			Slot:            uint64(attestation.Data.Slot),
			CommitteeIndex:  uint64(attestation.Data.Index),
			AggregationBits: attestation.AggregationBits,
			Validators:      make([]types.NamedValidator, len(attAssignments)),
			Signature:       attestation.Signature,
			BeaconBlockRoot: attestation.Data.BeaconBlockRoot,
			SourceEpoch:     uint64(attestation.Data.Source.Epoch),
			SourceRoot:      attestation.Data.Source.Root,
			TargetEpoch:     uint64(attestation.Data.Target.Epoch),
			TargetRoot:      attestation.Data.Target.Root,
		}
		for j := 0; j < len(attAssignments); j++ {
			attPageData.Validators[j] = types.NamedValidator{
				Index: attAssignments[j],
				Name:  services.GlobalBeaconService.GetValidatorName(attAssignments[j]),
			}
		}
		pageData.Attestations[i] = &attPageData
	}

	pageData.Deposits = make([]*models.SlotPageDeposit, pageData.DepositsCount)
	for i := uint64(0); i < pageData.DepositsCount; i++ {
		deposit := blockData.Block.Message.Body.Deposits[i]
		pageData.Deposits[i] = &models.SlotPageDeposit{
			PublicKey:             deposit.Data.Pubkey,
			Withdrawalcredentials: deposit.Data.WithdrawalCredentials,
			Amount:                uint64(deposit.Data.Amount),
			Signature:             deposit.Data.Signature,
		}
	}

	pageData.VoluntaryExits = make([]*models.SlotPageVoluntaryExit, pageData.VoluntaryExitsCount)
	for i := uint64(0); i < pageData.VoluntaryExitsCount; i++ {
		exit := blockData.Block.Message.Body.VoluntaryExits[i]
		pageData.VoluntaryExits[i] = &models.SlotPageVoluntaryExit{
			ValidatorIndex: uint64(exit.Message.ValidatorIndex),
			ValidatorName:  services.GlobalBeaconService.GetValidatorName(uint64(exit.Message.ValidatorIndex)),
			Epoch:          uint64(exit.Message.Epoch),
			Signature:      exit.Signature,
		}
	}

	pageData.AttesterSlashings = make([]*models.SlotPageAttesterSlashing, pageData.AttesterSlashingsCount)
	for i := uint64(0); i < pageData.AttesterSlashingsCount; i++ {
		slashing := blockData.Block.Message.Body.AttesterSlashings[i]
		slashingData := &models.SlotPageAttesterSlashing{
			Attestation1Indices:         make([]uint64, len(slashing.Attestation1.AttestingIndices)),
			Attestation1Signature:       slashing.Attestation1.Signature,
			Attestation1Slot:            uint64(slashing.Attestation1.Data.Slot),
			Attestation1Index:           uint64(slashing.Attestation1.Data.Index),
			Attestation1BeaconBlockRoot: slashing.Attestation1.Data.BeaconBlockRoot,
			Attestation1SourceEpoch:     uint64(slashing.Attestation1.Data.Source.Epoch),
			Attestation1SourceRoot:      slashing.Attestation1.Data.Source.Root,
			Attestation1TargetEpoch:     uint64(slashing.Attestation1.Data.Target.Epoch),
			Attestation1TargetRoot:      slashing.Attestation1.Data.Target.Root,
			Attestation2Indices:         make([]uint64, len(slashing.Attestation2.AttestingIndices)),
			Attestation2Signature:       slashing.Attestation2.Signature,
			Attestation2Slot:            uint64(slashing.Attestation2.Data.Slot),
			Attestation2Index:           uint64(slashing.Attestation2.Data.Index),
			Attestation2BeaconBlockRoot: slashing.Attestation2.Data.BeaconBlockRoot,
			Attestation2SourceEpoch:     uint64(slashing.Attestation2.Data.Source.Epoch),
			Attestation2SourceRoot:      slashing.Attestation2.Data.Source.Root,
			Attestation2TargetEpoch:     uint64(slashing.Attestation2.Data.Target.Epoch),
			Attestation2TargetRoot:      slashing.Attestation2.Data.Target.Root,
			SlashedValidators:           make([]types.NamedValidator, 0),
		}
		pageData.AttesterSlashings[i] = slashingData
		for j := range slashing.Attestation1.AttestingIndices {
			slashingData.Attestation1Indices[j] = uint64(slashing.Attestation1.AttestingIndices[j])
		}
		for j := range slashing.Attestation2.AttestingIndices {
			slashingData.Attestation2Indices[j] = uint64(slashing.Attestation2.AttestingIndices[j])
		}
		inter := intersect.Simple(slashing.Attestation1.AttestingIndices, slashing.Attestation2.AttestingIndices)
		for _, j := range inter {
			valIdx := uint64(j.(rpctypes.Uint64Str))
			slashingData.SlashedValidators = append(slashingData.SlashedValidators, types.NamedValidator{
				Index: valIdx,
				Name:  services.GlobalBeaconService.GetValidatorName(valIdx),
			})
		}
	}

	pageData.ProposerSlashings = make([]*models.SlotPageProposerSlashing, pageData.ProposerSlashingsCount)
	for i := uint64(0); i < pageData.ProposerSlashingsCount; i++ {
		slashing := blockData.Block.Message.Body.ProposerSlashings[i]
		pageData.ProposerSlashings[i] = &models.SlotPageProposerSlashing{
			ProposerIndex:     uint64(slashing.SignedHeader1.Message.ProposerIndex),
			ProposerName:      services.GlobalBeaconService.GetValidatorName(uint64(slashing.SignedHeader1.Message.ProposerIndex)),
			Header1Slot:       uint64(slashing.SignedHeader1.Message.Slot),
			Header1ParentRoot: slashing.SignedHeader1.Message.ParentRoot,
			Header1StateRoot:  slashing.SignedHeader1.Message.StateRoot,
			Header1BodyRoot:   slashing.SignedHeader1.Message.BodyRoot,
			Header1Signature:  slashing.SignedHeader1.Signature,
			Header2Slot:       uint64(slashing.SignedHeader2.Message.Slot),
			Header2ParentRoot: slashing.SignedHeader2.Message.ParentRoot,
			Header2StateRoot:  slashing.SignedHeader2.Message.StateRoot,
			Header2BodyRoot:   slashing.SignedHeader2.Message.BodyRoot,
			Header2Signature:  slashing.SignedHeader2.Signature,
		}
	}

	if epoch >= utils.Config.Chain.Config.AltairForkEpoch {
		syncAggregate := blockData.Block.Message.Body.SyncAggregate
		pageData.SyncAggregateBits = syncAggregate.SyncCommitteeBits
		pageData.SyncAggregateSignature = syncAggregate.SyncCommitteeSignature
		if assignments != nil {
			pageData.SyncAggCommittee = make([]types.NamedValidator, len(assignments.SyncAssignments))
			for idx, vidx := range assignments.SyncAssignments {
				pageData.SyncAggCommittee[idx] = types.NamedValidator{
					Index: vidx,
					Name:  services.GlobalBeaconService.GetValidatorName(vidx),
				}
			}
		} else {
			pageData.SyncAggCommittee = []types.NamedValidator{}
		}
		pageData.SyncAggParticipation = utils.SyncCommitteeParticipation(pageData.SyncAggregateBits)
	}

	if epoch >= utils.Config.Chain.Config.BellatrixForkEpoch {
		executionPayload := blockData.Block.Message.Body.ExecutionPayload
		pageData.ExecutionData = &models.SlotPageExecutionData{
			ParentHash:        executionPayload.ParentHash,
			FeeRecipient:      executionPayload.FeeRecipient,
			StateRoot:         executionPayload.StateRoot,
			ReceiptsRoot:      executionPayload.ReceiptsRoot,
			LogsBloom:         executionPayload.LogsBloom,
			Random:            executionPayload.PrevRandao,
			GasLimit:          uint64(executionPayload.GasLimit),
			GasUsed:           uint64(executionPayload.GasUsed),
			Timestamp:         uint64(executionPayload.Timestamp),
			Time:              time.Unix(int64(executionPayload.Timestamp), 0),
			ExtraData:         executionPayload.ExtraData,
			BaseFeePerGas:     uint64(executionPayload.BaseFeePerGas),
			BlockHash:         executionPayload.BlockHash,
			BlockNumber:       uint64(executionPayload.BlockNumber),
			TransactionsCount: uint64(len(executionPayload.Transactions)),
		}
	}

	if epoch >= utils.Config.Chain.Config.CappellaForkEpoch {
		pageData.BLSChangesCount = uint64(len(blockData.Block.Message.Body.SignedBLSToExecutionChange))
		pageData.BLSChanges = make([]*models.SlotPageBLSChange, pageData.BLSChangesCount)
		for i := uint64(0); i < pageData.BLSChangesCount; i++ {
			blschange := blockData.Block.Message.Body.SignedBLSToExecutionChange[i]
			pageData.BLSChanges[i] = &models.SlotPageBLSChange{
				ValidatorIndex: uint64(blschange.Message.ValidatorIndex),
				ValidatorName:  services.GlobalBeaconService.GetValidatorName(uint64(blschange.Message.ValidatorIndex)),
				BlsPubkey:      []byte(blschange.Message.FromBlsPubkey),
				Address:        []byte(blschange.Message.ToExecutionAddress),
				Signature:      []byte(blschange.Signature),
			}
		}

		pageData.WithdrawalsCount = uint64(len(blockData.Block.Message.Body.ExecutionPayload.Withdrawals))
		pageData.Withdrawals = make([]*models.SlotPageWithdrawal, pageData.WithdrawalsCount)
		for i := uint64(0); i < pageData.WithdrawalsCount; i++ {
			withdrawal := blockData.Block.Message.Body.ExecutionPayload.Withdrawals[i]
			pageData.Withdrawals[i] = &models.SlotPageWithdrawal{
				Index:          uint64(withdrawal.Index),
				ValidatorIndex: uint64(withdrawal.ValidatorIndex),
				ValidatorName:  services.GlobalBeaconService.GetValidatorName(uint64(withdrawal.ValidatorIndex)),
				Address:        withdrawal.Address,
				Amount:         uint64(withdrawal.Amount),
			}
		}
	}

	if epoch >= utils.Config.Chain.Config.DenebForkEpoch {
		pageData.BlobsCount = uint64(len(blockData.Block.Message.Body.BlobKzgCommitments))
		pageData.Blobs = make([]*models.SlotPageBlob, pageData.BlobsCount)
		for i := uint64(0); i < pageData.BlobsCount; i++ {
			blobData := &models.SlotPageBlob{
				Index:         i,
				KzgCommitment: blockData.Block.Message.Body.BlobKzgCommitments[i],
			}
			pageData.Blobs[i] = blobData
		}
	}

	return pageData
}
