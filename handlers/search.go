package handlers

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/pk910/light-beaconchain-explorer/types/models"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

var searchLikeRE = regexp.MustCompile(`^[0-9a-fA-F]{0,96}$`)

// Search will return the main "search" page using a go template
func Search(w http.ResponseWriter, r *http.Request) {
	var notfoundTemplateFiles = append(layoutTemplateFiles,
		"search/notfound.html",
	)

	urlArgs := r.URL.Query()
	searchQuery := urlArgs.Get("q")

	_, err := strconv.Atoi(searchQuery)
	if err == nil {
		blockResult := &dbtypes.SearchBlockResult{}
		err = db.ReaderDb.Get(blockResult, `
			SELECT slot, root, orphaned 
			FROM blocks 
			WHERE slot = $1
			LIMIT 1`, searchQuery)
		if err == nil {
			if blockResult.Orphaned {
				http.Redirect(w, r, fmt.Sprintf("/slot/0x%x", blockResult.Root), http.StatusMovedPermanently)
			} else {
				http.Redirect(w, r, fmt.Sprintf("/slot/%v", blockResult.Slot), http.StatusMovedPermanently)
			}
			return
		}
	}

	hashQuery := strings.Replace(searchQuery, "0x", "", -1)
	if len(hashQuery) == 64 {
		blockHash, err := hex.DecodeString(hashQuery)
		if err == nil {
			blockResult := &dbtypes.SearchBlockResult{}
			err = db.ReaderDb.Get(blockResult, `
			SELECT slot, root, orphaned 
			FROM blocks 
			WHERE root = $1 OR
				state_root = $1
			LIMIT 1`, blockHash)
			if err == nil {
				if blockResult.Orphaned {
					http.Redirect(w, r, fmt.Sprintf("/slot/0x%x", blockResult.Root), http.StatusMovedPermanently)
				} else {
					http.Redirect(w, r, fmt.Sprintf("/slot/%v", blockResult.Slot), http.StatusMovedPermanently)
				}
				return
			}
		}
	}

	graffiti := &dbtypes.SearchGraffitiResult{}
	err = db.ReaderDb.Get(graffiti, db.EngineQuery(map[dbtypes.DBEngineType]string{
		dbtypes.DBEnginePgsql: `
			SELECT graffiti
			FROM blocks
			WHERE graffiti_text ILIKE LOWER($1)
			LIMIT 1`,
		dbtypes.DBEngineSqlite: `
			SELECT graffiti
			FROM blocks
			WHERE graffiti_text LIKE LOWER($1)
			LIMIT 1`,
	}), "%"+searchQuery+"%")
	if err == nil {
		http.Redirect(w, r, "/slots?q="+searchQuery, http.StatusMovedPermanently)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	data := InitPageData(w, r, "search", "/search", fmt.Sprintf("Search: %v", searchQuery), notfoundTemplateFiles)
	if handleTemplateError(w, r, "search.go", "Search", "", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

// SearchAhead handles responses for the frontend search boxes
func SearchAhead(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	searchType := vars["type"]
	urlArgs := r.URL.Query()
	search := urlArgs.Get("q")
	search = strings.Replace(search, "0x", "", -1)
	search = strings.Replace(search, "0X", "", -1)
	var err error
	logger := logrus.WithField("searchType", searchType)
	var result interface{}

	switch searchType {
	case "epochs":
		dbres := &dbtypes.SearchAheadEpochsResult{}
		err = db.ReaderDb.Select(dbres, "SELECT epoch FROM epochs WHERE CAST(epoch AS text) LIKE $1 ORDER BY epoch LIMIT 10", search+"%")
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
		if len(search) <= 1 {
			break
		}
		if searchLikeRE.MatchString(search) {
			if len(search) == 64 {
				blockHash, err := hex.DecodeString(search)
				if err != nil {
					logger.Errorf("error parsing blockHash to int: %v", err)
					http.Error(w, "Internal server error", http.StatusServiceUnavailable)
					return
				}

				cachedBlock := services.GlobalBeaconService.GetCachedBlockByBlockroot(blockHash)
				if cachedBlock == nil {
					cachedBlock = services.GlobalBeaconService.GetCachedBlockByStateroot(blockHash)
				}
				if cachedBlock != nil {
					result = &[]models.SearchAheadSlotsResult{
						{
							Slot:     fmt.Sprintf("%v", uint64(cachedBlock.Header.Message.Slot)),
							Root:     cachedBlock.Root,
							Orphaned: cachedBlock.Orphaned,
						},
					}
				} else {
					dbres := &dbtypes.SearchAheadSlotsResult{}
					err = db.ReaderDb.Select(dbres, `
					SELECT slot, root, orphaned 
					FROM blocks 
					WHERE root = $1 OR
						state_root = $1
					ORDER BY slot LIMIT 1`, blockHash)
					if err != nil {
						logger.Errorf("error reading block root: %v", err)
						http.Error(w, "Internal server error", http.StatusServiceUnavailable)
						return
					}
					if len(*dbres) > 0 {
						result = &[]models.SearchAheadSlotsResult{
							{
								Slot:     fmt.Sprintf("%v", (*dbres)[0].Slot),
								Root:     (*dbres)[0].Root,
								Orphaned: (*dbres)[0].Orphaned,
							},
						}
					}

				}
			} else if _, convertErr := strconv.ParseInt(search, 10, 32); convertErr == nil {
				dbres := &dbtypes.SearchAheadSlotsResult{}
				err = db.ReaderDb.Select(dbres, `
				SELECT slot, root, orphaned 
				FROM blocks 
				WHERE slot = $1
				ORDER BY slot LIMIT 10`, search)
				if err == nil {
					model := make([]models.SearchAheadSlotsResult, len(*dbres))
					for idx, entry := range *dbres {
						model[idx] = models.SearchAheadSlotsResult{
							Slot:     fmt.Sprintf("%v", entry.Slot),
							Root:     entry.Root,
							Orphaned: entry.Orphaned,
						}
					}
					result = model
				}
			}
		}
	case "graffiti":
		graffiti := &dbtypes.SearchAheadGraffitiResult{}
		err = db.ReaderDb.Select(graffiti, db.EngineQuery(map[dbtypes.DBEngineType]string{
			dbtypes.DBEnginePgsql: `
				SELECT graffiti, count(*) as count
				FROM blocks
				WHERE graffiti_text ILIKE LOWER($1)
				GROUP BY graffiti
				ORDER BY count desc
				LIMIT 10`,
			dbtypes.DBEngineSqlite: `
				SELECT graffiti, count(*) as count
				FROM blocks
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

	default:
		http.Error(w, "Not found", 404)
		return
	}

	if err != nil {
		logger.WithError(err).WithField("searchType", searchType).Error("error doing query for searchAhead")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
	err = json.NewEncoder(w).Encode(result)
	if err != nil {
		logger.WithError(err).Error("error encoding searchAhead")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}
