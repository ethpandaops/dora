package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
)

// AddressBalances handles the /address/{address}/balances AJAX endpoint
// Returns JSON balance data for auto-refresh functionality
func AddressBalances(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	addressHex := strings.TrimPrefix(vars["address"], "0x")

	addressBytes, err := hex.DecodeString(addressHex)
	if err != nil || len(addressBytes) != 20 {
		http.Error(w, "Invalid address", http.StatusBadRequest)
		return
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)

	var pageData *AddressBalancesResponse
	if pageError == nil {
		pageData, pageError = getAddressBalancesPageData(addressBytes)
	}

	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(pageData); err != nil {
		logrus.WithError(err).Error("failed to encode balance response")
	}
}

func getAddressBalancesPageData(addressBytes []byte) (*AddressBalancesResponse, error) {
	pageData := &AddressBalancesResponse{}
	pageCacheKey := fmt.Sprintf("addr_balances:%x", addressBytes)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildBalancesResponse(pageCall.CallCtx, addressBytes)
		_ = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*AddressBalancesResponse)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

// AddressBalancesResponse is the JSON response for the balances endpoint
type AddressBalancesResponse struct {
	EthBalance    float64                        `json:"eth_balance"`
	EthBalanceRaw string                         `json:"eth_balance_raw"`
	TokenBalances []*AddressBalanceTokenResponse `json:"token_balances"`
}

// AddressBalanceTokenResponse represents a single token balance in the response
type AddressBalanceTokenResponse struct {
	TokenID    uint64  `json:"token_id"`
	Contract   string  `json:"contract"`
	Name       string  `json:"name"`
	Symbol     string  `json:"symbol"`
	Decimals   uint8   `json:"decimals"`
	Balance    float64 `json:"balance"`
	BalanceRaw string  `json:"balance_raw"`
}

func buildBalancesResponse(ctx context.Context, addressBytes []byte) (*AddressBalancesResponse, time.Duration) {
	response := &AddressBalancesResponse{
		TokenBalances: make([]*AddressBalanceTokenResponse, 0),
	}
	cacheTimeout := 2 * time.Minute

	// Try to get the account from the database
	account, _ := db.GetElAccountByAddress(ctx, addressBytes)
	if account == nil {
		// Create a placeholder account
		account = &dbtypes.ElAccount{
			ID:      0,
			Address: addressBytes,
		}
	}

	// Queue balance lookups (rate limited to 10min per account)
	// Even for unknown addresses (ID=0), we queue to check if they have balance
	if txIndexer := services.GlobalBeaconService.GetTxIndexer(); txIndexer != nil {
		txIndexer.QueueAddressBalanceLookups(account.ID, account.Address)
	}

	if account.ID == 0 {
		return response, cacheTimeout
	}

	// Get ETH balance (token_id = 0 is native token)
	if ethBalance, err := db.GetElBalance(ctx, account.ID, 0); err == nil {
		response.EthBalance = ethBalance.Balance
		response.EthBalanceRaw = hex.EncodeToString(ethBalance.BalanceRaw)
	}

	// Get token balances
	if balances, _, err := db.GetElBalancesByAccountID(ctx, account.ID, 0, 50); err == nil {
		// Collect token IDs for batch lookup
		tokenIDs := make([]uint64, 0, len(balances))
		for _, b := range balances {
			if b.TokenID > 0 { // Skip native token
				tokenIDs = append(tokenIDs, b.TokenID)
			}
		}

		// Batch lookup tokens
		tokenMap := make(map[uint64]*dbtypes.ElToken)
		if len(tokenIDs) > 0 {
			if tokens, err := db.GetElTokensByIDs(ctx, tokenIDs); err == nil {
				for _, t := range tokens {
					tokenMap[t.ID] = t
				}
			}
		}

		// Build token balance list (skip zero balances)
		for _, b := range balances {
			if b.TokenID == 0 {
				continue // Skip native token in token list
			}
			if b.Balance == 0 {
				continue // Skip zero balances
			}
			tb := &AddressBalanceTokenResponse{
				TokenID:    b.TokenID,
				Balance:    b.Balance,
				BalanceRaw: hex.EncodeToString(b.BalanceRaw),
			}
			if token, ok := tokenMap[b.TokenID]; ok {
				tb.Contract = hex.EncodeToString(token.Contract)
				tb.Name = token.Name
				tb.Symbol = token.Symbol
				tb.Decimals = token.Decimals
			}
			response.TokenBalances = append(response.TokenBalances, tb)
		}
	}

	return response, cacheTimeout
}
