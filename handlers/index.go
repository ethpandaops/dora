package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/pk910/light-beaconchain-explorer/types/models"
	"github.com/pk910/light-beaconchain-explorer/utils"
	"github.com/sirupsen/logrus"
)

// Index will return the main "index" page using a go template
func Index(w http.ResponseWriter, r *http.Request) {
	var indexTemplateFiles = append(layoutTemplateFiles,
		"index/index.html",
		"index/recentBlocks.html",
		"index/recentEpochs.html",
		"_svg/professor.html",
		"_svg/timeline.html",
	)

	var indexTemplate = templates.GetTemplate(indexTemplateFiles...)

	w.Header().Set("Content-Type", "text/html")
	data := InitPageData(w, r, "index", "", "", indexTemplateFiles)
	data.Data = getIndexPageData()

	if handleTemplateError(w, r, "index.go", "Index", "", indexTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getIndexPageData() *models.IndexPageData {
	pageData := &models.IndexPageData{}
	pageCacheKey := fmt.Sprintf("index")
	if services.GlobalBeaconService.GetFrontendCache(pageCacheKey, pageData) == nil {
		logrus.Printf("index page served from cache")
		return pageData
	}
	logrus.Printf("index page called")

	now := time.Now()
	currentEpoch := utils.TimeToEpoch(now)
	if currentEpoch < 0 {
		currentEpoch = 0
	}
	currentSlot := utils.TimeToSlot(uint64(now.Unix()))
	if currentSlot < 0 {
		currentSlot = 0
	}

	finalizedHead, _ := services.GlobalBeaconService.GetFinalizedBlockHead()
	recentEpochCount := 15
	recentBlockCount := 15

	// load recent epochs
	pageData.RecentEpochs = make([]*models.IndexPageDataEpochs, 0)
	epochsData := services.GlobalBeaconService.GetDbEpochs(uint64(currentEpoch), uint32(recentEpochCount))
	for i := 0; i < len(epochsData); i++ {
		epochData := epochsData[i]
		if epochData == nil {
			continue
		}
		finalized := false
		if finalizedHead != nil && uint64(finalizedHead.Data.Header.Message.Slot) >= epochData.Epoch*utils.Config.Chain.Config.SlotsPerEpoch {
			finalized = true
		}
		voteParticipation := float64(1)
		if epochData.Eligible > 0 {
			voteParticipation = float64(epochData.VotedTarget) * 100.0 / float64(epochData.Eligible)
		}
		pageData.RecentEpochs = append(pageData.RecentEpochs, &models.IndexPageDataEpochs{
			Epoch:             epochData.Epoch,
			Ts:                utils.EpochToTime(epochData.Epoch),
			Finalized:         finalized,
			EligibleEther:     epochData.Eligible,
			TargetVoted:       epochData.VotedTarget,
			HeadVoted:         epochData.VotedHead,
			TotalVoted:        epochData.VotedTotal,
			VoteParticipation: voteParticipation,
		})
	}
	pageData.RecentEpochCount = uint64(len(pageData.RecentEpochs))

	// load recent blocks
	pageData.RecentBlocks = make([]*models.IndexPageDataBlocks, 0)
	blocksData := services.GlobalBeaconService.GetDbBlocks(uint64(currentSlot), int32(recentBlockCount), false)
	for i := 0; i < len(blocksData); i++ {
		blockData := blocksData[i]
		blockStatus := 1
		if blockData.Orphaned {
			blockStatus = 2
		}
		pageData.RecentBlocks = append(pageData.RecentBlocks, &models.IndexPageDataBlocks{
			Epoch:        utils.EpochOfSlot(blockData.Slot),
			Slot:         blockData.Slot,
			EthBlock:     blockData.EthBlockNumber,
			Ts:           utils.SlotToTime(blockData.Slot),
			Proposer:     blockData.Proposer,
			ProposerName: services.GlobalBeaconService.GetValidatorName(blockData.Proposer),
			Status:       uint64(blockStatus),
			BlockRoot:    fmt.Sprintf("0x%x", blockData.Root),
		})
	}
	pageData.RecentBlockCount = uint64(len(pageData.RecentBlocks))

	if pageCacheKey != "" {
		services.GlobalBeaconService.SetFrontendCache(pageCacheKey, pageData, 12*time.Second)
	}
	return pageData
}
