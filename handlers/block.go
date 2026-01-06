package handlers

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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

	// Check rate limit
	if pageError := services.GlobalCallRateLimiter.CheckCallLimit(r, 1); pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

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
			data := InitPageData(w, r, "blockchain", "/block", fmt.Sprintf("Block %v", numberOrHash), notfoundTemplateFiles)
			data.Data = "block"
			w.Header().Set("Content-Type", "text/html")
			handleTemplateError(w, r, "block.go", "Block", "invalidInput", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
			return
		}
		filter.EthBlockNumber = &blockNum
	}

	// Search for the block
	blocks := services.GlobalBeaconService.GetDbBlocksByFilter(filter, 0, 10, 0)

	if len(blocks) == 0 || blocks[0].Block == nil {
		// Block not found
		data := InitPageData(w, r, "blockchain", "/block", fmt.Sprintf("Block %v", numberOrHash), notfoundTemplateFiles)
		data.Data = "block"
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "block.go", "Block", "notFound", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
		return
	}

	// Found the block - redirect to slot page using the block root
	block := blocks[0].Block
	redirectURL := fmt.Sprintf("/slot/0x%x", block.Root)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}
