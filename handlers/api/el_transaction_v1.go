package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
)

// APIElTransactionResponse represents the response for EL transaction endpoint
type APIElTransactionResponse struct {
	Status string                  `json:"status"`
	Data   *APIElTransactionData   `json:"data,omitempty"`
}

// APIElTransactionData represents the EL transaction data
type APIElTransactionData struct {
	// Transaction identifiers
	Hash         string `json:"hash"`
	BlockNumber  uint64 `json:"block_number"`
	BlockHash    string `json:"block_hash"`
	Timestamp    int64  `json:"timestamp"`
	Age          string `json:"age"`
	TransactionIndex uint `json:"transaction_index"`

	// Status
	Status     uint8  `json:"status"` // 1=success, 0=failed
	StatusText string `json:"status_text"`

	// Parties
	From         string `json:"from"`
	FromName     string `json:"from_name,omitempty"`
	To           string `json:"to,omitempty"`
	ToName       string `json:"to_name,omitempty"`
	ToIsContract bool   `json:"to_is_contract"`

	// Value
	Value    string `json:"value"`     // Wei
	ValueEth string `json:"value_eth"` // Formatted ETH

	// Gas
	GasLimit           uint64  `json:"gas_limit"`
	GasUsed            uint64  `json:"gas_used"`
	GasUsedPercent     float64 `json:"gas_used_percent"`
	GasPrice           *uint64 `json:"gas_price,omitempty"`            // Legacy transactions
	GasPriceGwei       string  `json:"gas_price_gwei,omitempty"`       // Formatted
	MaxFeePerGas       *uint64 `json:"max_fee_per_gas,omitempty"`      // EIP-1559
	MaxFeePerGasGwei   string  `json:"max_fee_per_gas_gwei,omitempty"` // Formatted
	MaxPriorityFeePerGas *uint64 `json:"max_priority_fee_per_gas,omitempty"` // EIP-1559
	MaxPriorityFeePerGasGwei string `json:"max_priority_fee_per_gas_gwei,omitempty"` // Formatted
	BaseFeePerGas      *uint64 `json:"base_fee_per_gas,omitempty"`     // EIP-1559
	BaseFeePerGasGwei  string  `json:"base_fee_per_gas_gwei,omitempty"` // Formatted
	EffectiveGasPrice  *uint64 `json:"effective_gas_price,omitempty"`
	EffectiveGasPriceGwei string `json:"effective_gas_price_gwei,omitempty"`
	TransactionFee     string  `json:"transaction_fee"`     // Wei
	TransactionFeeEth  string  `json:"transaction_fee_eth"` // Formatted ETH
	BurntFees          string  `json:"burnt_fees,omitempty"` // ETH (EIP-1559 only)
	BurntFeesWei       string  `json:"burnt_fees_wei,omitempty"` // Wei (EIP-1559 only)

	// Transaction data
	Nonce    uint64 `json:"nonce"`
	InputData string `json:"input_data"`
	MethodId  string `json:"method_id,omitempty"`
	MethodName string `json:"method_name,omitempty"`

	// Transaction type
	TxType uint8 `json:"tx_type"` // 0=legacy, 1=access list, 2=EIP-1559

	// Events/Logs
	LogCount uint                        `json:"log_count"`
	Logs     []*APIElTransactionLog      `json:"logs,omitempty"`

	// Internal transactions
	InternalTxCount uint                           `json:"internal_tx_count"`
	InternalTxs     []*APIElInternalTx             `json:"internal_txs,omitempty"`

	// Token transfers
	TokenTransferCount uint                          `json:"token_transfer_count"`
	TokenTransfers     []*APIElTokenTransfer         `json:"token_transfers,omitempty"`
}

// APIElTransactionLog represents an event log
type APIElTransactionLog struct {
	Index        uint     `json:"index"`
	Address      string   `json:"address"`
	AddressName  string   `json:"address_name,omitempty"`
	Topics       []string `json:"topics"`
	Data         string   `json:"data"`
	EventName    string   `json:"event_name,omitempty"`
	EventSig     string   `json:"event_sig,omitempty"`
}

// APIElInternalTx represents an internal transaction
type APIElInternalTx struct {
	Type      string `json:"type"`
	From      string `json:"from"`
	FromName  string `json:"from_name,omitempty"`
	To        string `json:"to,omitempty"`
	ToName    string `json:"to_name,omitempty"`
	Value     string `json:"value"`     // Wei
	ValueEth  string `json:"value_eth"` // Formatted ETH
	GasLimit  uint64 `json:"gas_limit"`
}

// APIElTokenTransfer represents a token transfer
type APIElTokenTransfer struct {
	TokenAddress   string `json:"token_address"`
	TokenName      string `json:"token_name,omitempty"`
	TokenSymbol    string `json:"token_symbol,omitempty"`
	TokenType      string `json:"token_type"`
	From           string `json:"from"`
	FromName       string `json:"from_name,omitempty"`
	To             string `json:"to"`
	ToName         string `json:"to_name,omitempty"`
	Value          string `json:"value,omitempty"`          // For ERC20
	ValueFormatted string `json:"value_formatted,omitempty"` // With decimals
	TokenId        string `json:"token_id,omitempty"`       // For ERC721/1155
}

