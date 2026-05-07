package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// resolveSlotOrHash parses a "slotOrHash" path parameter (slot number or
// 0x-prefixed 32-byte block root) and returns the matching canonical/orphaned
// slot record from the chain service. Returns nil when the value can't be
// parsed or no block is found, after writing an appropriate JSON error
// response. Shared by all /v1/slot/{slotOrHash}/... endpoints.
func resolveSlotOrHash(ctx context.Context, w http.ResponseWriter, slotOrHash string) *dbtypes.Slot {
	var filter *dbtypes.BlockFilter

	if strings.HasPrefix(slotOrHash, "0x") && len(slotOrHash) == 66 {
		rootBytes, err := hex.DecodeString(strings.TrimPrefix(slotOrHash, "0x"))
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid root format"}`, http.StatusBadRequest)
			return nil
		}
		filter = &dbtypes.BlockFilter{
			BlockRoot:    rootBytes,
			WithOrphaned: 1,
			WithMissing:  0,
		}
	} else {
		slot, err := strconv.ParseUint(slotOrHash, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid slot number or root format"}`, http.StatusBadRequest)
			return nil
		}
		filter = &dbtypes.BlockFilter{
			Slot:         &slot,
			WithOrphaned: 1,
			WithMissing:  0,
		}
	}

	assignedSlots := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, filter, 0, 1, 0)
	if len(assignedSlots) == 0 || assignedSlots[0].Block == nil {
		http.Error(w, `{"status": "ERROR: slot not found"}`, http.StatusNotFound)
		return nil
	}

	return assignedSlots[0].Block
}

// APISlotResponse represents the response for slot information
type APISlotResponse struct {
	Status string       `json:"status"`
	Data   *APISlotData `json:"data"`
}

// APISlotData represents detailed slot information
type APISlotData struct {
	AttestationsCount          uint64  `json:"attestationscount"`
	AttesterSlashingsCount     uint64  `json:"attesterslashingscount"`
	BlockRoot                  string  `json:"blockroot"`
	DepositsCount              uint64  `json:"depositscount"`
	Epoch                      uint64  `json:"epoch"`
	ExecBaseFeePerGas          uint64  `json:"exec_base_fee_per_gas"`
	ExecBlockHash              string  `json:"exec_block_hash"`
	ExecBlockNumber            uint64  `json:"exec_block_number"`
	ExecExtraData              string  `json:"exec_extra_data"`
	ExecFeeRecipient           string  `json:"exec_fee_recipient"`
	ExecGasLimit               uint64  `json:"exec_gas_limit"`
	ExecGasUsed                uint64  `json:"exec_gas_used"`
	ExecTransactionsCount      uint64  `json:"exec_transactions_count"`
	Graffiti                   string  `json:"graffiti"`
	GraffitiText               string  `json:"graffiti_text"`
	ParentRoot                 string  `json:"parentroot"`
	Proposer                   uint64  `json:"proposer"`
	ProposerSlashingsCount     uint64  `json:"proposerslashingscount"`
	Slot                       uint64  `json:"slot"`
	StateRoot                  string  `json:"stateroot"`
	Status                     string  `json:"status"`
	SyncAggregateParticipation float64 `json:"syncaggregate_participation"`
	VoluntaryExitsCount        uint64  `json:"voluntaryexitscount"`
	WithdrawalCount            uint64  `json:"withdrawalcount"`
	BlobCount                  uint64  `json:"blob_count"`
}

