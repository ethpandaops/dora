package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// APISlotBlockAccessListResponse represents the response for the slot BAL endpoint
type APISlotBlockAccessListResponse struct {
	Status string                      `json:"status"`
	Data   *APISlotBlockAccessListData `json:"data"`
}

// APISlotBlockAccessListData represents the EIP-7928 block access list for a slot
type APISlotBlockAccessListData struct {
	Slot      uint64                         `json:"slot"`
	BlockRoot string                         `json:"block_root"`
	Count     uint64                         `json:"count"`
	Accesses  []*APISlotBlockAccessListEntry `json:"accesses"`
}

// APISlotBlockAccessListEntry is the per-account section of the BAL.
type APISlotBlockAccessListEntry struct {
	Address        string                                `json:"address"`
	StorageChanges []*APISlotBlockAccessListStorageGroup `json:"storage_changes,omitempty"`
	StorageReads   []string                              `json:"storage_reads,omitempty"`
	BalanceChanges []*APISlotBlockAccessListBalance      `json:"balance_changes,omitempty"`
	NonceChanges   []*APISlotBlockAccessListNonce        `json:"nonce_changes,omitempty"`
	CodeChanges    []*APISlotBlockAccessListCode         `json:"code_changes,omitempty"`
}

// APISlotBlockAccessListStorageGroup groups the writes for a single storage slot.
type APISlotBlockAccessListStorageGroup struct {
	Slot    string                           `json:"slot"`
	Changes []*APISlotBlockAccessListStorage `json:"changes"`
}

// APISlotBlockAccessListStorage is a single storage write within a slot group.
type APISlotBlockAccessListStorage struct {
	BlockAccessIndex uint16 `json:"block_access_index"`
	Value            string `json:"value"`
}

// APISlotBlockAccessListBalance is a per-tx balance update for an account.
type APISlotBlockAccessListBalance struct {
	BlockAccessIndex uint16 `json:"block_access_index"`
	Balance          string `json:"balance"`
}

// APISlotBlockAccessListNonce is a per-tx nonce update for an account.
type APISlotBlockAccessListNonce struct {
	BlockAccessIndex uint16 `json:"block_access_index"`
	Nonce            uint64 `json:"nonce"`
}

// APISlotBlockAccessListCode is a per-tx code update for an account.
type APISlotBlockAccessListCode struct {
	BlockAccessIndex uint16 `json:"block_access_index"`
	Code             string `json:"code"`
}

