package handlers

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
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
		epoch = uint64(utils.TimeToEpoch(time.Now()))
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

	now := time.Now()
	currentSlot := utils.TimeToSlot(uint64(now.Unix()))
	currentEpoch := utils.EpochOfSlot(currentSlot)
	if epoch > currentEpoch {
		return nil, -1
	}

	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	slotAssignments, syncedEpochs := services.GlobalBeaconService.GetProposerAssignments(epoch, epoch)

	nextEpoch := epoch + 1
	if nextEpoch > currentEpoch {
		nextEpoch = 0
	}
	firstSlot := epoch * utils.Config.Chain.Config.SlotsPerEpoch
	lastSlot := firstSlot + utils.Config.Chain.Config.SlotsPerEpoch - 1
	pageData := &models.EpochPageData{
		Epoch:         epoch,
		PreviousEpoch: epoch - 1,
		NextEpoch:     nextEpoch,
		Ts:            utils.EpochToTime(epoch),
		Synchronized:  syncedEpochs[epoch],
		Finalized:     finalizedEpoch >= int64(epoch),
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
		}
		if dbEpoch.Eligible > 0 {
			pageData.TargetVoteParticipation = float64(dbEpoch.VotedTarget) * 100.0 / float64(dbEpoch.Eligible)
			pageData.HeadVoteParticipation = float64(dbEpoch.VotedHead) * 100.0 / float64(dbEpoch.Eligible)
			pageData.TotalVoteParticipation = float64(dbEpoch.VotedTotal) * 100.0 / float64(dbEpoch.Eligible)
		}
	}

	// load slots
	pageData.Slots = make([]*models.EpochPageDataSlot, 0)
	dbSlots := services.GlobalBeaconService.GetDbBlocksForSlots(uint64(lastSlot), uint32(utils.Config.Chain.Config.SlotsPerEpoch), true, true)
	blockCount := uint64(0)
	for _, dbSlot := range dbSlots {
		slot := uint64(dbSlot.Slot)

		if dbSlot.Status == 2 {
			pageData.OrphanedCount++
		} else {
			pageData.CanonicalCount++
		}

		proposer := dbSlot.Proposer
		if proposer == math.MaxInt64 {
			proposer = slotAssignments[slot]
		}

		slotData := &models.EpochPageDataSlot{
			Slot:                  slot,
			Epoch:                 utils.EpochOfSlot(slot),
			Ts:                    utils.SlotToTime(slot),
			Status:                uint8(dbSlot.Status),
			Proposer:              proposer,
			ProposerName:          services.GlobalBeaconService.GetValidatorName(proposer),
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
		pageData.Slots = append(pageData.Slots, slotData)
		blockCount++
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
