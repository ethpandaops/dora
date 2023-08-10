package handlers

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/pk910/light-beaconchain-explorer/db"
	"github.com/pk910/light-beaconchain-explorer/dbtypes"
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
		"index/networkOverview.html",
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
	pageData = services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildIndexPageData()
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	}).(*models.IndexPageData)
	return pageData
}

func buildIndexPageData() (*models.IndexPageData, time.Duration) {
	logrus.Printf("index page called")

	recentEpochCount := 15
	recentBlockCount := 15

	// network overview
	now := time.Now()
	currentEpoch := utils.TimeToEpoch(now)
	if currentEpoch < 0 {
		currentEpoch = 0
	}
	currentSlot := utils.TimeToSlot(uint64(now.Unix()))
	if currentSlot < 0 {
		currentSlot = 0
	}
	currentSlotIndex := (currentSlot % utils.Config.Chain.Config.SlotsPerEpoch) + 1

	finalizedHead, _ := services.GlobalBeaconService.GetFinalizedBlockHead()
	var finalizedSlot uint64
	if finalizedHead != nil {
		finalizedSlot = uint64(finalizedHead.Data.Header.Message.Slot)
	} else {
		finalizedSlot = 0
	}

	syncState := dbtypes.IndexerSyncState{}
	db.GetExplorerState("indexer.syncstate", &syncState)
	var isSynced bool
	if currentEpoch > int64(utils.Config.Indexer.EpochProcessingDelay) {
		isSynced = syncState.Epoch >= uint64(currentEpoch-int64(utils.Config.Indexer.EpochProcessingDelay))
	} else {
		isSynced = true
	}

	pageData := &models.IndexPageData{
		NetworkName:           utils.Config.Chain.Name,
		DepositContract:       utils.Config.Chain.Config.DepositContractAddress,
		ShowSyncingMessage:    !isSynced,
		CurrentEpoch:          uint64(currentEpoch),
		CurrentFinalizedEpoch: utils.EpochOfSlot(finalizedSlot),
		CurrentSlot:           currentSlot,
		CurrentSlotIndex:      currentSlotIndex,
		CurrentScheduledCount: utils.Config.Chain.Config.SlotsPerEpoch - currentSlotIndex,
		CurrentEpochProgress:  float64(100) * float64(currentSlotIndex) / float64(utils.Config.Chain.Config.SlotsPerEpoch),
	}
	if utils.Config.Chain.DisplayName != "" {
		pageData.NetworkName = utils.Config.Chain.DisplayName
	}

	currentValidatorSet := services.GlobalBeaconService.GetCachedValidatorSet()
	if currentValidatorSet != nil {
		for _, validator := range currentValidatorSet.Data {
			if strings.HasPrefix(validator.Status, "active") {
				pageData.ActiveValidatorCount++
				pageData.TotalEligibleEther += uint64(validator.Validator.EffectiveBalance)
				pageData.AverageValidatorBalance += uint64(validator.Balance)
			}
			if validator.Status == "pending_queued" {
				pageData.EnteringValidatorCount++
			}
			if validator.Status == "active_exiting" {
				pageData.ExitingValidatorCount++
			}
		}
		if pageData.AverageValidatorBalance > 0 {
			pageData.AverageValidatorBalance = pageData.AverageValidatorBalance / pageData.ActiveValidatorCount
		}
	}
	pageData.ValidatorsPerEpoch = utils.GetValidatorChurnLimit(pageData.ActiveValidatorCount)
	pageData.ValidatorsPerDay = pageData.ValidatorsPerEpoch * 225
	depositQueueTime := float64(pageData.EnteringValidatorCount) / float64(pageData.ValidatorsPerDay)
	if depositQueueTime > 0 {
		depositQueueDays, depositQueueFractionalDays := math.Modf(depositQueueTime)
		depositQueueHours := int(depositQueueFractionalDays * 24)
		pageData.NewDepositProcessAfter = fmt.Sprintf("%d days and %d hours", int(depositQueueDays), depositQueueHours)
	}

	networkGenesis, _ := services.GlobalBeaconService.GetGenesis()
	if networkGenesis != nil {
		pageData.GenesisTime = time.Unix(int64(networkGenesis.Data.GenesisTime), 0)
		pageData.GenesisForkVersion = networkGenesis.Data.GenesisForkVersion
		pageData.GenesisValidatorsRoot = networkGenesis.Data.GenesisValidatorsRoot
	}

	pageData.NetworkForks = make([]*models.IndexPageDataForks, 0)
	if utils.Config.Chain.Config.AltairForkEpoch < uint64(18446744073709551615) && utils.Config.Chain.Config.AltairForkVersion != "" {
		pageData.NetworkForks = append(pageData.NetworkForks, &models.IndexPageDataForks{
			Name:    "Altair",
			Epoch:   utils.Config.Chain.Config.AltairForkEpoch,
			Version: utils.MustParseHex(utils.Config.Chain.Config.AltairForkVersion),
			Active:  uint64(currentEpoch) >= utils.Config.Chain.Config.AltairForkEpoch,
		})
	}
	if utils.Config.Chain.Config.BellatrixForkEpoch < uint64(18446744073709551615) && utils.Config.Chain.Config.BellatrixForkVersion != "" {
		pageData.NetworkForks = append(pageData.NetworkForks, &models.IndexPageDataForks{
			Name:    "Bellatrix",
			Epoch:   utils.Config.Chain.Config.BellatrixForkEpoch,
			Version: utils.MustParseHex(utils.Config.Chain.Config.BellatrixForkVersion),
			Active:  uint64(currentEpoch) >= utils.Config.Chain.Config.BellatrixForkEpoch,
		})
	}
	if utils.Config.Chain.Config.CappellaForkEpoch < uint64(18446744073709551615) && utils.Config.Chain.Config.CappellaForkVersion != "" {
		pageData.NetworkForks = append(pageData.NetworkForks, &models.IndexPageDataForks{
			Name:    "Cappella",
			Epoch:   utils.Config.Chain.Config.CappellaForkEpoch,
			Version: utils.MustParseHex(utils.Config.Chain.Config.CappellaForkVersion),
			Active:  uint64(currentEpoch) >= utils.Config.Chain.Config.CappellaForkEpoch,
		})
	}
	if utils.Config.Chain.Config.DenebForkEpoch < uint64(18446744073709551615) && utils.Config.Chain.Config.DenebForkVersion != "" {
		pageData.NetworkForks = append(pageData.NetworkForks, &models.IndexPageDataForks{
			Name:    "Deneb",
			Epoch:   utils.Config.Chain.Config.DenebForkEpoch,
			Version: utils.MustParseHex(utils.Config.Chain.Config.DenebForkVersion),
			Active:  uint64(currentEpoch) >= utils.Config.Chain.Config.DenebForkEpoch,
		})
	}

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
		if blockData == nil {
			continue
		}
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

	return pageData, 12 * time.Second
}
