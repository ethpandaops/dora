package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/spamoor/txtypes"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// APISlotInclusionListsResponse represents the response for the slot inclusion lists endpoint.
type APISlotInclusionListsResponse struct {
	Status string                     `json:"status"`
	Data   *APISlotInclusionListsData `json:"data"`
}

// APISlotInclusionListsData groups the EIP-7805 inclusion lists for a slot.
type APISlotInclusionListsData struct {
	Slot           uint64                  `json:"slot"`
	BlockRoot      string                  `json:"block_root"`
	Count          uint64                  `json:"count"`
	TargetSlot     uint64                  `json:"target_slot"`
	TargetRoot     string                  `json:"target_block_root,omitempty"`
	TargetNumber   uint64                  `json:"target_block_number,omitempty"`
	TargetGasLeft  uint64                  `json:"target_gas_left,omitempty"`
	InclusionLists []*APISlotInclusionList `json:"inclusion_lists"`
}

// APISlotInclusionList is a single signed inclusion list (EIP-7805).
type APISlotInclusionList struct {
	ValidatorIndex    uint64                             `json:"validator_index"`
	ValidatorName     string                             `json:"validator_name,omitempty"`
	DependentRoot     string                             `json:"dependent_root"`
	Signature         string                             `json:"signature"`
	SeenDelayMs       int64                              `json:"seen_delay_ms"`
	Timely            bool                               `json:"timely"`
	Equivocation      bool                               `json:"equivocation"`
	TransactionsCount uint64                             `json:"transactions_count"`
	Transactions      []*APISlotInclusionListTransaction `json:"transactions"`
}

// APISlotInclusionListTransaction describes one transaction in an inclusion list.
type APISlotInclusionListTransaction struct {
	Index      uint64 `json:"index"`
	Hash       string `json:"hash"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	Value      string `json:"value,omitempty"`
	Nonce      uint64 `json:"nonce"`
	GasLimit   uint64 `json:"gas_limit"`
	Type       uint8  `json:"type"`
	DataLen    uint64 `json:"data_len"`
	IsIncluded bool   `json:"is_included"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	DecodeErr  string `json:"decode_error,omitempty"`
}

// APISlotInclusionListsV1 returns the EIP-7805 inclusion lists for a slot.
// @Summary Get inclusion lists for a slot
// @Description Returns the cached EIP-7805 inclusion lists for a slot. Each transaction is checked against
// @Description the payload of the block at slot+1 (the payload the lists constrain): included, or the reason
// @Description it was validly omitted (block full, nonce too low/gap, insufficient funds, ...), or unsatisfied.
// @Tags Slot
// @Produce json
// @Param slotOrHash path string true "Slot number or block root (0x-prefixed hex)"
// @Success 200 {object} APISlotInclusionListsResponse
// @Failure 400 {object} map[string]string "Invalid slot number or root format"
// @Failure 404 {object} map[string]string "Slot not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/slot/{slotOrHash}/inclusion_lists [get]
// @ID getSlotInclusionLists
func APISlotInclusionListsV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	slotOrHash := mux.Vars(r)["slotOrHash"]
	dbSlot := resolveSlotOrHash(r.Context(), w, slotOrHash)
	if dbSlot == nil {
		return
	}

	indexer := services.GlobalBeaconService.GetBeaconIndexer()
	inclusionLists := indexer.GetInclusionListsBySlot(phase0.Slot(dbSlot.Slot))

	evaluation := services.GlobalBeaconService.EvaluateInclusionLists(r.Context(), phase0.Slot(dbSlot.Slot), inclusionLists)

	apiLists := make([]*APISlotInclusionList, 0, len(inclusionLists))
	for idx, entry := range inclusionLists {
		il := entry.InclusionList
		if il == nil || il.Message == nil {
			continue
		}
		listInfo := evaluation.Lists[idx]

		valIndex := uint64(il.Message.ValidatorIndex)
		listEntry := &APISlotInclusionList{
			ValidatorIndex: valIndex,
			ValidatorName:  services.GlobalBeaconService.GetValidatorNameAt(valIndex, phase0.Slot(dbSlot.Slot)),
			DependentRoot:  fmt.Sprintf("0x%x", il.Message.DependentRoot[:]),
			Signature:      fmt.Sprintf("0x%x", il.Signature[:]),
			SeenDelayMs:    listInfo.SeenDelay.Milliseconds(),
			Timely:         listInfo.Timely,
			Equivocation:   listInfo.Equivocation,
			Transactions:   make([]*APISlotInclusionListTransaction, 0, len(il.Message.Transactions)),
		}

		for idx, txBytes := range il.Message.Transactions {
			txEntry := &APISlotInclusionListTransaction{
				Index:   uint64(idx),
				DataLen: uint64(len(txBytes)),
			}

			tx, err := txtypes.DecodeTx(txBytes)
			if err != nil {
				txEntry.DecodeErr = err.Error()
			} else {
				txEntry.Hash = fmt.Sprintf("0x%x", tx.Hash().Bytes())
				txEntry.Type = tx.Type()
				txEntry.GasLimit = tx.Gas()
				txEntry.Nonce = tx.Nonce()
				if tx.To() != nil {
					txEntry.To = fmt.Sprintf("0x%x", tx.To().Bytes())
				}
				if v := tx.Value(); v != nil {
					txEntry.Value = v.String()
				}
				if from, err := tx.From(tx.ChainId()); err == nil {
					txEntry.From = fmt.Sprintf("0x%x", from.Bytes())
				}
				if txEval := evaluation.Transactions[tx.Hash()]; txEval != nil {
					txEntry.IsIncluded = txEval.Status == services.ILTxStatusIncluded
					txEntry.Status = txEval.Status.Label()
					txEntry.Reason = txEval.Reason
				}
			}

			listEntry.Transactions = append(listEntry.Transactions, txEntry)
		}
		listEntry.TransactionsCount = uint64(len(listEntry.Transactions))

		apiLists = append(apiLists, listEntry)
	}

	resp := APISlotInclusionListsResponse{
		Status: "OK",
		Data: &APISlotInclusionListsData{
			Slot:           dbSlot.Slot,
			BlockRoot:      fmt.Sprintf("0x%x", dbSlot.Root),
			Count:          uint64(len(apiLists)),
			TargetSlot:     uint64(evaluation.TargetSlot),
			TargetNumber:   evaluation.TargetBlockNumber,
			TargetGasLeft:  evaluation.TargetGasLeft,
			InclusionLists: apiLists,
		},
	}

	if evaluation.TargetBlockRoot != nil {
		resp.Data.TargetRoot = fmt.Sprintf("0x%x", evaluation.TargetBlockRoot)
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logrus.WithError(err).Error("failed to encode inclusion lists response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
	}
}
