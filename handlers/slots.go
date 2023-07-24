package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/pk910/light-beaconchain-explorer/types/models"
	"github.com/pk910/light-beaconchain-explorer/utils"
	"github.com/sirupsen/logrus"
)

// Slots will return the main "slots" page using a go template
func Slots(w http.ResponseWriter, r *http.Request) {
	var slotsTemplateFiles = append(layoutTemplateFiles,
		"slots/slots.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(slotsTemplateFiles...)

	w.Header().Set("Content-Type", "text/html")
	data := InitPageData(w, r, "blockchain", "/slots", "Slots", slotsTemplateFiles)

	urlArgs := r.URL.Query()
	var firstSlot uint64 = 0
	if urlArgs.Has("slot") {
		firstSlot, _ = strconv.ParseUint(urlArgs.Get("slot"), 10, 64)
	}
	var pageSize uint64 = 50
	if urlArgs.Has("count") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("count"), 10, 64)
	}
	pageData := getSlotsPageData(firstSlot, pageSize)

	logrus.Printf("slots page called")
	data.Data = pageData

	if handleTemplateError(w, r, "slots.go", "Slots", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getSlotsPageData(firstSlot uint64, pageSize uint64) *models.SlotsPageData {
	pageData := &models.SlotsPageData{}

	now := time.Now()
	currentSlot := utils.TimeToSlot(uint64(now.Unix()))
	currentEpoch := utils.EpochOfSlot(currentSlot)
	maxSlot := currentSlot + 8
	if maxSlot >= (currentEpoch+1)*utils.Config.Chain.Config.SlotsPerEpoch {
		maxSlot = ((currentEpoch + 1) * utils.Config.Chain.Config.SlotsPerEpoch) - 1
	}
	if firstSlot == 0 || firstSlot > uint64(maxSlot) {
		pageData.IsDefaultPage = true
		firstSlot = uint64(maxSlot)
	}

	if pageSize > 100 {
		pageSize = 100
	}
	pageData.TotalPages = (maxSlot / pageSize)
	if (maxSlot % pageSize) > 0 {
		pageData.TotalPages++
	}
	pageData.PageSize = pageSize
	slotOffset := maxSlot - firstSlot
	pageData.CurrentPageIndex = (slotOffset / pageSize) + 1
	if (slotOffset % pageSize) > 0 {
		pageData.CurrentPageIndex++
	}
	pageData.CurrentPageSlot = firstSlot
	pageData.PrevPageIndex = pageData.CurrentPageIndex - 1
	pageData.PrevPageSlot = pageData.CurrentPageSlot + pageSize
	if pageData.CurrentPageSlot > pageSize {
		pageData.NextPageIndex = pageData.CurrentPageIndex + 1
		pageData.NextPageSlot = pageData.CurrentPageSlot - pageSize
	}

	finalizedHead, _ := services.GlobalBeaconService.GetFinalizedBlockHead()
	currentEpochStats := services.GlobalBeaconService.GetCachedEpochStats(currentEpoch)
	slotLimit := pageSize
	var lastSlot uint64
	if firstSlot > uint64(slotLimit) {
		lastSlot = firstSlot - uint64(slotLimit)
	} else {
		lastSlot = 0
	}

	// get affected epochs
	firstEpoch := utils.EpochOfSlot(firstSlot)
	lastEpoch := utils.EpochOfSlot(lastSlot)
	dbEpochs := services.GlobalBeaconService.GetDbEpochs(firstEpoch, uint32(firstEpoch-lastEpoch+1))
	dbEpochMap := make(map[uint64]*dbtypes.Epoch)
	for i := 0; i < len(dbEpochs); i++ {
		if dbEpochs[i] != nil {
			dbEpochMap[dbEpochs[i].Epoch] = dbEpochs[i]
		}
	}
	missingDuties := make(map[uint64]map[uint8]uint64)

	// load slots
	pageData.Slots = make([]*models.SlotsPageDataSlot, 0)
	dbSlots := services.GlobalBeaconService.GetDbBlocksForSlots(uint64(firstSlot), uint32(slotLimit), true)
	dbIdx := 0
	dbCnt := len(dbSlots)
	blockCount := uint64(0)
	for slotIdx := int64(firstSlot); slotIdx >= int64(lastSlot); slotIdx-- {
		slot := uint64(slotIdx)
		finalized := false
		if finalizedHead != nil && uint64(finalizedHead.Data.Header.Message.Slot) >= slot {
			finalized = true
		}
		haveBlock := false
		for dbIdx < dbCnt && dbSlots[dbIdx] != nil && dbSlots[dbIdx].Slot == slot {
			dbSlot := dbSlots[dbIdx]
			dbIdx++
			blockStatus := uint8(1)
			if dbSlot.Orphaned {
				blockStatus = 2
			}

			slotData := &models.SlotsPageDataSlot{
				Slot:                  slot,
				Epoch:                 utils.EpochOfSlot(slot),
				Ts:                    utils.SlotToTime(slot),
				Finalized:             finalized,
				Status:                blockStatus,
				Synchronized:          true,
				Proposer:              dbSlot.Proposer,
				ProposerName:          "", // TODO
				AttestationCount:      dbSlot.AttestationCount,
				DepositCount:          dbSlot.DepositCount,
				ExitCount:             dbSlot.ExitCount,
				ProposerSlashingCount: dbSlot.ProposerSlashingCount,
				AttesterSlashingCount: dbSlot.AttesterSlashingCount,
				SyncParticipation:     float64(dbSlot.SyncParticipation),
				EthTransactionCount:   dbSlot.EthTransactionCount,
				EthBlockNumber:        dbSlot.EthBlockNumber,
				Graffiti:              dbSlot.Graffiti,
				BlockRoot:             dbSlot.Root,
			}
			pageData.Slots = append(pageData.Slots, slotData)
			blockCount++
			haveBlock = true
		}

		if !haveBlock {
			epoch := utils.EpochOfSlot(slot)
			slotIdx := slot % utils.Config.Chain.Config.SlotsPerEpoch
			slotData := &models.SlotsPageDataSlot{
				Slot:      slot,
				Epoch:     epoch,
				Ts:        utils.SlotToTime(slot),
				Finalized: finalized,
				Scheduled: epoch >= currentEpoch,
				Status:    0,
			}

			dbEpoch := dbEpochMap[epoch]
			if dbEpoch != nil {
				slotData.Synchronized = true
			}
			if epoch == currentEpoch && currentEpochStats != nil {
				if currentEpochStats.Assignments == nil {
					slotData.Synchronized = false
				} else {
					slotData.Proposer = currentEpochStats.Assignments.ProposerAssignments[slot]
				}
			} else {
				missingEpochDuties := missingDuties[epoch]
				if missingEpochDuties == nil && dbEpoch != nil {
					missingEpochDuties = utils.BytesToMissingDuties(dbEpoch.MissingDuties)
					missingDuties[epoch] = missingEpochDuties
				}
				if missingEpochDuties != nil {
					slotData.Proposer = missingEpochDuties[uint8(slotIdx)]
				}
			}
			slotData.ProposerName = "" // TODO

			pageData.Slots = append(pageData.Slots, slotData)
			blockCount++
		}
	}
	pageData.SlotCount = uint64(blockCount)
	pageData.FirstSlot = firstSlot
	pageData.LastSlot = lastSlot

	return pageData
}
