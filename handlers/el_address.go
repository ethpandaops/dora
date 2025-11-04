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

// ElAddress handles the EL address detail page
func ElAddress(w http.ResponseWriter, r *http.Request) {
	var elAddressTemplateFiles = append(layoutTemplateFiles,
		"el_address/el_address.html",
	)

	var elAddressTemplate = templates.GetTemplate(elAddressTemplateFiles...)

	// Check if EL indexer is enabled
	if !services.GlobalBeaconService.IsElIndexerEnabled() {
		handlePageError(w, r, fmt.Errorf("execution layer indexer not enabled"))
		return
	}

	data := InitPageData(w, r, "blockchain", "/address", "Address", elAddressTemplateFiles)

	vars := mux.Vars(r)
	addressStr := vars["address"]

	var pageData *models.ElAddressPageData
	var pageError error

	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		// Get query parameters
		tab := r.URL.Query().Get("tab")
		if tab == "" {
			tab = "transactions"
		}

		pageStr := r.URL.Query().Get("page")
		page := uint(1)
		if pageStr != "" {
			if p, err := strconv.ParseUint(pageStr, 10, 32); err == nil {
				page = uint(p)
			}
		}

		pageData, pageError = getElAddressPageData(addressStr, tab, page)
	}

	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	data.Data = pageData

	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "el_address.go", "ElAddress", "", elAddressTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getElAddressPageData(addressStr string, tab string, page uint) (*models.ElAddressPageData, error) {
	canonicalForkIds := services.GlobalBeaconService.GetCanonicalForkIds()

	// Parse address
	if len(addressStr) < 2 || addressStr[:2] != "0x" {
		return nil, fmt.Errorf("invalid address format")
	}

	addressBytes := common.HexToAddress(addressStr).Bytes()
	address, err := db.GetElAddress(addressBytes)
	if err != nil {
		return nil, fmt.Errorf("address not found: %v", err)
	}

	// Build page data
	pageData := &models.ElAddressPageData{
		Address:        "0x" + hex.EncodeToString(address.Address),
		IsContract:     address.IsContract,
		FirstSeen:      time.Unix(int64(address.FirstSeenTimestamp), 0),
		FirstSeenAge:   utils.FormatDuration(time.Since(time.Unix(int64(address.FirstSeenTimestamp), 0))),
		LastSeen:       time.Unix(int64(address.LastSeenTimestamp), 0),
		LastSeenAge:    utils.FormatDuration(time.Since(time.Unix(int64(address.LastSeenTimestamp), 0))),
		TxCount:        address.TxCount,
		InTxCount:      address.InTxCount,
		OutTxCount:     address.OutTxCount,
		CurrentTab:     tab,
		PageSize:       25,
		CurrentPage:    page,
		ContractVerified: address.ContractVerified,
	}

	// ENS name
	if address.EnsName != nil {
		pageData.EnsName = *address.EnsName
	}
	if address.CustomName != nil {
		pageData.CustomName = *address.CustomName
	}

	// Balance
	balance := new(big.Int).SetBytes(address.Balance)
	pageData.Balance = balance.String()
	pageData.BalanceEth = utils.FormatEth(balance)

	// Contract info
	if address.IsContract {
		if address.ContractCreator != nil {
			pageData.ContractCreator = "0x" + hex.EncodeToString(address.ContractCreator)
		}
		if address.CreationTxHash != nil {
			pageData.CreationTxHash = "0x" + hex.EncodeToString(address.CreationTxHash)
		}
		if address.CreationTimestamp != nil {
			pageData.CreationTimestamp = time.Unix(int64(*address.CreationTimestamp), 0)
		}
	}

	// Pagination
	offset := (page - 1) * pageData.PageSize
	limit := pageData.PageSize

	// Load data based on tab
	switch tab {
	case "transactions":
		txs, totalCount, err := db.GetElTransactionsByAddress(addressBytes, offset, limit, canonicalForkIds)
		if err == nil {
			pageData.TotalCount = totalCount
			pageData.TotalPages = uint((totalCount + uint64(limit) - 1) / uint64(limit))

			pageData.Transactions = make([]*models.ElAddressTransaction, len(txs))
			for i, tx := range txs {
				txData := &models.ElAddressTransaction{
					Hash:        "0x" + hex.EncodeToString(tx.Hash),
					BlockNumber: tx.BlockNumber,
					Timestamp:   time.Unix(int64(tx.BlockTimestamp), 0),
					Age:         utils.FormatDuration(time.Since(time.Unix(int64(tx.BlockTimestamp), 0))),
					From:        "0x" + hex.EncodeToString(tx.FromAddress),
					Status:      tx.Status,
					GasUsed:     tx.GasUsed,
				}

				if tx.ToAddress != nil {
					txData.To = "0x" + hex.EncodeToString(tx.ToAddress)
				}

				// Determine direction
				if hex.EncodeToString(tx.FromAddress) == hex.EncodeToString(addressBytes) {
					if tx.ToAddress != nil && hex.EncodeToString(tx.ToAddress) == hex.EncodeToString(addressBytes) {
						txData.Direction = "SELF"
					} else {
						txData.Direction = "OUT"
					}
				} else {
					txData.Direction = "IN"
				}

				// Value
				value := new(big.Int).SetBytes(tx.Value)
				txData.ValueEth = utils.FormatEth(value)

				// Method name
				if tx.MethodId != nil && len(tx.MethodId) >= 4 {
					if sig, err := db.GetElMethodSignature(tx.MethodId); err == nil {
						txData.MethodName = sig.Name
					}
				}

				// Effective fee
				if tx.EffectiveGasPrice != nil {
					txData.EffectiveFeeGwei = utils.FormatGwei(*tx.EffectiveGasPrice)
				}

				pageData.Transactions[i] = txData
			}
		}

	case "internal":
		if utils.Config.Indexer.ElTrackInternalTxs {
			internalTxs, totalCount, err := db.GetElInternalTxsByAddress(addressBytes, offset, limit)
			if err == nil {
				pageData.TotalCount = totalCount
				pageData.TotalPages = uint((totalCount + uint64(limit) - 1) / uint64(limit))

				pageData.InternalTxs = make([]*models.ElAddressInternalTx, len(internalTxs))
				for i, itx := range internalTxs {
					itxData := &models.ElAddressInternalTx{
						TransactionHash: "0x" + hex.EncodeToString(itx.TransactionHash),
						BlockNumber:     itx.BlockNumber,
						Timestamp:       time.Unix(int64(itx.BlockTimestamp), 0),
						Age:             utils.FormatDuration(time.Since(time.Unix(int64(itx.BlockTimestamp), 0))),
						From:            "0x" + hex.EncodeToString(itx.FromAddress),
						Type:            itx.TraceType,
					}

					if itx.ToAddress != nil {
						itxData.To = "0x" + hex.EncodeToString(itx.ToAddress)
					}

					// Direction
					if hex.EncodeToString(itx.FromAddress) == hex.EncodeToString(addressBytes) {
						itxData.Direction = "OUT"
					} else {
						itxData.Direction = "IN"
					}

					// Value
					value := new(big.Int).SetBytes(itx.Value)
					itxData.ValueEth = utils.FormatEth(value)

					pageData.InternalTxs[i] = itxData
				}
			}
		}

	case "erc20", "tokens":
		if utils.Config.Indexer.ElTrackTokens {
			tokenTransfers, totalCount, err := db.GetElTokenTransfersByAddress(addressBytes, offset, limit)
			if err == nil {
				pageData.TotalCount = totalCount
				pageData.TotalPages = uint((totalCount + uint64(limit) - 1) / uint64(limit))

				pageData.TokenTransfers = make([]*models.ElAddressTokenTransfer, len(tokenTransfers))
				for i, transfer := range tokenTransfers {
					transferData := &models.ElAddressTokenTransfer{
						TransactionHash: "0x" + hex.EncodeToString(transfer.TransactionHash),
						BlockNumber:     transfer.BlockNumber,
						Timestamp:       time.Unix(int64(transfer.BlockTimestamp), 0),
						Age:             utils.FormatDuration(time.Since(time.Unix(int64(transfer.BlockTimestamp), 0))),
						TokenAddress:    "0x" + hex.EncodeToString(transfer.TokenAddress),
						TokenType:       transfer.TokenType,
						From:            "0x" + hex.EncodeToString(transfer.FromAddress),
						To:              "0x" + hex.EncodeToString(transfer.ToAddress),
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

					// Direction
					if hex.EncodeToString(transfer.FromAddress) == hex.EncodeToString(addressBytes) {
						transferData.Direction = "OUT"
					} else {
						transferData.Direction = "IN"
					}

					// Value
					if transfer.Value != nil {
						transferData.Value = hex.EncodeToString(transfer.Value)
						// TODO: Format with decimals
						transferData.ValueFormatted = transferData.Value
					}

					if transfer.TokenId != nil {
						transferData.TokenId = hex.EncodeToString(transfer.TokenId)
					}

					pageData.TokenTransfers[i] = transferData
				}
			}
		}
	}

	// Get token balances
	if utils.Config.Indexer.ElTrackTokens {
		balances, err := db.GetElTokenBalancesByAddress(addressBytes)
		if err == nil && len(balances) > 0 {
			pageData.TokenHoldingsCount = uint(len(balances))
			pageData.TokenHoldings = make([]*models.ElTokenBalance, len(balances))

			for i, bal := range balances {
				tokenBal := &models.ElTokenBalance{
					TokenAddress: "0x" + hex.EncodeToString(bal.TokenAddress),
					Balance:      hex.EncodeToString(bal.Balance),
					UpdatedAt:    time.Unix(int64(bal.UpdatedTimestamp), 0),
				}

				// Get token info
				if token, err := db.GetElTokenContract(bal.TokenAddress); err == nil {
					tokenBal.TokenType = token.TokenType
					if token.Symbol != nil {
						tokenBal.TokenSymbol = *token.Symbol
					}
					if token.Name != nil {
						tokenBal.TokenName = *token.Name
					}
					if token.Decimals != nil {
						tokenBal.Decimals = *token.Decimals
					}
				}

				// TODO: Format balance with decimals
				tokenBal.BalanceFormatted = tokenBal.Balance

				pageData.TokenHoldings[i] = tokenBal
			}
		}
	}

	return pageData, nil
}
