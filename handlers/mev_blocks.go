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

// MevBlocks will return the filtered "mev_blocks" page using a go template
func MevBlocks(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"mev_blocks/mev_blocks.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "blockchain", "/mev/blocks", "MEV Blocks", templateFiles)

	urlArgs := r.URL.Query()
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	var pageIdx uint64 = 1
	if urlArgs.Has("p") {
		pageIdx, _ = strconv.ParseUint(urlArgs.Get("p"), 10, 64)
		if pageIdx < 1 {
			pageIdx = 1
		}
	}

	var minSlot uint64
	var maxSlot uint64
	var minIndex uint64
	var maxIndex uint64
	var vname string
	var withRelays string
	var withProposed string

	if urlArgs.Has("f") {
		if urlArgs.Has("f.mins") {
			minSlot, _ = strconv.ParseUint(urlArgs.Get("f.mins"), 10, 64)
		}
		if urlArgs.Has("f.maxs") {
			maxSlot, _ = strconv.ParseUint(urlArgs.Get("f.maxs"), 10, 64)
		}
		if urlArgs.Has("f.mini") {
			minIndex, _ = strconv.ParseUint(urlArgs.Get("f.mini"), 10, 64)
		}
		if urlArgs.Has("f.maxi") {
			maxIndex, _ = strconv.ParseUint(urlArgs.Get("f.maxi"), 10, 64)
		}
		if urlArgs.Has("f.vname") {
			vname = urlArgs.Get("f.vname")
		}
		if urlArgs.Has("f.relays") {
			withRelays = urlArgs.Get("f.relays")
		}
		if urlArgs.Has("f.proposed") {
			withProposed = urlArgs.Get("f.proposed")
		}
	}
	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredMevBlocksPageData(pageIdx, pageSize, minSlot, maxSlot, minIndex, maxIndex, vname, withRelays, withProposed)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "mev_blocks.go", "MevBlocks", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getFilteredMevBlocksPageData(pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, minIndex uint64, maxIndex uint64, vname string, withRelays string, withProposed string) (*models.MevBlocksPageData, error) {
	pageData := &models.MevBlocksPageData{}
	pageCacheKey := fmt.Sprintf("mev_blocks:%v:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, minSlot, maxSlot, minIndex, maxIndex, vname, withRelays, withProposed)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(_ *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredMevBlocksPageData(pageIdx, pageSize, minSlot, maxSlot, minIndex, maxIndex, vname, withRelays, withProposed)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.MevBlocksPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildFilteredMevBlocksPageData(pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, minIndex uint64, maxIndex uint64, vname string, withRelays string, withProposed string) *models.MevBlocksPageData {
	filterArgs := url.Values{}
	if minSlot != 0 {
		filterArgs.Add("f.mins", fmt.Sprintf("%v", minSlot))
	}
	if maxSlot != 0 {
		filterArgs.Add("f.maxs", fmt.Sprintf("%v", maxSlot))
	}
	if minIndex != 0 {
		filterArgs.Add("f.mini", fmt.Sprintf("%v", minIndex))
	}
	if maxIndex != 0 {
		filterArgs.Add("f.maxi", fmt.Sprintf("%v", maxIndex))
	}
	if vname != "" {
		filterArgs.Add("f.vname", vname)
	}
	if withRelays != "" {
		filterArgs.Add("f.relays", withRelays)
	}
	if withProposed != "" {
		filterArgs.Add("f.proposed", withProposed)
	}

	pageData := &models.MevBlocksPageData{
		FilterMinSlot:       minSlot,
		FilterMaxSlot:       maxSlot,
		FilterMinIndex:      minIndex,
		FilterMaxIndex:      maxIndex,
		FilterValidatorName: vname,
		FilterRelays:        map[uint8]bool{},
		FilterRelayOpts:     []*models.MevBlocksPageDataRelay{},
		FilterProposed:      map[uint8]bool{},
	}

	for _, relay := range utils.Config.MevIndexer.Relays {
		pageData.FilterRelayOpts = append(pageData.FilterRelayOpts, &models.MevBlocksPageDataRelay{
			Index: uint64(relay.Index),
			Name:  relay.Name,
		})
	}

	withRelayIdx := []uint8{}
	for _, relayIdStr := range strings.Split(withRelays, " ") {
		if relayIdStr == "" {
			continue
		}

		relayId, err := strconv.ParseUint(relayIdStr, 10, 64)
		if err != nil {
			continue
		}

		if relayId < 63 {
			withRelayIdx = append(withRelayIdx, uint8(relayId))
			pageData.FilterRelays[uint8(relayId)] = true
		}
	}

	withProposedOpts := []uint8{}
	for _, proposedStr := range strings.Split(withProposed, " ") {
		if proposedStr == "" {
			continue
		}

		proposedOpt, err := strconv.ParseUint(proposedStr, 10, 64)
		if err != nil {
			continue
		}

		if proposedOpt <= 2 {
			withProposedOpts = append(withProposedOpts, uint8(proposedOpt))
			pageData.FilterProposed[uint8(proposedOpt)] = true
		}
	}

	logrus.Debugf("mev_blocks page called: %v:%v [%v,%v,%v,%v,%v]", pageIdx, pageSize, minSlot, maxSlot, minIndex, maxIndex, vname)
	if pageIdx == 1 {
		pageData.IsDefaultPage = true
	}

	if pageSize > 100 {
		pageSize = 100
	}
	pageData.PageSize = pageSize
	pageData.TotalPages = pageIdx
	pageData.CurrentPageIndex = pageIdx
	if pageIdx > 1 {
		pageData.PrevPageIndex = pageIdx - 1
	}

	// load mev blocks
	mevBlockFilter := &dbtypes.MevBlockFilter{
		MinSlot:      minSlot,
		MaxSlot:      maxSlot,
		MinIndex:     minIndex,
		MaxIndex:     maxIndex,
		ProposerName: vname,
		MevRelay:     withRelayIdx,
		Proposed:     withProposedOpts,
	}

	offset := (pageIdx - 1) * pageSize
	dbMevBlocks, totalRows, err := db.GetMevBlocksFiltered(offset, uint32(pageSize), mevBlockFilter)
	if err != nil {
		panic(err)
	}

	chainState := services.GlobalBeaconService.GetChainState()

	for _, mevBlock := range dbMevBlocks {
		mevBlockData := &models.MevBlocksPageDataBlock{
			SlotNumber:     mevBlock.SlotNumber,
			BlockHash:      mevBlock.BlockHash,
			BlockNumber:    mevBlock.BlockNumber,
			Time:           chainState.SlotToTime(phase0.Slot(mevBlock.SlotNumber)),
			ValidatorIndex: mevBlock.ProposerIndex,
			ValidatorName:  services.GlobalBeaconService.GetValidatorName(mevBlock.ProposerIndex),
			BuilderPubkey:  mevBlock.BuilderPubkey,
			Proposed:       mevBlock.Proposed,
			Relays:         []*models.MevBlocksPageDataRelay{},
			FeeRecipient:   mevBlock.FeeRecipient,
			TxCount:        mevBlock.TxCount,
			BlobCount:      mevBlock.BlobCount,
			GasUsed:        mevBlock.GasUsed,
			BlockValue:     mevBlock.BlockValueGwei,
		}

		for _, relay := range utils.Config.MevIndexer.Relays {
			relayFlag := uint64(1) << uint64(relay.Index)
			if mevBlock.SeenbyRelays&relayFlag > 0 {
				mevBlockData.Relays = append(mevBlockData.Relays, &models.MevBlocksPageDataRelay{
					Index: uint64(relay.Index),
					Name:  relay.Name,
				})
			}
		}
		mevBlockData.RelayCount = uint64(len(mevBlockData.Relays))

		pageData.MevBlocks = append(pageData.MevBlocks, mevBlockData)
	}
	pageData.BlockCount = uint64(len(pageData.MevBlocks))

	if pageData.BlockCount > 0 {
		pageData.FirstIndex = pageData.MevBlocks[0].SlotNumber
		pageData.LastIndex = pageData.MevBlocks[pageData.BlockCount-1].SlotNumber
	}

	pageData.TotalPages = totalRows / pageSize
	if totalRows%pageSize > 0 {
		pageData.TotalPages++
	}
	pageData.LastPageIndex = pageData.TotalPages
	if pageIdx < pageData.TotalPages {
		pageData.NextPageIndex = pageIdx + 1
	}

	// Populate UrlParams for page jump functionality
	pageData.UrlParams = make(map[string]string)
	for key, values := range filterArgs {
		if len(values) > 0 {
			pageData.UrlParams[key] = values[0]
		}
	}
	pageData.UrlParams["c"] = fmt.Sprintf("%v", pageData.PageSize)

	pageData.FirstPageLink = fmt.Sprintf("/mev/blocks?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/mev/blocks?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/mev/blocks?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/mev/blocks?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	return pageData
}
