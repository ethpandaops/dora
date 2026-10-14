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
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
)

// executionProofTypeNames maps the EIP-8025 proof types of the proof devnets to the
// zkVM + EL combination that produces them.
var executionProofTypeNames = map[uint8]string{
	1: "OpenVM + Ethrex",
	2: "SP1 + Ethrex",
	3: "Zisk + Ethrex",
	4: "OpenVM + Reth",
	5: "SP1 + Reth",
	6: "Zisk + Reth",
	7: "Zisk + Zesu",
}

// executionProofTypeName returns the display name of a proof type.
func executionProofTypeName(proofType uint8) string {
	if name, ok := executionProofTypeNames[proofType]; ok {
		return name
	}
	return fmt.Sprintf("zkVM + EL #%d", proofType)
}

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

// buildSlotExecutionProofsData lists the proofs of a block. The proofs of a block
// still being collected can grow, so the list of an unfinalized block is cached
// briefly; the stored list of a finalized block is final.
func buildSlotExecutionProofsData(ctx context.Context, slot phase0.Slot, blockRoot phase0.Root) (*models.SlotExecutionProofsResponse, time.Duration) {
	proofs := services.GlobalBeaconService.GetBlockExecutionProofs(ctx, slot, blockRoot, false)

	result := &models.SlotExecutionProofsResponse{
		Slot:      uint64(slot),
		BlockRoot: fmt.Sprintf("0x%x", blockRoot[:]),
		Proofs:    make([]*models.SlotExecutionProof, 0, len(proofs)),
	}
	for _, proof := range proofs {
		entry := &models.SlotExecutionProof{
			ProofType:   proof.ProofType,
			ProofName:   executionProofTypeName(proof.ProofType),
			Signed:      proof.Signed,
			Size:        proof.DataSize,
			DownloadUrl: fmt.Sprintf("/slot/0x%x/proofs/%d", blockRoot[:], proof.ProofType),
		}
		if proof.Signed {
			entry.ValidatorIndex = proof.ValidatorIndex
			entry.ValidatorName = services.GlobalBeaconService.GetValidatorNameAt(proof.ValidatorIndex, slot)
			entry.Signature = fmt.Sprintf("0x%x", proof.Signature[:])
		}
		result.Proofs = append(result.Proofs, entry)
	}

	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	chainState := services.GlobalBeaconService.GetChainState()
	if chainState.EpochOfSlot(slot) < finalizedEpoch {
		return result, 30 * time.Minute
	}
	return result, 30 * time.Second
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
