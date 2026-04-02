package handlers

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

var searchLikeRE = regexp.MustCompile(`^[0-9a-fA-F]{0,96}$`)

// searchResolverResult is the cached outcome of resolving a search query (redirect URL or empty = not found).
type searchResolverResult struct {
	RedirectURL string `json:"redirect_url"`
}

// Search will return the main "search" page using a go template
func Search(w http.ResponseWriter, r *http.Request) {
	var notfoundTemplateFiles = append(layoutTemplateFiles,
		"search/notfound.html",
	)

	urlArgs := r.URL.Query()
	searchQuery := strings.TrimSpace(urlArgs.Get("q"))

	var result searchResolverResult
	var pageErr error
	if searchQuery != "" {
		pageCacheKey := fmt.Sprintf("search:%s", searchQuery)
		result, pageErr = getSearchResolverResult(pageCacheKey, searchQuery)
	}
	if pageErr != nil {
		handlePageError(w, r, pageErr)
		return
	}
	if result.RedirectURL != "" {
		http.Redirect(w, r, result.RedirectURL, http.StatusMovedPermanently)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	data := InitPageData(w, r, "search", "/search", fmt.Sprintf("Search: %v", searchQuery), notfoundTemplateFiles)
	if handleTemplateError(w, r, "search.go", "Search", "", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getSearchResolverResult(pageCacheKey, searchQuery string) (searchResolverResult, error) {
	pageData := searchResolverResult{}
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		res, cacheTimeout := buildSearchResolverResult(pageCall.CallCtx, searchQuery)
		pageCall.CacheTimeout = cacheTimeout
		return res
	})
	if pageErr != nil {
		return searchResolverResult{}, pageErr
	}
	if res, ok := pageRes.(searchResolverResult); ok {
		return res, nil
	}
	return searchResolverResult{}, ErrInvalidPageModel
}

func buildSearchResolverResult(ctx context.Context, searchQuery string) (searchResolverResult, time.Duration) {
	cacheTimeout := 2 * time.Minute

	_, err := strconv.Atoi(searchQuery)
	if err == nil {
		// Check if it's a validator index first
		validatorIndex, err := strconv.ParseUint(searchQuery, 10, 64)
		if err == nil {
			validator := services.GlobalBeaconService.GetValidatorByIndex(phase0.ValidatorIndex(validatorIndex), false)
			if validator != nil {
				return searchResolverResult{RedirectURL: fmt.Sprintf("/validator/%v", validatorIndex)}, cacheTimeout
			}
		}

		blockResult := &dbtypes.SearchBlockResult{}
		err = db.ReaderDb.GetContext(ctx, blockResult, `
			SELECT slot, root, status
			FROM slots
			WHERE slot = $1 AND status != 0
			LIMIT 1`, searchQuery)
		if err == nil {
			if blockResult.Status == dbtypes.Orphaned {
				return searchResolverResult{RedirectURL: fmt.Sprintf("/slot/0x%x", blockResult.Root)}, cacheTimeout
			}
			return searchResolverResult{RedirectURL: fmt.Sprintf("/slot/%v", blockResult.Slot)}, cacheTimeout
		}
	}

	hashQuery := strings.Replace(searchQuery, "0x", "", -1)
	hashQuery = strings.Replace(hashQuery, "0X", "", -1)
	if len(hashQuery) == 96 {
		validatorPubkey, err := hex.DecodeString(hashQuery)
		if err == nil && len(validatorPubkey) == 48 {
			validatorIndex, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(validatorPubkey))
			if found {
				return searchResolverResult{RedirectURL: fmt.Sprintf("/validator/%v", validatorIndex)}, cacheTimeout
			}
		}
	} else if len(hashQuery) == 64 {
		if utils.Config.ExecutionIndexer.Enabled {
			txHashBytes, err := hex.DecodeString(hashQuery)
			if err == nil && len(txHashBytes) == 32 {
				txs, err := db.GetElTransactionsByHash(ctx, txHashBytes)
				if err == nil && len(txs) > 0 {
					return searchResolverResult{RedirectURL: fmt.Sprintf("/tx/0x%x", txHashBytes)}, cacheTimeout
				}
			}
		}
		blockHash, err := hex.DecodeString(hashQuery)
		if err == nil {
			blockResult := &dbtypes.SearchBlockResult{}
			err = db.ReaderDb.GetContext(ctx, blockResult, `
			SELECT slot, root, orphaned
			FROM slots
			WHERE root = $1 OR
				state_root = $1
			LIMIT 1`, blockHash)
			if err == nil {
				if blockResult.Status == dbtypes.Orphaned {
					return searchResolverResult{RedirectURL: fmt.Sprintf("/slot/0x%x", blockResult.Root)}, cacheTimeout
				}
				return searchResolverResult{RedirectURL: fmt.Sprintf("/slot/%v", blockResult.Slot)}, cacheTimeout
			}
		}
	} else if len(hashQuery) == 40 && utils.Config.ExecutionIndexer.Enabled {
		addressBytes, err := hex.DecodeString(hashQuery)
		if err == nil && len(addressBytes) == 20 {
			return searchResolverResult{RedirectURL: fmt.Sprintf("/address/0x%x", addressBytes)}, cacheTimeout
		}
	}

	names := &dbtypes.SearchNameResult{}
	err = db.ReaderDb.GetContext(ctx, names, db.EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			SELECT name
			FROM validator_names
			WHERE name ILIKE LOWER($1)
			LIMIT 1`,
		dbtypes.DBEngineSqlite: `
			SELECT name
			FROM validator_names
			WHERE name LIKE LOWER($1)
			LIMIT 1`,
	}), "%"+searchQuery+"%")
	if err == nil {
		return searchResolverResult{RedirectURL: "/slots/filtered?f&f.missing=1&f.orphaned=1&f.pname=" + searchQuery}, cacheTimeout
	}

	graffiti := &dbtypes.SearchGraffitiResult{}
	err = db.ReaderDb.GetContext(ctx, graffiti, db.EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			SELECT graffiti
			FROM slots
			WHERE graffiti_text ILIKE LOWER($1)
			LIMIT 1`,
		dbtypes.DBEngineSqlite: `
			SELECT graffiti
			FROM slots
			WHERE graffiti_text LIKE LOWER($1)
			LIMIT 1`,
	}), "%"+searchQuery+"%")
	if err == nil {
		return searchResolverResult{RedirectURL: "/slots/filtered?f&f.missing=1&f.orphaned=1&f.graffiti=" + searchQuery}, cacheTimeout
	}

	return searchResolverResult{}, cacheTimeout
}