// APISlotBlockAccessListV1 returns the EIP-7928 block access list for a slot
// @Summary Get block access list for a slot
// @Description Returns the EIP-7928 block access list (BAL) decoded for a specific slot.
// @Tags Slot
// @Produce json
// @Param slotOrHash path string true "Slot number or block root (0x-prefixed hex)"
// @Success 200 {object} APISlotBlockAccessListResponse
// @Failure 400 {object} map[string]string "Invalid slot number or root format"
// @Failure 404 {object} map[string]string "Slot not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/slot/{slotOrHash}/block_access_list [get]
// @ID getSlotBlockAccessList
func APISlotBlockAccessListV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// First confirm the slot exists in the indexer (clean 404 on miss). Going
	// straight to GetSlotDetailsByBlockroot for an unknown root surfaces a
	// "no clients available" error from beacon header loading and turns into a
	// 500 instead of a 404.
	dbSlot := resolveSlotOrHash(r.Context(), w, mux.Vars(r)["slotOrHash"])
	if dbSlot == nil {
		return
	}

	var resolvedRoot phase0.Root
	copy(resolvedRoot[:], dbSlot.Root)
	resolvedSlot := dbSlot.Slot

	blockData, err := services.GlobalBeaconService.GetSlotDetailsByBlockroot(r.Context(), resolvedRoot)
	if err != nil {
		logrus.WithError(err).Error("failed to load slot details")
		http.Error(w, `{"status": "ERROR: failed to load slot details"}`, http.StatusInternalServerError)
		return
	}
	if blockData == nil {
		// Indexer knows the slot but block details aren't loadable — treat as missing.
		writeBALResponse(w, resolvedSlot, resolvedRoot[:], nil)
		return
	}

	if len(blockData.BlockAccessList) == 0 {
		// No BAL is a valid response (pre-fulu / pre-7928 blocks). Return empty list.
		writeBALResponse(w, resolvedSlot, resolvedRoot[:], nil)
		return
	}

	accesses, decodeErr := utils.DecodeBlockAccessList(blockData.BlockAccessList)
	if decodeErr != nil {
		logrus.WithError(decodeErr).Errorf("failed to decode block access list for slot %v", resolvedSlot)
		http.Error(w, `{"status": "ERROR: failed to decode block access list"}`, http.StatusInternalServerError)
		return
	}

	apiEntries := make([]*APISlotBlockAccessListEntry, len(accesses))
	for i, entry := range accesses {
		apiEntry := &APISlotBlockAccessListEntry{
			Address: fmt.Sprintf("0x%x", entry.Address[:]),
		}

		if len(entry.StorageWrites) > 0 {
			apiEntry.StorageChanges = make([]*APISlotBlockAccessListStorageGroup, len(entry.StorageWrites))
			for j, group := range entry.StorageWrites {
				changes := make([]*APISlotBlockAccessListStorage, len(group.Accesses))
				for k, write := range group.Accesses {
					changes[k] = &APISlotBlockAccessListStorage{
						BlockAccessIndex: write.TxIdx,
						Value:            fmt.Sprintf("0x%x", write.ValueAfter),
					}
				}
				apiEntry.StorageChanges[j] = &APISlotBlockAccessListStorageGroup{
					Slot:    fmt.Sprintf("0x%x", group.Slot),
					Changes: changes,
				}
			}
		}

		if len(entry.StorageReads) > 0 {
			apiEntry.StorageReads = make([]string, len(entry.StorageReads))
			for j, read := range entry.StorageReads {
				apiEntry.StorageReads[j] = fmt.Sprintf("0x%x", read)
			}
		}

		if len(entry.BalanceChanges) > 0 {
			apiEntry.BalanceChanges = make([]*APISlotBlockAccessListBalance, len(entry.BalanceChanges))
			for j, balance := range entry.BalanceChanges {
				apiEntry.BalanceChanges[j] = &APISlotBlockAccessListBalance{
					BlockAccessIndex: balance.TxIdx,
					Balance:          fmt.Sprintf("0x%x", balance.Balance),
				}
			}
		}

		if len(entry.NonceChanges) > 0 {
			apiEntry.NonceChanges = make([]*APISlotBlockAccessListNonce, len(entry.NonceChanges))
			for j, nonce := range entry.NonceChanges {
				apiEntry.NonceChanges[j] = &APISlotBlockAccessListNonce{
					BlockAccessIndex: nonce.TxIdx,
					Nonce:            nonce.Nonce,
				}
			}
		}

		if len(entry.CodeChanges) > 0 {
			apiEntry.CodeChanges = make([]*APISlotBlockAccessListCode, len(entry.CodeChanges))
			for j, code := range entry.CodeChanges {
				apiEntry.CodeChanges[j] = &APISlotBlockAccessListCode{
					BlockAccessIndex: code.TxIndex,
					Code:             fmt.Sprintf("0x%x", code.Code),
				}
			}
		}

		apiEntries[i] = apiEntry
	}

	writeBALResponse(w, resolvedSlot, resolvedRoot[:], apiEntries)
}

func writeBALResponse(w http.ResponseWriter, slot uint64, root []byte, entries []*APISlotBlockAccessListEntry) {
	if entries == nil {
		entries = []*APISlotBlockAccessListEntry{}
	}
	resp := APISlotBlockAccessListResponse{
		Status: "OK",
		Data: &APISlotBlockAccessListData{
			Slot:      slot,
			BlockRoot: fmt.Sprintf("0x%x", root),
			Count:     uint64(len(entries)),
			Accesses:  entries,
		},
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logrus.WithError(err).Error("failed to encode block access list response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
	}
}
