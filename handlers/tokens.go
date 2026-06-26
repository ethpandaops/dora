package handlers

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// Tokens renders the detected-tokens list page.
func Tokens(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"tokens/tokens.html",
		"_shared/pager.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "blockchain", "/tokens", "Tokens", templateFiles)

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
	pageIdx := uint64(1)
	if urlArgs.Has("p") {
		if p, err := strconv.ParseUint(urlArgs.Get("p"), 10, 64); err == nil && p > 0 {
			pageIdx = p
		}
	}
	search := strings.TrimSpace(urlArgs.Get("q"))

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getTokensPageData(pageIdx, pageSize, search)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "tokens.go", "Tokens", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getTokensPageData(pageIdx uint64, pageSize uint64, search string) (*models.TokensPageData, error) {
	pageData := &models.TokensPageData{}
	pageCacheKey := fmt.Sprintf("tokens:%v:%v:%v", pageIdx, pageSize, search)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildTokensPageData(pageCall.CallCtx, pageIdx, pageSize, search)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.TokensPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildTokensPageData(ctx context.Context, pageIdx uint64, pageSize uint64, search string) (*models.TokensPageData, time.Duration) {
	logrus.Debugf("tokens page called: p=%v c=%v q=%v", pageIdx, pageSize, search)

	pageData := &models.TokensPageData{
		PageSize:    pageSize,
		SearchQuery: search,
	}

	filter := &dbtypes.ElTokenFilter{}
	if search != "" {
		// A 0x-prefixed 20-byte hex is treated as a contract lookup; otherwise
		// the term is matched against the token name.
		if hexStr := strings.TrimPrefix(search, "0x"); len(hexStr) == 40 {
			if b, err := hex.DecodeString(hexStr); err == nil {
				filter.Contract = b
			} else {
				filter.Name = search
			}
		} else {
			filter.Name = search
		}
	}

	offset := (pageIdx - 1) * pageSize
	dbTokens, totalCount, _ := db.GetElTokensFiltered(ctx, offset, uint32(pageSize), filter)

	pageData.TotalCount = totalCount
	totalPages := uint64(math.Ceil(float64(totalCount) / float64(pageSize)))
	if totalCount > 0 {
		pageData.FirstItem = offset + 1
		pageData.LastItem = min(offset+pageSize, totalCount)
	}

	pageData.Tokens = make([]*models.TokensPageDataToken, 0, len(dbTokens))
	for _, t := range dbTokens {
		pageData.Tokens = append(pageData.Tokens, &models.TokensPageDataToken{
			Contract:      t.Contract,
			TokenType:     t.TokenType,
			TokenTypeName: tokenTypeName(t.TokenType),
			Name:          t.Name,
			Symbol:        t.Symbol,
			Decimals:      uint8(t.Decimals),
		})
	}

	// Pagination via the shared offset pager (preserves page size + search).
	params := []models.UrlParam{{Key: "c", Value: strconv.FormatUint(pageSize, 10)}}
	if search != "" {
		params = append(params, models.UrlParam{Key: "q", Value: search})
	}
	pageData.Pager = buildOffsetPager("/tokens", params, pageIdx, totalPages)

	return pageData, 1 * time.Minute
}
