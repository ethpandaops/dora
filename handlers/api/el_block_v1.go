package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
)

// APIElBlockResponse represents the response for EL block endpoint
type APIElBlockResponse struct {
	Status string         `json:"status"`
	Data   *APIElBlockData `json:"data,omitempty"`
}

// APIElBlockData represents the EL block data
type APIElBlockData struct {
	// Block identifiers
	Number       uint64 `json:"number"`
	Hash         string `json:"hash"`
	ParentHash   string `json:"parent_hash"`
	StateRoot    string `json:"state_root"`
	ReceiptsRoot string `json:"receipts_root"`

	// Timing
	Timestamp int64  `json:"timestamp"`
	Age       string `json:"age"`

	// Block producer
	FeeRecipient     string `json:"fee_recipient"`
	FeeRecipientName string `json:"fee_recipient_name,omitempty"`
	BlockReward      string `json:"block_reward"`     // Wei
	BlockRewardEth   string `json:"block_reward_eth"` // Formatted in ETH
	TotalDifficulty  string `json:"total_difficulty"`
	Difficulty       string `json:"difficulty"`

	// Size
	Size   uint64 `json:"size"`
	SizeKB string `json:"size_kb"`

	// Gas
	GasUsed        uint64  `json:"gas_used"`
	GasLimit       uint64  `json:"gas_limit"`
	GasUsedPercent float64 `json:"gas_used_percent"`
	BaseFeePerGas  *uint64 `json:"base_fee_per_gas,omitempty"`
	BaseFeeGwei    string  `json:"base_fee_gwei,omitempty"`
	BurntFees      string  `json:"burnt_fees,omitempty"`     // ETH
	BurntFeesWei   string  `json:"burnt_fees_wei,omitempty"` // Wei

	// Transactions
	TransactionCount uint                      `json:"transaction_count"`
	Transactions     []*APIElBlockTransaction  `json:"transactions"`

	// Metadata
	ExtraData string `json:"extra_data"`
	Nonce     string `json:"nonce"`
	Orphaned  bool   `json:"orphaned"`

	// Internal counts
	InternalTxCount uint `json:"internal_tx_count"`
	EventCount      uint `json:"event_count"`
}

// APIElBlockTransaction represents a transaction in the block
type APIElBlockTransaction struct {
	Hash          string `json:"hash"`
	MethodName    string `json:"method_name,omitempty"`
	From          string `json:"from"`
	FromName      string `json:"from_name,omitempty"`
	To            string `json:"to,omitempty"`
	ToName        string `json:"to_name,omitempty"`
	ToIsContract  bool   `json:"to_is_contract"`
	Value         string `json:"value"`     // Wei
	ValueEth      string `json:"value_eth"` // Formatted ETH
	Status        uint8  `json:"status"`    // 1=success, 0=failed
	GasUsed       uint64 `json:"gas_used"`
	EffectiveFee  string `json:"effective_fee,omitempty"` // In Gwei
}

