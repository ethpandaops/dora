package handlers

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/mux"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// ElTransaction handles the EL transaction detail page
func ElTransaction(w http.ResponseWriter, r *http.Request) {
	var elTxTemplateFiles = append(layoutTemplateFiles,
		"el_transaction/el_transaction.html",
	)

	var elTxTemplate = templates.GetTemplate(elTxTemplateFiles...)

	// Check if EL indexer is enabled
	if !services.GlobalBeaconService.IsElIndexerEnabled() {
		handlePageError(w, r, fmt.Errorf("execution layer indexer not enabled"))
		return
	}

	data := InitPageData(w, r, "blockchain", "/tx", "Transaction", elTxTemplateFiles)

	vars := mux.Vars(r)
	txHash := vars["txHash"]

	var pageData *models.ElTransactionPageData
	var pageError error

	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		pageData, pageError = getElTransactionPageData(txHash)
	}

	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	data.Data = pageData

	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "el_transaction.go", "ElTransaction", "", elTxTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getElTransactionPageData(txHashStr string) (*models.ElTransactionPageData, error) {
	canonicalForkIds := services.GlobalBeaconService.GetCanonicalForkIds()

	// Parse tx hash
	if len(txHashStr) < 2 || txHashStr[:2] != "0x" {
		return nil, fmt.Errorf("invalid transaction hash format")
	}

	txHash := common.HexToHash(txHashStr).Bytes()
	tx, err := db.GetElTransaction(txHash, canonicalForkIds)
	if err != nil {
		return nil, fmt.Errorf("transaction not found: %v", err)
	}

	// Build page data
	pageData := &models.ElTransactionPageData{
		Hash:             "0x" + hex.EncodeToString(tx.Hash),
		BlockNumber:      tx.BlockNumber,
		BlockHash:        "0x" + hex.EncodeToString(tx.BlockHash),
		Timestamp:        time.Unix(int64(tx.BlockTimestamp), 0),
		Age:              utils.FormatDuration(time.Since(time.Unix(int64(tx.BlockTimestamp), 0))),
		Status:           tx.Status,
		StatusText:       getStatusText(tx.Status),
		TransactionIndex: tx.TransactionIndex,
		From:             "0x" + hex.EncodeToString(tx.FromAddress),
		Nonce:            tx.Nonce,
		TransactionType:  tx.TransactionType,
		GasLimit:         tx.GasLimit,
		GasUsed:          tx.GasUsed,
		GasUsedPercent:   float64(tx.GasUsed) / float64(tx.GasLimit) * 100.0,
		LogsCount:        tx.LogsCount,
		InternalTxCount:  tx.InternalTxCount,
	}

	// Error message
	if tx.ErrorMessage != nil {
		pageData.ErrorMessage = *tx.ErrorMessage
	}

	// To address
	if tx.ToAddress != nil {
		pageData.To = "0x" + hex.EncodeToString(tx.ToAddress)
		// Check if contract
		if addr, err := db.GetElAddress(tx.ToAddress); err == nil {
			pageData.ToIsContract = addr.IsContract
		}
	}

	// Contract created
	if tx.ContractAddress != nil {
		pageData.ContractAddress = "0x" + hex.EncodeToString(tx.ContractAddress)
		pageData.ContractCreated = true
	}

	// Check if from is contract
	if addr, err := db.GetElAddress(tx.FromAddress); err == nil {
		pageData.FromIsContract = addr.IsContract
		if addr.EnsName != nil {
			pageData.FromName = *addr.EnsName
		} else if addr.CustomName != nil {
			pageData.FromName = *addr.CustomName
		}
	}

	// Value
	value := new(big.Int).SetBytes(tx.Value)
	pageData.Value = value.String()
	pageData.ValueEth = utils.FormatEth(value)

	// Gas prices
	if tx.GasPrice != nil {
		pageData.GasPrice = tx.GasPrice
		pageData.GasPriceGwei = utils.FormatGwei(*tx.GasPrice)
	}

	// EIP-1559 fields
	if tx.MaxFeePerGas != nil {
		pageData.MaxFeePerGas = tx.MaxFeePerGas
		pageData.MaxFeeGwei = utils.FormatGwei(*tx.MaxFeePerGas)
	}

	if tx.MaxPriorityFeePerGas != nil {
		pageData.MaxPriorityFeePerGas = tx.MaxPriorityFeePerGas
		pageData.MaxPriorityGwei = utils.FormatGwei(*tx.MaxPriorityFeePerGas)
	}

	if tx.EffectiveGasPrice != nil {
		pageData.EffectiveGasPrice = tx.EffectiveGasPrice
		pageData.EffectiveGasPriceGwei = utils.FormatGwei(*tx.EffectiveGasPrice)

		// Calculate transaction fee
		txFee := new(big.Int).Mul(
			big.NewInt(int64(tx.GasUsed)),
			big.NewInt(int64(*tx.EffectiveGasPrice)),
		)
		pageData.TransactionFee = utils.FormatEth(txFee)
		pageData.TransactionFeeWei = txFee.String()
	}

	// Get base fee from block for burnt fees calculation
	if block, err := db.GetElBlock(tx.BlockHash); err == nil && block.BaseFeePerGas != nil {
		pageData.BaseFeePerGas = block.BaseFeePerGas
		pageData.BaseFeeGwei = utils.FormatGwei(*block.BaseFeePerGas)

		// Calculate burnt fees
		burntFees := new(big.Int).Mul(
			big.NewInt(int64(tx.GasUsed)),
			big.NewInt(int64(*block.BaseFeePerGas)),
		)
		pageData.BurntFees = utils.FormatEth(burntFees)
		pageData.BurntFeesWei = burntFees.String()
	}

	// Position in block
	if block, err := db.GetElBlock(tx.BlockHash); err == nil {
		pageData.PositionInBlock = fmt.Sprintf("%d of %d", tx.TransactionIndex+1, block.TransactionCount)
	}

	// Calculate confirmations
	chainState := services.GlobalBeaconService.GetExecutionChainState()
	if chainState != nil {
		// Get current block from chain state
		// For now, use a placeholder
		pageData.Confirmations = 0 // TODO: Get from chain state
	}

	// Input data
	if tx.InputData != nil {
		pageData.InputData = "0x" + hex.EncodeToString(tx.InputData)

		// Method signature
		if tx.MethodId != nil && len(tx.MethodId) >= 4 {
			pageData.MethodId = "0x" + hex.EncodeToString(tx.MethodId)
			if sig, err := db.GetElMethodSignature(tx.MethodId); err == nil {
				pageData.MethodSignature = sig.Name
			}
		}
	}

	// Get logs/events
	events, err := db.GetElEventsByTransaction(tx.Hash)
	if err == nil && len(events) > 0 {
		pageData.Logs = make([]*models.ElTransactionLog, len(events))
		for i, event := range events {
			log := &models.ElTransactionLog{
				Index:   event.LogIndex,
				Address: "0x" + hex.EncodeToString(event.Address),
			}

			// Topics
			topics := []string{}
			if event.Topic0 != nil {
				topics = append(topics, "0x"+hex.EncodeToString(event.Topic0))
			}
			if event.Topic1 != nil {
				topics = append(topics, "0x"+hex.EncodeToString(event.Topic1))
			}
			if event.Topic2 != nil {
				topics = append(topics, "0x"+hex.EncodeToString(event.Topic2))
			}
			if event.Topic3 != nil {
				topics = append(topics, "0x"+hex.EncodeToString(event.Topic3))
			}
			log.Topics = topics

			if event.Data != nil {
				log.Data = "0x" + hex.EncodeToString(event.Data)
			}

			if event.EventName != nil {
				log.EventName = *event.EventName
			}

			pageData.Logs[i] = log
		}
	}

	// Get internal transactions if enabled
	if utils.Config.Indexer.ElTrackInternalTxs {
		internalTxs, err := db.GetElInternalTxsByTransaction(tx.Hash)
		if err == nil && len(internalTxs) > 0 {
			pageData.InternalTxs = make([]*models.ElInternalTxSummary, len(internalTxs))
			for i, itx := range internalTxs {
				summary := &models.ElInternalTxSummary{
					TraceAddress: itx.TraceAddress,
					Type:         itx.TraceType,
					From:         "0x" + hex.EncodeToString(itx.FromAddress),
				}

				if itx.CallType != nil {
					summary.CallType = *itx.CallType
				}

				if itx.ToAddress != nil {
					summary.To = "0x" + hex.EncodeToString(itx.ToAddress)
				}

				if itx.Error != nil {
					summary.Error = *itx.Error
				}

				value := new(big.Int).SetBytes(itx.Value)
				summary.Value = value.String()
				summary.ValueEth = utils.FormatEth(value)

				if itx.Gas != nil {
					summary.Gas = *itx.Gas
				}
				if itx.GasUsed != nil {
					summary.GasUsed = *itx.GasUsed
				}

				pageData.InternalTxs[i] = summary
			}
		}
	}

	// Get token transfers if enabled
	if utils.Config.Indexer.ElTrackTokens {
		tokenTransfers, err := db.GetElTokenTransfersByTransaction(tx.Hash)
		if err == nil && len(tokenTransfers) > 0 {
			pageData.TokenTransfers = make([]*models.ElTokenTransferSummary, len(tokenTransfers))
			for i, transfer := range tokenTransfers {
				summary := &models.ElTokenTransferSummary{
					TokenAddress: "0x" + hex.EncodeToString(transfer.TokenAddress),
					TokenType:    transfer.TokenType,
					From:         "0x" + hex.EncodeToString(transfer.FromAddress),
					To:           "0x" + hex.EncodeToString(transfer.ToAddress),
				}

				// Get token info
				if token, err := db.GetElTokenContract(transfer.TokenAddress); err == nil {
					if token.Symbol != nil {
						summary.TokenSymbol = *token.Symbol
					}
					if token.Name != nil {
						summary.TokenName = *token.Name
					}
				}

				if transfer.Value != nil {
					summary.Value = hex.EncodeToString(transfer.Value)
					// TODO: Format with decimals
				}

				if transfer.TokenId != nil {
					summary.TokenId = hex.EncodeToString(transfer.TokenId)
				}

				pageData.TokenTransfers[i] = summary
			}
		}
	}

	return pageData, nil
}

func getStatusText(status uint8) string {
	if status == 1 {
		return "Success"
	}
	return "Failed"
}
