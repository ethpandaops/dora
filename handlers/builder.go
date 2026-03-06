package handlers

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
)

// BuilderDetail will return the main "builder" page using a go template
func BuilderDetail(w http.ResponseWriter, r *http.Request) {
	var builderTemplateFiles = append(layoutTemplateFiles,
		"builder/builder.html",
		"builder/recentBlocks.html",
		"builder/recentBids.html",
		"builder/recentDeposits.html",
		"_svg/timeline.html",
	)
	var notfoundTemplateFiles = append(layoutTemplateFiles,
		"builder/notfound.html",
	)

	var pageTemplate = templates.GetTemplate(builderTemplateFiles...)
	data := InitPageData(w, r, "builders", "/builder", "Builder", builderTemplateFiles)

	var builder *gloas.Builder
	var builderIndex uint64
	var superseded bool

	vars := mux.Vars(r)
	idxOrPubKey := strings.Replace(vars["idxOrPubKey"], "0x", "", -1)
	builderPubKey, err := hex.DecodeString(idxOrPubKey)
	if err != nil || len(builderPubKey) != 48 {
		// search by index
		idx, err := strconv.ParseUint(vars["idxOrPubKey"], 10, 64)
		if err == nil {
			builderIndex = idx
			builder = services.GlobalBeaconService.GetBuilderByIndex(gloas.BuilderIndex(idx))
			if builder == nil {
				// Try from DB
				dbBuilder := db.GetActiveBuilderByIndex(r.Context(), idx)
				if dbBuilder != nil {
					builder = beacon.UnwrapDbBuilder(dbBuilder)
					superseded = dbBuilder.Superseded
				}
			}
		}
	} else {
		// search by pubkey
		dbBuilder := db.GetBuilderByPubkey(r.Context(), builderPubKey)
		if dbBuilder != nil {
			builderIndex = dbBuilder.BuilderIndex
			superseded = dbBuilder.Superseded
			builder = services.GlobalBeaconService.GetBuilderByIndex(gloas.BuilderIndex(dbBuilder.BuilderIndex))
			if builder == nil {
				builder = beacon.UnwrapDbBuilder(dbBuilder)
			}
		}
	}

	if builder == nil {
		data := InitPageData(w, r, "builders", "/builder", "Builder not found", notfoundTemplateFiles)
		w.Header().Set("Content-Type", "text/html")
		handleTemplateError(w, r, "builder.go", "BuilderDetail", "", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data))
		return
	}

	tabView := "blocks"
	if r.URL.Query().Has("v") {
		tabView = r.URL.Query().Get("v")
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getBuilderPageData(builderIndex, superseded, tabView)
	}
	if data.Data == nil {
		pageError = errors.New("builder not found")
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")

	if r.URL.Query().Has("lazy") {
		// return the selected tab content only (lazy loaded)
		handleTemplateError(w, r, "builder.go", "BuilderDetail", "", pageTemplate.ExecuteTemplate(w, "lazyPage", data.Data))
	} else {
		handleTemplateError(w, r, "builder.go", "BuilderDetail", "", pageTemplate.ExecuteTemplate(w, "layout", data))
	}
}

