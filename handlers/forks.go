package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// Forks will return the main "forks" page using a go template
func Forks(w http.ResponseWriter, r *http.Request) {
	var forksTemplateFiles = append(layoutTemplateFiles,
		"forks/forks.html",
	)

	var pageTemplate = templates.GetTemplate(forksTemplateFiles...)
	data := InitPageData(w, r, "forks", "/forks", "Forks", forksTemplateFiles)

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getForksPageData()
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	if r.Header.Get("Content-Type") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		forksDataBytes, err := json.Marshal(data.Data)
		if err != nil {
			w.Write([]byte(fmt.Sprintf("%s", err.Error())))
			return
		}
		w.Write(forksDataBytes)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "forks.go", "Forks", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getForksPageData() (*models.ForksPageData, error) {
	pageData := &models.ForksPageData{}
	pageCacheKey := "forks"
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildForksPageData()
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ForksPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildForksPageData() (*models.ForksPageData, time.Duration) {
	logrus.Debugf("forks page called")
	pageData := &models.ForksPageData{}

	headForks := services.GlobalBeaconService.GetConsensusClientForks()
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	cacheTime := specs.SecondsPerSlot

	// check each fork if it's really a fork and not just a syncing/stuck client
	finalizedEpoch, _ := services.GlobalBeaconService.GetBeaconIndexer().GetBlockCacheState()
	for idx, fork := range headForks {
		if idx == 0 {
			continue
		}
		if fork.Slot < chainState.EpochToSlot(finalizedEpoch) {
			// check block
			dbBlock := db.GetSlotByRoot(fork.Root[:])
			if dbBlock != nil && dbBlock.Status == dbtypes.Canonical {
				headForks[0].AllClients = append(headForks[0].AllClients, fork.AllClients...)
				headForks[idx] = nil
			}
		}
	}

	for _, fork := range headForks {
		if fork == nil {
			continue
		}
		forkData := &models.ForksPageDataFork{
			HeadSlot: uint64(fork.Slot),
			HeadRoot: fork.Root[:],
			Clients:  []*models.ForksPageDataClient{},
		}
		pageData.Forks = append(pageData.Forks, forkData)

		for _, client := range fork.AllClients {
			consensusClient := client.GetClient()
			clientHeadSlot, _ := consensusClient.GetLastHead()
			forkClient := &models.ForksPageDataClient{
				Index:       int(client.GetIndex()) + 1,
				Name:        consensusClient.GetName(),
				Version:     consensusClient.GetVersion(),
				Status:      consensusClient.GetStatus().String(),
				LastRefresh: consensusClient.GetLastEventTime(),
				HeadSlot:    uint64(clientHeadSlot),
				Distance:    uint64(fork.Slot - clientHeadSlot),
			}
			if lastErr := consensusClient.GetLastClientError(); lastErr != nil {
				forkClient.LastError = lastErr.Error()
			}
			forkData.Clients = append(forkData.Clients, forkClient)
		}
		sort.Slice(forkData.Clients, func(a, b int) bool {
			return forkData.Clients[a].Index < forkData.Clients[b].Index
			/*
				clientA := forkData.Clients[a]
				clientB := forkData.Clients[b]
				if clientA.Distance == clientB.Distance {
					return clientA.Index < clientB.Index
				}
				return clientA.Distance < clientB.Distance
			*/
		})
		forkData.ClientCount = uint64(len(forkData.Clients))
	}
	pageData.ForkCount = uint64(len(pageData.Forks))

	return pageData, cacheTime
}
