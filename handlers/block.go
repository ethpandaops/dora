package handlers

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
)

// Block handles the /block/{numberOrHash} route
// It looks up the block by EL block number or hash and redirects to the slot page
func Block(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	numberOrHash := vars["numberOrHash"]

	var notfoundTemplateFiles = append(layoutTemplateFiles,
		"slot/notfound.html",
	)

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)

	var redirectRef []byte
	if pageError == nil {
		redirectRef, pageError = getBlockPageRedirect(numberOrHash)
	}

	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	switch {
	case len(redirectRef) == 32:
		redirectURL := fmt.Sprintf("/slot/0x%x", redirectRef)
		http.Redirect(w, r, redirectURL, http.StatusFound)
	case bytes.Equal(redirectRef, []byte("invalid")):
		data := InitPageData(w, r, "blockchain", "/block", fmt.Sprintf("Block %v", numberOrHash), notfoundTemplateFiles)
		data.Data = "block"
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "block.go", "Block", "invalidInput", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
	case bytes.Equal(redirectRef, []byte("notfound")):
		data := InitPageData(w, r, "blockchain", "/block", fmt.Sprintf("Block %v", numberOrHash), notfoundTemplateFiles)
		data.Data = "block"
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "block.go", "Block", "notFound", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
	}
}

func getBlockPageRedirect(numberOrHash string) ([]byte, error) {
	pageData := []byte{}
	pageCacheKey := fmt.Sprintf("block_redirect:%s", numberOrHash)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildBlockPageRedirect(pageCall.CallCtx, numberOrHash)
		_ = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.([]byte)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildBlockPageRedirect(ctx context.Context, numberOrHash string) ([]byte, time.Duration) {
	// Build filter based on input
	filter := &dbtypes.BlockFilter{
		WithOrphaned: 1, // Include both canonical and orphaned
	}

	// Try to parse as hex hash first (with or without 0x prefix)
	hashStr := strings.TrimPrefix(numberOrHash, "0x")
	if len(hashStr) == 64 {
		hashBytes, err := hex.DecodeString(hashStr)
		if err == nil {
			filter.EthBlockHash = hashBytes
		}
	}

	// If not a valid hash, try to parse as block number
	if len(filter.EthBlockHash) == 0 {
		blockNum, err := strconv.ParseUint(numberOrHash, 10, 64)
		if err != nil {
			// Invalid input - show not found
			return []byte("invalid"), 20 * time.Minute
		}
		filter.EthBlockNumber = &blockNum
	}

	// Search for the block
	blocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, filter, 0, 10, 0)

	if len(blocks) == 0 || blocks[0].Block == nil {
		// Block not found
		return []byte("notfound"), 2 * time.Minute
	}

	// Found the block - redirect to slot page using the block root
	block := blocks[0].Block

	return block.Root, 2 * time.Minute
}