// searchAheadCached wraps the JSON result and an optional error for cache/demux.
type searchAheadCached struct {
	Data interface{} `json:"-"`
	Err  string      `json:"-"`
}

// SearchAhead handles responses for the frontend search boxes
func SearchAhead(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	searchType := vars["type"]
	urlArgs := r.URL.Query()
	search := strings.Trim(urlArgs.Get("q"), " \t")
	search = strings.Replace(search, "0x", "", -1)
	search = strings.Replace(search, "0X", "", -1)

	// 404 before cache so we don't cache disabled/unknown types
	allowedTypes := map[string]bool{
		"epochs":       true,
		"slots":        true,
		"execblocks":   true,
		"graffiti":     true,
		"valname":      true,
		"validator":    true,
		"addresses":    utils.Config.ExecutionIndexer.Enabled,
		"transactions": utils.Config.ExecutionIndexer.Enabled,
	}
	if !allowedTypes[searchType] {
		http.Error(w, "Not found", 404)
		return
	}

	pageCacheKey := fmt.Sprintf("search_ahead:%s:%s", searchType, search)
	var pageData searchAheadCached
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, &pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		out, cacheTimeout := buildSearchAheadResult(pageCall.CallCtx, searchType, search)
		pageCall.CacheTimeout = cacheTimeout
		return out
	})
	if pageErr != nil {
		logrus.WithError(pageErr).WithField("searchType", searchType).Error("search ahead error")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
	var res searchAheadCached
	if pageRes, ok := pageRes.(*searchAheadCached); ok {
		res = *pageRes
	} else {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
	if res.Err != "" {
		logrus.WithField("searchType", searchType).WithField("err", res.Err).Error("search ahead build error")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
	if err := json.NewEncoder(w).Encode(res.Data); err != nil {
		logrus.WithError(err).Error("error encoding searchAhead")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

func buildSearchAheadResult(ctx context.Context, searchType, search string) (*searchAheadCached, time.Duration) {
	cacheTimeout := 30 * time.Second
	var result interface{}
	var err error

	indexer := services.GlobalBeaconService.GetBeaconIndexer()
	_, pruneEpoch := indexer.GetBlockCacheState()
	chainState := services.GlobalBeaconService.GetChainState()
	minSlotIdx := chainState.EpochStartSlot(pruneEpoch)

	switch searchType {
	case "epochs":
		dbres := &dbtypes.SearchAheadEpochsResult{}
		err = db.ReaderDb.SelectContext(ctx, dbres, "SELECT epoch FROM epochs WHERE CAST(epoch AS text) LIKE $1 ORDER BY epoch LIMIT 10", search+"%")
		if err == nil {
			model := make([]models.SearchAheadEpochsResult, len(*dbres))
			for idx, entry := range *dbres {
				model[idx] = models.SearchAheadEpochsResult{
					Epoch: fmt.Sprintf("%v", entry.Epoch),
				}
			}
			result = model
		}
	case "slots":
		if len(search) == 0 {
			break
		}
		if !searchLikeRE.MatchString(search) {
			break
		}
		if len(search) == 64 {
			blockHash, decErr := hex.DecodeString(search)
			if decErr != nil {
				return &searchAheadCached{Err: decErr.Error()}, cacheTimeout
			}

			cachedBlock := indexer.GetBlockByRoot(phase0.Root(blockHash))
			if cachedBlock == nil {
				cachedBlock = indexer.GetBlockByStateRoot(phase0.Root(blockHash))
			}
			if cachedBlock != nil {
				header := cachedBlock.GetHeader()
				result = &[]models.SearchAheadSlotsResult{
					{
						Slot:     fmt.Sprintf("%v", uint64(header.Message.Slot)),
						Root:     phase0.Root(cachedBlock.Root),
						Orphaned: !indexer.IsCanonicalBlock(cachedBlock, nil),
					},
				}
			} else {
				dbres := &dbtypes.SearchAheadSlotsResult{}
				err = db.ReaderDb.SelectContext(ctx, dbres, `
				SELECT slot, root, status
				FROM slots
				WHERE slot < $1 AND (root = $2 OR state_root = $2)
				ORDER BY slot LIMIT 1`, minSlotIdx, blockHash)
				if err != nil {
					return &searchAheadCached{Err: err.Error()}, cacheTimeout
				}
				if len(*dbres) > 0 {
					result = &[]models.SearchAheadSlotsResult{
						{
							Slot:     fmt.Sprintf("%v", (*dbres)[0].Slot),
							Root:     phase0.Root((*dbres)[0].Root),
							Orphaned: (*dbres)[0].Status == dbtypes.Orphaned,
						},
					}
				}
			}
		} else if blockNumber, convertErr := strconv.ParseUint(search, 10, 32); convertErr == nil {
			cachedBlocks := indexer.GetBlocksBySlot(phase0.Slot(blockNumber))
			if len(cachedBlocks) > 0 {
				res := make([]*models.SearchAheadSlotsResult, 0)
				for _, cachedBlock := range cachedBlocks {
					header := cachedBlock.GetHeader()
					if header == nil {
						continue
					}
					res = append(res, &models.SearchAheadSlotsResult{
						Slot:     fmt.Sprintf("%v", uint64(header.Message.Slot)),
						Root:     phase0.Root(cachedBlock.Root),
						Orphaned: !indexer.IsCanonicalBlock(cachedBlock, nil),
					})
				}
				result = res
			} else {
				dbres := &dbtypes.SearchAheadSlotsResult{}
				err = db.ReaderDb.SelectContext(ctx, dbres, `
				SELECT slot, root, status
				FROM slots
				WHERE slot = $1 AND status != 0
				ORDER BY slot LIMIT 10`, blockNumber)
				if err == nil {
					model := make([]models.SearchAheadSlotsResult, len(*dbres))
					for idx, entry := range *dbres {
						model[idx] = models.SearchAheadSlotsResult{
							Slot:     fmt.Sprintf("%v", entry.Slot),
							Root:     phase0.Root(entry.Root),
							Orphaned: entry.Status == dbtypes.Orphaned,
						}
					}
					result = model
				}
			}
		}
	case "execblocks":
		if len(search) == 0 {
			break
		}
		if !searchLikeRE.MatchString(search) {
			break
		}
		if len(search) == 64 {
			blockHash, decErr := hex.DecodeString(search)
			if decErr != nil {
				return &searchAheadCached{Err: decErr.Error()}, cacheTimeout
			}

			cachedBlocks := indexer.GetBlocksByExecutionBlockHash(phase0.Hash32(blockHash))
			if len(cachedBlocks) > 0 {
				res := make([]*models.SearchAheadExecBlocksResult, len(cachedBlocks))
				for idx, cachedBlock := range cachedBlocks {
					header := cachedBlock.GetHeader()
					index := cachedBlock.GetBlockIndex(ctx)
					res[idx] = &models.SearchAheadExecBlocksResult{
						Slot:       fmt.Sprintf("%v", uint64(header.Message.Slot)),
						Root:       phase0.Root(cachedBlock.Root),
						ExecHash:   phase0.Hash32(index.ExecutionHash),
						ExecNumber: index.ExecutionNumber,
						Orphaned:   !indexer.IsCanonicalBlock(cachedBlock, nil),
					}
				}
				result = res
			} else {
				dbres := &dbtypes.SearchAheadExecBlocksResult{}
				err = db.ReaderDb.SelectContext(ctx, dbres, `
				SELECT slot, root, eth_block_hash, eth_block_number, status
				FROM slots
				WHERE slot < $1 AND eth_block_hash = $2
				ORDER BY slot LIMIT 10`, minSlotIdx, blockHash)
				if err != nil {
					return &searchAheadCached{Err: err.Error()}, cacheTimeout
				}
				if len(*dbres) > 0 {
					result = &[]models.SearchAheadExecBlocksResult{
						{
							Slot:       fmt.Sprintf("%v", (*dbres)[0].Slot),
							Root:       phase0.Root((*dbres)[0].Root),
							ExecHash:   phase0.Hash32((*dbres)[0].ExecHash),
							ExecNumber: (*dbres)[0].ExecNumber,
							Orphaned:   (*dbres)[0].Status == dbtypes.Orphaned,
						},
					}
				}

			}
		} else if blockNumber, convertErr := strconv.ParseUint(search, 10, 32); convertErr == nil {
			cachedBlocks := indexer.GetBlocksByExecutionBlockNumber(blockNumber)
			if len(cachedBlocks) > 0 {
				res := make([]*models.SearchAheadExecBlocksResult, 0)
				for _, cachedBlock := range cachedBlocks {
					header := cachedBlock.GetHeader()
					index := cachedBlock.GetBlockIndex(ctx)
					res = append(res, &models.SearchAheadExecBlocksResult{
						Slot:       fmt.Sprintf("%v", uint64(header.Message.Slot)),
						Root:       phase0.Root(cachedBlock.Root),
						ExecHash:   phase0.Hash32(index.ExecutionHash),
						ExecNumber: index.ExecutionNumber,
						Orphaned:   !indexer.IsCanonicalBlock(cachedBlock, nil),
					})
				}
				result = res
			} else {
				dbres := &dbtypes.SearchAheadExecBlocksResult{}
				err = db.ReaderDb.SelectContext(ctx, dbres, `
				SELECT slot, root, eth_block_hash, eth_block_number, status
				FROM slots
				WHERE slot < $1 AND eth_block_number = $2
				ORDER BY slot LIMIT 10`, minSlotIdx, blockNumber)
				if err == nil {
					model := make([]models.SearchAheadExecBlocksResult, len(*dbres))
					for idx, entry := range *dbres {
						model[idx] = models.SearchAheadExecBlocksResult{
							Slot:       fmt.Sprintf("%v", entry.Slot),
							Root:       phase0.Root(entry.Root),
							ExecHash:   phase0.Hash32(entry.ExecHash),
							ExecNumber: entry.ExecNumber,
							Orphaned:   entry.Status == dbtypes.Orphaned,
						}
					}
					result = model
				}
			}
		}
	case "graffiti":
		graffiti := &dbtypes.SearchAheadGraffitiResult{}
		err = db.ReaderDb.SelectContext(ctx, graffiti, db.EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql: `
				SELECT graffiti, count(*) as count
				FROM slots
				WHERE graffiti_text ILIKE LOWER($1)
				GROUP BY graffiti
				ORDER BY count desc
				LIMIT 10`,
			dbtypes.DBEngineSqlite: `
				SELECT graffiti, count(*) as count
				FROM slots
				WHERE graffiti_text LIKE LOWER($1)
				GROUP BY graffiti
				ORDER BY count desc
				LIMIT 10`,
		}), "%"+search+"%")
		if err == nil {
			model := make([]models.SearchAheadGraffitiResult, len(*graffiti))
			for i, entry := range *graffiti {
				model[i] = models.SearchAheadGraffitiResult{
					Graffiti: utils.FormatGraffitiString(entry.Graffiti),
					Count:    fmt.Sprintf("%v", entry.Count),
				}
			}
			result = model
		}
	case "valname":
		names := &dbtypes.SearchAheadValidatorNameResult{}
		err = db.ReaderDb.SelectContext(ctx, names, db.EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql: `
				SELECT name, count(*) as count
				FROM validator_names
				LEFT JOIN slots ON validator_names."index" = slots.proposer
				WHERE name ILIKE LOWER($1)
				GROUP BY name
				ORDER BY count desc
				LIMIT 10`,
			dbtypes.DBEngineSqlite: `
				SELECT name, count(*) as count
				FROM validator_names
				LEFT JOIN slots ON validator_names."index" = slots.proposer
				WHERE name LIKE LOWER($1)
				GROUP BY name
				ORDER BY count desc
				LIMIT 10`,
		}), "%"+search+"%")
		if err == nil {
			model := make([]models.SearchAheadValidatorNameResult, len(*names))
			for i, entry := range *names {
				model[i] = models.SearchAheadValidatorNameResult{
					Name:  utils.FormatGraffitiString(entry.Name),
					Count: fmt.Sprintf("%v", entry.Count),
				}
			}
			result = model
		}
	case "validator":
		if len(search) == 0 {
			break
		}
		if !searchLikeRE.MatchString(search) {
			break
		}

		// Check if search is numeric (validator index)
		if validatorIndex, convertErr := strconv.ParseUint(search, 10, 64); convertErr == nil {
			validator := services.GlobalBeaconService.GetValidatorByIndex(phase0.ValidatorIndex(validatorIndex), false)
			if validator != nil {
				result = &[]models.SearchAheadValidatorResult{
					{
						Index:  fmt.Sprintf("%v", validatorIndex),
						Pubkey: fmt.Sprintf("0x%x", validator.Validator.PublicKey),
						Name:   services.GlobalBeaconService.GetValidatorName(validatorIndex),
					},
				}
			}
		} else if len(search) >= 2 && len(search) <= 96 {
			// Search by pubkey prefix
			validators := &dbtypes.SearchAheadValidatorResult{}
			err = db.ReaderDb.SelectContext(ctx, validators, db.EngineQuery(map[dbtypes.DBEngineType]string{
				dbtypes.DBEnginePgsql: `
					SELECT v.validator_index, v.pubkey
					FROM validators v
					WHERE ENCODE(v.pubkey, 'hex') ILIKE $1
					ORDER BY v.validator_index
					LIMIT 10`,
				dbtypes.DBEngineSqlite: `
					SELECT v.validator_index, v.pubkey
					FROM validators v
					WHERE HEX(v.pubkey) LIKE UPPER($1)
					ORDER BY v.validator_index
					LIMIT 10`,
			}), search+"%")
			if err == nil {
				model := make([]models.SearchAheadValidatorResult, len(*validators))
				for i, entry := range *validators {
					model[i] = models.SearchAheadValidatorResult{
						Index:  fmt.Sprintf("%v", entry.Index),
						Pubkey: fmt.Sprintf("0x%x", entry.Pubkey),
						Name:   services.GlobalBeaconService.GetValidatorName(entry.Index),
					}
				}
				result = model
			}
		}
	case "addresses":
		if len(search) == 0 {
			break
		}
		if !searchLikeRE.MatchString(search) {
			break
		}
		if len(search) == 40 {
			// Full 20-byte address - always recognize even if not in DB
			addressBytes, err := hex.DecodeString(search)
			if err == nil && len(addressBytes) == 20 {
				// Try to get from DB first
				account, _ := db.GetElAccountByAddress(ctx, addressBytes)
				result = &[]models.SearchAheadAddressResult{
					{
						Address:    fmt.Sprintf("0x%x", addressBytes),
						IsContract: account != nil && account.IsContract,
						HasData:    account != nil && account.ID > 0,
					},
				}
			}
		} else if len(search) >= 2 && len(search) < 40 {
			// Search by address prefix in DB
			addresses := &dbtypes.SearchAheadAddressResult{}
			err = db.ReaderDb.SelectContext(ctx, addresses, db.EngineQuery(map[dbtypes.DBEngineType]string{
				dbtypes.DBEnginePgsql: `
					SELECT address, is_contract
					FROM el_accounts
					WHERE ENCODE(address, 'hex') ILIKE $1
					ORDER BY funded DESC, id ASC
					LIMIT 10`,
				dbtypes.DBEngineSqlite: `
					SELECT address, is_contract
					FROM el_accounts
					WHERE HEX(address) LIKE UPPER($1)
					ORDER BY funded DESC, id ASC
					LIMIT 10`,
			}), search+"%")
			if err == nil {
				model := make([]models.SearchAheadAddressResult, len(*addresses))
				for i, entry := range *addresses {
					model[i] = models.SearchAheadAddressResult{
						Address:    fmt.Sprintf("0x%x", entry.Address),
						IsContract: entry.IsContract,
						HasData:    true,
					}
				}
				result = model
			}
		}
	case "transactions":
		if len(search) == 0 {
			break
		}
		if !searchLikeRE.MatchString(search) {
			break
		}
		if len(search) == 64 {
			// Full 32-byte transaction hash
			txHashBytes, err := hex.DecodeString(search)
			if err == nil && len(txHashBytes) == 32 {
				txs, err := db.GetElTransactionsByHash(ctx, txHashBytes)
				if err == nil && len(txs) > 0 {
					// Use the first (canonical) transaction
					tx := txs[0]
					result = &[]models.SearchAheadTransactionResult{
						{
							TxHash:      fmt.Sprintf("0x%x", txHashBytes),
							BlockNumber: tx.BlockNumber,
							Reverted:    tx.Reverted,
						},
					}
				}
			}
		} else if len(search) >= 2 && len(search) < 64 {
			// Search by transaction hash prefix in DB
			transactions := &dbtypes.SearchAheadTransactionResult{}
			err = db.ReaderDb.SelectContext(ctx, transactions, db.EngineQuery(map[dbtypes.DBEngineType]string{
				dbtypes.DBEnginePgsql: `
					SELECT DISTINCT ON (tx_hash) tx_hash, block_number, reverted
					FROM el_transactions
					WHERE ENCODE(tx_hash, 'hex') ILIKE $1
					ORDER BY tx_hash, block_number DESC
					LIMIT 10`,
				dbtypes.DBEngineSqlite: `
					SELECT t1.tx_hash, t1.block_number, t1.reverted
					FROM el_transactions t1
					INNER JOIN (
						SELECT tx_hash, MAX(block_number) as max_block_number
						FROM el_transactions
						WHERE HEX(tx_hash) LIKE UPPER($1)
						GROUP BY tx_hash
					) t2 ON t1.tx_hash = t2.tx_hash AND t1.block_number = t2.max_block_number
					WHERE HEX(t1.tx_hash) LIKE UPPER($1)
					ORDER BY t1.block_number DESC
					LIMIT 10`,
			}), search+"%")
			if err == nil {
				model := make([]models.SearchAheadTransactionResult, len(*transactions))
				for i, entry := range *transactions {
					model[i] = models.SearchAheadTransactionResult{
						TxHash:      fmt.Sprintf("0x%x", entry.TxHash),
						BlockNumber: entry.BlockNumber,
						Reverted:    entry.Reverted,
					}
				}
				result = model
			}
		}
	}

	if err != nil {
		return &searchAheadCached{Err: err.Error()}, cacheTimeout
	}
	return &searchAheadCached{Data: result}, cacheTimeout
}
