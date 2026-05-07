package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	v1 "github.com/ethpandaops/go-eth2-client/api/v1"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// ValidatorsSummary will return the validators summary page with client distribution matrix
func ValidatorsSummary(w http.ResponseWriter, r *http.Request) {
	var validatorsSummaryTemplateFiles = append(layoutTemplateFiles,
		"validators_summary/validators_summary.html",
	)

	var pageTemplate = templates.GetTemplate(validatorsSummaryTemplateFiles...)
	data := InitPageData(w, r, "validators", "/validators/summary", "Validator Summary", validatorsSummaryTemplateFiles)

	urlArgs := r.URL.Query()
	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getValidatorsSummaryPageData()
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	if urlArgs.Has("json") {
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(data.Data)
		if err != nil {
			logrus.WithError(err).Error("error encoding validators summary data")
			http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		}
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "validators_summary.go", "ValidatorsSummary", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getValidatorsSummaryPageData() (*models.ValidatorsSummaryPageData, error) {
	pageData := &models.ValidatorsSummaryPageData{}
	pageCacheKey := "validators_summary"
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildValidatorsSummaryPageData(pageCall.CallCtx)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ValidatorsSummaryPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

type validatorsSummaryClientBalances struct {
	online  uint64
	offline uint64
}

type validatorsSummaryInclusionStats struct {
	count      uint64
	totalDelay uint64
}

type validatorsSummaryProposalStats struct {
	expected uint64
	proposed uint64
}

type validatorsSummaryPtcStats struct {
	expected uint64
	included uint64
}

func buildValidatorsSummaryPageData(ctx context.Context) (*models.ValidatorsSummaryPageData, time.Duration) {
	logrus.Debugf("validators summary page called")
	pageData := &models.ValidatorsSummaryPageData{}

	// Cache for one epoch duration - get actual timing from chain state
	chainState := services.GlobalBeaconService.GetChainState()
	specs := chainState.GetSpecs()
	// Epoch duration = slots per epoch * slot duration
	epochDuration := time.Duration(specs.SlotsPerEpoch*specs.SlotDurationMs) * time.Millisecond
	cacheTime := epochDuration

	// Get all validators (we'll filter out exited ones in processing)
	validatorFilter := dbtypes.ValidatorFilter{
		Status: []v1.ValidatorState{
			v1.ValidatorStateActiveOngoing,
			v1.ValidatorStateActiveExiting,
			v1.ValidatorStateActiveSlashed,
		},
	}
	validators, _ := services.GlobalBeaconService.GetFilteredValidatorSet(ctx, &validatorFilter, false)

	if len(validators) == 0 {
		return pageData, cacheTime
	}

	// Parse client types from validator names and group by client combinations
	clientCombinations := make(map[execution.ClientType]map[consensus.ClientType]*models.ValidatorsSummaryMatrixCell)
	executionClients := make(map[execution.ClientType]bool)
	consensusClients := make(map[consensus.ClientType]bool)
	totalEffectiveBalance := uint64(0)

	// Track balances per client for breakdown table
	elClientBalances := make(map[execution.ClientType]*validatorsSummaryClientBalances) // [client][online/offline] -> balance
	clClientBalances := make(map[consensus.ClientType]*validatorsSummaryClientBalances)

	// Track inclusion distance per client and per combination
	elInclStats := make(map[execution.ClientType]*validatorsSummaryInclusionStats)
	clInclStats := make(map[consensus.ClientType]*validatorsSummaryInclusionStats)
	combinationInclStats := make(map[execution.ClientType]map[consensus.ClientType]*validatorsSummaryInclusionStats)
	totalInclCount := uint64(0)
	totalInclDelay := uint64(0)

	// Track block proposal stats per client and per combination
	elProposalStats := make(map[execution.ClientType]*validatorsSummaryProposalStats)
	clProposalStats := make(map[consensus.ClientType]*validatorsSummaryProposalStats)
	combinationProposalStats := make(map[execution.ClientType]map[consensus.ClientType]*validatorsSummaryProposalStats)
	totalProposalExpected := uint64(0)
	totalProposalProposed := uint64(0)

	// Track PTC inclusion stats per client and per combination
	elPtcStats := make(map[execution.ClientType]*validatorsSummaryPtcStats)
	clPtcStats := make(map[consensus.ClientType]*validatorsSummaryPtcStats)
	combinationPtcStats := make(map[execution.ClientType]map[consensus.ClientType]*validatorsSummaryPtcStats)
	totalPtcExpected := uint64(0)
	totalPtcIncluded := uint64(0)

	// Lookback over the same window as the attestation inclusion stats (last 2 epochs)
	proposalStatsByValidator := services.GlobalBeaconService.GetValidatorProposalStats(ctx, 2)
	ptcStatsByValidator := services.GlobalBeaconService.GetValidatorPtcStats(ctx, 2)

	onlineEffectiveBalance := uint64(0)
	activeValidators := uint64(0)

	for _, validator := range validators {
		if validator.Validator == nil {
			continue
		}

		validatorName := services.GlobalBeaconService.GetValidatorName(uint64(validator.Index))
		executionClient, consensusClient := parseClientTypesFromName(validatorName)

		executionClients[executionClient] = true
		consensusClients[consensusClient] = true

		if clientCombinations[executionClient] == nil {
			clientCombinations[executionClient] = make(map[consensus.ClientType]*models.ValidatorsSummaryMatrixCell)
		}

		if clientCombinations[executionClient][consensusClient] == nil {
			clientCombinations[executionClient][consensusClient] = &models.ValidatorsSummaryMatrixCell{
				ExecutionClient: executionClient.String(),
				ConsensusClient: consensusClient.String(),
			}
		}

		// Initialize client balance tracking
		if elClientBalances[executionClient] == nil {
			elClientBalances[executionClient] = &validatorsSummaryClientBalances{}
		}
		if clClientBalances[consensusClient] == nil {
			clClientBalances[consensusClient] = &validatorsSummaryClientBalances{}
		}

		cell := clientCombinations[executionClient][consensusClient]
		cell.ValidatorCount++
		cell.EffectiveBalance += uint64(validator.Validator.EffectiveBalance)
		totalEffectiveBalance += uint64(validator.Validator.EffectiveBalance)

		effectiveBalance := uint64(validator.Validator.EffectiveBalance)

		// Check if validator is online - only for active validators
		isOnline := false
		if validator.Status == v1.ValidatorStateActiveOngoing || validator.Status == v1.ValidatorStateActiveExiting {
			liveness := services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3)
			if liveness >= 2 { // Consider online if attested in 2+ of last 3 epochs
				isOnline = true
				onlineEffectiveBalance += effectiveBalance
			}
		}
		// For pending validators, consider them as "offline" (not yet active)
		// For slashed validators, they can still be online if actively attesting

		if isOnline {
			cell.OnlineValidators++
			cell.OnlineEffectiveBalance += effectiveBalance
			elClientBalances[executionClient].online += effectiveBalance
			clClientBalances[consensusClient].online += effectiveBalance
		} else {
			cell.OfflineValidators++
			cell.OfflineEffectiveBalance += effectiveBalance
			elClientBalances[executionClient].offline += effectiveBalance
			clClientBalances[consensusClient].offline += effectiveBalance
		}

		// accumulate inclusion distance stats from cached blocks (last 2 epochs)
		inclCount, inclTotalDelay := services.GlobalBeaconService.GetValidatorInclusionDistance(validator.Index, 2)
		if inclCount > 0 {
			if elInclStats[executionClient] == nil {
				elInclStats[executionClient] = &validatorsSummaryInclusionStats{}
			}
			elInclStats[executionClient].count += inclCount
			elInclStats[executionClient].totalDelay += inclTotalDelay

			if clInclStats[consensusClient] == nil {
				clInclStats[consensusClient] = &validatorsSummaryInclusionStats{}
			}
			clInclStats[consensusClient].count += inclCount
			clInclStats[consensusClient].totalDelay += inclTotalDelay

			if combinationInclStats[executionClient] == nil {
				combinationInclStats[executionClient] = make(map[consensus.ClientType]*validatorsSummaryInclusionStats)
			}
			if combinationInclStats[executionClient][consensusClient] == nil {
				combinationInclStats[executionClient][consensusClient] = &validatorsSummaryInclusionStats{}
			}
			combinationInclStats[executionClient][consensusClient].count += inclCount
			combinationInclStats[executionClient][consensusClient].totalDelay += inclTotalDelay

			totalInclCount += inclCount
			totalInclDelay += inclTotalDelay
		}

		// accumulate block proposal stats (last 2 epochs)
		if propStat := proposalStatsByValidator[validator.Index]; propStat != nil && propStat.Expected > 0 {
			if elProposalStats[executionClient] == nil {
				elProposalStats[executionClient] = &validatorsSummaryProposalStats{}
			}
			elProposalStats[executionClient].expected += propStat.Expected
			elProposalStats[executionClient].proposed += propStat.Proposed

			if clProposalStats[consensusClient] == nil {
				clProposalStats[consensusClient] = &validatorsSummaryProposalStats{}
			}
			clProposalStats[consensusClient].expected += propStat.Expected
			clProposalStats[consensusClient].proposed += propStat.Proposed

			if combinationProposalStats[executionClient] == nil {
				combinationProposalStats[executionClient] = make(map[consensus.ClientType]*validatorsSummaryProposalStats)
			}
			if combinationProposalStats[executionClient][consensusClient] == nil {
				combinationProposalStats[executionClient][consensusClient] = &validatorsSummaryProposalStats{}
			}
			combinationProposalStats[executionClient][consensusClient].expected += propStat.Expected
			combinationProposalStats[executionClient][consensusClient].proposed += propStat.Proposed

			totalProposalExpected += propStat.Expected
			totalProposalProposed += propStat.Proposed
		}

		// accumulate PTC inclusion stats (last 2 epochs, Gloas+ only)
		if ptcStat := ptcStatsByValidator[validator.Index]; ptcStat != nil && ptcStat.Expected > 0 {
			if elPtcStats[executionClient] == nil {
				elPtcStats[executionClient] = &validatorsSummaryPtcStats{}
			}
			elPtcStats[executionClient].expected += ptcStat.Expected
			elPtcStats[executionClient].included += ptcStat.Included

			if clPtcStats[consensusClient] == nil {
				clPtcStats[consensusClient] = &validatorsSummaryPtcStats{}
			}
			clPtcStats[consensusClient].expected += ptcStat.Expected
			clPtcStats[consensusClient].included += ptcStat.Included

			if combinationPtcStats[executionClient] == nil {
				combinationPtcStats[executionClient] = make(map[consensus.ClientType]*validatorsSummaryPtcStats)
			}
			if combinationPtcStats[executionClient][consensusClient] == nil {
				combinationPtcStats[executionClient][consensusClient] = &validatorsSummaryPtcStats{}
			}
			combinationPtcStats[executionClient][consensusClient].expected += ptcStat.Expected
			combinationPtcStats[executionClient][consensusClient].included += ptcStat.Included

			totalPtcExpected += ptcStat.Expected
			totalPtcIncluded += ptcStat.Included
		}

		activeValidators++
	}

	// Calculate percentages, health status and avg inclusion delay per cell
	for elClient := range clientCombinations {
		for clClient := range clientCombinations[elClient] {
			cell := clientCombinations[elClient][clClient]
			if cell.ValidatorCount == 0 {
				cell.HealthStatus = "empty"
				continue
			}

			cell.BalancePercentage = (float64(cell.EffectiveBalance) / float64(totalEffectiveBalance)) * 100
			cell.OnlinePercentage = (float64(cell.OnlineEffectiveBalance) / float64(cell.EffectiveBalance)) * 100

			if combinationInclStats[elClient] != nil {
				if stats := combinationInclStats[elClient][clClient]; stats != nil && stats.count > 0 {
					cell.AvgInclusionDelay = float64(stats.totalDelay) / float64(stats.count)
				}
			}

			if combinationProposalStats[elClient] != nil {
				if stats := combinationProposalStats[elClient][clClient]; stats != nil && stats.expected > 0 {
					cell.HasProposalData = true
					cell.ProposalsExpected = stats.expected
					cell.ProposalsProposed = stats.proposed
					cell.ProposalRate = (float64(stats.proposed) / float64(stats.expected)) * 100
				}
			}

			if combinationPtcStats[elClient] != nil {
				if stats := combinationPtcStats[elClient][clClient]; stats != nil && stats.expected > 0 {
					cell.HasPtcData = true
					cell.PtcVotesExpected = stats.expected
					cell.PtcVotesIncluded = stats.included
					cell.PtcInclusionRate = (float64(stats.included) / float64(stats.expected)) * 100
				}
			}

			cell.HealthStatus = computeHealthStatus(cell.OnlinePercentage, cell.HasProposalData, cell.ProposalRate, cell.HasPtcData, cell.PtcInclusionRate)
		}
	}

	// Convert maps to sorted slices for consistent ordering
	elClientList := make([]execution.ClientType, 0, len(executionClients))
	for client := range executionClients {
		elClientList = append(elClientList, client)
	}
	sort.Slice(elClientList, func(i, j int) bool {
		return elClientList[i].String() < elClientList[j].String()
	})

	clClientList := make([]consensus.ClientType, 0, len(consensusClients))
	for client := range consensusClients {
		clClientList = append(clClientList, client)
	}
	sort.Slice(clClientList, func(i, j int) bool {
		return clClientList[i].String() < clClientList[j].String()
	})

	// Build dynamic matrix
	matrix := make([][]models.ValidatorsSummaryMatrixCell, len(elClientList))
	for i, elClient := range elClientList {
		matrix[i] = make([]models.ValidatorsSummaryMatrixCell, len(clClientList))
		for j, clClient := range clClientList {
			if clientCombinations[elClient] != nil && clientCombinations[elClient][clClient] != nil {
				matrix[i][j] = *clientCombinations[elClient][clClient]
			} else {
				matrix[i][j] = models.ValidatorsSummaryMatrixCell{
					ExecutionClient: elClient.String(),
					ConsensusClient: clClient.String(),
					HealthStatus:    "empty",
				}
			}
		}
	}

	// Build client breakdown for detailed table
	clientBreakdown := buildClientBreakdown(clientCombinations, totalEffectiveBalance, elClientList, clClientList, elClientBalances, clClientBalances, elInclStats, clInclStats, elProposalStats, clProposalStats, elPtcStats, clPtcStats)

	elClientListStrings := make([]string, len(elClientList))
	for i, elClient := range elClientList {
		elClientListStrings[i] = elClient.String()
	}

	clClientListStrings := make([]string, len(clClientList))
	for i, clClient := range clClientList {
		clClientListStrings[i] = clClient.String()
	}

	pageData.ClientMatrix = matrix
	pageData.ExecutionClients = elClientListStrings
	pageData.ConsensusClients = clClientListStrings
	pageData.TotalValidators = activeValidators
	pageData.TotalEffectiveETH = totalEffectiveBalance / 1000000000 // Convert to ETH as whole numbers
	pageData.OverallHealthy = onlineEffectiveBalance / 1000000000   // Convert online EB to ETH
	pageData.ClientBreakdown = clientBreakdown
	pageData.NetworkHealthScore = (float64(onlineEffectiveBalance) / float64(totalEffectiveBalance)) * 100

	if totalInclCount > 0 {
		pageData.AvgInclusionDelay = float64(totalInclDelay) / float64(totalInclCount)
		pageData.HasInclusionData = true
	}

	if totalProposalExpected > 0 {
		pageData.HasProposalData = true
		pageData.ProposalsExpected = totalProposalExpected
		pageData.ProposalsProposed = totalProposalProposed
		pageData.ProposalRate = (float64(totalProposalProposed) / float64(totalProposalExpected)) * 100
	}

	if totalPtcExpected > 0 {
		pageData.HasPtcData = true
		pageData.PtcVotesExpected = totalPtcExpected
		pageData.PtcVotesIncluded = totalPtcIncluded
		pageData.PtcInclusionRate = (float64(totalPtcIncluded) / float64(totalPtcExpected)) * 100
	}

	return pageData, cacheTime
}

// computeHealthStatus returns the health bucket based on every available metric.
// Green ("healthy") requires 100% on every observed metric. 0/0 (no observed
// duties) is vacuously 100% and does not block green — only an actual failure
// (proposed < expected, or included < expected) drops the cell out of healthy.
func computeHealthStatus(onlinePct float64, hasProposal bool, proposalPct float64, hasPtc bool, ptcPct float64) string {
	healthy := onlinePct == 100
	warning := onlinePct >= 95
	if hasProposal {
		if proposalPct < 100 {
			healthy = false
		}
		if proposalPct < 95 {
			warning = false
		}
	}
	if hasPtc {
		if ptcPct < 100 {
			healthy = false
		}
		if ptcPct < 95 {
			warning = false
		}
	}

	switch {
	case healthy:
		return "healthy"
	case warning:
		return "warning"
	default:
		return "critical"
	}
}

func parseClientTypesFromName(validatorName string) (execution.ClientType, consensus.ClientType) {
	if validatorName == "" {
		return execution.UnknownClient, consensus.UnknownClient
	}

	name := strings.ToLower(validatorName)

	// Parse execution client - check nimbusel patterns first to avoid confusion with nimbus
	executionClient := execution.UnknownClient
	if strings.Contains(name, "nimbusel") || strings.Contains(name, "nimbus-el") {
		executionClient = execution.NimbusELClient
	} else if strings.Contains(name, "geth") {
		executionClient = execution.GethClient
	} else if strings.Contains(name, "besu") {
		executionClient = execution.BesuClient
	} else if strings.Contains(name, "nethermind") {
		executionClient = execution.NethermindClient
	} else if strings.Contains(name, "reth") {
		executionClient = execution.RethClient
	} else if strings.Contains(name, "erigon") {
		executionClient = execution.ErigonClient
	} else if strings.Contains(name, "ethrex") {
		executionClient = execution.EthrexClient
	}

	// Parse consensus client - use exact word matching to avoid nimbus/nimbusel conflicts
	consensusClient := consensus.UnknownClient
	if strings.Contains(name, "lighthouse") {
		consensusClient = consensus.LighthouseClient
	} else if strings.Contains(name, "prysm") {
		consensusClient = consensus.PrysmClient
	} else if strings.Contains(name, "teku") {
		consensusClient = consensus.TekuClient
	} else if strings.Contains(name, "lodestar") {
		consensusClient = consensus.LodestarClient
	} else if strings.Contains(name, "grandine") {
		consensusClient = consensus.GrandineClient
	} else if regexp.MustCompile(`\bnimbus\b`).MatchString(name) {
		consensusClient = consensus.NimbusClient
	}

	return executionClient, consensusClient
}

func buildClientBreakdown(
	clientCombinations map[execution.ClientType]map[consensus.ClientType]*models.ValidatorsSummaryMatrixCell,
	totalEffectiveBalance uint64,
	elClients []execution.ClientType,
	clClients []consensus.ClientType,
	elClientBalances map[execution.ClientType]*validatorsSummaryClientBalances,
	clClientBalances map[consensus.ClientType]*validatorsSummaryClientBalances,
	elInclStats map[execution.ClientType]*validatorsSummaryInclusionStats,
	clInclStats map[consensus.ClientType]*validatorsSummaryInclusionStats,
	elProposalStats map[execution.ClientType]*validatorsSummaryProposalStats,
	clProposalStats map[consensus.ClientType]*validatorsSummaryProposalStats,
	elPtcStats map[execution.ClientType]*validatorsSummaryPtcStats,
	clPtcStats map[consensus.ClientType]*validatorsSummaryPtcStats,
) []models.ValidatorsSummaryClientBreak {
	breakdown := []models.ValidatorsSummaryClientBreak{}

	// Aggregate by execution client
	elStats := make(map[execution.ClientType]*models.ValidatorsSummaryClientBreak, len(elClients))
	for _, elClient := range elClients {
		elStats[elClient] = &models.ValidatorsSummaryClientBreak{
			ClientName:              elClient.String(),
			Layer:                   "execution",
			ClientType:              elClient.String(),
			OnlineEffectiveBalance:  elClientBalances[elClient].online,
			OfflineEffectiveBalance: elClientBalances[elClient].offline,
		}
	}

	// Aggregate by consensus client
	clStats := make(map[consensus.ClientType]*models.ValidatorsSummaryClientBreak, len(clClients))
	for _, clClient := range clClients {
		clStats[clClient] = &models.ValidatorsSummaryClientBreak{
			ClientName:              clClient.String(),
			Layer:                   "consensus",
			ClientType:              clClient.String(),
			OnlineEffectiveBalance:  clClientBalances[clClient].online,
			OfflineEffectiveBalance: clClientBalances[clClient].offline,
		}
	}

	// Sum up stats
	for elClient := range clientCombinations {
		for clClient, cell := range clientCombinations[elClient] {
			if cell.ValidatorCount == 0 {
				continue
			}

			// Add to execution client stats
			if elStats[elClient] != nil {
				elStats[elClient].ValidatorCount += cell.ValidatorCount
				elStats[elClient].EffectiveBalance += cell.EffectiveBalance
				elStats[elClient].OnlineValidators += cell.OnlineValidators
			}

			// Add to consensus client stats
			if clStats[clClient] != nil {
				clStats[clClient].ValidatorCount += cell.ValidatorCount
				clStats[clClient].EffectiveBalance += cell.EffectiveBalance
				clStats[clClient].OnlineValidators += cell.OnlineValidators
			}
		}
	}

	// Calculate percentages, health status and avg inclusion delay
	for elClient, stat := range elStats {
		if stat.ValidatorCount > 0 {
			stat.EffectiveBalance = stat.OnlineEffectiveBalance + stat.OfflineEffectiveBalance
			stat.BalancePercentage = (float64(stat.EffectiveBalance) / float64(totalEffectiveBalance)) * 100
			stat.OnlinePercentage = (float64(stat.OnlineEffectiveBalance) / float64(stat.EffectiveBalance)) * 100
			stat.OfflineValidators = stat.ValidatorCount - stat.OnlineValidators

			if inclStats := elInclStats[elClient]; inclStats != nil && inclStats.count > 0 {
				stat.AvgInclusionDelay = float64(inclStats.totalDelay) / float64(inclStats.count)
			}

			if propStats := elProposalStats[elClient]; propStats != nil && propStats.expected > 0 {
				stat.HasProposalData = true
				stat.ProposalsExpected = propStats.expected
				stat.ProposalsProposed = propStats.proposed
				stat.ProposalRate = (float64(propStats.proposed) / float64(propStats.expected)) * 100
			}

			if ptcStats := elPtcStats[elClient]; ptcStats != nil && ptcStats.expected > 0 {
				stat.HasPtcData = true
				stat.PtcVotesExpected = ptcStats.expected
				stat.PtcVotesIncluded = ptcStats.included
				stat.PtcInclusionRate = (float64(ptcStats.included) / float64(ptcStats.expected)) * 100
			}

			stat.HealthStatus = computeHealthStatus(stat.OnlinePercentage, stat.HasProposalData, stat.ProposalRate, stat.HasPtcData, stat.PtcInclusionRate)

			breakdown = append(breakdown, *stat)
		}
	}

	for clClient, stat := range clStats {
		if stat.ValidatorCount > 0 {
			stat.EffectiveBalance = stat.OnlineEffectiveBalance + stat.OfflineEffectiveBalance
			stat.BalancePercentage = (float64(stat.EffectiveBalance) / float64(totalEffectiveBalance)) * 100
			stat.OnlinePercentage = (float64(stat.OnlineEffectiveBalance) / float64(stat.EffectiveBalance)) * 100
			stat.OfflineValidators = stat.ValidatorCount - stat.OnlineValidators

			if inclStats := clInclStats[clClient]; inclStats != nil && inclStats.count > 0 {
				stat.AvgInclusionDelay = float64(inclStats.totalDelay) / float64(inclStats.count)
			}

			if propStats := clProposalStats[clClient]; propStats != nil && propStats.expected > 0 {
				stat.HasProposalData = true
				stat.ProposalsExpected = propStats.expected
				stat.ProposalsProposed = propStats.proposed
				stat.ProposalRate = (float64(propStats.proposed) / float64(propStats.expected)) * 100
			}

			if ptcStats := clPtcStats[clClient]; ptcStats != nil && ptcStats.expected > 0 {
				stat.HasPtcData = true
				stat.PtcVotesExpected = ptcStats.expected
				stat.PtcVotesIncluded = ptcStats.included
				stat.PtcInclusionRate = (float64(ptcStats.included) / float64(ptcStats.expected)) * 100
			}

			stat.HealthStatus = computeHealthStatus(stat.OnlinePercentage, stat.HasProposalData, stat.ProposalRate, stat.HasPtcData, stat.PtcInclusionRate)

			breakdown = append(breakdown, *stat)
		}
	}

	// Sort alphabetically by client name within each layer
	sort.Slice(breakdown, func(i, j int) bool {
		// First sort by layer (execution before consensus)
		if breakdown[i].Layer != breakdown[j].Layer {
			return breakdown[i].Layer == "execution"
		}
		// Then sort alphabetically by client name
		return breakdown[i].ClientName < breakdown[j].ClientName
	})

	return breakdown
}
