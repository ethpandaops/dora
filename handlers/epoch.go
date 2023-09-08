package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/pk910/light-beaconchain-explorer/types/models"
	"github.com/pk910/light-beaconchain-explorer/utils"
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
	w.Header().Set("Content-Type", "text/html")

	var pageData *models.EpochPageData
	var epoch uint64
	vars := mux.Vars(r)

	if vars["epoch"] != "" {
		epoch, _ = strconv.ParseUint(vars["epoch"], 10, 64)
	} else {
		epoch = uint64(utils.TimeToEpoch(time.Now()))
	}

	pageData = getEpochPageData(epoch)
	if pageData == nil {
		data := InitPageData(w, r, "blockchain", "/epoch", fmt.Sprintf("Epoch %v", epoch), notfoundTemplateFiles)
		if handleTemplateError(w, r, "slot.go", "Slot", "blockSlot", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
			return // an error has occurred and was processed
		}
		return
	}

	data := InitPageData(w, r, "blockchain", "/epoch", fmt.Sprintf("Epoch %v", epoch), epochTemplateFiles)
	data.Data = pageData

	if handleTemplateError(w, r, "epoch.go", "Epoch", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getEpochPageData(epoch uint64) *models.EpochPageData {
	pageData := &models.EpochPageData{}
	pageCacheKey := fmt.Sprintf("epoch:%v", epoch)
	pageData = services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildEpochPageData(epoch)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	}).(*models.EpochPageData)
	return pageData
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
	dbSlots := services.GlobalBeaconService.GetDbBlocksForSlots(uint64(lastSlot), uint32(utils.Config.Chain.Config.SlotsPerEpoch), true)
	dbIdx := 0
	dbCnt := len(dbSlots)
	blockCount := uint64(0)
	for slotIdx := int64(lastSlot); slotIdx >= int64(firstSlot); slotIdx-- {
		slot := uint64(slotIdx)
		haveBlock := false
		for dbIdx < dbCnt && dbSlots[dbIdx] != nil && dbSlots[dbIdx].Slot == slot {
			dbSlot := dbSlots[dbIdx]
			dbIdx++
			blockStatus := uint8(1)
			if dbSlot.Orphaned == 1 {
				blockStatus = 2
				pageData.OrphanedCount++
			} else {
				pageData.CanonicalCount++
			}

			slotData := &models.EpochPageDataSlot{
				Slot:                  slot,
				Epoch:                 utils.EpochOfSlot(slot),
				Ts:                    utils.SlotToTime(slot),
				Status:                blockStatus,
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
			pageData.Slots = append(pageData.Slots, slotData)
			blockCount++
			haveBlock = true
		}

		if !haveBlock {
			slotData := &models.EpochPageDataSlot{
				Slot:         slot,
				Epoch:        epoch,
				Ts:           utils.SlotToTime(slot),
				Scheduled:    slot >= currentSlot,
				Status:       0,
				Proposer:     slotAssignments[slot],
				ProposerName: services.GlobalBeaconService.GetValidatorName(slotAssignments[slot]),
			}
			if slotData.Scheduled {
				pageData.ScheduledCount++
			} else {
				pageData.MissedCount++
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
