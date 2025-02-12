package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
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

func getEpochPageData(epoch uint64) (*models.EpochPageData, error) {
	pageData := &models.EpochPageData{}
	pageCacheKey := fmt.Sprintf("epoch:%v", epoch)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildEpochPageData(epoch)
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

func buildEpochPageData(epoch uint64) (*models.EpochPageData, time.Duration) {
	logrus.Debugf("epoch page called: %v", epoch)

	beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	currentSlot := chainState.CurrentSlot()
	currentEpoch := chainState.EpochOfSlot(currentSlot)
	if epoch > uint64(currentEpoch) {
		return nil, -1
	}

	processedEpoch, _ := beaconIndexer.GetBlockCacheState()
	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	epochStats := beaconIndexer.GetEpochStats(phase0.Epoch(epoch), nil)

	syncedEpoch := epochStats != nil
	if !syncedEpoch && epoch < uint64(processedEpoch) {
		syncedEpoch = db.IsEpochSynchronized(epoch)
	}

	nextEpoch := epoch + 1
	if nextEpoch > uint64(currentEpoch) {
		nextEpoch = 0
	}
	firstSlot := chainState.EpochToSlot(phase0.Epoch(epoch))
	lastSlot := chainState.EpochToSlot(phase0.Epoch(epoch+1)) - 1
	pageData := &models.EpochPageData{
		Epoch:         epoch,
		PreviousEpoch: epoch - 1,
		NextEpoch:     nextEpoch,
		Ts:            chainState.SlotToTime(chainState.EpochToSlot(phase0.Epoch(epoch))),
		Synchronized:  syncedEpoch,
		Finalized:     finalizedEpoch > phase0.Epoch(epoch),
	}

	dbEpochs := services.GlobalBeaconService.GetDbEpochs(epoch, 1)
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
		pageData.HeadVoted = dbEpoch.VotedHead
		pageData.TotalVoted = dbEpoch.VotedTotal
		pageData.SyncParticipation = float64(dbEpoch.SyncParticipation) * 100
		pageData.ValidatorCount = dbEpoch.ValidatorCount
		if dbEpoch.ValidatorCount > 0 {
			pageData.AverageValidatorBalance = dbEpoch.ValidatorBalance / dbEpoch.ValidatorCount
		} else {
			pageData.Synchronized = false
		}
		if dbEpoch.Eligible > 0 {
			pageData.TargetVoteParticipation = float64(dbEpoch.VotedTarget) * 100.0 / float64(dbEpoch.Eligible)
			pageData.HeadVoteParticipation = float64(dbEpoch.VotedHead) * 100.0 / float64(dbEpoch.Eligible)
			pageData.TotalVoteParticipation = float64(dbEpoch.VotedTotal) * 100.0 / float64(dbEpoch.Eligible)
		}
	}

	// load slots
	pageData.Slots = make([]*models.EpochPageDataSlot, 0)
	dbSlots := services.GlobalBeaconService.GetDbBlocksForSlots(uint64(lastSlot), uint32(specs.SlotsPerEpoch), true, true)
	dbIdx := 0
	dbCnt := len(dbSlots)
	blockCount := uint64(0)
	for slotIdx := int64(lastSlot); slotIdx >= int64(firstSlot); slotIdx-- {
		slot := uint64(slotIdx)
		for dbIdx < dbCnt && dbSlots[dbIdx] != nil && dbSlots[dbIdx].Slot == slot {
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
				ProposerName:          services.GlobalBeaconService.GetValidatorName(dbSlot.Proposer),
				AttestationCount:      dbSlot.AttestationCount,
				DepositCount:          dbSlot.DepositCount,
				ExitCount:             dbSlot.ExitCount,
				ProposerSlashingCount: dbSlot.ProposerSlashingCount,
				AttesterSlashingCount: dbSlot.AttesterSlashingCount,
				SyncParticipation:     float64(dbSlot.SyncParticipation) * 100,
				EthTransactionCount:   dbSlot.EthTransactionCount,
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
	}
	pageData.BlockCount = uint64(blockCount)

	var cacheTimeout time.Duration
	if !pageData.Synchronized {
		cacheTimeout = 5 * time.Minute
	} else if pageData.Finalized {
		cacheTimeout = 30 * time.Minute
	} else {
		cacheTimeout = 12 * time.Second
	}
	return pageData, cacheTimeout
}
