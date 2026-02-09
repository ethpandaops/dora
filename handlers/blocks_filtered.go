package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// BlocksFiltered will return the filtered "blocks" page using a go template
func BlocksFiltered(w http.ResponseWriter, r *http.Request) {
	var blocksTemplateFiles = append(layoutTemplateFiles,
		"blocks_filtered/blocks_filtered.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(blocksTemplateFiles...)
	data := InitPageData(w, r, "blockchain", "/blocks/filtered", "Filtered Blocks", blocksTemplateFiles)

	urlArgs := r.URL.Query()
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	var pageIdx uint64 = 0
	if urlArgs.Has("s") {
		pageIdx, _ = strconv.ParseUint(urlArgs.Get("s"), 10, 64)
	}
	var displayColumns uint64 = 0
	if urlArgs.Has("d") {
		displayColumns = utils.DecodeUint64BitfieldFromQuery(r.URL.RawQuery, "d")
	}

	var extradata string
	var invertextradata bool
	var minGasUsed string
	var maxGasUsed string
	var minGasLimit string
	var maxGasLimit string
	var minBlockSize string
	var maxBlockSize string
	var withMevBlock uint64
	var minTxCount string
	var maxTxCount string
	var minBlobCount string
	var maxBlobCount string
	var minEpoch string
	var maxEpoch string

	if urlArgs.Has("f") {
		if urlArgs.Has("f.extra") {
			extradata = urlArgs.Get("f.extra")
		}
		if urlArgs.Has("f.einvert") {
			invertextradata = urlArgs.Get("f.einvert") == "on"
		}
		if urlArgs.Has("f.mingas") {
			minGasUsed = urlArgs.Get("f.mingas")
		}
		if urlArgs.Has("f.maxgas") {
			maxGasUsed = urlArgs.Get("f.maxgas")
		}
		if urlArgs.Has("f.minlimit") {
			minGasLimit = urlArgs.Get("f.minlimit")
		}
		if urlArgs.Has("f.maxlimit") {
			maxGasLimit = urlArgs.Get("f.maxlimit")
		}
		if urlArgs.Has("f.minsize") {
			minBlockSize = urlArgs.Get("f.minsize")
		}
		if urlArgs.Has("f.maxsize") {
			maxBlockSize = urlArgs.Get("f.maxsize")
		}
		if urlArgs.Has("f.mev") {
			withMevBlock, _ = strconv.ParseUint(urlArgs.Get("f.mev"), 10, 64)
		}
		if urlArgs.Has("f.mintx") {
			minTxCount = urlArgs.Get("f.mintx")
		}
		if urlArgs.Has("f.maxtx") {
			maxTxCount = urlArgs.Get("f.maxtx")
		}
		if urlArgs.Has("f.minblob") {
			minBlobCount = urlArgs.Get("f.minblob")
		}
		if urlArgs.Has("f.maxblob") {
			maxBlobCount = urlArgs.Get("f.maxblob")
		}
		if urlArgs.Has("f.minepoch") {
			minEpoch = urlArgs.Get("f.minepoch")
		}
		if urlArgs.Has("f.maxepoch") {
			maxEpoch = urlArgs.Get("f.maxepoch")
		}
	} else {
		withMevBlock = 1
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredBlocksPageData(pageIdx, pageSize, extradata, invertextradata, minGasUsed, maxGasUsed, minGasLimit, maxGasLimit, minBlockSize, maxBlockSize, uint8(withMevBlock), minTxCount, maxTxCount, minBlobCount, maxBlobCount, minEpoch, maxEpoch, displayColumns)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "blocks_filtered.go", "BlocksFiltered", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getFilteredBlocksPageData(pageIdx uint64, pageSize uint64, extradata string, invertextradata bool, minGasUsed string, maxGasUsed string, minGasLimit string, maxGasLimit string, minBlockSize string, maxBlockSize string, withMevBlock uint8, minTxCount string, maxTxCount string, minBlobCount string, maxBlobCount string, minEpoch string, maxEpoch string, displayColumns uint64) (*models.BlocksFilteredPageData, error) {
	pageData := &models.BlocksFilteredPageData{}
	pageCacheKey := fmt.Sprintf("blocks_filtered:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, extradata, invertextradata, minGasUsed, maxGasUsed, minGasLimit, maxGasLimit, minBlockSize, maxBlockSize, withMevBlock, minTxCount, maxTxCount, minBlobCount, maxBlobCount, minEpoch, maxEpoch, displayColumns)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredBlocksPageData(pageIdx, pageSize, extradata, invertextradata, minGasUsed, maxGasUsed, minGasLimit, maxGasLimit, minBlockSize, maxBlockSize, withMevBlock, minTxCount, maxTxCount, minBlobCount, maxBlobCount, minEpoch, maxEpoch, displayColumns)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.BlocksFilteredPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildFilteredBlocksPageData(pageIdx uint64, pageSize uint64, extradata string, invertextradata bool, minGasUsed string, maxGasUsed string, minGasLimit string, maxGasLimit string, minBlockSize string, maxBlockSize string, withMevBlock uint8, minTxCount string, maxTxCount string, minBlobCount string, maxBlobCount string, minEpoch string, maxEpoch string, displayColumns uint64) *models.BlocksFilteredPageData {
	chainState := services.GlobalBeaconService.GetChainState()
	filterArgs := url.Values{}

	if extradata != "" {
		filterArgs.Add("f.extra", extradata)
	}
	if invertextradata {
		filterArgs.Add("f.einvert", "on")
	}
	if minGasUsed != "" {
		filterArgs.Add("f.mingas", minGasUsed)
	}
	if maxGasUsed != "" {
		filterArgs.Add("f.maxgas", maxGasUsed)
	}
	if minGasLimit != "" {
		filterArgs.Add("f.minlimit", minGasLimit)
	}
	if maxGasLimit != "" {
		filterArgs.Add("f.maxlimit", maxGasLimit)
	}
	if minBlockSize != "" {
		filterArgs.Add("f.minsize", minBlockSize)
	}
	if maxBlockSize != "" {
		filterArgs.Add("f.maxsize", maxBlockSize)
	}
	if withMevBlock != 0 {
		filterArgs.Add("f.mev", fmt.Sprintf("%v", withMevBlock))
	}
	if minTxCount != "" {
		filterArgs.Add("f.mintx", minTxCount)
	}
	if maxTxCount != "" {
		filterArgs.Add("f.maxtx", maxTxCount)
	}
	if minBlobCount != "" {
		filterArgs.Add("f.minblob", minBlobCount)
	}
	if maxBlobCount != "" {
		filterArgs.Add("f.maxblob", maxBlobCount)
	}
	if minEpoch != "" {
		filterArgs.Add("f.minepoch", minEpoch)
	}
	if maxEpoch != "" {
		filterArgs.Add("f.maxepoch", maxEpoch)
	}

	displayMap := make(map[uint64]bool, 13)
	if displayColumns != 0 {
		for i := 0; i < 64; i++ {
			if displayColumns&(1<<i) != 0 {
				displayMap[uint64(i+1)] = true
			}
		}
	}
	if len(displayMap) == 0 {
		displayMap = map[uint64]bool{
			1:  true,  // Epoch
			2:  true,  // Block Number
			3:  true,  // Slot
			4:  true,  // Status
			5:  true,  // Time
			6:  true,  // Tx Count
			7:  true,  // Blob Count
			8:  true,  // Gas Usage
			9:  true,  // Gas Limit
			10: true,  // MEV Block
			11: true,  // Block Size
			12: true,  // Extra Data
			13: false, // Fee Recipient
		}
	} else {
		displayMask := uint64(0)
		for col := range displayMap {
			if col == 0 || col > 64 {
				continue
			}
			displayMask |= 1 << (col - 1)
		}
		filterArgs.Add("d", fmt.Sprintf("0x%x", displayMask))
	}

	pageData := &models.BlocksFilteredPageData{
		FilterExtraData:       extradata,
		FilterInvertExtraData: invertextradata,
		FilterMinGasUsed:      minGasUsed,
		FilterMaxGasUsed:      maxGasUsed,
		FilterMinGasLimit:     minGasLimit,
		FilterMaxGasLimit:     maxGasLimit,
		FilterMinBlockSize:    minBlockSize,
		FilterMaxBlockSize:    maxBlockSize,
		FilterWithMevBlock:    withMevBlock,
		FilterMinTxCount:      minTxCount,
		FilterMaxTxCount:      maxTxCount,
		FilterMinBlobCount:    minBlobCount,
		FilterMaxBlobCount:    maxBlobCount,
		FilterMinEpoch:        minEpoch,
		FilterMaxEpoch:        maxEpoch,

		DisplayEpoch:        displayMap[1],
		DisplayBlockNumber:  displayMap[2],
		DisplaySlot:         displayMap[3],
		DisplayStatus:       displayMap[4],
		DisplayTime:         displayMap[5],
		DisplayTxCount:      displayMap[6],
		DisplayBlobCount:    displayMap[7],
		DisplayGasUsage:     displayMap[8],
		DisplayGasLimit:     displayMap[9],
		DisplayMevBlock:     displayMap[10],
		DisplayBlockSize:    displayMap[11],
		DisplayElExtraData:  displayMap[12],
		DisplayFeeRecipient: displayMap[13],
		DisplayColCount:     uint64(len(displayMap)),
	}
	logrus.Debugf("blocks_filtered page called: %v:%v [%v]", pageIdx, pageSize, extradata)
	if pageIdx == 0 {
		pageData.IsDefaultPage = true
	}

	if pageSize > 100 {
		pageSize = 100
	}
	pageData.PageSize = pageSize
	pageData.TotalPages = pageIdx + 1
	pageData.CurrentPageIndex = pageIdx + 1
	pageData.CurrentPageSlot = pageIdx
	if pageIdx >= 1 {
		pageData.PrevPageIndex = pageIdx
		pageData.PrevPageSlot = pageIdx - 1
	}
	pageData.LastPageSlot = 0

	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()

	pageData.Blocks = make([]*models.BlocksFilteredPageDataBlock, 0)
	blockFilter := &dbtypes.BlockFilter{
		ExtraData:       extradata,
		InvertExtraData: invertextradata,
		WithOrphaned:    0,
		WithMissing:     0,
		WithMevBlock:    withMevBlock,
	}

	if minGasUsed != "" {
		minGas, err := strconv.ParseUint(minGasUsed, 10, 64)
		if err == nil {
			blockFilter.MinGasUsed = &minGas
		}
	}
	if maxGasUsed != "" {
		maxGas, err := strconv.ParseUint(maxGasUsed, 10, 64)
		if err == nil {
			blockFilter.MaxGasUsed = &maxGas
		}
	}
	if minGasLimit != "" {
		minLimit, err := strconv.ParseUint(minGasLimit, 10, 64)
		if err == nil {
			blockFilter.MinGasLimit = &minLimit
		}
	}
	if maxGasLimit != "" {
		maxLimit, err := strconv.ParseUint(maxGasLimit, 10, 64)
		if err == nil {
			blockFilter.MaxGasLimit = &maxLimit
		}
	}
	if minBlockSize != "" {
		minSize, err := strconv.ParseUint(minBlockSize, 10, 64)
		if err == nil {
			minSizeBytes := minSize * 1024
			blockFilter.MinBlockSize = &minSizeBytes
		}
	}
	if maxBlockSize != "" {
		maxSize, err := strconv.ParseUint(maxBlockSize, 10, 64)
		if err == nil {
			maxSizeBytes := maxSize * 1024
			blockFilter.MaxBlockSize = &maxSizeBytes
		}
	}
	if minTxCount != "" {
		minTx, err := strconv.ParseUint(minTxCount, 10, 64)
		if err == nil {
			blockFilter.MinTxCount = &minTx
		}
	}
	if maxTxCount != "" {
		maxTx, err := strconv.ParseUint(maxTxCount, 10, 64)
		if err == nil {
			blockFilter.MaxTxCount = &maxTx
		}
	}
	if minBlobCount != "" {
		minBlob, err := strconv.ParseUint(minBlobCount, 10, 64)
		if err == nil {
			blockFilter.MinBlobCount = &minBlob
		}
	}
	if maxBlobCount != "" {
		maxBlob, err := strconv.ParseUint(maxBlobCount, 10, 64)
		if err == nil {
			blockFilter.MaxBlobCount = &maxBlob
		}
	}
	if minEpoch != "" {
		minEp, err := strconv.ParseUint(minEpoch, 10, 64)
		if err == nil {
			blockFilter.MinEpoch = &minEp
		}
	}
	if maxEpoch != "" {
		maxEp, err := strconv.ParseUint(maxEpoch, 10, 64)
		if err == nil {
			blockFilter.MaxEpoch = &maxEp
		}
	}

	withScheduledCount := uint64(0)

	dbBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(blockFilter, pageIdx, uint32(pageSize), withScheduledCount)
	mevBlocksMap := make(map[string]*dbtypes.MevBlock)

	if pageData.DisplayMevBlock || withMevBlock == 2 {
		var execBlockHashes [][]byte

		for _, dbBlock := range dbBlocks {
			if dbBlock.Block != nil && dbBlock.Block.Status > 0 && dbBlock.Block.EthBlockHash != nil {
				execBlockHashes = append(execBlockHashes, dbBlock.Block.EthBlockHash)
			}
		}

		if len(execBlockHashes) > 0 {
			mevBlocksMap = db.GetMevBlocksByBlockHashes(execBlockHashes)
		}
	}

	haveMore := false
	for idx, dbBlock := range dbBlocks {
		if idx >= int(pageSize) {
			haveMore = true
			break
		}

		if dbBlock.Block == nil || dbBlock.Block.EthBlockNumber == nil {
			continue
		}

		slot := phase0.Slot(dbBlock.Slot)
		isMevBlock := false
		mevBlockRelays := ""

		if dbBlock.Block.EthBlockHash != nil {
			if mevBlock, exists := mevBlocksMap[fmt.Sprintf("%x", dbBlock.Block.EthBlockHash)]; exists {
				isMevBlock = true

				var relays []string
				for _, relay := range utils.Config.MevIndexer.Relays {
					relayFlag := uint64(1) << uint64(relay.Index)
					if mevBlock.SeenbyRelays&relayFlag > 0 {
						relays = append(relays, relay.Name)
					}
				}
				mevBlockRelays = strings.Join(relays, ", ")
			}
		}

		if withMevBlock == 2 && !isMevBlock {
			continue
		}
		if withMevBlock == 0 && isMevBlock {
			continue
		}

		blockData := &models.BlocksFilteredPageDataBlock{
			Epoch:               uint64(chainState.EpochOfSlot(slot)),
			Slot:                uint64(slot),
			EthBlockNumber:      *dbBlock.Block.EthBlockNumber,
			Ts:                  chainState.SlotToTime(slot),
			Status:              uint8(dbBlock.Block.Status),
			EthTransactionCount: dbBlock.Block.EthTransactionCount,
			BlobCount:           dbBlock.Block.BlobCount,
			ElExtraData:         dbBlock.Block.EthBlockExtra,
			GasUsed:             dbBlock.Block.EthGasUsed,
			GasLimit:            dbBlock.Block.EthGasLimit,
			BlockSize:           dbBlock.Block.BlockSize,
			BlockRoot:           dbBlock.Block.Root,
			IsMevBlock:          isMevBlock,
			MevBlockRelays:      mevBlockRelays,
			FeeRecipient:        dbBlock.Block.EthFeeRecipient,
		}

		if finalizedEpoch >= chainState.EpochOfSlot(slot) {
			blockData.Status = 1
		}

		pageData.Blocks = append(pageData.Blocks, blockData)
	}
	pageData.BlockCount = uint64(len(pageData.Blocks))
	if pageData.BlockCount > 0 {
		pageData.FirstBlock = pageData.Blocks[0].EthBlockNumber
		pageData.LastBlock = pageData.Blocks[pageData.BlockCount-1].EthBlockNumber
	}
	if haveMore {
		pageData.NextPageIndex = pageIdx + 1
		pageData.NextPageSlot = pageIdx + 1
		pageData.TotalPages++
	}

	pageData.UrlParams = make(map[string]string, len(filterArgs)+1)
	for key, values := range filterArgs {
		if len(values) > 0 {
			pageData.UrlParams[key] = values[0]
		}
	}
	pageData.UrlParams["c"] = fmt.Sprintf("%v", pageData.PageSize)

	pageData.FirstPageLink = fmt.Sprintf("/blocks/filtered?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/blocks/filtered?f&%v&c=%v&s=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageSlot)
	pageData.NextPageLink = fmt.Sprintf("/blocks/filtered?f&%v&c=%v&s=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageSlot)
	pageData.LastPageLink = fmt.Sprintf("/blocks/filtered?f&%v&c=%v&s=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageSlot)

	return pageData
}
