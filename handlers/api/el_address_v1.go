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

// APIElAddressResponse represents the response for EL address endpoint
type APIElAddressResponse struct {
	Status string              `json:"status"`
	Data   *APIElAddressData   `json:"data,omitempty"`
}

// APIElAddressData represents the EL address data
type APIElAddressData struct {
	// Address info
	Address    string `json:"address"`
	EnsName    string `json:"ens_name,omitempty"`
	CustomName string `json:"custom_name,omitempty"`
	IsContract bool   `json:"is_contract"`

	// Balance
	Balance    string `json:"balance"`     // Wei
	BalanceEth string `json:"balance_eth"` // Formatted ETH

	// Activity
	FirstSeenBlock  *uint64 `json:"first_seen_block,omitempty"`
	FirstSeenTime   *int64  `json:"first_seen_time,omitempty"`
	LastSeenBlock   *uint64 `json:"last_seen_block,omitempty"`
	LastSeenTime    *int64  `json:"last_seen_time,omitempty"`
	TransactionCount uint64  `json:"transaction_count"`

	// Contract details
	ContractCreator     string `json:"contract_creator,omitempty"`
	ContractCreatedTx   string `json:"contract_created_tx,omitempty"`
	ContractCreatedTime *int64 `json:"contract_created_time,omitempty"`

	// Token holdings (if enabled)
	TokenHoldings []*APIElTokenBalance `json:"token_holdings,omitempty"`

	// Transactions (paginated)
	Transactions   []*APIElAddressTransaction `json:"transactions,omitempty"`
	TotalTxCount   uint64                     `json:"total_tx_count"`
	PageNumber     uint64                     `json:"page_number"`
	PageSize       uint64                     `json:"page_size"`
	HasNextPage    bool                       `json:"has_next_page"`
}

// APIElTokenBalance represents a token balance
type APIElTokenBalance struct {
	TokenAddress   string `json:"token_address"`
	TokenName      string `json:"token_name,omitempty"`
	TokenSymbol    string `json:"token_symbol,omitempty"`
	TokenType      string `json:"token_type"`
	Balance        string `json:"balance"`
	BalanceFormatted string `json:"balance_formatted,omitempty"`
	LastUpdated    int64  `json:"last_updated"`
}

// APIElAddressTransaction represents a transaction involving the address
type APIElAddressTransaction struct {
	Hash             string `json:"hash"`
	BlockNumber      uint64 `json:"block_number"`
	Timestamp        int64  `json:"timestamp"`
	Age              string `json:"age"`
	From             string `json:"from"`
	FromName         string `json:"from_name,omitempty"`
	To               string `json:"to,omitempty"`
	ToName           string `json:"to_name,omitempty"`
	ToIsContract     bool   `json:"to_is_contract"`
	Direction        string `json:"direction"` // "IN", "OUT", "SELF"
	Value            string `json:"value"`     // Wei
	ValueEth         string `json:"value_eth"` // Formatted ETH
	Status           uint8  `json:"status"`
	GasUsed          uint64 `json:"gas_used"`
	EffectiveFee     string `json:"effective_fee,omitempty"` // Gwei
	MethodName       string `json:"method_name,omitempty"`
}