// APIElTransactionV1 returns EL transaction details
// @Summary Get execution layer transaction details
// @Description Returns detailed information about an execution layer transaction
// @Tags Execution Layer
// @Accept json
// @Produce json
// @Param txHash path string true "Transaction hash (0x-prefixed)"
// @Success 200 {object} APIElTransactionResponse
// @Failure 400 {object} map[string]string "Invalid transaction hash"
// @Failure 404 {object} map[string]string "Transaction not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "EL indexer not enabled"
// @Router /v1/execution/tx/{txHash} [get]
// @ID getElTransaction
func APIElTransactionV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check if EL indexer is enabled
	if !services.GlobalBeaconService.IsElIndexerEnabled() {
		http.Error(w, `{"status": "ERROR: execution layer indexer not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	vars := mux.Vars(r)
	txHashStr := vars["txHash"]

	// Parse transaction hash
	if len(txHashStr) < 2 || txHashStr[:2] != "0x" {
		http.Error(w, `{"status": "ERROR: invalid transaction hash format"}`, http.StatusBadRequest)
		return
	}

	txHash := common.HexToHash(txHashStr).Bytes()
	tx, err := db.GetElTransaction(txHash)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"status": "ERROR: transaction not found: %v"}`, err), http.StatusNotFound)
		return
	}

	// Build response data
	txData := &APIElTransactionData{
		Hash:             "0x" + hex.EncodeToString(tx.Hash),
		BlockNumber:      tx.BlockNumber,
		BlockHash:        "0x" + hex.EncodeToString(tx.BlockHash),
		Timestamp:        int64(tx.BlockTimestamp),
		Age:              utils.FormatDuration(time.Since(time.Unix(int64(tx.BlockTimestamp), 0))),
		TransactionIndex: tx.TransactionIndex,
		Status:           tx.Status,
		From:             "0x" + hex.EncodeToString(tx.FromAddress),
		Nonce:            tx.Nonce,
		InputData:        "0x" + hex.EncodeToString(tx.InputData),
		TxType:           tx.TxType,
		GasLimit:         tx.GasLimit,
		GasUsed:          tx.GasUsed,
		GasUsedPercent:   float64(tx.GasUsed) / float64(tx.GasLimit) * 100.0,
	}

	// Status text
	if tx.Status == 1 {
		txData.StatusText = "Success"
	} else {
		txData.StatusText = "Failed"
	}

	// To address
	if tx.ToAddress != nil {
		txData.To = "0x" + hex.EncodeToString(tx.ToAddress)
		// Check if contract
		if addr, err := db.GetElAddress(tx.ToAddress); err == nil {
			txData.ToIsContract = addr.IsContract
			if addr.EnsName != nil {
				txData.ToName = *addr.EnsName
			} else if addr.CustomName != nil {
				txData.ToName = *addr.CustomName
			}
		}
	}

	// From name
	if addr, err := db.GetElAddress(tx.FromAddress); err == nil {
		if addr.EnsName != nil {
			txData.FromName = *addr.EnsName
		} else if addr.CustomName != nil {
			txData.FromName = *addr.CustomName
		}
	}

	// Value
	value := new(big.Int).SetBytes(tx.Value)
	txData.Value = value.String()
	txData.ValueEth = utils.FormatEth(value)

	// Gas pricing (EIP-1559 vs legacy)
	if tx.MaxFeePerGas != nil && tx.MaxPriorityFeePerGas != nil {
		// EIP-1559 transaction
		txData.MaxFeePerGas = tx.MaxFeePerGas
		txData.MaxFeePerGasGwei = utils.FormatGwei(*tx.MaxFeePerGas)
		txData.MaxPriorityFeePerGas = tx.MaxPriorityFeePerGas
		txData.MaxPriorityFeePerGasGwei = utils.FormatGwei(*tx.MaxPriorityFeePerGas)

		// Get base fee from block
		if block, err := db.GetElBlock(tx.BlockHash); err == nil && block.BaseFeePerGas != nil {
			txData.BaseFeePerGas = block.BaseFeePerGas
			txData.BaseFeePerGasGwei = utils.FormatGwei(*block.BaseFeePerGas)

			// Calculate burnt fees
			burntFees := new(big.Int).Mul(
				big.NewInt(int64(tx.GasUsed)),
				big.NewInt(int64(*block.BaseFeePerGas)),
			)
			txData.BurntFees = utils.FormatEth(burntFees)
			txData.BurntFeesWei = burntFees.String()
		}
	} else if tx.GasPrice != nil {
		// Legacy transaction
		txData.GasPrice = tx.GasPrice
		txData.GasPriceGwei = utils.FormatGwei(*tx.GasPrice)
	}

	// Effective gas price and transaction fee
	if tx.EffectiveGasPrice != nil {
		txData.EffectiveGasPrice = tx.EffectiveGasPrice
		txData.EffectiveGasPriceGwei = utils.FormatGwei(*tx.EffectiveGasPrice)

		txFee := new(big.Int).Mul(
			big.NewInt(int64(tx.GasUsed)),
			big.NewInt(int64(*tx.EffectiveGasPrice)),
		)
		txData.TransactionFee = txFee.String()
		txData.TransactionFeeEth = utils.FormatEth(txFee)
	}

	// Method ID and name
	if tx.MethodId != nil && len(tx.MethodId) >= 4 {
		txData.MethodId = "0x" + hex.EncodeToString(tx.MethodId)
		if sig, err := db.GetElMethodSignature(tx.MethodId); err == nil {
			txData.MethodName = sig.Name
		}
	}

	// Get logs/events
	if utils.Config.Indexer.ElTrackTokens {
		events, err := db.GetElEventsByTransaction(tx.Hash)
		if err == nil && len(events) > 0 {
			txData.LogCount = uint(len(events))
			txData.Logs = make([]*APIElTransactionLog, len(events))
			for i, evt := range events {
				log := &APIElTransactionLog{
					Index:   evt.LogIndex,
					Address: "0x" + hex.EncodeToString(evt.Address),
					Data:    "0x" + hex.EncodeToString(evt.Data),
				}

				// Topics
				log.Topics = make([]string, 0)
				if evt.Topic0 != nil {
					log.Topics = append(log.Topics, "0x"+hex.EncodeToString(evt.Topic0))
				}
				if evt.Topic1 != nil {
					log.Topics = append(log.Topics, "0x"+hex.EncodeToString(evt.Topic1))
				}
				if evt.Topic2 != nil {
					log.Topics = append(log.Topics, "0x"+hex.EncodeToString(evt.Topic2))
				}
				if evt.Topic3 != nil {
					log.Topics = append(log.Topics, "0x"+hex.EncodeToString(evt.Topic3))
				}

				// Event signature
				if evt.Topic0 != nil {
					if sig, err := db.GetElEventSignature(evt.Topic0); err == nil {
						log.EventName = sig.Name
						log.EventSig = sig.Signature
					}
				}

				txData.Logs[i] = log
			}
		}
	}

	// Get internal transactions
	if utils.Config.Indexer.ElTrackInternalTxs {
		internalTxs, err := db.GetElInternalTxsByTransaction(tx.Hash)
		if err == nil && len(internalTxs) > 0 {
			txData.InternalTxCount = uint(len(internalTxs))
			txData.InternalTxs = make([]*APIElInternalTx, len(internalTxs))
			for i, itx := range internalTxs {
				internalTxData := &APIElInternalTx{
					Type:     itx.TraceType,
					From:     "0x" + hex.EncodeToString(itx.FromAddress),
					GasLimit: itx.GasLimit,
				}

				if itx.ToAddress != nil {
					internalTxData.To = "0x" + hex.EncodeToString(itx.ToAddress)
				}

				value := new(big.Int).SetBytes(itx.Value)
				internalTxData.Value = value.String()
				internalTxData.ValueEth = utils.FormatEth(value)

				txData.InternalTxs[i] = internalTxData
			}
		}
	}

	// Get token transfers
	if utils.Config.Indexer.ElTrackTokens {
		transfers, err := db.GetElTokenTransfersByTransaction(tx.Hash)
		if err == nil && len(transfers) > 0 {
			txData.TokenTransferCount = uint(len(transfers))
			txData.TokenTransfers = make([]*APIElTokenTransfer, len(transfers))
			for i, transfer := range transfers {
				transferData := &APIElTokenTransfer{
					TokenAddress: "0x" + hex.EncodeToString(transfer.TokenAddress),
					TokenType:    transfer.TokenType,
					From:         "0x" + hex.EncodeToString(transfer.FromAddress),
					To:           "0x" + hex.EncodeToString(transfer.ToAddress),
				}

				// Get token info
				if token, err := db.GetElTokenContract(transfer.TokenAddress); err == nil {
					if token.Symbol != nil {
						transferData.TokenSymbol = *token.Symbol
					}
					if token.Name != nil {
						transferData.TokenName = *token.Name
					}
				}

				// Value
				if transfer.Value != nil {
					transferData.Value = hex.EncodeToString(transfer.Value)
					transferData.ValueFormatted = transferData.Value
				}

				if transfer.TokenId != nil {
					transferData.TokenId = hex.EncodeToString(transfer.TokenId)
				}

				txData.TokenTransfers[i] = transferData
			}
		}
	}

	response := APIElTransactionResponse{
		Status: "OK",
		Data:   txData,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode EL transaction response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
