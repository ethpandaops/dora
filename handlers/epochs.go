package handlers

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/pk910/light-beaconchain-explorer/types/models"
	"github.com/pk910/light-beaconchain-explorer/utils"
	"github.com/sirupsen/logrus"
)

// Epochs will return the main "epochs" page using a go template
func Epochs(w http.ResponseWriter, r *http.Request) {
	var indexTemplateFiles = append(layoutTemplateFiles,
		"epochs/epochs.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(indexTemplateFiles...)

	w.Header().Set("Content-Type", "text/html")
	data := InitPageData(w, r, "blockchain", "/epochs", "Epochs", indexTemplateFiles)

	urlArgs := r.URL.Query()
	var firstEpoch uint64 = math.MaxUint64
	if urlArgs.Has("epoch") {
		firstEpoch, _ = strconv.ParseUint(urlArgs.Get("epoch"), 10, 64)
	}
	var pageSize uint64 = 50
	if urlArgs.Has("count") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("count"), 10, 64)
	}
	data.Data = getEpochsPageData(firstEpoch, pageSize)

	if handleTemplateError(w, r, "epochs.go", "Epochs", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getEpochsPageData(firstEpoch uint64, pageSize uint64) *models.EpochsPageData {
	pageData := &models.EpochsPageData{}
	pageCacheKey := fmt.Sprintf("epochs:%v:%v", firstEpoch, pageSize)
	pageData = services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildEpochsPageData(firstEpoch, pageSize)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	}).(*models.EpochsPageData)
	return pageData
}

func buildEpochsPageData(firstEpoch uint64, pageSize uint64) (*models.EpochsPageData, time.Duration) {
	logrus.Printf("epochs page called: %v:%v", firstEpoch, pageSize)
	pageData := &models.EpochsPageData{}

	now := time.Now()
	currentEpoch := utils.TimeToEpoch(now)
	if currentEpoch < 0 {
		currentEpoch = 0
	}
	if firstEpoch > uint64(currentEpoch) {
		pageData.IsDefaultPage = true
		firstEpoch = uint64(currentEpoch)
	}

	if pageSize > 100 {
		pageSize = 100
	}
	pagesBefore := (firstEpoch + 1) / pageSize
	if ((firstEpoch + 1) % pageSize) > 0 {
		pagesBefore++
	}
	pagesAfter := (uint64(currentEpoch) - firstEpoch) / pageSize
	if ((uint64(currentEpoch) - firstEpoch) % pageSize) > 0 {
		pagesAfter++
	}
	pageData.PageSize = pageSize
	pageData.TotalPages = pagesBefore + pagesAfter
	pageData.CurrentPageIndex = pagesAfter + 1
	pageData.CurrentPageEpoch = firstEpoch
	pageData.PrevPageIndex = pageData.CurrentPageIndex - 1
	pageData.PrevPageEpoch = pageData.CurrentPageEpoch + pageSize
	if pageData.CurrentPageEpoch >= pageSize {
		pageData.NextPageIndex = pageData.CurrentPageIndex + 1
		pageData.NextPageEpoch = pageData.CurrentPageEpoch - pageSize
	}
	pageData.LastPageEpoch = pageSize - 1

	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	epochLimit := pageSize

	// load epochs
	pageData.Epochs = make([]*models.EpochsPageDataEpoch, 0)
	dbEpochs := services.GlobalBeaconService.GetDbEpochs(uint64(firstEpoch), uint32(epochLimit))
	dbIdx := 0
	dbCnt := len(dbEpochs)
	epochCount := uint64(0)
	allFinalized := true
	allSynchronized := true
	for epochIdx := int64(firstEpoch); epochIdx >= 0 && epochCount < epochLimit; epochIdx-- {
		epoch := uint64(epochIdx)
		finalized := finalizedEpoch >= epochIdx
		if !finalized {
			allFinalized = false
		}
		epochData := &models.EpochsPageDataEpoch{
			Epoch:     epoch,
			Ts:        utils.EpochToTime(epoch),
			Finalized: finalized,
		}
		if dbIdx < dbCnt && dbEpochs[dbIdx] != nil && dbEpochs[dbIdx].Epoch == epoch {
			dbEpoch := dbEpochs[dbIdx]
			dbIdx++
			epochData.Synchronized = true
			epochData.CanonicalBlockCount = uint64(dbEpoch.BlockCount)
			epochData.OrphanedBlockCount = uint64(dbEpoch.OrphanedCount)
			epochData.AttestationCount = dbEpoch.AttestationCount
			epochData.DepositCount = dbEpoch.DepositCount
			epochData.ExitCount = dbEpoch.ExitCount
			epochData.ProposerSlashingCount = dbEpoch.ProposerSlashingCount
			epochData.AttesterSlashingCount = dbEpoch.AttesterSlashingCount
			epochData.EligibleEther = dbEpoch.Eligible
			epochData.TargetVoted = dbEpoch.VotedTarget
			epochData.HeadVoted = dbEpoch.VotedHead
			epochData.TotalVoted = dbEpoch.VotedTotal
			if dbEpoch.Eligible > 0 {
				epochData.TargetVoteParticipation = float64(dbEpoch.VotedTarget) * 100.0 / float64(dbEpoch.Eligible)
				epochData.HeadVoteParticipation = float64(dbEpoch.VotedHead) * 100.0 / float64(dbEpoch.Eligible)
				epochData.TotalVoteParticipation = float64(dbEpoch.VotedTotal) * 100.0 / float64(dbEpoch.Eligible)
			}
			epochData.EthTransactionCount = dbEpoch.EthTransactionCount
		} else {
			allSynchronized = false
		}
		pageData.Epochs = append(pageData.Epochs, epochData)
		epochCount++
	}
	pageData.EpochCount = uint64(epochCount)
	pageData.FirstEpoch = firstEpoch
	pageData.LastEpoch = firstEpoch - pageData.EpochCount + 1

	var cacheTimeout time.Duration
	if !allSynchronized {
		cacheTimeout = 30 * time.Second
	} else if allFinalized {
		cacheTimeout = 30 * time.Minute
	} else if firstEpoch+2 < uint64(currentEpoch) {
		cacheTimeout = 10 * time.Minute
	} else {
		cacheTimeout = 12 * time.Second
	}
	return pageData, cacheTimeout
}