// APIElBlockV1 returns EL block details
// @Summary Get execution layer block details
// @Description Returns detailed information about an execution layer block by number or hash
// @Tags Execution Layer
// @Accept json
// @Produce json
// @Param blockIdent path string true "Block number or hash (0x-prefixed)"
// @Success 200 {object} APIElBlockResponse
// @Failure 400 {object} map[string]string "Invalid block identifier"
// @Failure 404 {object} map[string]string "Block not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "EL indexer not enabled"
// @Router /v1/execution/block/{blockIdent} [get]
// @ID getElBlock
func APIElBlockV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check if EL indexer is enabled
	if !services.GlobalBeaconService.IsElIndexerEnabled() {
		http.Error(w, `{"status": "ERROR: execution layer indexer not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	vars := mux.Vars(r)
	blockIdent := vars["blockIdent"]

	canonicalForkIds := services.GlobalBeaconService.GetCanonicalForkIds()

	// Try to parse as block number
	var block *dbtypes.ElBlock
	var err error

	if blockNum, parseErr := strconv.ParseUint(blockIdent, 10, 64); parseErr == nil {
		block, err = db.GetElBlockByNumber(blockNum, canonicalForkIds)
	} else {
		// Try as block hash
		if len(blockIdent) > 2 && blockIdent[:2] == "0x" {
			blockHash := common.HexToHash(blockIdent).Bytes()
			block, err = db.GetElBlock(blockHash)
		} else {
			http.Error(w, `{"status": "ERROR: invalid block identifier"}`, http.StatusBadRequest)
			return
		}
	}

	if err != nil {
		http.Error(w, fmt.Sprintf(`{"status": "ERROR: block not found: %v"}`, err), http.StatusNotFound)
		return
	}

	// Build response data
	blockData := &APIElBlockData{
		Number:           block.Number,
		Hash:             "0x" + hex.EncodeToString(block.Hash),
		ParentHash:       "0x" + hex.EncodeToString(block.ParentHash),
		StateRoot:        "0x" + hex.EncodeToString(block.StateRoot),
		ReceiptsRoot:     "0x" + hex.EncodeToString(block.ReceiptsRoot),
		Timestamp:        int64(block.Timestamp),
		Age:              utils.FormatDuration(time.Since(time.Unix(int64(block.Timestamp), 0))),
		FeeRecipient:     "0x" + hex.EncodeToString(block.FeeRecipient),
		Size:             block.Size,
		SizeKB:           fmt.Sprintf("%.2f KB", float64(block.Size)/1024.0),
		GasUsed:          block.GasUsed,
		GasLimit:         block.GasLimit,
		GasUsedPercent:   float64(block.GasUsed) / float64(block.GasLimit) * 100.0,
		TransactionCount: block.TransactionCount,
		InternalTxCount:  block.InternalTxCount,
		EventCount:       block.EventCount,
		ExtraData:        "0x" + hex.EncodeToString(block.ExtraData),
		Nonce:            "0x" + hex.EncodeToString(block.Nonce),
		Orphaned:         block.Orphaned,
		TotalDifficulty:  hex.EncodeToString(block.TotalDifficulty),
		Difficulty:       hex.EncodeToString(block.Difficulty),
	}

	// Format base fee if present
	if block.BaseFeePerGas != nil {
		blockData.BaseFeePerGas = block.BaseFeePerGas
		blockData.BaseFeeGwei = utils.FormatGwei(*block.BaseFeePerGas)

		// Calculate burnt fees
		burntFees := new(big.Int).Mul(
			big.NewInt(int64(block.GasUsed)),
			big.NewInt(int64(*block.BaseFeePerGas)),
		)
		blockData.BurntFees = utils.FormatEth(burntFees)
		blockData.BurntFeesWei = burntFees.String()
	}

	// Format block reward
	if len(block.TotalFees) > 0 {
		totalFees := new(big.Int).SetBytes(block.TotalFees)
		blockData.BlockReward = totalFees.String()
		blockData.BlockRewardEth = utils.FormatEth(totalFees)
	}

	// Get fee recipient name if available
	if addr, err := db.GetElAddress(block.FeeRecipient); err == nil {
		if addr.EnsName != nil {
			blockData.FeeRecipientName = *addr.EnsName
		} else if addr.CustomName != nil {
			blockData.FeeRecipientName = *addr.CustomName
		}
	}

	// Get transactions (limit to first 100 for API)
	txs, _, err := db.GetElTransactionsByBlock(block.Hash, canonicalForkIds)
	if err == nil && len(txs) > 0 {
		if len(txs) > 100 {
			txs = txs[:100]
		}

		blockData.Transactions = make([]*APIElBlockTransaction, len(txs))
		for i, tx := range txs {
			txData := &APIElBlockTransaction{
				Hash:    "0x" + hex.EncodeToString(tx.Hash),
				From:    "0x" + hex.EncodeToString(tx.FromAddress),
				Status:  tx.Status,
				GasUsed: tx.GasUsed,
			}

			if tx.ToAddress != nil {
				txData.To = "0x" + hex.EncodeToString(tx.ToAddress)
				// Check if contract
				if addr, err := db.GetElAddress(tx.ToAddress); err == nil {
					txData.ToIsContract = addr.IsContract
				}
			}

			// Format value
			value := new(big.Int).SetBytes(tx.Value)
			txData.Value = value.String()
			txData.ValueEth = utils.FormatEth(value)

			// Get method name if available
			if tx.MethodId != nil && len(tx.MethodId) >= 4 {
				if sig, err := db.GetElMethodSignature(tx.MethodId); err == nil {
					txData.MethodName = sig.Name
				}
			}

			// Format effective gas price
			if tx.EffectiveGasPrice != nil {
				txData.EffectiveFee = utils.FormatGwei(*tx.EffectiveGasPrice)
			}

			blockData.Transactions[i] = txData
		}
	}

	response := APIElBlockResponse{
		Status: "OK",
		Data:   blockData,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode EL block response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
