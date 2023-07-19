package handlers

import (
	"encoding/hex"
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
	)
	var notfoundTemplateFiles = append(layoutTemplateFiles,
		"slot/notfound.html",
	)
	var errorTemplateFiles = append(layoutTemplateFiles,
		"slot/error.html",
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

	finalizedHead, err := services.GlobalBeaconService.GetFinalizedBlockHead()
	var blockData *rpctypes.CombinedBlockResponse
	if err == nil {
		if blockSlot > -1 {
			blockData, err = services.GlobalBeaconService.GetSlotDetailsBySlot(uint64(blockSlot))
		} else {
			blockData, err = services.GlobalBeaconService.GetSlotDetailsByBlockroot(blockRootHash)
		}
	}

	var slot uint64
	if blockData == nil && err == nil {
		if blockSlot > -1 {
			slot = uint64(blockSlot)
		} else {
			data := InitPageData(w, r, "blockchain", "/slots", fmt.Sprintf("Slot %v", slotOrHash), notfoundTemplateFiles)
			data.Data = "slot"
			if handleTemplateError(w, r, "slot.go", "Slot", "notFound", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
				return // an error has occurred and was processed
			}
			return
		}
	} else if err != nil {
		logrus.Printf("slot page error: %v", err)
		data := InitPageData(w, r, "blockchain", "/slots", fmt.Sprintf("Slot %v", slotOrHash), errorTemplateFiles)
		data.Data = err.Error()
		if handleTemplateError(w, r, "slot.go", "Slot", "notFound", templates.GetTemplate(errorTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
			return // an error has occurred and was processed
		}
		return
	} else {
		slot = uint64(blockData.Header.Data.Header.Message.Slot)
	}

	pageData := &models.SlotPageData{
		Slot:           slot,
		Epoch:          services.EpochOfSlot(slot),
		EpochFinalized: uint64(finalizedHead.Data.Header.Message.Slot) >= slot,
		Ts:             services.SlotToTime(slot),
		NextSlot:       slot + 1,
		PreviousSlot:   slot - 1,
	}

	assignments, err := services.GlobalBeaconService.GetEpochAssignments(services.EpochOfSlot(slot))
	if err != nil {
		logrus.Printf("assignments error: %v", err)
		data := InitPageData(w, r, "blockchain", "/slots", fmt.Sprintf("Slot %v", slotOrHash), errorTemplateFiles)
		data.Data = err.Error()
		if handleTemplateError(w, r, "slot.go", "Slot", "notFound", templates.GetTemplate(errorTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
			return // an error has occurred and was processed
		}
		return
	}

	template := templates.GetTemplate(slotTemplateFiles...)
	data := InitPageData(w, r, "blockchain", "/slots", fmt.Sprintf("Slot %v", slotOrHash), slotTemplateFiles)

	if blockData == nil {
		pageData.Status = uint16(models.SlotStatusMissed)
		pageData.Proposer = assignments.ProposerAssignments[slot]
	} else {
		if blockData.Header.Data.Canonical {
			pageData.Status = uint16(models.SlotStatusFound)
		} else {
			pageData.Status = uint16(models.SlotStatusOrphaned)
		}
		pageData.Proposer = uint64(blockData.Block.Data.Message.ProposerIndex)
		pageData.Block = getSlotPageBlockData(blockData, assignments)
	}

	logrus.Printf("slot page called")
	data.Data = pageData

	if handleTemplateError(w, r, "index.go", "Slot", "", template.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getSlotPageBlockData(blockData *rpctypes.CombinedBlockResponse, assignments *rpctypes.EpochAssignments) *models.SlotPageBlockData {
	pageData := &models.SlotPageBlockData{
		BlockRoot:              utils.MustParseHex(blockData.Header.Data.Root),
		ParentRoot:             utils.MustParseHex(blockData.Header.Data.Header.Message.ParentRoot),
		StateRoot:              utils.MustParseHex(blockData.Header.Data.Header.Message.StateRoot),
		Signature:              utils.MustParseHex(blockData.Header.Data.Header.Signature),
		RandaoReveal:           utils.MustParseHex(blockData.Block.Data.Message.Body.RandaoReveal),
		Graffiti:               utils.MustParseHex(blockData.Block.Data.Message.Body.Graffiti),
		Eth1dataDepositroot:    utils.MustParseHex(blockData.Block.Data.Message.Body.Eth1Data.DepositRoot),
		Eth1dataDepositcount:   uint64(blockData.Block.Data.Message.Body.Eth1Data.DepositCount),
		Eth1dataBlockhash:      utils.MustParseHex(blockData.Block.Data.Message.Body.Eth1Data.BlockHash),
		ProposerSlashingsCount: uint64(len(blockData.Block.Data.Message.Body.ProposerSlashings)),
		AttesterSlashingsCount: uint64(len(blockData.Block.Data.Message.Body.AttesterSlashings)),
		AttestationsCount:      uint64(len(blockData.Block.Data.Message.Body.Attestations)),
		DepositsCount:          uint64(len(blockData.Block.Data.Message.Body.Deposits)),
		VoluntaryExitsCount:    uint64(len(blockData.Block.Data.Message.Body.VoluntaryExits)),
		SlashingsCount:         uint64(len(blockData.Block.Data.Message.Body.ProposerSlashings)) + uint64(len(blockData.Block.Data.Message.Body.AttesterSlashings)),
	}

	pageData.Attestations = make([]*models.SlotPageAttestation, pageData.AttestationsCount)
	for i := uint64(0); i < pageData.AttestationsCount; i++ {
		attestation := blockData.Block.Data.Message.Body.Attestations[i]
		pageData.Attestations[i] = &models.SlotPageAttestation{
			Slot:            uint64(attestation.Data.Slot),
			CommitteeIndex:  uint64(attestation.Data.Index),
			AggregationBits: utils.MustParseHex(attestation.AggregationBits),
			Validators:      assignments.AttestorAssignments[fmt.Sprintf("%v-%v", uint64(attestation.Data.Slot), uint64(attestation.Data.Index))],
			Signature:       utils.MustParseHex(attestation.Signature),
			BeaconBlockRoot: utils.MustParseHex(attestation.Data.BeaconBlockRoot),
			SourceEpoch:     uint64(attestation.Data.Source.Epoch),
			SourceRoot:      utils.MustParseHex(attestation.Data.Source.Root),
			TargetEpoch:     uint64(attestation.Data.Target.Epoch),
			TargetRoot:      utils.MustParseHex(attestation.Data.Target.Root),
		}
	}

	pageData.Deposits = make([]*models.SlotPageDeposit, pageData.DepositsCount)
	for i := uint64(0); i < pageData.DepositsCount; i++ {
		deposit := blockData.Block.Data.Message.Body.Deposits[i]
		pageData.Deposits[i] = &models.SlotPageDeposit{
			PublicKey:             utils.MustParseHex(deposit.Data.Pubkey),
			Withdrawalcredentials: utils.MustParseHex(deposit.Data.WithdrawalCredentials),
			Amount:                uint64(deposit.Data.Amount),
			Signature:             utils.MustParseHex(deposit.Data.Signature),
		}
	}

	pageData.VoluntaryExits = make([]*models.SlotPageVoluntaryExit, pageData.VoluntaryExitsCount)
	for i := uint64(0); i < pageData.VoluntaryExitsCount; i++ {
		exit := blockData.Block.Data.Message.Body.VoluntaryExits[i]
		pageData.VoluntaryExits[i] = &models.SlotPageVoluntaryExit{
			ValidatorIndex: uint64(exit.Message.ValidatorIndex),
			Epoch:          uint64(exit.Message.Epoch),
			Signature:      utils.MustParseHex(exit.Signature),
		}
	}

	pageData.AttesterSlashings = make([]*models.SlotPageAttesterSlashing, pageData.AttesterSlashingsCount)
	for i := uint64(0); i < pageData.AttesterSlashingsCount; i++ {
		slashing := blockData.Block.Data.Message.Body.AttesterSlashings[i]
		slashingData := &models.SlotPageAttesterSlashing{
			Attestation1Indices:         make([]uint64, len(slashing.Attestation1.AttestingIndices)),
			Attestation1Signature:       utils.MustParseHex(slashing.Attestation1.Signature),
			Attestation1Slot:            uint64(slashing.Attestation1.Data.Slot),
			Attestation1Index:           uint64(slashing.Attestation1.Data.Index),
			Attestation1BeaconBlockRoot: utils.MustParseHex(slashing.Attestation1.Data.BeaconBlockRoot),
			Attestation1SourceEpoch:     uint64(slashing.Attestation1.Data.Source.Epoch),
			Attestation1SourceRoot:      utils.MustParseHex(slashing.Attestation1.Data.Source.Root),
			Attestation1TargetEpoch:     uint64(slashing.Attestation1.Data.Target.Epoch),
			Attestation1TargetRoot:      utils.MustParseHex(slashing.Attestation1.Data.Target.Root),
			Attestation2Indices:         make([]uint64, len(slashing.Attestation2.AttestingIndices)),
			Attestation2Signature:       utils.MustParseHex(slashing.Attestation2.Signature),
			Attestation2Slot:            uint64(slashing.Attestation2.Data.Slot),
			Attestation2Index:           uint64(slashing.Attestation2.Data.Index),
			Attestation2BeaconBlockRoot: utils.MustParseHex(slashing.Attestation2.Data.BeaconBlockRoot),
			Attestation2SourceEpoch:     uint64(slashing.Attestation2.Data.Source.Epoch),
			Attestation2SourceRoot:      utils.MustParseHex(slashing.Attestation2.Data.Source.Root),
			Attestation2TargetEpoch:     uint64(slashing.Attestation2.Data.Target.Epoch),
			Attestation2TargetRoot:      utils.MustParseHex(slashing.Attestation2.Data.Target.Root),
			SlashedValidators:           make([]uint64, 0),
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
			slashingData.SlashedValidators = append(slashingData.SlashedValidators, uint64(j.(rpctypes.Uint64Str)))
		}
	}

	pageData.ProposerSlashings = make([]*models.SlotPageProposerSlashing, pageData.ProposerSlashingsCount)
	for i := uint64(0); i < pageData.ProposerSlashingsCount; i++ {
		slashing := blockData.Block.Data.Message.Body.ProposerSlashings[i]
		pageData.ProposerSlashings[i] = &models.SlotPageProposerSlashing{
			ProposerIndex:     uint64(slashing.SignedHeader1.Message.ProposerIndex),
			Header1Slot:       uint64(slashing.SignedHeader1.Message.Slot),
			Header1ParentRoot: utils.MustParseHex(slashing.SignedHeader1.Message.ParentRoot),
			Header1StateRoot:  utils.MustParseHex(slashing.SignedHeader1.Message.StateRoot),
			Header1BodyRoot:   utils.MustParseHex(slashing.SignedHeader1.Message.BodyRoot),
			Header1Signature:  utils.MustParseHex(slashing.SignedHeader1.Signature),
			Header2Slot:       uint64(slashing.SignedHeader2.Message.Slot),
			Header2ParentRoot: utils.MustParseHex(slashing.SignedHeader2.Message.ParentRoot),
			Header2StateRoot:  utils.MustParseHex(slashing.SignedHeader2.Message.StateRoot),
			Header2BodyRoot:   utils.MustParseHex(slashing.SignedHeader2.Message.BodyRoot),
			Header2Signature:  utils.MustParseHex(slashing.SignedHeader2.Signature),
		}
	}

	epoch := services.EpochOfSlot(uint64(blockData.Header.Data.Header.Message.Slot))
	if epoch >= utils.Config.Chain.Config.AltairForkEpoch {
		syncAggregate := blockData.Block.Data.Message.Body.SyncAggregate
		pageData.SyncAggregateBits = utils.MustParseHex(syncAggregate.SyncCommitteeBits)
		pageData.SyncAggregateSignature = utils.MustParseHex(syncAggregate.SyncCommitteeSignature)
		pageData.SyncAggCommittee = assignments.SyncAssignments
		pageData.SyncAggParticipation = utils.SyncCommitteeParticipation(pageData.SyncAggregateBits)
	}

	if epoch >= utils.Config.Chain.Config.BellatrixForkEpoch {
		executionPayload := blockData.Block.Data.Message.Body.ExecutionPayload
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
		pageData.BLSChangesCount = uint64(len(blockData.Block.Data.Message.Body.SignedBLSToExecutionChange))
		pageData.BLSChanges = make([]*models.SlotPageBLSChange, pageData.BLSChangesCount)
		for i := uint64(0); i < pageData.BLSChangesCount; i++ {
			blschange := blockData.Block.Data.Message.Body.SignedBLSToExecutionChange[i]
			pageData.BLSChanges[i] = &models.SlotPageBLSChange{
				ValidatorIndex: uint64(blschange.Message.ValidatorIndex),
				BlsPubkey:      []byte(blschange.Message.FromBlsPubkey),
				Address:        []byte(blschange.Message.ToExecutionAddress),
				Signature:      []byte(blschange.Signature),
			}
		}

		pageData.WithdrawalsCount = uint64(len(blockData.Block.Data.Message.Body.ExecutionPayload.Withdrawals))
		pageData.Withdrawals = make([]*models.SlotPageWithdrawal, pageData.WithdrawalsCount)
		for i := uint64(0); i < pageData.WithdrawalsCount; i++ {
			withdrawal := blockData.Block.Data.Message.Body.ExecutionPayload.Withdrawals[i]
			pageData.Withdrawals[i] = &models.SlotPageWithdrawal{
				Index:          uint64(withdrawal.Index),
				ValidatorIndex: uint64(withdrawal.ValidatorIndex),
				Address:        withdrawal.Address,
				Amount:         uint64(withdrawal.Amount),
			}
		}
	}

	return pageData
}
