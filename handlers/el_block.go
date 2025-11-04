package handlers

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/mux"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// ElBlock handles the EL block detail page
func ElBlock(w http.ResponseWriter, r *http.Request) {
	var elBlockTemplateFiles = append(layoutTemplateFiles,
		"el_block/el_block.html",
	)

	var elBlockTemplate = templates.GetTemplate(elBlockTemplateFiles...)

	// Check if EL indexer is enabled
	if !services.GlobalBeaconService.IsElIndexerEnabled() {
		handlePageError(w, r, fmt.Errorf("execution layer indexer not enabled"))
		return
	}

	data := InitPageData(w, r, "blockchain", "/block", "Execution Block", elBlockTemplateFiles)

	vars := mux.Vars(r)
	blockIdent := vars["blockIdent"]

	var pageData *models.ElBlockPageData
	var pageError error

	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		pageData, pageError = getElBlockPageData(blockIdent)
	}

	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	data.Data = pageData

	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "el_block.go", "ElBlock", "", elBlockTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getElBlockPageData(blockIdent string) (*models.ElBlockPageData, error) {
	var block *dbtypes.ElBlock
	var err error

	canonicalForkIds := services.GlobalBeaconService.GetCanonicalForkIds()

	// Try to parse as block number
	if blockNum, parseErr := strconv.ParseUint(blockIdent, 10, 64); parseErr == nil {
		block, err = db.GetElBlockByNumber(blockNum, canonicalForkIds)
	} else {
		// Try as block hash
		if len(blockIdent) > 2 && blockIdent[:2] == "0x" {
			blockHash := common.HexToHash(blockIdent).Bytes()
			block, err = db.GetElBlock(blockHash)
		} else {
			return nil, fmt.Errorf("invalid block identifier")
		}
	}

	if err != nil {
		return nil, fmt.Errorf("block not found: %v", err)
	}

	// Build page data
	pageData := &models.ElBlockPageData{
		Number:       block.Number,
		Hash:         "0x" + hex.EncodeToString(block.Hash),
		ParentHash:   "0x" + hex.EncodeToString(block.ParentHash),
		StateRoot:    "0x" + hex.EncodeToString(block.StateRoot),
		ReceiptsRoot: "0x" + hex.EncodeToString(block.ReceiptsRoot),
		Timestamp:    time.Unix(int64(block.Timestamp), 0),
		Age:          utils.FormatDuration(time.Since(time.Unix(int64(block.Timestamp), 0))),
		FeeRecipient: "0x" + hex.EncodeToString(block.FeeRecipient),
		Size:         block.Size,
		SizeKB:       fmt.Sprintf("%.2f KB", float64(block.Size)/1024.0),
		GasUsed:      block.GasUsed,
		GasLimit:     block.GasLimit,
		GasUsedPercent: float64(block.GasUsed) / float64(block.GasLimit) * 100.0,
		TransactionCount: block.TransactionCount,
		InternalTxCount: block.InternalTxCount,
		EventCount:      block.EventCount,
		ExtraData:    "0x" + hex.EncodeToString(block.ExtraData),
		Nonce:        "0x" + hex.EncodeToString(block.Nonce),
		Orphaned:     block.Orphaned,
		TotalDifficulty: hex.EncodeToString(block.TotalDifficulty),
		Difficulty:      hex.EncodeToString(block.Difficulty),
	}

	// Format base fee if present
	if block.BaseFeePerGas != nil {
		pageData.BaseFeePerGas = block.BaseFeePerGas
		pageData.BaseFeeGwei = utils.FormatGwei(*block.BaseFeePerGas)

		// Calculate burnt fees
		burntFees := new(big.Int).Mul(
			big.NewInt(int64(block.GasUsed)),
			big.NewInt(int64(*block.BaseFeePerGas)),
		)
		pageData.BurntFees = utils.FormatEth(burntFees)
		pageData.BurntFeesWei = burntFees.String()
	}

	// Format block reward
	if len(block.TotalFees) > 0 {
		totalFees := new(big.Int).SetBytes(block.TotalFees)
		pageData.BlockReward = totalFees.String()
		pageData.BlockRewardEth = utils.FormatEth(totalFees)
	}

	// Get fee recipient name if available
	if addr, err := db.GetElAddress(block.FeeRecipient); err == nil {
		if addr.EnsName != nil {
			pageData.FeeRecipientName = *addr.EnsName
		} else if addr.CustomName != nil {
			pageData.FeeRecipientName = *addr.CustomName
		}
	}

	// Get transactions (limit to first 100 for display)
	txs, _, err := db.GetElTransactionsByBlock(block.Hash, canonicalForkIds)
	if err == nil && len(txs) > 0 {
		if len(txs) > 100 {
			txs = txs[:100]
		}

		pageData.Transactions = make([]*models.ElBlockTransactionSummary, len(txs))
		for i, tx := range txs {
			txSummary := &models.ElBlockTransactionSummary{
				Hash:     "0x" + hex.EncodeToString(tx.Hash),
				From:     "0x" + hex.EncodeToString(tx.FromAddress),
				Status:   tx.Status,
				GasUsed:  tx.GasUsed,
			}

			if tx.ToAddress != nil {
				txSummary.To = "0x" + hex.EncodeToString(tx.ToAddress)
				// Check if contract
				if addr, err := db.GetElAddress(tx.ToAddress); err == nil {
					txSummary.ToIsContract = addr.IsContract
				}
			}

			// Format value
			value := new(big.Int).SetBytes(tx.Value)
			txSummary.Value = value.String()
			txSummary.ValueEth = utils.FormatEth(value)

			// Get method name if available
			if tx.MethodId != nil && len(tx.MethodId) >= 4 {
				if sig, err := db.GetElMethodSignature(tx.MethodId); err == nil {
					txSummary.MethodName = sig.Name
				}
			}

			// Format effective gas price
			if tx.EffectiveGasPrice != nil {
				txSummary.EffectiveFee = utils.FormatGwei(*tx.EffectiveGasPrice)
			}

			pageData.Transactions[i] = txSummary
		}
	}

	return pageData, nil
}
