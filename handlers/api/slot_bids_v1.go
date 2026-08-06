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

	// Bid target classification relative to the slot's actual block parent chain.
	// ParentClass: "parent" (targets the block's actual beacon parent), "reorg" (targets a
	// deeper ancestor, orphaning ReorgDepth blocks), "orphaned" (targets a block outside
	// the block's chain), "unknown" (parent root not resolvable).
	ParentClass string `json:"parent_class"`
	// ParentFull: the bid builds on the targeted block's own payload (full) rather than an
	// earlier payload (empty).
	ParentFull bool   `json:"parent_full"`
	ReorgDepth uint64 `json:"reorg_depth,omitempty"`
	// ParentSlot is the slot of the targeted beacon parent block (0 if unknown).
	ParentSlot uint64 `json:"parent_slot,omitempty"`
	// ElParentSlot is the slot whose payload block hash matches the bid's parent hash,
	// i.e. the EL head the bid builds on (omitted if not resolvable).
	ElParentSlot *uint64 `json:"el_parent_slot,omitempty"`
	// ElParentUnrevealed: the bid's parent hash matches a committed payload hash that was
	// never revealed - such a bid can never become valid.
	ElParentUnrevealed bool `json:"el_parent_unrevealed,omitempty"`
	// CandidateKey is the buildoor-style candidate identifier (parent_full, parent_empty,
	// grandparent_full, grandparent_empty, ancestor-N_full/-empty); empty for orphaned-fork
	// and unknown-parent bids.
	CandidateKey string `json:"candidate_key,omitempty"`

	// Gossip visibility: SeenCount of SeenTotal connected clients observed the bid
	// on gossip. SeenTotal 0 = no observation data available for this bid.
	// Per-client details are served by the /slot/{slotOrHash}/bidseen page endpoint.
	SeenCount uint32 `json:"seen_count"`
	SeenTotal uint32 `json:"seen_total"`
}

// APISlotBidsV1 returns all execution payload bids submitted for a slot (ePBS, EIP-7732)
// @Summary Get execution payload bids for a slot
// @Description Returns all execution payload bids submitted for a slot (ePBS, gloas+), including bids targeting other forks or deeper ancestors (reorg bids). Each bid is classified against the slot's actual block parent chain.
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

	var parentRoot phase0.Root
	copy(parentRoot[:], dbSlot.ParentRoot)
	bids := services.GlobalBeaconService.GetSlotBidsClassified(r.Context(), phase0.Slot(dbSlot.Slot), parentRoot)

	apiBids := make([]*APISlotBid, 0, len(bids))
	for _, bid := range bids {
		// The winner is the exact bid committed in the block: several builders may bid the
		// same block hash, so the builder index and parent root must match too.
		isWinning := len(dbSlot.EthBlockHash) > 0 &&
			bytes.Equal(bid.BlockHash, dbSlot.EthBlockHash) &&
			bid.BuilderIndex == dbSlot.BuilderIndex &&
			bytes.Equal(bid.ParentRoot, dbSlot.ParentRoot)

		// Dora's bid indexer casts the on-chain uint64 BuilderIndex to int64 and
		// uses -1 (== MaxUint64 reinterpreted) as a "self-built" sentinel. Surface
		// the spec-faithful uint64 on the wire plus a boolean for ergonomics.
		isSelfBuilt := bid.BuilderIndex < 0
		builderIndexU := uint64(bid.BuilderIndex)
		builderName := ""
		if !isSelfBuilt {
			builderName = services.GlobalBeaconService.GetValidatorName(builderIndexU | services.BuilderIndexFlag)
		}

		apiBid := &APISlotBid{
			ParentRoot:         fmt.Sprintf("0x%x", bid.ParentRoot),
			ParentHash:         fmt.Sprintf("0x%x", bid.ParentHash),
			BlockHash:          fmt.Sprintf("0x%x", bid.BlockHash),
			FeeRecipient:       fmt.Sprintf("0x%x", bid.FeeRecipient),
			GasLimit:           bid.GasLimit,
			BuilderIndex:       builderIndexU,
			BuilderName:        builderName,
			IsSelfBuilt:        isSelfBuilt,
			Slot:               bid.Slot,
			Value:              bid.Value,
			ElPayment:          bid.ElPayment,
			TotalValue:         bid.Value + bid.ElPayment,
			IsWinning:          isWinning,
			ParentClass:        bid.ParentClass.String(),
			ParentFull:         bid.ParentFull,
			ReorgDepth:         bid.ReorgDepth,
			ParentSlot:         bid.ParentSlot,
			ElParentUnrevealed: bid.ElParentUnrevealed,
			CandidateKey:       bidCandidateKey(bid),
			SeenCount:          bid.SeenCount,
			SeenTotal:          bid.SeenTotal,
		}
		if bid.ElParentKnown {
			elParentSlot := bid.ElParentSlot
			apiBid.ElParentSlot = &elParentSlot
		}

		apiBids = append(apiBids, apiBid)
	}

	// Bids for the block's actual parent root first, then bids targeting other parents
	// (reorg/fork bids); within each group sorted by total value descending.
	sort.SliceStable(apiBids, func(i, j int) bool {
		iParent := apiBids[i].ParentClass == "parent"
		jParent := apiBids[j].ParentClass == "parent"
		if iParent != jParent {
			return iParent
		}
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

// bidCandidateKey maps a classified bid onto the candidate keys used by build tooling
// (parent_full, parent_empty, grandparent_full, grandparent_empty, ancestor-N_full/-empty).
func bidCandidateKey(bid *services.ClassifiedBlockBid) string {
	var position string
	switch bid.ParentClass {
	case services.BidParentClassParent:
		position = "parent"
	case services.BidParentClassReorg:
		if bid.ReorgDepth == 1 {
			position = "grandparent"
		} else {
			position = fmt.Sprintf("ancestor-%v", bid.ReorgDepth+1)
		}
	default:
		return ""
	}

	if bid.ParentFull {
		return position + "_full"
	}
	return position + "_empty"
}
