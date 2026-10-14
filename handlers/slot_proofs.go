package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/executionproof"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
)

// resolveSlotPathBlock resolves the slot number or block root of a slot page path to
// an indexed block (a slot number yields its canonical block). Only a block the
// explorer knows is served, so an unknown root cannot drive lookups.
func resolveSlotPathBlock(ctx context.Context, slotOrHash string) (phase0.Slot, phase0.Root, bool) {
	hashStr := strings.ReplaceAll(slotOrHash, "0x", "")
	blockFilter := &dbtypes.BlockFilter{WithOrphaned: 1}
	if blockRoot, err := hex.DecodeString(hashStr); err == nil && len(hashStr) == 64 {
		blockFilter.BlockRoot = blockRoot
	} else {
		blockSlot, err := strconv.ParseUint(slotOrHash, 10, 64)
		if err != nil || blockSlot >= 2147483648 { // block slot must be lower then max int4
			return 0, phase0.Root{}, false
		}
		blockFilter.Slot = &blockSlot
	}

	dbBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, blockFilter, 0, 1, 0)
	if len(dbBlocks) == 0 || dbBlocks[0].Block == nil || len(dbBlocks[0].Block.Root) != 32 {
		return 0, phase0.Root{}, false
	}

	return phase0.Slot(dbBlocks[0].Block.Slot), phase0.Root(dbBlocks[0].Block.Root), true
}

// SlotExecutionProofs returns the EIP-8025 execution proofs collected for the block
// identified by the path slot number or block root as JSON, without the proof bytes.
// The slot page loads it lazily when the proofs tab is opened; the proofs are served
// from the indexer's collection and the blockdb, never from the beacon nodes.
func SlotExecutionProofs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	slot, blockRoot, found := resolveSlotPathBlock(r.Context(), mux.Vars(r)["slotOrHash"])
	if !found {
		http.Error(w, "Block not found", http.StatusNotFound)
		return
	}

	cacheKey := fmt.Sprintf("slotproofs:%x", blockRoot[:])
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey, true, &models.SlotExecutionProofsResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout := buildSlotExecutionProofsData(pageCall.CallCtx, slot, blockRoot)
		pageCall.CacheTimeout = cacheTimeout
		return data
	})
	if pageErr != nil {
		logrus.WithError(pageErr).Error("error building slot execution proofs data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	result, ok := pageRes.(*models.SlotExecutionProofsResponse)
	if !ok {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	if err := json.NewEncoder(w).Encode(result); err != nil {
		logrus.WithError(err).Error("error encoding slot execution proofs data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

// buildSlotExecutionProofsData lists the proofs of a block with what the explorer
// can tell about them: the public input they commit to against the one the block
// expects, and their prover signatures. The proofs of a block still being collected
// can grow, so the list of an unfinalized block is cached briefly; the stored list
// of a finalized block is final.
func buildSlotExecutionProofsData(ctx context.Context, slot phase0.Slot, blockRoot phase0.Root) (*models.SlotExecutionProofsResponse, time.Duration) {
	analysis := services.GlobalBeaconService.AnalyzeBlockExecutionProofs(ctx, slot, blockRoot)

	result := &models.SlotExecutionProofsResponse{
		Slot:                     uint64(slot),
		BlockRoot:                fmt.Sprintf("0x%x", blockRoot[:]),
		ExpectedPublicInput:      slotExecutionProofPublicInput(analysis.Expected),
		ExpectedPublicInputError: analysis.ExpectedError,
		Proofs:                   make([]*models.SlotExecutionProof, 0, len(analysis.Proofs)),
	}
	for _, entry := range analysis.Proofs {
		proof := entry.Proof
		model := &models.SlotExecutionProof{
			ProofType:        proof.ProofType,
			ProofName:        fmt.Sprintf("unassigned proof type #%d", proof.ProofType),
			SignatureValid:   entry.SignatureValid,
			ProverActive:     entry.ProverActive,
			Size:             proof.DataSize,
			DownloadUrl:      fmt.Sprintf("/slot/0x%x/proofs/%d", blockRoot[:], proof.ProofType),
			DecodeError:      entry.DecodeError,
			PublicInputMatch: entry.PublicInputMatch,
			ProgramVKMatch:   entry.ProgramVKMatch,
		}
		if entry.Type != nil {
			model.ProofName = entry.Type.Name()
			model.Guest = entry.Type.Guest
			model.ZkVM = string(entry.Type.ZkVM)
			model.ProgramVK = fmt.Sprintf("0x%x", entry.Type.ProgramVK)
			model.BrowserVerifiable = entry.Type.ZkVM.BrowserVerifiable()
		}
		model.ValidatorIndex = proof.ValidatorIndex
		model.ValidatorName = services.GlobalBeaconService.GetValidatorNameAt(proof.ValidatorIndex, slot)
		model.Signature = fmt.Sprintf("0x%x", proof.Signature[:])
		if committed := entry.Committed; committed != nil {
			model.ProofMode = committed.ProofMode
			model.SdkVersion = committed.SdkVersion
			model.CommittedPublicInput = slotExecutionProofPublicInput(committed.PublicInput)
			if committed.PublicInput == nil {
				model.DecodeError = committed.DecodeError
			}
			if committed.ProgramVK != nil {
				model.CommittedProgramVK = fmt.Sprintf("0x%x", committed.ProgramVK)
			}
		}
		result.Proofs = append(result.Proofs, model)
	}

	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	chainState := services.GlobalBeaconService.GetChainState()
	if chainState.EpochOfSlot(slot) < finalizedEpoch {
		return result, 30 * time.Minute
	}
	return result, 30 * time.Second
}

// slotExecutionProofPublicInput converts a public input to its page model.
func slotExecutionProofPublicInput(input *executionproof.PublicInput) *models.SlotExecutionProofPublicInput {
	if input == nil {
		return nil
	}
	return &models.SlotExecutionProofPublicInput{
		NewPayloadRequestRoot: fmt.Sprintf("0x%x", input.NewPayloadRequestRoot[:]),
		SuccessfulValidation:  input.SuccessfulValidation,
		ChainID:               input.ChainID,
		SchemaID:              input.SchemaID,
		Raw:                   fmt.Sprintf("0x%x", input.Marshal()),
	}
}

// SlotExecutionProofDownload serves the bytes of one execution proof of a block as
// file download.
func SlotExecutionProofDownload(w http.ResponseWriter, r *http.Request) {
	if pageError := services.GlobalCallRateLimiter.CheckCallLimit(r, 1); pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	vars := mux.Vars(r)
	proofType, err := strconv.ParseUint(vars["type"], 10, 8)
	if err != nil {
		http.Error(w, "Invalid proof type", http.StatusBadRequest)
		return
	}

	slot, blockRoot, found := resolveSlotPathBlock(r.Context(), vars["slotOrHash"])
	if !found {
		http.Error(w, "Block not found", http.StatusNotFound)
		return
	}

	for _, proof := range services.GlobalBeaconService.GetBlockExecutionProofs(r.Context(), slot, blockRoot, true) {
		if proof.ProofType != uint8(proofType) || proof.ProofData == nil {
			continue
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=proof-%d-%x-type%d.bin", slot, blockRoot[:], proof.ProofType))
		_, _ = w.Write(proof.ProofData)
		return
	}

	http.Error(w, "Proof not found", http.StatusNotFound)
}
