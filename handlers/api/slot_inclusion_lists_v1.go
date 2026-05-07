package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
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
	InclusionLists []*APISlotInclusionList `json:"inclusion_lists"`
}

// APISlotInclusionList is a single signed inclusion list (EIP-7805).
type APISlotInclusionList struct {
	ValidatorIndex             uint64                             `json:"validator_index"`
	ValidatorName              string                             `json:"validator_name,omitempty"`
	InclusionListCommitteeRoot string                             `json:"inclusion_list_committee_root"`
	Signature                  string                             `json:"signature"`
	TransactionsCount          uint64                             `json:"transactions_count"`
	Transactions               []*APISlotInclusionListTransaction `json:"transactions"`
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
	DecodeErr  string `json:"decode_error,omitempty"`
}

// APISlotInclusionListsV1 returns the EIP-7805 inclusion lists for a slot.
// @Summary Get inclusion lists for a slot
// @Description Returns the cached EIP-7805 inclusion lists for a slot, with each transaction
// @Description marked as included or not based on whether it appears in the slot's block transactions.
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

	// Build a set of block transaction hashes so each inclusion list transaction
	// can be marked included/not-included. For gloas+ the execution payload lives
	// in the SignedExecutionPayloadEnvelope, not on the block — VersionedSigned
	// BeaconBlock.ExecutionPayload() returns "no execution payload in gloas/heze"
	// in that case, so we need to construct the payload manually.
	blockTxHashes := make(map[string]bool)
	if blockData, err := services.GlobalBeaconService.GetSlotDetailsByBlockroot(r.Context(), phase0.Root(dbSlot.Root)); err == nil && blockData != nil && blockData.Block != nil {
		var executionPayload *spec.VersionedExecutionPayload
		if blockData.Block.Version >= spec.DataVersionGloas && blockData.Payload != nil {
			executionPayload = &spec.VersionedExecutionPayload{Version: blockData.Block.Version}
			switch blockData.Block.Version {
			case spec.DataVersionGloas:
				executionPayload.Gloas = blockData.Payload.Message.Payload
			case spec.DataVersionHeze:
				executionPayload.Heze = blockData.Payload.Message.Payload
			}
		} else {
			executionPayload, _ = blockData.Block.ExecutionPayload()
		}

		if executionPayload != nil {
			if txs, err := executionPayload.Transactions(); err == nil {
				for _, txBytes := range txs {
					var tx ethtypes.Transaction
					if err := tx.UnmarshalBinary(txBytes); err == nil {
						blockTxHashes[string(tx.Hash().Bytes())] = true
					}
				}
			}
		}
	}

	apiLists := make([]*APISlotInclusionList, 0, len(inclusionLists))
	for _, il := range inclusionLists {
		if il == nil || il.Message == nil {
			continue
		}

		valIndex := uint64(il.Message.ValidatorIndex)
		listEntry := &APISlotInclusionList{
			ValidatorIndex:             valIndex,
			ValidatorName:              services.GlobalBeaconService.GetValidatorName(valIndex),
			InclusionListCommitteeRoot: fmt.Sprintf("0x%x", il.Message.InclusionListCommitteeRoot[:]),
			Signature:                  fmt.Sprintf("0x%x", il.Signature[:]),
			Transactions:               make([]*APISlotInclusionListTransaction, 0, len(il.Message.Transactions)),
		}

		for idx, txBytes := range il.Message.Transactions {
			txEntry := &APISlotInclusionListTransaction{
				Index:   uint64(idx),
				DataLen: uint64(len(txBytes)),
			}

			var tx ethtypes.Transaction
			if err := tx.UnmarshalBinary(txBytes); err != nil {
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
				if from, err := ethtypes.Sender(ethtypes.LatestSignerForChainID(tx.ChainId()), &tx); err == nil {
					txEntry.From = fmt.Sprintf("0x%x", from.Bytes())
				}
				txEntry.IsIncluded = blockTxHashes[string(tx.Hash().Bytes())]
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
			InclusionLists: apiLists,
		},
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logrus.WithError(err).Error("failed to encode inclusion lists response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
	}
}
