package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// APISlotBidsResponse represents the response for the slot bids endpoint
type APISlotBidsResponse struct {
	Status string           `json:"status"`
	Data   *APISlotBidsData `json:"data"`
}

// APISlotBidsData represents the bids data for a slot
type APISlotBidsData struct {
	Slot      uint64        `json:"slot"`
	BlockRoot string        `json:"block_root"`
	Count     uint64        `json:"count"`
	Bids      []*APISlotBid `json:"bids"`
}

// APISlotBid represents a single execution payload bid (ePBS, EIP-7732)
type APISlotBid struct {
	ParentRoot   string `json:"parent_root"`
	ParentHash   string `json:"parent_hash"`
	BlockHash    string `json:"block_hash"`
	FeeRecipient string `json:"fee_recipient"`
	GasLimit     uint64 `json:"gas_limit"`
	BuilderIndex uint64 `json:"builder_index"`
	BuilderName  string `json:"builder_name,omitempty"`
	IsSelfBuilt  bool   `json:"is_self_built"`
	Slot         uint64 `json:"slot"`
	Value        uint64 `json:"value"`
	ElPayment    uint64 `json:"el_payment"`
	TotalValue   uint64 `json:"total_value"`
	IsWinning    bool   `json:"is_winning"`
}

// APISlotBidsV1 returns all execution payload bids submitted for a slot (ePBS, EIP-7732)
// @Summary Get execution payload bids for a slot
// @Description Returns the execution payload bids submitted for a slot's parent root (ePBS, gloas+).
// @Tags Slot
// @Produce json
// @Param slotOrHash path string true "Slot number or block root (0x-prefixed hex)"
// @Success 200 {object} APISlotBidsResponse
// @Failure 400 {object} map[string]string "Invalid slot number or root format"
// @Failure 404 {object} map[string]string "Slot not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/slot/{slotOrHash}/bids [get]
// @ID getSlotBids
func APISlotBidsV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	slotOrHash := mux.Vars(r)["slotOrHash"]
	dbSlot := resolveSlotOrHash(r.Context(), w, slotOrHash)
	if dbSlot == nil {
		return
	}

	indexer := services.GlobalBeaconService.GetBeaconIndexer()
	var parentRoot phase0.Root
	copy(parentRoot[:], dbSlot.ParentRoot)
	bids := indexer.GetBlockBids(parentRoot, phase0.Slot(dbSlot.Slot))

	apiBids := make([]*APISlotBid, 0, len(bids))
	for _, bid := range bids {
		isWinning := len(dbSlot.EthBlockHash) > 0 && bytes.Equal(bid.BlockHash, dbSlot.EthBlockHash)

		// Dora's bid indexer casts the on-chain uint64 BuilderIndex to int64 and
		// uses -1 (== MaxUint64 reinterpreted) as a "self-built" sentinel. Surface
		// the spec-faithful uint64 on the wire plus a boolean for ergonomics.
		isSelfBuilt := bid.BuilderIndex < 0
		builderIndexU := uint64(bid.BuilderIndex)
		builderName := ""
		if !isSelfBuilt {
			builderName = services.GlobalBeaconService.GetValidatorName(builderIndexU | services.BuilderIndexFlag)
		}

		apiBids = append(apiBids, &APISlotBid{
			ParentRoot:   fmt.Sprintf("0x%x", bid.ParentRoot),
			ParentHash:   fmt.Sprintf("0x%x", bid.ParentHash),
			BlockHash:    fmt.Sprintf("0x%x", bid.BlockHash),
			FeeRecipient: fmt.Sprintf("0x%x", bid.FeeRecipient),
			GasLimit:     bid.GasLimit,
			BuilderIndex: builderIndexU,
			BuilderName:  builderName,
			IsSelfBuilt:  isSelfBuilt,
			Slot:         bid.Slot,
			Value:        bid.Value,
			ElPayment:    bid.ElPayment,
			TotalValue:   bid.Value + bid.ElPayment,
			IsWinning:    isWinning,
		})
	}

	sort.SliceStable(apiBids, func(i, j int) bool {
		return apiBids[i].TotalValue > apiBids[j].TotalValue
	})

	response := APISlotBidsResponse{
		Status: "OK",
		Data: &APISlotBidsData{
			Slot:      dbSlot.Slot,
			BlockRoot: fmt.Sprintf("0x%x", dbSlot.Root),
			Count:     uint64(len(apiBids)),
			Bids:      apiBids,
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode slot bids response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
	}
}
