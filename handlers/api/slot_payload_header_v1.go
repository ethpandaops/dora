package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// APISlotPayloadHeaderResponse represents the response for the slot payload header endpoint.
type APISlotPayloadHeaderResponse struct {
	Status string                    `json:"status"`
	Data   *APISlotPayloadHeaderData `json:"data"`
}

// APISlotPayloadHeaderData wraps the gloas signed execution payload bid (header).
type APISlotPayloadHeaderData struct {
	Slot               uint64   `json:"slot"`
	BlockRoot          string   `json:"block_root"`
	HasPayloadHeader   bool     `json:"has_payload_header"`
	PayloadStatus      uint16   `json:"payload_status"`
	PayloadStatusName  string   `json:"payload_status_name"`
	ParentBlockHash    string   `json:"parent_block_hash,omitempty"`
	ParentBlockRoot    string   `json:"parent_block_root,omitempty"`
	BlockHash          string   `json:"block_hash,omitempty"`
	GasLimit           uint64   `json:"gas_limit,omitempty"`
	BuilderIndex       uint64   `json:"builder_index"`
	BuilderName        string   `json:"builder_name,omitempty"`
	IsSelfBuilt        bool     `json:"is_self_built"`
	Value              uint64   `json:"value,omitempty"`
	BlobKZGCommitments []string `json:"blob_kzg_commitments,omitempty"`
	Signature          string   `json:"signature,omitempty"`
}

// APISlotPayloadHeaderV1 returns the gloas+ signed execution payload bid (header) for a slot.
// @Summary Get the execution payload header for a slot
// @Description Returns the gloas+ signed execution payload bid (header) included in a block,
// @Description plus its observed payload status (canonical / orphaned). Pre-gloas blocks return
// @Description has_payload_header=false.
// @Tags Slot
// @Produce json
// @Param slotOrHash path string true "Slot number or block root (0x-prefixed hex)"
// @Success 200 {object} APISlotPayloadHeaderResponse
// @Failure 400 {object} map[string]string "Invalid slot number or root format"
// @Failure 404 {object} map[string]string "Slot not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/slot/{slotOrHash}/payload_header [get]
// @ID getSlotPayloadHeader
func APISlotPayloadHeaderV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Resolve through the indexer first so unknown slots/roots return 404
	// instead of bubbling up a 500 from beacon header loading.
	dbSlot := resolveSlotOrHash(r.Context(), w, mux.Vars(r)["slotOrHash"])
	if dbSlot == nil {
		return
	}

	var resolvedRoot phase0.Root
	copy(resolvedRoot[:], dbSlot.Root)

	data := &APISlotPayloadHeaderData{
		Slot:              dbSlot.Slot,
		BlockRoot:         fmt.Sprintf("0x%x", dbSlot.Root),
		PayloadStatusName: payloadStatusName(dbtypes.PayloadStatusMissing),
	}

	blockData, err := services.GlobalBeaconService.GetSlotDetailsByBlockroot(r.Context(), resolvedRoot)
	if err != nil {
		logrus.WithError(err).Error("failed to load slot details")
		http.Error(w, `{"status": "ERROR: failed to load slot details"}`, http.StatusInternalServerError)
		return
	}
	if blockData == nil || blockData.Block == nil {
		writePayloadHeaderResponse(w, data)
		return
	}

	if blockData.Block.Version < spec.DataVersionGloas {
		writePayloadHeaderResponse(w, data)
		return
	}

	if blockData.Block.Message == nil || blockData.Block.Message.Body == nil {
		writePayloadHeaderResponse(w, data)
		return
	}

	signedBid := blockData.Block.Message.Body.SignedExecutionPayloadBid
	if signedBid == nil || signedBid.Message == nil {
		writePayloadHeaderResponse(w, data)
		return
	}
	bid := signedBid.Message

	commitments := make([]string, len(bid.BlobKZGCommitments))
	for i := range bid.BlobKZGCommitments {
		commitments[i] = fmt.Sprintf("0x%x", bid.BlobKZGCommitments[i][:])
	}

	// Self-built blocks come over the wire with builder_index = MaxUint64; mirror
	// the bids endpoint and surface that as is_self_built rather than as a magic
	// "named validator at MaxUint64" lookup.
	builderIndexU := uint64(bid.BuilderIndex)
	isSelfBuilt := builderIndexU == math.MaxUint64

	blockHash := bid.BlockHash

	data.HasPayloadHeader = true
	data.ParentBlockHash = fmt.Sprintf("0x%x", bid.ParentBlockHash[:])
	data.ParentBlockRoot = fmt.Sprintf("0x%x", bid.ParentBlockRoot[:])
	data.BlockHash = fmt.Sprintf("0x%x", blockHash[:])
	data.GasLimit = bid.GasLimit
	data.BuilderIndex = builderIndexU
	data.IsSelfBuilt = isSelfBuilt
	if !isSelfBuilt {
		data.BuilderName = services.GlobalBeaconService.GetValidatorName(builderIndexU | services.BuilderIndexFlag)
	}
	data.Value = uint64(bid.Value)
	data.BlobKZGCommitments = commitments
	data.Signature = fmt.Sprintf("0x%x", signedBid.Signature[:])

	// Determine payload status — match the slot page logic: status is canonical
	// unless we have a canonical child that does NOT build on this payload.
	status := dbtypes.PayloadStatusCanonical
	if blockData.Payload != nil {
		childSlots := services.GlobalBeaconService.GetDbBlocksByParentRoot(r.Context(), blockData.Root)
		hasCanonicalChild := false
		payloadIncluded := false
		for _, child := range childSlots {
			if child.Status != dbtypes.Canonical {
				continue
			}
			hasCanonicalChild = true
			if bytes.Equal(child.EthBlockParentHash, blockHash[:]) {
				payloadIncluded = true
				break
			}
		}
		if hasCanonicalChild && !payloadIncluded {
			status = dbtypes.PayloadStatusOrphaned
		}
	} else {
		status = dbtypes.PayloadStatusMissing
	}
	data.PayloadStatus = uint16(status)
	data.PayloadStatusName = payloadStatusName(status)

	writePayloadHeaderResponse(w, data)
}

func payloadStatusName(status dbtypes.PayloadStatus) string {
	switch status {
	case dbtypes.PayloadStatusCanonical:
		return "canonical"
	case dbtypes.PayloadStatusOrphaned:
		return "orphaned"
	case dbtypes.PayloadStatusMissing:
		return "missing"
	default:
		return "unknown"
	}
}

func writePayloadHeaderResponse(w http.ResponseWriter, data *APISlotPayloadHeaderData) {
	resp := APISlotPayloadHeaderResponse{Status: "OK", Data: data}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logrus.WithError(err).Error("failed to encode payload header response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
	}
}