func getBuilderPageData(builderIndex uint64, superseded bool, tabView string) (*models.BuilderPageData, error) {
	pageData := &models.BuilderPageData{}
	pageCacheKey := fmt.Sprintf("builder:%v:%v", builderIndex, tabView)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildBuilderPageData(pageCall.CallCtx, builderIndex, superseded, tabView)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.BuilderPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildBuilderPageData(ctx context.Context, builderIndex uint64, superseded bool, tabView string) (*models.BuilderPageData, time.Duration) {
	logrus.Debugf("builder page called: %v", builderIndex)

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	currentEpoch := chainState.CurrentEpoch()

	// Get builder data
	builder := services.GlobalBeaconService.GetBuilderByIndex(gloas.BuilderIndex(builderIndex))
	if builder == nil {
		// Try from DB
		dbBuilder := db.GetActiveBuilderByIndex(ctx, builderIndex)
		if dbBuilder != nil {
			builder = beacon.UnwrapDbBuilder(dbBuilder)
			superseded = dbBuilder.Superseded
		}
	}
	if builder == nil {
		return nil, 0
	}

	// Determine state
	state := "Active"
	if superseded {
		state = "Superseded"
	} else if builder.WithdrawableEpoch <= currentEpoch {
		state = "Exited"
	}

	pageData := &models.BuilderPageData{
		CurrentEpoch:     uint64(currentEpoch),
		Index:            builderIndex,
		Name:             services.GlobalBeaconService.GetValidatorName(builderIndex | services.BuilderIndexFlag),
		PublicKey:        builder.PublicKey[:],
		Balance:          uint64(builder.Balance),
		ExecutionAddress: builder.ExecutionAddress[:],
		Version:          builder.Version,
		State:            state,
		IsSuperseded:     superseded,
		TabView:          tabView,
		GloasIsActive:    specs.GloasForkEpoch != nil && uint64(currentEpoch) >= *specs.GloasForkEpoch,
	}

	// Deposit epoch
	if builder.DepositEpoch < 18446744073709551615 {
		pageData.ShowDeposit = true
		pageData.DepositEpoch = uint64(builder.DepositEpoch)
		pageData.DepositTs = chainState.EpochToTime(builder.DepositEpoch)
	}

	// Withdrawable epoch
	if builder.WithdrawableEpoch < 18446744073709551615 {
		pageData.ShowWithdrawable = true
		pageData.WithdrawableEpoch = uint64(builder.WithdrawableEpoch)
		pageData.WithdrawableTs = chainState.EpochToTime(builder.WithdrawableEpoch)
	}

	// Load tab-specific data
	switch tabView {
	case "blocks":
		pageData.RecentBlocks = buildBuilderRecentBlocks(ctx, builderIndex, chainState)
	case "bids":
		pageData.RecentBids = buildBuilderRecentBids(ctx, builderIndex, chainState)
	case "deposits":
		pageData.RecentDeposits = buildBuilderRecentDeposits(ctx, builderIndex, chainState)
	}

	return pageData, 10 * time.Minute
}

func buildBuilderRecentBlocks(ctx context.Context, builderIndex uint64, chainState *consensus.ChainState) []*models.BuilderPageDataBlock {
	// Filter blocks by builder index using the new DB filter
	builderIndexInt64 := int64(builderIndex)
	filter := &dbtypes.BlockFilter{
		BuilderIndex: &builderIndexInt64,
		WithOrphaned: 1, // Include both canonical and orphaned
		WithMissing:  0, // Exclude missing blocks
	}

	// Get blocks built by this builder
	dbBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, filter, 0, 20, 0)

	// Collect block hashes for batch bid lookup
	blockHashes := make([][]byte, 0, len(dbBlocks))
	validBlocks := make([]*dbtypes.Slot, 0, len(dbBlocks))

	for _, assignedSlot := range dbBlocks {
		if assignedSlot.Block == nil {
			continue
		}
		slot := assignedSlot.Block

		// Only include blocks with actual payloads
		if slot.PayloadStatus != dbtypes.PayloadStatusCanonical && slot.PayloadStatus != dbtypes.PayloadStatusOrphaned {
			continue
		}

		if len(slot.EthBlockHash) > 0 {
			blockHashes = append(blockHashes, slot.EthBlockHash)
			validBlocks = append(validBlocks, slot)
		}
	}

	// Batch fetch all bids for these block hashes
	bidsMap := db.GetBidsByBlockHashes(ctx, blockHashes, builderIndex)

	// Build result
	blocks := make([]*models.BuilderPageDataBlock, 0, len(validBlocks))
	for _, slot := range validBlocks {
		block := &models.BuilderPageDataBlock{
			Epoch:        uint64(chainState.EpochOfSlot(phase0.Slot(slot.Slot))),
			Slot:         slot.Slot,
			Ts:           chainState.SlotToTime(phase0.Slot(slot.Slot)),
			BlockRoot:    slot.Root,
			BlockHash:    slot.EthBlockHash,
			Status:       uint16(slot.PayloadStatus),
			FeeRecipient: slot.EthFeeRecipient,
			GasLimit:     slot.EthGasLimit,
		}

		// Look up bid info for Value and ElPayment from the batch result
		blockHashKey := fmt.Sprintf("%x", slot.EthBlockHash)
		if bid, ok := bidsMap[blockHashKey]; ok {
			block.Value = bid.Value
			block.ElPayment = bid.ElPayment
		}

		blocks = append(blocks, block)
	}

	return blocks
}

func buildBuilderRecentBids(ctx context.Context, builderIndex uint64, chainState *consensus.ChainState) []*models.BuilderPageDataBid {
	bids, _ := db.GetBidsByBuilderIndex(ctx, builderIndex, 0, 20)

	result := make([]*models.BuilderPageDataBid, 0, len(bids))
	for _, bid := range bids {
		bidData := &models.BuilderPageDataBid{
			Slot:         bid.Slot,
			Ts:           chainState.SlotToTime(phase0.Slot(bid.Slot)),
			ParentRoot:   bid.ParentRoot,
			ParentHash:   bid.ParentHash,
			BlockHash:    bid.BlockHash,
			FeeRecipient: bid.FeeRecipient,
			GasLimit:     bid.GasLimit,
			Value:        bid.Value,
			ElPayment:    bid.ElPayment,
			IsWinning:    false,
		}

		// Check if this bid won (payload was included)
		slots := db.GetSlotsByBlockHash(ctx, bid.BlockHash)
		for _, slot := range slots {
			if slot.PayloadStatus == dbtypes.PayloadStatusCanonical {
				bidData.IsWinning = true
				break
			}
		}

		result = append(result, bidData)
	}

	return result
}

func buildBuilderRecentDeposits(ctx context.Context, builderIndex uint64, chainState *consensus.ChainState) []*models.BuilderPageDataDeposit {
	// Builder exits are tracked as voluntary exits with BuilderIndexFlag set
	builderIndexWithFlag := builderIndex | services.BuilderIndexFlag
	filter := &dbtypes.VoluntaryExitFilter{
		MinIndex: builderIndexWithFlag,
		MaxIndex: builderIndexWithFlag,
	}

	exits, _ := services.GlobalBeaconService.GetVoluntaryExitsByFilter(ctx, filter, 0, 20)

	result := make([]*models.BuilderPageDataDeposit, 0, len(exits))
	for _, exit := range exits {
		result = append(result, &models.BuilderPageDataDeposit{
			Type:       "exit",
			SlotNumber: exit.SlotNumber,
			SlotRoot:   exit.SlotRoot,
			Time:       chainState.SlotToTime(phase0.Slot(exit.SlotNumber)),
			Orphaned:   exit.Orphaned,
		})
	}

	return result
}
