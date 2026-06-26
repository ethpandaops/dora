package handlers

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

func tokenTypeName(tokenType uint8) string {
	switch tokenType {
	case 1:
		return "ERC20"
	case 2:
		return "ERC721"
	case 3:
		return "ERC1155"
	default:
		return "Unknown"
	}
}

// Token renders the EIP-3091 token detail page for /token/{address}.
func Token(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"token/token.html",
		"_shared/pager.html",
		"_shared/el_filter_assets.html",
	)
	var notfoundTemplateFiles = append(layoutTemplateFiles,
		"token/notfound.html",
	)

	vars := mux.Vars(r)
	addressHex := strings.TrimPrefix(vars["address"], "0x")
	addressBytes, err := hex.DecodeString(addressHex)
	if err != nil || len(addressBytes) != 20 {
		data := InitPageData(w, r, "blockchain", "/tokens", "Token not found", notfoundTemplateFiles)
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "token.go", "Token", "invalidAddress", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
		return
	}

	urlArgs := r.URL.Query()
	pageSize := uint64(defaultElListPageSize)
	if urlArgs.Has("c") {
		if c, err := strconv.ParseUint(urlArgs.Get("c"), 10, 64); err == nil && c > 0 {
			pageSize = c
			if pageSize > maxElListPageSize {
				pageSize = maxElListPageSize
			}
		}
	}
	beforeTxUid, beforeTxIdx, pageNum := parseElPageParam(urlArgs.Get("p"))
	filterForm, filterSuffix := parseTransfersFilterForm(urlArgs)
	colMask := utils.DecodeUint64BitfieldFromQuery(r.URL.RawQuery, "d")

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	var pageData *models.TokenPageData
	if pageError == nil {
		pageData, pageError = getTokenPageData(addressBytes, beforeTxUid, beforeTxIdx, pageNum, pageSize, filterForm, filterSuffix, colMask)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	if pageData == nil {
		data := InitPageData(w, r, "blockchain", "/tokens", "Token not found", notfoundTemplateFiles)
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "token.go", "Token", "notFound", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
		return
	}

	pageTemplate := templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "blockchain", "/tokens", fmt.Sprintf("Token %s", common.BytesToAddress(addressBytes).Hex()), templateFiles)
	data.Data = pageData
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "token.go", "Token", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getTokenPageData(contract []byte, beforeTxUid uint64, beforeTxIdx uint32, pageNum uint64, pageSize uint64, filterForm *models.TransfersFilter, filterSuffix string, colMask uint64) (*models.TokenPageData, error) {
	pageData := &models.TokenPageData{}
	pageCacheKey := fmt.Sprintf("token:%x:%v:%v:%v:%v:%v:%v", contract, beforeTxUid, beforeTxIdx, pageNum, pageSize, filterSuffix, colMask)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildTokenPageData(pageCall.CallCtx, contract, beforeTxUid, beforeTxIdx, pageNum, pageSize, filterForm, filterSuffix, colMask)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr != nil {
		return nil, pageErr
	}
	if pageRes != nil {
		resData, resOk := pageRes.(*models.TokenPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	// A nil contract field signals "token not found" to the handler.
	if len(pageData.Contract) == 0 {
		return nil, nil
	}
	return pageData, nil
}

func buildTokenPageData(ctx context.Context, contract []byte, beforeTxUid uint64, beforeTxIdx uint32, pageNum uint64, pageSize uint64, filterForm *models.TransfersFilter, filterSuffix string, colMask uint64) (*models.TokenPageData, time.Duration) {
	logrus.Debugf("token page called: 0x%x before=%v/%v page=%v", contract, beforeTxUid, beforeTxIdx, pageNum)

	token, err := db.GetElTokenByContract(ctx, contract)
	if err != nil || token == nil {
		// Not found - return empty page data (nil contract) with a short cache.
		return &models.TokenPageData{}, 12 * time.Second
	}

	// Default columns when none specified: Block, Age, Method, Amount, Status.
	const defaultColMask = 0x1f
	if colMask == 0 {
		colMask = defaultColMask
	}

	pageData := &models.TokenPageData{
		Contract:      token.Contract,
		TokenType:     token.TokenType,
		TokenTypeName: tokenTypeName(token.TokenType),
		Name:          token.Name,
		Symbol:        token.Symbol,
		Decimals:      uint8(token.Decimals),
		PageSize:      pageSize,
		Filter:        filterForm,
		ColumnMask:    colMask,
		DisplayBlock:  colMask&0x1 != 0,
		DisplayAge:    colMask&0x2 != 0,
		DisplayMethod: colMask&0x4 != 0,
		DisplayAmount: colMask&0x8 != 0,
		DisplayStatus: colMask&0x10 != 0,
	}

	// The token is fixed to this page; the form's token field is ignored.
	tokenID := token.ID
	filter := resolveTransferFilter(ctx, filterForm)
	filter.TokenID = &tokenID
	filter.TokenTypes = nil
	dbTransfers, hasNext, _ := db.GetElTokenTransfersFiltered(ctx, filter, beforeTxUid, beforeTxIdx, uint32(pageSize))
	pageData.Transfers = enrichElTokenTransferRows(ctx, dbTransfers)

	tokenHex := common.BytesToAddress(token.Contract).Hex()
	suffix := fmt.Sprintf("c=%v", pageSize)
	if filterSuffix != "" {
		suffix += "&" + filterSuffix
	}
	var nextA uint64
	var nextB uint32
	hasPrev, atFirst := false, true
	var prevA uint64
	var prevB uint32
	if len(dbTransfers) > 0 {
		last := dbTransfers[len(dbTransfers)-1]
		nextA, nextB = last.TxUid, last.TxIdx
		first := dbTransfers[0]
		prevA, prevB, hasPrev, atFirst, _ = db.GetElTokenTransfersPrevAnchor(ctx, filter, first.TxUid, first.TxIdx, uint32(pageSize))
	}
	pageData.Pager = buildElPager("/token/"+tokenHex, suffix, pageNum, hasNext, nextA, nextB, hasPrev, atFirst, prevA, prevB, true)

	cacheTimeout := 5 * time.Minute
	if beforeTxUid == 0 {
		cacheTimeout = 30 * time.Second
	}
	return pageData, cacheTimeout
}
