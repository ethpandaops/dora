package handlers

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
)

// Epoch will return the main "epoch" page using a go template
func Epoch(w http.ResponseWriter, r *http.Request) {
	var epochTemplateFiles = append(layoutTemplateFiles,
		"epoch/epoch.html",
	)
	var notfoundTemplateFiles = append(layoutTemplateFiles,
		"epoch/notfound.html",
	)
	var pageTemplate = templates.GetTemplate(epochTemplateFiles...)

	var pageData *models.EpochPageData
	var epoch uint64
	vars := mux.Vars(r)

	if vars["epoch"] != "" {
		epoch, _ = strconv.ParseUint(vars["epoch"], 10, 64)
	} else {
		chainState := services.GlobalBeaconService.GetChainState()
		epoch = uint64(chainState.CurrentEpoch())
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		pageData, pageError = getEpochPageData(epoch)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	if pageData == nil {
		data := InitPageData(w, r, "blockchain", "/epoch", fmt.Sprintf("Epoch %v", epoch), notfoundTemplateFiles)
		w.Header().Set("Content-Type", "text/html")
		if handleTemplateError(w, r, "slot.go", "Slot", "blockSlot", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
			return // an error has occurred and was processed
		}
		return
	}

	data := InitPageData(w, r, "blockchain", "/epoch", fmt.Sprintf("Epoch %v", epoch), epochTemplateFiles)
	data.Data = pageData
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "epoch.go", "Epoch", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

// clampPercent bounds a bar-segment percentage to the [0, 100] range so rounding noise or
// next-epoch vote inclusion can't produce a negative or overflowing segment width.
func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func getEpochPageData(epoch uint64) (*models.EpochPageData, error) {
	pageData := &models.EpochPageData{}
	pageCacheKey := fmt.Sprintf("epoch:%v", epoch)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildEpochPageData(pageCall.CallCtx, epoch)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.EpochPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildEpochPageData(ctx context.Context, epoch uint64) (*models.EpochPageData, time.Duration) {
	logrus.Debugf("epoch page called: %v", epoch)

	beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	currentSlot := chainState.CurrentSlot()
	currentEpoch := chainState.EpochOfSlot(currentSlot)

	// An epoch beyond the current one has no indexed data yet. It must still render
	// (never 404) so the prev/next header navigation keeps working when paging
	// through epochs by range - we show a scheduled view with a time estimate and,
	// when the proposer lookahead already covers it, the assigned proposers.
	isFuture := phase0.Epoch(epoch) > currentEpoch

	processedEpoch, _ := beaconIndexer.GetBlockCacheState()
	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	epochStats := beaconIndexer.GetEpochStats(phase0.Epoch(epoch), nil)

	// Proposer duties for the lookahead window are available from the epoch stats and
	// are used to fill in scheduled-slot proposers for upcoming epochs.
	var epochStatsValues *beacon.EpochStatsValues
	if epochStats != nil {
		epochStatsValues = epochStats.GetOrLoadValues(ctx, beaconIndexer, true, false)
	}

	syncedEpoch := epochStats != nil
	if !syncedEpoch && epoch < uint64(processedEpoch) {
		syncedEpoch = db.IsEpochSynchronized(ctx, epoch)
	}

	// Forward navigation is unbounded (mirrors the slot page) so users can page into
	// upcoming epochs; the previous-epoch link is suppressed only at genesis.
	nextEpoch := epoch + 1
	firstSlot := chainState.EpochToSlot(phase0.Epoch(epoch))
	lastSlot := chainState.EpochToSlot(phase0.Epoch(epoch+1)) - 1
	pageData := &models.EpochPageData{
		Epoch:         epoch,
		PreviousEpoch: epoch - 1,
		NextEpoch:     nextEpoch,
		Ts:            chainState.SlotToTime(chainState.EpochToSlot(phase0.Epoch(epoch))),
		Synchronized:  syncedEpoch,
		Finalized:     finalizedEpoch > 0 && finalizedEpoch >= phase0.Epoch(epoch),
		Future:        isFuture,
	}

	dbEpochs := services.GlobalBeaconService.GetDbEpochs(ctx, epoch, 1)
	dbEpoch := dbEpochs[0]
	if dbEpoch != nil {
		pageData.AttestationCount = dbEpoch.AttestationCount
		pageData.DepositCount = dbEpoch.DepositCount
		pageData.ExitCount = dbEpoch.ExitCount
		pageData.WithdrawalCount = dbEpoch.WithdrawCount
		pageData.WithdrawalAmount = dbEpoch.WithdrawAmount
		pageData.ProposerSlashingCount = dbEpoch.ProposerSlashingCount
		pageData.AttesterSlashingCount = dbEpoch.AttesterSlashingCount
		pageData.EligibleEther = dbEpoch.Eligible
		pageData.TargetVoted = dbEpoch.VotedTarget
		pageData.TargetVotedSlashed = dbEpoch.VotedTargetSlashed
		pageData.HeadVoted = dbEpoch.VotedHead
		pageData.TotalVoted = dbEpoch.VotedTotal
		pageData.SyncParticipation = float64(dbEpoch.SyncParticipation) * 100
		pageData.ValidatorCount = dbEpoch.ValidatorCount
		pageData.EthTransactionCount = dbEpoch.EthTransactionCount
		pageData.BlobCount = dbEpoch.BlobCount
		if dbEpoch.ValidatorCount > 0 {
			pageData.AverageValidatorBalance = dbEpoch.ValidatorBalance / dbEpoch.ValidatorCount
		} else {
			pageData.Synchronized = false
		}
		if dbEpoch.Eligible > 0 {
			eligible := float64(dbEpoch.Eligible)
			pageData.TargetVoteParticipation = float64(dbEpoch.VotedTarget) * 100.0 / eligible
			pageData.TargetVoteSlashedParticipation = float64(dbEpoch.VotedTargetSlashed) * 100.0 / eligible
			pageData.HeadVoteParticipation = float64(dbEpoch.VotedHead) * 100.0 / eligible
			pageData.TotalVoteParticipation = float64(dbEpoch.VotedTotal) * 100.0 / eligible

			// derive the remaining bar segments, clamping to avoid tiny negatives from rounding /
			// next-epoch vote inclusion.
			pageData.TargetVoteWrongParticipation = clampPercent(pageData.TotalVoteParticipation - pageData.TargetVoteParticipation - pageData.TargetVoteSlashedParticipation)
			pageData.HeadVoteWrongParticipation = clampPercent(pageData.TotalVoteParticipation - pageData.HeadVoteParticipation)
			pageData.MissingParticipation = clampPercent(100.0 - pageData.TotalVoteParticipation)
		}
		pageData.FinalityThreshold = 100.0 * 2.0 / 3.0
	}

	// load slots
	pageData.Slots = make([]*models.EpochPageDataSlot, 0)
	dbSlots := services.GlobalBeaconService.GetDbBlocksForSlots(ctx, uint64(lastSlot), uint32(specs.SlotsPerEpoch), true, true)
	dbIdx := 0
	dbCnt := len(dbSlots)
	blockCount := uint64(0)
	for slotIdx := int64(lastSlot); slotIdx >= int64(firstSlot); slotIdx-- {
		slot := uint64(slotIdx)
		matched := false
		for dbIdx < dbCnt && dbSlots[dbIdx] != nil && dbSlots[dbIdx].Slot == slot {
			matched = true
			dbSlot := dbSlots[dbIdx]
			dbIdx++

			switch dbSlot.Status {
			case dbtypes.Orphaned:
				pageData.OrphanedCount++
			case dbtypes.Canonical:
				pageData.CanonicalCount++
			case dbtypes.Missing:
				pageData.MissedCount++
			}

			payloadStatus := dbSlot.PayloadStatus
			if !chainState.IsEip7732Enabled(phase0.Epoch(epoch)) {
				payloadStatus = dbtypes.PayloadStatusCanonical
			}

			slotData := &models.EpochPageDataSlot{
				Slot:                  slot,
				Epoch:                 uint64(chainState.EpochOfSlot(phase0.Slot(slot))),
				Ts:                    chainState.SlotToTime(phase0.Slot(slot)),
				Scheduled:             slot >= uint64(currentSlot) && dbSlot.Status == dbtypes.Missing,
				Status:                uint8(dbSlot.Status),
				PayloadStatus:         uint8(payloadStatus),
				Proposer:              dbSlot.Proposer,
				ProposerName:          services.GlobalBeaconService.GetValidatorNameAt(dbSlot.Proposer, phase0.Slot(slot)),
				AttestationCount:      dbSlot.AttestationCount,
				DepositCount:          dbSlot.DepositCount,
				ExitCount:             dbSlot.ExitCount,
				ProposerSlashingCount: dbSlot.ProposerSlashingCount,
				AttesterSlashingCount: dbSlot.AttesterSlashingCount,
				SyncParticipation:     float64(dbSlot.SyncParticipation) * 100,
				EthTransactionCount:   dbSlot.EthTransactionCount,
				BlobCount:             dbSlot.BlobCount,
				Graffiti:              dbSlot.Graffiti,
				BlockRoot:             dbSlot.Root,
			}
			if dbSlot.EthBlockNumber != nil {
				slotData.WithEthBlock = true
				slotData.EthBlockNumber = *dbSlot.EthBlockNumber
			}
			if slotData.Scheduled {
				pageData.ScheduledCount++
				pageData.MissedCount--
			}
			pageData.Slots = append(pageData.Slots, slotData)
			blockCount++
		}

		// Synthesize scheduled rows for upcoming slots that have no indexed entry yet
		// (future epochs beyond the proposer-assignment range). Proposers are filled in
		// from the lookahead when available, otherwise rendered as unknown.
		if !matched && slot >= uint64(currentSlot) {
			proposer := uint64(math.MaxInt64)
			if epochStatsValues != nil {
				if slotIndex := int(chainState.SlotToSlotIndex(phase0.Slot(slot))); slotIndex < len(epochStatsValues.ProposerDuties) {
					proposer = uint64(epochStatsValues.ProposerDuties[slotIndex])
				}
			}

			pageData.Slots = append(pageData.Slots, &models.EpochPageDataSlot{
				Slot:          slot,
				Epoch:         uint64(chainState.EpochOfSlot(phase0.Slot(slot))),
				Ts:            chainState.SlotToTime(phase0.Slot(slot)),
				Scheduled:     true,
				Status:        uint8(dbtypes.Missing),
				PayloadStatus: uint8(dbtypes.PayloadStatusCanonical),
				Proposer:      proposer,
				ProposerName:  services.GlobalBeaconService.GetValidatorName(proposer),
			})
			pageData.ScheduledCount++
			blockCount++
		}
	}
	pageData.BlockCount = uint64(blockCount)

	var cacheTimeout time.Duration
	switch {
	case isFuture:
		// Refresh as the epoch approaches so the proposer lookahead appears once known,
		// but don't rebuild constantly for epochs that are still far out.
		if timeDiff := time.Until(pageData.Ts); timeDiff > 10*time.Minute {
			cacheTimeout = 10 * time.Minute
		} else if timeDiff > 12*time.Second {
			cacheTimeout = timeDiff
		} else {
			cacheTimeout = 12 * time.Second
		}
	case !pageData.Synchronized:
		cacheTimeout = 5 * time.Minute
	case pageData.Finalized:
		cacheTimeout = 30 * time.Minute
	default:
		cacheTimeout = 12 * time.Second
	}
	return pageData, cacheTimeout
}
