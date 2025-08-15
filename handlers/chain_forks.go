package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

const DefaultChainForksPageSize = 8640 // 24 hours at 12 seconds per slot

// ChainForks will return the chain forks visualization page using a go template
func ChainForks(w http.ResponseWriter, r *http.Request) {
	var chainForksTemplateFiles = append(layoutTemplateFiles,
		"chain_forks/chain_forks.html",
	)

	var pageTemplate = templates.GetTemplate(chainForksTemplateFiles...)
	data := InitPageData(w, r, "blockchain", "/chain-forks", "Chain Forks", chainForksTemplateFiles)

	// Parse start slot parameter
	var startSlot uint64 = 0
	if startSlotStr := r.URL.Query().Get("start"); startSlotStr != "" {
		if parsed, err := strconv.ParseUint(startSlotStr, 10, 64); err == nil {
			startSlot = parsed
		}
	}

	// Parse page size parameter (in epochs)
	var pageSizeEpochs uint64 = 0 // 0 means use default
	if pageSizeStr := r.URL.Query().Get("size"); pageSizeStr != "" {
		if parsed, err := strconv.ParseUint(pageSizeStr, 10, 64); err == nil && parsed > 0 && parsed <= 10000 {
			pageSizeEpochs = parsed
		}
	}

	// If no start slot specified, use recent head slot
	if startSlot == 0 {
		chainState := services.GlobalBeaconService.GetChainState()
		currentSlot := uint64(chainState.CurrentSlot())
		// Start from current slot and go backwards
		startSlot = currentSlot
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getChainForksPageData(startSlot, pageSizeEpochs)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		chainForksDataBytes, err := json.Marshal(data.Data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, err = w.Write(chainForksDataBytes)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error writing response: %v", err), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "chain_forks.go", "ChainForks", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getChainForksPageData(startSlot uint64, pageSizeEpochs uint64) (*models.ChainForksPageData, error) {
	pageData := &models.ChainForksPageData{}
	pageCacheKey := fmt.Sprintf("chain_forks_%d_%d", startSlot, pageSizeEpochs)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildChainForksPageData(startSlot, pageSizeEpochs)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ChainForksPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildChainForksPageData(startSlot uint64, pageSizeEpochs uint64) (*models.ChainForksPageData, time.Duration) {
	// Calculate slot ranges for time window
	// startSlot parameter = head slot (most recent, shown at top of visualization)
	// actualStartSlot = older slot (shown at bottom of visualization, goes back ~1000 slots)
	endSlot := startSlot                                             // Head slot (newest)
	actualStartSlot := calculateStartSlot(startSlot, pageSizeEpochs) // Goes back in time for time window

	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()

	// Debug: log time window calculation
	logrus.Infof("Chain forks: pageSizeEpochs=%d, slots %d-%d, epochs %d-%d",
		pageSizeEpochs, actualStartSlot, endSlot,
		uint64(chainState.EpochOfSlot(phase0.Slot(actualStartSlot))),
		uint64(chainState.EpochOfSlot(phase0.Slot(endSlot))))

	// Calculate epochs (225 epochs = 1 day)
	startEpoch := uint64(chainState.EpochOfSlot(phase0.Slot(actualStartSlot)))
	endEpoch := uint64(chainState.EpochOfSlot(phase0.Slot(endSlot)))

	// Calculate epoch counts for time selectors
	secondsPerEpoch := uint64(specs.SlotsPerEpoch) * uint64(specs.SecondsPerSlot.Seconds())
	epochsFor3h := uint64(3*3600) / secondsPerEpoch
	epochsFor12h := uint64(12*3600) / secondsPerEpoch
	epochsFor1d := uint64(24*3600) / secondsPerEpoch
	epochsFor7d := uint64(7*24*3600) / secondsPerEpoch

	// Initialize page data
	pageData := &models.ChainForksPageData{
		StartSlot:      actualStartSlot,
		EndSlot:        endSlot,
		StartEpoch:     startEpoch,
		EndEpoch:       endEpoch,
		PageSize:       DefaultChainForksPageSize,
		PageSizeEpochs: pageSizeEpochs,
		FinalitySlot:   uint64(chainState.GetFinalizedSlot()),
		ChainSpecs: &models.ChainSpecs{
			SlotsPerEpoch:  uint64(specs.SlotsPerEpoch),
			SecondsPerSlot: uint64(specs.SecondsPerSlot.Seconds()),
			EpochsFor3h:    epochsFor3h,
			EpochsFor12h:   epochsFor12h,
			EpochsFor1d:    epochsFor1d,
			EpochsFor7d:    epochsFor7d,
		},
	}
	cacheTime := specs.SecondsPerSlot * 12

	// Get fork data from database
	slotRange := endSlot - actualStartSlot
	dbForks, err := db.GetForkVisualizationData(actualStartSlot, slotRange)
	if err != nil {
		logrus.Errorf("Error fetching fork visualization data: %v", err)
		return pageData, cacheTime
	}

	// Process forks with epoch-based participation data
	indexer := services.GlobalBeaconService.GetBeaconIndexer()
	forks, err := processForksWithEpochData(dbForks, indexer, startEpoch, endEpoch)
	if err != nil {
		logrus.Errorf("Error processing forks with epoch-based participation data: %v", err)
		return pageData, 0
	}
	pageData.Forks = forks

	// Add canonical chain and mark canonical forks
	pageData.Forks = addCanonicalChain(pageData.Forks, indexer)

	// Sort forks by base slot for proper visualization
	sort.Slice(pageData.Forks, func(i, j int) bool {
		if pageData.Forks[i].BaseSlot == pageData.Forks[j].BaseSlot {
			return pageData.Forks[i].ForkId < pageData.Forks[j].ForkId
		}
		return pageData.Forks[i].BaseSlot < pageData.Forks[j].BaseSlot
	})

	// Build diagram data
	pageData.ChainDiagram = buildChainDiagram(pageData.Forks, startEpoch, endEpoch, indexer)

	// Set up pagination
	var pageSize uint64
	if pageSizeEpochs > 0 {
		pageSize = pageSizeEpochs * uint64(specs.SlotsPerEpoch)
	} else {
		pageSize = uint64(DefaultChainForksPageSize)
	}

	if startSlot >= pageSize {
		prevSlot := startSlot - pageSize
		pageData.PrevPageSlot = &prevSlot
	}

	finalizedSlot := uint64(chainState.GetFinalizedSlot())
	if startSlot+pageSize < finalizedSlot {
		nextSlot := startSlot + pageSize
		pageData.NextPageSlot = &nextSlot
	}

	return pageData, cacheTime
}

// calculateForkHead calculates fork head using pre-fetched blocks
func calculateForkHead(dbFork *dbtypes.Fork, hasHead bool, allForks []*dbtypes.Fork, forkBlocks []*beacon.Block) uint64 {
	if hasHead {
		// Fork has a head - find the latest slot from cached blocks or database
		latestSlot := dbFork.BaseSlot

		// Check cached blocks first
		for _, block := range forkBlocks {
			if uint64(block.Slot) > latestSlot && uint64(block.Slot) >= dbFork.BaseSlot {
				latestSlot = uint64(block.Slot)
			}
		}

		// Always check database for finalized blocks (especially important for forks that started before finalization)
		finalizedHead := getFinalizedForkHead(dbFork.ForkId, dbFork.BaseSlot)
		if finalizedHead > latestSlot {
			latestSlot = finalizedHead
		}

		return latestSlot
	}

	// Fork was superseded - find where it ended
	headSlot := dbFork.LeafSlot
	for _, childFork := range allForks {
		if childFork.ParentFork == dbFork.ForkId && childFork.BaseSlot > headSlot {
			headSlot = childFork.BaseSlot
		}
	}

	return headSlot
}

// getFinalizedForkHead finds the highest finalized slot for a given fork ID
func getFinalizedForkHead(forkId uint64, baseSlot uint64) uint64 {
	var slot uint64

	err := db.ReaderDb.Get(&slot, `
		SELECT slot 
		FROM slots 
		WHERE fork_id = $1 AND slot >= $2
		ORDER BY slot DESC
		LIMIT 1
	`, forkId, baseSlot)

	if err != nil {
		return baseSlot
	}

	return slot
}

func buildChainDiagram(forks []*models.ChainFork, startEpoch, endEpoch uint64, indexer *beacon.Indexer) *models.ChainDiagram {
	// Get specs for proper epoch to slot conversion
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	slotsPerEpoch := uint64(specs.SlotsPerEpoch)

	diagram := &models.ChainDiagram{
		Epochs: make([]uint64, 0),
		Forks:  make([]*models.DiagramFork, 0),
		CanonicalLine: &models.DiagramCanonicalLine{
			StartSlot: startEpoch * slotsPerEpoch, // Convert epoch to slot using actual spec
			EndSlot:   endEpoch * slotsPerEpoch,
		},
	}

	// Create epochs array (every 5 epochs for visualization stepping, starting from rounded value)
	firstEpoch := (startEpoch / 5) * 5 // Round down to nearest 5
	for epoch := firstEpoch; epoch <= endEpoch; epoch += 5 {
		diagram.Epochs = append(diagram.Epochs, epoch)
	}

	// Build fork tree structure - assign horizontal positions to avoid overlap
	forkMap := make(map[uint64]*models.ChainFork)
	for _, fork := range forks {
		forkMap[fork.ForkId] = fork
	}

	// Get canonical fork IDs from chain service for proper marking
	canonicalForkIds := services.GlobalBeaconService.GetCanonicalForkIds()
	canonicalForkIdSet := make(map[uint64]bool)
	for _, forkId := range canonicalForkIds {
		canonicalForkIdSet[forkId] = true
	}

	// Mark canonical forks for proper rendering - client will handle positioning
	for _, fork := range forks {
		if canonicalForkIdSet[fork.ForkId] || fork.ForkId == 0 {
			fork.IsCanonical = true
		}
	}

	// Create diagram forks - client will handle positioning
	for _, fork := range forks {
		diagramFork := &models.DiagramFork{
			ForkId:               fork.ForkId,
			BaseSlot:             fork.BaseSlot,
			BaseRoot:             fork.BaseRoot,
			LeafSlot:             fork.LeafSlot,
			LeafRoot:             fork.LeafRoot,
			HeadSlot:             fork.HeadSlot,
			HeadRoot:             fork.HeadRoot,
			Length:               fork.Length,
			BlockCount:           fork.BlockCount,
			Participation:        fork.Participation,
			ParticipationByEpoch: fork.ParticipationByEpoch,
			ParentFork:           fork.ParentFork,
			IsCanonical:          fork.IsCanonical,
		}
		diagram.Forks = append(diagram.Forks, diagramFork)
	}

	return diagram
}

// calculateStartSlot determines the actual start slot for the fork visualization
func calculateStartSlot(startSlot uint64, pageSizeEpochs uint64) uint64 {
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	slotsPerEpoch := uint64(specs.SlotsPerEpoch)

	var recentSlotWindow uint64
	if pageSizeEpochs > 0 {
		// Convert epochs to slots using actual chain specs
		recentSlotWindow = pageSizeEpochs * slotsPerEpoch
	} else {
		// Use default page size
		recentSlotWindow = uint64(DefaultChainForksPageSize)
	}

	// Debug logging
	logrus.Infof("calculateStartSlot: startSlot=%d, pageSizeEpochs=%d, slotsPerEpoch=%d, recentSlotWindow=%d",
		startSlot, pageSizeEpochs, slotsPerEpoch, recentSlotWindow)

	if startSlot > recentSlotWindow {
		result := startSlot - recentSlotWindow
		logrus.Infof("calculateStartSlot: returning %d (startSlot - recentSlotWindow)", result)
		return result
	}

	// If requested window is larger than available history, start from slot 0
	// but still respect the window size for the end slot calculation
	logrus.Infof("calculateStartSlot: returning 0 (requested window larger than available history)")
	return 0
}

// processForksWithEpochData converts database forks to page data with epoch-based participation
func processForksWithEpochData(dbForks []*dbtypes.Fork, indexer *beacon.Indexer, startEpoch, endEpoch uint64) ([]*models.ChainFork, error) {
	forks := make([]*models.ChainFork, 0, len(dbForks)+1)

	// Build child fork map
	childForkMap := make(map[uint64]bool)
	for _, dbFork := range dbForks {
		if dbFork.ParentFork != 0 {
			childForkMap[dbFork.ParentFork] = true
		}
	}

	// Get chain state for epoch/slot calculations
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	slotsPerEpoch := uint64(specs.SlotsPerEpoch)

	// Extract fork IDs for participation query, including canonical chain (fork ID 0)
	forkIds := make([]uint64, len(dbForks)+1)
	forkIds[0] = 0 // Always include canonical chain for finalized blocks
	for i, dbFork := range dbForks {
		forkIds[i+1] = dbFork.ForkId
	}

	// Fetch all fork blocks from cache once (optimization)
	forkBlocksCache := make(map[uint64][]*beacon.Block)
	for _, dbFork := range dbForks {
		forkBlocks := indexer.GetBlocksByForkId(beacon.ForkKey(dbFork.ForkId))
		forkBlocksCache[dbFork.ForkId] = forkBlocks
	}

	// Also fetch canonical chain blocks from cache (for unfinalized blocks)
	// Find the current canonical fork ID from the latest available fork's parent
	finalizedForkId := indexer.GetFinalizedForkId()
	if finalizedForkId != 0 {
		// Get blocks from the current canonical fork for unfinalized canonical chain
		canonicalBlocks := indexer.GetBlocksByForkId(finalizedForkId)
		if len(canonicalBlocks) > 0 {
			// Add these blocks as canonical chain (fork ID 0) for participation calculation
			forkBlocksCache[0] = canonicalBlocks
		}
	}

	// Get epoch boundaries for different data sources
	finalizedEpoch, prunedEpoch := indexer.GetBlockCacheState()

	// Build participation lookup map: forkId -> epoch -> participation
	participationMap := make(map[uint64]map[uint64]*models.EpochParticipation)

	// 1. Fetch finalized canonical epochs participation (epoch <= finalizedEpoch from epochs table)
	if uint64(finalizedEpoch) >= startEpoch {
		finalizedEndEpoch := endEpoch
		if uint64(finalizedEpoch) < endEpoch {
			finalizedEndEpoch = uint64(finalizedEpoch)
		}

		finalizedData, err := db.GetFinalizedEpochParticipation(startEpoch, finalizedEndEpoch)
		if err != nil {
			return nil, err
		}

		// Add finalized canonical epochs data (fork ID 0)
		if participationMap[0] == nil {
			participationMap[0] = make(map[uint64]*models.EpochParticipation)
		}
		for _, data := range finalizedData {
			participationMap[0][data.Epoch] = &models.EpochParticipation{
				Epoch:         data.Epoch,
				Participation: calculateParticipation(data.VotedTarget, data.Eligible),
				SlotCount:     data.BlockCount,
			}
		}
	}

	// 2. Fetch pruned epochs participation (prunedEpoch < epoch <= finalizedEpoch from unfinalized_epochs table)
	if uint64(prunedEpoch) < uint64(finalizedEpoch) && uint64(prunedEpoch) < endEpoch && startEpoch <= uint64(finalizedEpoch) {
		unfinalizedStartEpoch := startEpoch
		if uint64(prunedEpoch) > startEpoch {
			unfinalizedStartEpoch = uint64(prunedEpoch)
		}

		unfinalizedEndEpoch := endEpoch
		if uint64(finalizedEpoch) < endEpoch {
			unfinalizedEndEpoch = uint64(finalizedEpoch)
		}

		if unfinalizedStartEpoch <= unfinalizedEndEpoch {
			unfinalizedData, err := db.GetUnfinalizedEpochParticipation(unfinalizedStartEpoch, unfinalizedEndEpoch)
			if err != nil {
				return nil, err
			}

			// Add unfinalized epochs data
			for _, data := range unfinalizedData {
				if participationMap[data.HeadForkId] == nil {
					participationMap[data.HeadForkId] = make(map[uint64]*models.EpochParticipation)
				}
				participationMap[data.HeadForkId][data.Epoch] = &models.EpochParticipation{
					Epoch:         data.Epoch,
					Participation: calculateParticipation(data.VotedTarget, data.Eligible),
					SlotCount:     data.BlockCount,
				}
			}
		}
	}

	// 3. Fetch orphaned epoch participation data for non-canonical finalized epochs
	orphanedEpochData, err := db.GetOrphanedEpochParticipation(startEpoch, endEpoch)
	if err != nil {
		return nil, err
	}

	// Build orphaned epoch lookup map: forkId -> epoch -> participation
	orphanedEpochMap := make(map[uint64]map[uint64]*db.OrphanedEpochParticipation)
	for _, oData := range orphanedEpochData {
		if orphanedEpochMap[oData.HeadForkId] == nil {
			orphanedEpochMap[oData.HeadForkId] = make(map[uint64]*db.OrphanedEpochParticipation)
		}
		orphanedEpochMap[oData.HeadForkId][oData.Epoch] = oData

		// Also add to participationMap for easier access
		if participationMap[oData.HeadForkId] == nil {
			participationMap[oData.HeadForkId] = make(map[uint64]*models.EpochParticipation)
		}
		participationMap[oData.HeadForkId][oData.Epoch] = &models.EpochParticipation{
			Epoch:         oData.Epoch,
			Participation: calculateParticipation(oData.VotedTarget, oData.Eligible),
			SlotCount:     oData.BlockCount,
		}
	}

	// 4. Add participation from cached epochs (epoch > prunedEpoch)
	if uint64(prunedEpoch) < endEpoch {
		cacheStartEpoch := uint64(prunedEpoch)

		nextEpochBlocks := make([]*beacon.Block, 0)

		for epoch := cacheStartEpoch; epoch <= endEpoch; epoch++ {

			epochBlocks := make([]*beacon.Block, 0)
			if len(nextEpochBlocks) > 0 {
				epochBlocks = append(epochBlocks, nextEpochBlocks...)
			} else {
				for slot := chainState.EpochToSlot(phase0.Epoch(epoch)); slot < chainState.EpochToSlot(phase0.Epoch(epoch+1)); slot++ {
					epochBlocks = append(epochBlocks, indexer.GetBlocksBySlot(phase0.Slot(slot))...)
				}
			}

			nextEpochBlocks = nextEpochBlocks[:0]
			for slot := chainState.EpochToSlot(phase0.Epoch(epoch + 1)); slot < chainState.EpochToSlot(phase0.Epoch(epoch+2)); slot++ {
				nextEpochBlocks = append(nextEpochBlocks, indexer.GetBlocksBySlot(phase0.Slot(slot))...)
			}
			epochBlocks = append(epochBlocks, nextEpochBlocks...)

			if len(epochBlocks) == 0 {
				continue
			}

			forkIds := make([]beacon.ForkKey, 0)
			for _, block := range epochBlocks {
				forkId := block.GetForkId()
				found := false
				for _, id := range forkIds {
					if id == forkId {
						found = true
						break
					}
				}
				if !found {
					forkIds = append(forkIds, forkId)
				}
			}

			for _, forkId := range forkIds {
				epochStats := indexer.GetEpochStats(phase0.Epoch(epoch), (*beacon.ForkKey)(&forkId))
				if epochStats == nil {
					continue
				}

				var headBlock *beacon.Block
				blockCount := 0
				for _, block := range epochBlocks {
					if block.GetForkId() == forkId {
						if headBlock == nil || block.Slot > headBlock.Slot {
							headBlock = block
						}

						if chainState.EpochOfSlot(phase0.Slot(block.Slot)) == phase0.Epoch(epoch) {
							blockCount++
						}
					}
				}

				epochVotes := epochStats.GetEpochVotes(indexer, headBlock)
				if epochVotes != nil {
					epochStatsValues := epochStats.GetValues(true)
					if epochStatsValues != nil {
						votedTarget := uint64(epochVotes.CurrentEpoch.TargetVoteAmount + epochVotes.NextEpoch.TargetVoteAmount)
						eligible := uint64(epochStatsValues.EffectiveBalance)

						participation := calculateParticipation(votedTarget, eligible)

						if participationMap[uint64(forkId)] == nil {
							participationMap[uint64(forkId)] = make(map[uint64]*models.EpochParticipation)
						}
						participationMap[uint64(forkId)][epoch] = &models.EpochParticipation{
							Epoch:         epoch,
							Participation: participation,
							SlotCount:     uint64(blockCount),
						}
					}
				}
			}
		}
	}

	// Fetch block counts for all forks (finalized blocks) - just count by fork_id
	finalizedSlot := chainState.EpochToSlot(finalizedEpoch)
	blockCountStartSlot := chainState.EpochToSlot(phase0.Epoch(startEpoch))
	blockCountEndSlot := chainState.EpochToSlot(phase0.Epoch(endEpoch))
	if endEpoch > uint64(finalizedEpoch) {
		blockCountEndSlot = finalizedSlot
	}
	blockCounts, err := db.GetForkBlockCounts(uint64(blockCountStartSlot), uint64(blockCountEndSlot))
	if err != nil {
		return nil, err
	}

	// Process each fork
	for _, dbFork := range dbForks {
		hasHead := !childForkMap[dbFork.ForkId]

		// Calculate head slot using cached blocks
		headSlot := calculateForkHead(dbFork, hasHead, dbForks, forkBlocksCache[dbFork.ForkId])

		// Get epoch-based participation data for this fork
		epochParticipation := make([]*models.EpochParticipation, 0)
		var participationSum float64
		var participationCount int

		// Get participation data from participationMap (already includes all sources)
		if forkParticipation, exists := participationMap[dbFork.ForkId]; exists {
			for epoch := startEpoch; epoch <= endEpoch; epoch++ {
				if epochData, hasData := forkParticipation[epoch]; hasData {
					epochParticipation = append(epochParticipation, epochData)
					participationSum += epochData.Participation
					participationCount++
				}
			}
		}

		// Check for inherited orphaned epoch stats for epochs without direct data
		for epoch := startEpoch; epoch <= endEpoch; epoch++ {
			// Skip if we already have participation data for this epoch
			hasExistingData := false
			if forkParticipation, exists := participationMap[dbFork.ForkId]; exists {
				if _, hasData := forkParticipation[epoch]; hasData {
					hasExistingData = true
				}
			}

			if !hasExistingData {
				// If no direct match, check parent forks for inherited orphaned epoch stats
				if orphanedEpochMap[dbFork.ForkId] != nil {
					if _, hasDirectMatch := orphanedEpochMap[dbFork.ForkId][epoch]; !hasDirectMatch {
						// Walk up the fork tree to find orphaned epoch stats
						currentForkId := dbFork.ForkId
						for {
							// Find forks that build on this fork
							foundChildWithData := false
							for _, childFork := range dbForks {
								if childFork.ParentFork == currentForkId {
									if childOrphanedData, hasChildOrphaned := orphanedEpochMap[childFork.ForkId]; hasChildOrphaned {
										if orphanedData, hasEpoch := childOrphanedData[epoch]; hasEpoch {
											epochParticipation = append(epochParticipation, &models.EpochParticipation{
												Epoch:         epoch,
												Participation: calculateParticipation(orphanedData.VotedTarget, orphanedData.Eligible),
												SlotCount:     orphanedData.BlockCount,
											})
											participationSum += calculateParticipation(orphanedData.VotedTarget, orphanedData.Eligible)
											participationCount++
											foundChildWithData = true
											break
										}
									}
								}
							}

							if foundChildWithData {
								break
							}

							// Move to the next fork in the chain
							nextForkFound := false
							for _, fork := range dbForks {
								if fork.ParentFork == currentForkId {
									currentForkId = fork.ForkId
									nextForkFound = true
									break
								}
							}

							if !nextForkFound {
								break
							}
						}
					}
				}
			}
		}

		// Sort epoch participation by epoch
		sort.Slice(epochParticipation, func(i, j int) bool {
			return epochParticipation[i].Epoch < epochParticipation[j].Epoch
		})

		// Calculate average participation from epoch data
		var avgParticipation float64
		if participationCount > 0 {
			avgParticipation = participationSum / float64(participationCount)
		}

		// Calculate block count for this fork - blocks are already marked with correct fork_id
		blockCount := blockCounts[dbFork.ForkId] // From finalized blocks

		// Add blocks from cache (unfinalized) - all blocks with this fork_id
		if cachedBlocks, exists := forkBlocksCache[dbFork.ForkId]; exists {
			blockCount += uint64(len(cachedBlocks))
		}

		chainFork := &models.ChainFork{
			ForkId:               dbFork.ForkId,
			BaseSlot:             dbFork.BaseSlot,
			BaseRoot:             dbFork.BaseRoot,
			LeafSlot:             dbFork.LeafSlot,
			LeafRoot:             dbFork.LeafRoot,
			HeadSlot:             headSlot,
			HeadRoot:             nil,
			ParentFork:           dbFork.ParentFork,
			Participation:        avgParticipation,
			ParticipationByEpoch: epochParticipation,
			IsCanonical:          false,
			Length:               headSlot - dbFork.BaseSlot + 1,
			BlockCount:           blockCount,
		}
		forks = append(forks, chainFork)
	}

	if finalizedForkId != 0 {
		participationData, ok := participationMap[uint64(finalizedForkId)]
		if ok {
			for epoch, participation := range participationData {
				participationMap[0][epoch] = participation
			}

			delete(participationMap, uint64(finalizedForkId))
		}
	}

	// Also process canonical chain (fork ID 0) if we have participation data for it
	if canonicalParticipation, hasCanonical := participationMap[0]; hasCanonical {
		epochParticipation := make([]*models.EpochParticipation, 0)
		var participationSum float64
		var participationCount int

		for epoch := startEpoch; epoch <= endEpoch; epoch++ {
			if epochData, hasData := canonicalParticipation[epoch]; hasData {
				epochParticipation = append(epochParticipation, epochData)
				participationSum += epochData.Participation
				participationCount++
			}
		}

		var avgParticipation float64
		if participationCount > 0 {
			avgParticipation = participationSum / float64(participationCount)
		}

		var latestForkBuildingOnFinalizedFork *models.ChainFork = nil
		for _, fork := range forks {
			if (fork.ParentFork == uint64(finalizedForkId) || fork.ParentFork == 0) && (latestForkBuildingOnFinalizedFork == nil || fork.BaseSlot > latestForkBuildingOnFinalizedFork.BaseSlot) {
				latestForkBuildingOnFinalizedFork = fork
			}
		}

		var canonicalEndSlot uint64
		if latestForkBuildingOnFinalizedFork != nil && latestForkBuildingOnFinalizedFork.LeafSlot >= uint64(finalizedSlot) {
			canonicalEndSlot = latestForkBuildingOnFinalizedFork.BaseSlot
		} else if canonicalHead := indexer.GetCanonicalHead(nil); canonicalHead != nil {
			canonicalEndSlot = uint64(canonicalHead.Slot)
		} else {
			canonicalEndSlot = 0
		}

		// Calculate block count for canonical chain
		canonicalBlockCount := blockCounts[0] // From finalized blocks
		if finalizedForkId != 0 {
			canonicalBlockCount += blockCounts[uint64(finalizedForkId)]
		}

		// Add blocks from cache (unfinalized) - all blocks with fork_id = 0
		if canonicalCachedBlocks, exists := forkBlocksCache[0]; exists {
			canonicalBlockCount += uint64(len(canonicalCachedBlocks))
		}

		canonicalChain := &models.ChainFork{
			ForkId:               0,
			BaseSlot:             startEpoch * slotsPerEpoch,
			BaseRoot:             nil,
			LeafSlot:             startEpoch * slotsPerEpoch,
			LeafRoot:             nil,
			HeadSlot:             canonicalEndSlot,
			HeadRoot:             nil,
			ParentFork:           0,
			Participation:        avgParticipation,
			ParticipationByEpoch: epochParticipation,
			IsCanonical:          true,
			Length:               canonicalEndSlot - (startEpoch * slotsPerEpoch) + 1,
			BlockCount:           canonicalBlockCount,
		}
		forks = append(forks, canonicalChain)
	}

	return forks, nil
}

// calculateParticipation calculates voting participation percentage from raw voting data
func calculateParticipation(votedTarget, eligible uint64) float64 {
	if eligible == 0 {
		return 0
	}
	return (float64(votedTarget) / float64(eligible))
}

// addCanonicalChain adds canonical chain representation and marks canonical forks
func addCanonicalChain(forks []*models.ChainFork, indexer *beacon.Indexer) []*models.ChainFork {
	canonicalForkIds := services.GlobalBeaconService.GetCanonicalForkIds()
	canonicalForkIdSet := make(map[uint64]bool)
	for _, forkId := range canonicalForkIds {
		canonicalForkIdSet[forkId] = true
	}

	// Mark canonical forks
	canonicalHead := indexer.GetCanonicalHead(nil)
	var currentCanonicalForkId uint64 = 0
	if canonicalHead != nil {
		currentCanonicalForkId = uint64(canonicalHead.GetForkId())
	}

	for _, fork := range forks {
		if canonicalForkIdSet[fork.ForkId] || fork.ForkId == 0 {
			if fork.ForkId == 0 || fork.ForkId == currentCanonicalForkId {
				fork.IsCanonical = true
			}
		}
	}

	// Check if we already have a canonical chain (fork ID 0) with participation data
	hasCanonicalChain := false
	for _, fork := range forks {
		if fork.ForkId == 0 {
			hasCanonicalChain = true
			break
		}
	}

	// Only create fake canonical chain if we don't already have real one
	if !hasCanonicalChain {
		// Find orphan forks and add canonical chain if needed
		existingForkIds := make(map[uint64]bool)
		for _, fork := range forks {
			existingForkIds[fork.ForkId] = true
		}

		var earliestOrphanFork *models.ChainFork = nil
		for _, fork := range forks {
			if fork.ForkId != 1 && fork.ParentFork != 0 && !existingForkIds[fork.ParentFork] {
				if earliestOrphanFork == nil || fork.BaseSlot < earliestOrphanFork.BaseSlot {
					earliestOrphanFork = fork
				}
			}
		}

		if canonicalHead != nil && earliestOrphanFork != nil {
			canonicalChain := createCanonicalChainFork(forks, canonicalForkIdSet, earliestOrphanFork)

			// Update orphan forks to connect to canonical chain
			for _, fork := range forks {
				if fork.ForkId != 1 && fork.ParentFork != 0 && !existingForkIds[fork.ParentFork] {
					fork.ParentFork = 0 // Connect to canonical chain
				}
			}

			forks = append(forks, canonicalChain)
		}
	} else {
		// We have a real canonical chain, just update orphan forks to connect to it
		existingForkIds := make(map[uint64]bool)
		for _, fork := range forks {
			existingForkIds[fork.ForkId] = true
		}

		for _, fork := range forks {
			if fork.ForkId != 1 && fork.ParentFork != 0 && !existingForkIds[fork.ParentFork] {
				fork.ParentFork = 0 // Connect to canonical chain
			}
		}
	}

	return forks
}

// createCanonicalChainFork creates the canonical chain representation
func createCanonicalChainFork(forks []*models.ChainFork, canonicalForkIdSet map[uint64]bool, earliestOrphanFork *models.ChainFork) *models.ChainFork {
	canonicalEndSlot := earliestOrphanFork.BaseSlot
	for _, fork := range forks {
		if canonicalForkIdSet[fork.ForkId] && fork.BaseSlot < canonicalEndSlot {
			canonicalEndSlot = fork.BaseSlot
		}
	}

	// Calculate average participation (all values are already 0-1)
	participation := 0.95 // Default high participation for canonical chain
	participationSum := participation
	participationCount := 1 // Count the default value

	for _, fork := range forks {
		if fork.Participation > 0 {
			participationSum += fork.Participation // Already 0-1, no conversion needed
			participationCount++
		}
	}

	participation = participationSum / float64(participationCount)

	// Note: ParticipationByEpoch should now be populated by processForksWithEpochData
	// since we added fork ID 0 to the database and cache queries

	// Calculate block count for the canonical chain segment
	// This is a rough estimate since we don't have the exact data here
	// The actual block count is calculated in processForksWithEpochData
	blockCount := uint64(float64(canonicalEndSlot+1) * 0.95) // Assume ~95% block production

	return &models.ChainFork{
		ForkId:        0,
		BaseSlot:      0,
		BaseRoot:      nil,
		LeafSlot:      0,
		LeafRoot:      nil,
		HeadSlot:      canonicalEndSlot,
		HeadRoot:      nil,
		ParentFork:    0,
		Participation: participation,
		IsCanonical:   true,
		Length:        canonicalEndSlot + 1,
		BlockCount:    blockCount,
	}
}
