package handlers

import (
	"net/http"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

func Blobs(w http.ResponseWriter, r *http.Request) {
	var blobsTemplateFiles = append(layoutTemplateFiles,
		"blobs/blobs.html",
	)

	var pageTemplate = templates.GetTemplate(blobsTemplateFiles...)
	data := InitPageData(w, r, "blockchain", "/blobs", "Blobs", blobsTemplateFiles)

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getBlobsPageData()
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "blobs.go", "Blobs", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getBlobsPageData() (*models.BlobsPageData, error) {
	pageData := &models.BlobsPageData{}
	pageCacheKey := "blobs"
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) any {
		pageData, cacheTimeout := buildBlobsPageData()
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.BlobsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildBlobsPageData() (*models.BlobsPageData, time.Duration) {
	logrus.Debugf("blobs page called")

	chainState := services.GlobalBeaconService.GetChainState()
	currentSlot := chainState.CurrentSlot()
	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()

	pageData := &models.BlobsPageData{
		StorageCalculator: &models.StorageCalculatorData{
			MinEth:             32,
			MaxEth:             4196,
			DefaultEth:         256,
			ColumnSize:         2.0,
			TotalColumns:       128,
			MinColumnsNonVal:   4,
			MinColumnsVal:      8,
			FreeValidatorCount: 8,
		},
	}

	stats, err := db.GetBlobStatistics(uint64(currentSlot))
	if err != nil {
		logrus.WithError(err).Error("error getting blob statistics")
	} else {
		pageData.TotalBlobs = stats.TotalBlobs
		pageData.TotalBlobsInBlocks = stats.TotalBlobsInBlocks
		pageData.AvgBlobsPerBlock = stats.AvgBlobsPerBlock
		pageData.BlobsLast1h = stats.BlobsLast1h
		pageData.BlobsLast24h = stats.BlobsLast24h
		pageData.BlobsLast7d = stats.BlobsLast7d
		pageData.BlobsLast30d = stats.BlobsLast18d
		pageData.BlocksWithBlobsLast1h = stats.BlocksWithBlobsLast1h
		pageData.BlocksWithBlobsLast24h = stats.BlocksWithBlobsLast24h
		pageData.BlocksWithBlobsLast7d = stats.BlocksWithBlobsLast7d
		pageData.BlocksWithBlobsLast18d = stats.BlocksWithBlobsLast18d
		pageData.BlobGasLast1h = stats.BlobGasLast1h
		pageData.BlobGasLast24h = stats.BlobGasLast24h
		pageData.BlobGasLast7d = stats.BlobGasLast7d
		pageData.BlobGasLast18d = stats.BlobGasLast18d
		pageData.TotalBlobGasUsed = stats.TotalBlobGasUsed
		pageData.AvgBlobGasPerBlock = stats.AvgBlobGasPerBlock
	}

	minBlobCount := uint64(1)
	blocksData := services.GlobalBeaconService.GetDbBlocksByFilter(&dbtypes.BlockFilter{
		WithOrphaned: 0,
		WithMissing:  0,
		MinBlobCount: &minBlobCount,
	}, 0, 20, 0)

	pageData.LatestBlobBlocks = make([]*models.LatestBlobBlock, 0, len(blocksData))
	for _, blockData := range blocksData {
		block := blockData.Block
		blockSlot := phase0.Slot(block.Slot)
		finalized := finalizedEpoch > 0 && finalizedEpoch >= chainState.EpochOfSlot(blockSlot)

		var blockNumber uint64
		if block.EthBlockNumber != nil {
			blockNumber = *block.EthBlockNumber
		}

		pageData.LatestBlobBlocks = append(pageData.LatestBlobBlocks, &models.LatestBlobBlock{
			Slot:         block.Slot,
			BlockNumber:  blockNumber,
			Timestamp:    chainState.SlotToTime(blockSlot),
			BlobCount:    block.BlobCount,
			Proposer:     block.Proposer,
			ProposerName: services.GlobalBeaconService.GetValidatorName(block.Proposer),
			BlockRoot:    block.Root,
			Finalized:    finalized,
		})
	}

	cacheTimeout := 12 * time.Second

	return pageData, cacheTimeout
}