// APISlotV1 returns information about a specific slot by slot number or block root
// @Summary Get slot information
// @Description Returns detailed information about a specific slot from the database. Accepts either slot number or block root (0x-prefixed hex)
// @Tags Slot
// @Produce json
// @Param slotOrHash path string true "Slot number or block root (0x-prefixed hex)"
// @Success 200 {object} APISlotResponse
// @Failure 400 {object} map[string]string "Invalid slot number or root format"
// @Failure 404 {object} map[string]string "Slot not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/slot/{slotOrHash} [get]
// @ID getSlot
func APISlotV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	slotOrHash := vars["slotOrHash"]

	var filter *dbtypes.BlockFilter

	// Check if it's a block root (0x-prefixed hex) or slot number
	if strings.HasPrefix(slotOrHash, "0x") && len(slotOrHash) == 66 {
		// It's a block root
		rootStr := strings.TrimPrefix(slotOrHash, "0x")
		rootBytes, err := hex.DecodeString(rootStr)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid root format"}`, http.StatusBadRequest)
			return
		}

		// Create filter for block root
		filter = &dbtypes.BlockFilter{
			BlockRoot:    rootBytes,
			WithOrphaned: 1, // Include all (canonical + orphaned)
			WithMissing:  0, // Exclude missing (they don't have roots)
		}
	} else {
		// It's a slot number
		slot, err := strconv.ParseUint(slotOrHash, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "invalid slot number or root format"}`, http.StatusBadRequest)
			return
		}

		// Create filter for specific slot
		filter = &dbtypes.BlockFilter{
			Slot:         &slot,
			WithOrphaned: 1, // Include all (canonical + orphaned)
			WithMissing:  1, // Include missing blocks
		}
	}

	// Get slot data using ChainService with filter
	assignedSlots := services.GlobalBeaconService.GetDbBlocksByFilter(r.Context(), filter, 0, 1, 0)

	// Handle case where slot is not found
	if len(assignedSlots) == 0 {
		http.Error(w, `{"status": "ERROR: slot not found"}`, http.StatusNotFound)
		return
	}

	// Process the first (and only) assigned slot
	assignedSlot := assignedSlots[0]

	// Convert AssignedSlot to our API response format
	if assignedSlot.Block == nil {
		// Missing block - create minimal response
		response := APISlotResponse{
			Status: "OK",
			Data: &APISlotData{
				Slot:     assignedSlot.Slot,
				Proposer: assignedSlot.Proposer,
				Status:   "Missing",
				Epoch:    assignedSlot.Slot / 32, // Slots per epoch
			},
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			logrus.WithError(err).Error("failed to encode response")
			http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		}
		return
	}

	// Process actual block data
	dbSlot := assignedSlot.Block

	// Convert status
	var status string
	switch dbSlot.Status {
	case dbtypes.Missing:
		status = "Missing"
	case dbtypes.Canonical:
		status = "Canonical"
	case dbtypes.Orphaned:
		status = "Orphaned"
	default:
		status = "Unknown"
	}

	// Build response
	data := &APISlotData{
		AttestationsCount:          dbSlot.AttestationCount,
		AttesterSlashingsCount:     dbSlot.AttesterSlashingCount,
		BlockRoot:                  fmt.Sprintf("0x%x", dbSlot.Root),
		DepositsCount:              dbSlot.DepositCount,
		Epoch:                      dbSlot.Slot / 32, // Slots per epoch
		Graffiti:                   fmt.Sprintf("0x%x", dbSlot.Graffiti),
		GraffitiText:               dbSlot.GraffitiText,
		ParentRoot:                 fmt.Sprintf("0x%x", dbSlot.ParentRoot),
		Proposer:                   dbSlot.Proposer,
		ProposerSlashingsCount:     dbSlot.ProposerSlashingCount,
		Slot:                       dbSlot.Slot,
		StateRoot:                  fmt.Sprintf("0x%x", dbSlot.StateRoot),
		Status:                     status,
		SyncAggregateParticipation: float64(dbSlot.SyncParticipation),
		VoluntaryExitsCount:        dbSlot.ExitCount,
		WithdrawalCount:            dbSlot.WithdrawCount,
		ExecTransactionsCount:      dbSlot.EthTransactionCount,
		BlobCount:                  dbSlot.BlobCount,
	}

	// Add execution layer data if available
	if dbSlot.EthBlockNumber != nil {
		data.ExecBlockNumber = *dbSlot.EthBlockNumber
	}
	if len(dbSlot.EthBlockHash) > 0 {
		data.ExecBlockHash = fmt.Sprintf("0x%x", dbSlot.EthBlockHash)
	}
	if len(dbSlot.EthBlockExtra) > 0 {
		data.ExecExtraData = fmt.Sprintf("0x%x", dbSlot.EthBlockExtra)
	}
	if len(dbSlot.EthFeeRecipient) > 0 {
		data.ExecFeeRecipient = fmt.Sprintf("0x%x", dbSlot.EthFeeRecipient)
	}
	data.ExecGasLimit = dbSlot.EthGasLimit
	data.ExecGasUsed = dbSlot.EthGasUsed
	data.ExecBaseFeePerGas = dbSlot.EthBaseFee

	response := APISlotResponse{
		Status: "OK",
		Data:   data,
	}

	// Send response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