// APIElAddressV1 returns EL address details
// @Summary Get execution layer address details
// @Description Returns detailed information about an execution layer address
// @Tags Execution Layer
// @Accept json
// @Produce json
// @Param address path string true "Address (0x-prefixed)"
// @Param page query int false "Page number for transactions (starts at 1, default 1)"
// @Param limit query int false "Number of transactions per page (max 100, default 25)"
// @Param tab query string false "Data type to return: transactions, internal, tokens (default: transactions)"
// @Success 200 {object} APIElAddressResponse
// @Failure 400 {object} map[string]string "Invalid address"
// @Failure 404 {object} map[string]string "Address not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "EL indexer not enabled"
// @Router /v1/execution/address/{address} [get]
// @ID getElAddress
func APIElAddressV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check if EL indexer is enabled
	if !services.GlobalBeaconService.IsElIndexerEnabled() {
		http.Error(w, `{"status": "ERROR: execution layer indexer not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	vars := mux.Vars(r)
	addressStr := vars["address"]

	// Parse address
	if len(addressStr) < 2 || addressStr[:2] != "0x" {
		http.Error(w, `{"status": "ERROR: invalid address format"}`, http.StatusBadRequest)
		return
	}

	address := common.HexToAddress(addressStr).Bytes()

	// Get address info
	addr, err := db.GetElAddress(address)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"status": "ERROR: address not found: %v"}`, err), http.StatusNotFound)
		return
	}

	// Parse pagination parameters
	query := r.URL.Query()
	pageNumber := uint64(1)
	if query.Has("page") {
		if parsed, err := strconv.ParseUint(query.Get("page"), 10, 64); err == nil && parsed > 0 {
			pageNumber = parsed
		}
	}

	pageSize := uint64(25)
	if query.Has("limit") {
		if parsed, err := strconv.ParseUint(query.Get("limit"), 10, 64); err == nil && parsed > 0 {
			if parsed > 100 {
				parsed = 100
			}
			pageSize = parsed
		}
	}

	tab := query.Get("tab")
	if tab == "" {
		tab = "transactions"
	}

	canonicalForkIds := services.GlobalBeaconService.GetCanonicalForkIds()

	// Build response data
	addressData := &APIElAddressData{
		Address:    "0x" + hex.EncodeToString(addr.Address),
		IsContract: addr.IsContract,
	}

	// ENS name
	if addr.EnsName != nil {
		addressData.EnsName = *addr.EnsName
	}
	if addr.CustomName != nil {
		addressData.CustomName = *addr.CustomName
	}

	// Balance
	if addr.Balance != nil {
		balance := new(big.Int).SetBytes(addr.Balance)
		addressData.Balance = balance.String()
		addressData.BalanceEth = utils.FormatEth(balance)
	}

	// Activity timestamps
	if addr.FirstSeenBlock != nil {
		addressData.FirstSeenBlock = addr.FirstSeenBlock
		if addr.FirstSeenTime != nil {
			timestamp := int64(*addr.FirstSeenTime)
			addressData.FirstSeenTime = &timestamp
		}
	}
	if addr.LastSeenBlock != nil {
		addressData.LastSeenBlock = addr.LastSeenBlock
		if addr.LastSeenTime != nil {
			timestamp := int64(*addr.LastSeenTime)
			addressData.LastSeenTime = &timestamp
		}
	}

	// Contract details
	if addr.IsContract {
		if addr.ContractCreator != nil {
			addressData.ContractCreator = "0x" + hex.EncodeToString(addr.ContractCreator)
		}
		if addr.ContractCreationTx != nil {
			addressData.ContractCreatedTx = "0x" + hex.EncodeToString(addr.ContractCreationTx)
		}
		if addr.ContractCreationTime != nil {
			timestamp := int64(*addr.ContractCreationTime)
			addressData.ContractCreatedTime = &timestamp
		}
	}

	// Get token holdings if enabled
	if utils.Config.Indexer.ElTrackTokens && tab == "tokens" {
		balances, err := db.GetElTokenBalancesByAddress(address)
		if err == nil && len(balances) > 0 {
			addressData.TokenHoldings = make([]*APIElTokenBalance, 0)
			for _, bal := range balances {
				tokenBal := &APIElTokenBalance{
					TokenAddress: "0x" + hex.EncodeToString(bal.TokenAddress),
					TokenType:    bal.TokenType,
					LastUpdated:  int64(bal.LastUpdated),
				}

				// Get token info
				if token, err := db.GetElTokenContract(bal.TokenAddress); err == nil {
					if token.Symbol != nil {
						tokenBal.TokenSymbol = *token.Symbol
					}
					if token.Name != nil {
						tokenBal.TokenName = *token.Name
					}
				}

				if bal.Balance != nil {
					tokenBal.Balance = hex.EncodeToString(bal.Balance)
					tokenBal.BalanceFormatted = tokenBal.Balance // Could format with decimals
				}

				addressData.TokenHoldings = append(addressData.TokenHoldings, tokenBal)
			}
		}
	}

	// Get transactions
	if tab == "transactions" {
		offset := (pageNumber - 1) * pageSize
		txs, totalCount, err := db.GetElTransactionsByAddress(address, canonicalForkIds, pageSize, offset)
		if err == nil {
			addressData.TotalTxCount = totalCount
			addressData.PageNumber = pageNumber
			addressData.PageSize = pageSize
			addressData.HasNextPage = (offset + uint64(len(txs))) < totalCount

			if len(txs) > 0 {
				addressData.Transactions = make([]*APIElAddressTransaction, len(txs))
				for i, tx := range txs {
					txData := &APIElAddressTransaction{
						Hash:        "0x" + hex.EncodeToString(tx.Hash),
						BlockNumber: tx.BlockNumber,
						Timestamp:   int64(tx.BlockTimestamp),
						Age:         utils.FormatDuration(time.Since(time.Unix(int64(tx.BlockTimestamp), 0))),
						From:        "0x" + hex.EncodeToString(tx.FromAddress),
						Status:      tx.Status,
						GasUsed:     tx.GasUsed,
					}

					// Determine direction
					if tx.ToAddress != nil {
						txData.To = "0x" + hex.EncodeToString(tx.ToAddress)

						fromIsTarget := common.BytesToAddress(tx.FromAddress) == common.BytesToAddress(address)
						toIsTarget := common.BytesToAddress(tx.ToAddress) == common.BytesToAddress(address)

						if fromIsTarget && toIsTarget {
							txData.Direction = "SELF"
						} else if fromIsTarget {
							txData.Direction = "OUT"
						} else {
							txData.Direction = "IN"
						}

						// Check if contract
						if addr, err := db.GetElAddress(tx.ToAddress); err == nil {
							txData.ToIsContract = addr.IsContract
							if addr.EnsName != nil {
								txData.ToName = *addr.EnsName
							}
						}
					} else {
						txData.Direction = "OUT" // Contract creation
					}

					// From name
					if addr, err := db.GetElAddress(tx.FromAddress); err == nil {
						if addr.EnsName != nil {
							txData.FromName = *addr.EnsName
						}
					}

					// Value
					value := new(big.Int).SetBytes(tx.Value)
					txData.Value = value.String()
					txData.ValueEth = utils.FormatEth(value)

					// Method name
					if tx.MethodId != nil && len(tx.MethodId) >= 4 {
						if sig, err := db.GetElMethodSignature(tx.MethodId); err == nil {
							txData.MethodName = sig.Name
						}
					}

					// Effective fee
					if tx.EffectiveGasPrice != nil {
						txData.EffectiveFee = utils.FormatGwei(*tx.EffectiveGasPrice)
					}

					addressData.Transactions[i] = txData
				}
			}
		}
	}

	response := APIElAddressResponse{
		Status: "OK",
		Data:   addressData,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode EL address response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
