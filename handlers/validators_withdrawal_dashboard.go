package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
)

const validatorsWithdrawalDashboardTableLimit = 250
const validatorsWithdrawalDashboardLivenessEpochs = 3

// ValidatorsWithdrawalDashboard returns a validator performance dashboard scoped to one withdrawal address.
func ValidatorsWithdrawalDashboard(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"validators_withdrawal_dashboard/validators_withdrawal_dashboard.html",
		"_svg/professor.html",
	)

	pageTemplate := templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/withdrawal-dashboard", "Withdrawal Dashboard", templateFiles)

	urlArgs := r.URL.Query()
	query := strings.TrimSpace(urlArgs.Get("withdrawal"))
	if query == "" {
		query = strings.TrimSpace(urlArgs.Get("address"))
	}
	if query == "" {
		query = strings.TrimSpace(urlArgs.Get("q"))
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getValidatorsWithdrawalDashboardPageData(query)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}

	if urlArgs.Has("json") {
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(data.Data)
		if err != nil {
			logrus.WithError(err).Error("error encoding withdrawal dashboard data")
			http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		}
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "validators_withdrawal_dashboard.go", "ValidatorsWithdrawalDashboard", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}

func getValidatorsWithdrawalDashboardPageData(query string) (*models.ValidatorsWithdrawalDashboardPageData, error) {
	pageData := &models.ValidatorsWithdrawalDashboardPageData{}
	pageCacheKey := fmt.Sprintf("validators_withdrawal_dashboard:%v", query)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildValidatorsWithdrawalDashboardPageData(query)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ValidatorsWithdrawalDashboardPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildValidatorsWithdrawalDashboardPageData(query string) (*models.ValidatorsWithdrawalDashboardPageData, time.Duration) {
	pageData := &models.ValidatorsWithdrawalDashboardPageData{
		Query:      query,
		HasQuery:   query != "",
		Validators: []*models.ValidatorsWithdrawalDashboardValidator{},
	}
	cacheTime := 10 * time.Second

	if query == "" {
		return pageData, cacheTime
	}

	withdrawalAddress, withdrawalCreds, err := utils.ParseWithdrawalAddressOrCredentials(query)
	if err != nil {
		pageData.Error = err.Error()
		return pageData, cacheTime
	}

	pageData.NormalizedAddress = withdrawalAddress
	pageData.Credentials = withdrawalCreds
	if len(withdrawalAddress) == 0 && len(withdrawalCreds) == 32 {
		pageData.NormalizedAddress = utils.WithdrawalCredentialsAddress(withdrawalCreds)
	}

	filter := dbtypes.ValidatorFilter{
		WithdrawalAddress: withdrawalAddress,
		WithdrawalCreds:   withdrawalCreds,
		OrderBy:           dbtypes.ValidatorOrderIndexAsc,
	}
	validators, validatorCount := services.GlobalBeaconService.GetFilteredValidatorSet(context.Background(), &filter, true)
	pageData.ValidatorCount = validatorCount
	sort.SliceStable(validators, func(i, j int) bool {
		iRank := validatorWithdrawalDashboardStateRank(validators[i].Status)
		jRank := validatorWithdrawalDashboardStateRank(validators[j].Status)
		if iRank != jRank {
			return iRank < jRank
		}
		return validators[i].Index < validators[j].Index
	})

	queryArgs := url.Values{}
	queryArgs.Set("f.withdrawal", query)
	pageData.ValidatorsLink = fmt.Sprintf("/validators?f&%v", queryArgs.Encode())
	activityArgs := url.Values{}
	activityArgs.Set("group", "4")
	activityArgs.Set("search", query)
	pageData.ActivityLink = fmt.Sprintf("/validators/activity?%v", activityArgs.Encode())

	currentEpoch := services.GlobalBeaconService.GetChainState().CurrentEpoch()
	livenessStartEpoch := validatorsWithdrawalDashboardLivenessStartEpoch(currentEpoch)
	livenessEpochs, participationLoading, participationLoadingPct := validatorsWithdrawalDashboardLivenessWindow(currentEpoch, livenessStartEpoch)

	livenessTotal := uint64(0)
	for _, validator := range validators {
		if validator.Validator == nil {
			continue
		}

		validatorData := &models.ValidatorsWithdrawalDashboardValidator{
			Index:             uint64(validator.Index),
			Name:              services.GlobalBeaconService.GetValidatorName(uint64(validator.Index)),
			PublicKey:         validator.Validator.PublicKey[:],
			Balance:           uint64(validator.Balance),
			EffectiveBalance:  uint64(validator.Validator.EffectiveBalance),
			State:             validatorWithdrawalDashboardState(validator.Status),
			LivenessMax:       uint8(livenessEpochs),
			WithdrawalAddress: utils.WithdrawalCredentialsAddress(validator.Validator.WithdrawalCredentials),
		}

		pageData.TotalBalance += validatorData.Balance
		pageData.TotalEffectiveBalance += validatorData.EffectiveBalance

		isActive := validator.Status == v1.ValidatorStateActiveOngoing ||
			validator.Status == v1.ValidatorStateActiveExiting ||
			validator.Status == v1.ValidatorStateActiveSlashed
		if isActive {
			pageData.ActiveCount++
			validatorData.ShowLiveness = true
			liveness, _ := services.GlobalBeaconService.GetBeaconIndexer().GetValidatorActivityCount(validator.Index, livenessStartEpoch)
			if liveness > uint64(livenessEpochs) {
				liveness = uint64(livenessEpochs)
			}
			validatorData.Liveness = uint8(liveness)
			validatorData.LivenessPercent = float64(liveness) * 100 / float64(livenessEpochs)
			livenessTotal += liveness
			if liveness > 0 {
				pageData.OnlineCount++
			} else {
				pageData.OfflineCount++
			}
		}

		switch validatorData.State {
		case "Pending":
			pageData.PendingCount++
		case "Exiting":
			pageData.ExitingCount++
		case "Exited":
			pageData.ExitedCount++
		case "Withdrawable":
			pageData.WithdrawableCount++
		case "Withdrawn":
			pageData.WithdrawnCount++
		case "Slashed":
			pageData.SlashedCount++
		}

		if len(pageData.Validators) < validatorsWithdrawalDashboardTableLimit {
			pageData.Validators = append(pageData.Validators, validatorData)
		}
	}
	pageData.QueuedDepositCount = getWithdrawalDashboardQueuedDepositCount(withdrawalAddress, withdrawalCreds)
	pageData.PendingTotalCount = pageData.PendingCount + pageData.QueuedDepositCount

	pageData.DisplayedValidatorCount = uint64(len(pageData.Validators))
	if pageData.ActiveCount > 0 {
		pageData.OnlineRate = float64(pageData.OnlineCount) * 100 / float64(pageData.ActiveCount)
		pageData.OfflineRate = float64(pageData.OfflineCount) * 100 / float64(pageData.ActiveCount)
		pageData.ParticipationRate = float64(livenessTotal) * 100 / float64(pageData.ActiveCount*uint64(livenessEpochs))
	}
	pageData.ParticipationLoading = pageData.ActiveCount > 0 && participationLoading
	if pageData.ParticipationLoading {
		pageData.ParticipationLoadingPct = participationLoadingPct
		pageData.ParticipationLoadingText = "Still indexing all validators. This will auto-update when ready."
	}
	pageData.HealthStatus = validatorsWithdrawalDashboardHealth(pageData.ParticipationRate)

	return pageData, cacheTime
}

func getWithdrawalDashboardQueuedDepositCount(withdrawalAddress []byte, withdrawalCreds []byte) uint64 {
	depositQueue := services.GlobalBeaconService.GetBeaconIndexer().GetLatestDepositQueue(nil)
	if len(depositQueue) == 0 {
		return 0
	}

	newValidators := map[string]bool{}
	for _, deposit := range depositQueue {
		if deposit == nil {
			continue
		}
		if len(withdrawalAddress) > 0 {
			wdcreds := deposit.WithdrawalCredentials[:]
			if wdcreds[0] != 0x01 && wdcreds[0] != 0x02 {
				continue
			}
			if !bytes.Equal(wdcreds[12:], withdrawalAddress) {
				continue
			}
		}
		if len(withdrawalCreds) > 0 && !bytes.Equal(deposit.WithdrawalCredentials[:], withdrawalCreds) {
			continue
		}

		if _, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(deposit.Pubkey); found {
			continue
		}
		newValidators[string(deposit.Pubkey[:])] = true
	}

	return uint64(len(newValidators))
}

func validatorWithdrawalDashboardState(status v1.ValidatorState) string {
	switch status {
	case v1.ValidatorStatePendingInitialized, v1.ValidatorStatePendingQueued:
		return "Pending"
	}
	switch status {
	case v1.ValidatorStateActiveOngoing:
		return "Active"
	case v1.ValidatorStateActiveExiting:
		return "Exiting"
	case v1.ValidatorStateActiveSlashed:
		return "Slashed"
	case v1.ValidatorStateExitedUnslashed:
		return "Exited"
	case v1.ValidatorStateExitedSlashed:
		return "Slashed"
	case v1.ValidatorStateWithdrawalPossible:
		return "Withdrawable"
	case v1.ValidatorStateWithdrawalDone:
		return "Withdrawn"
	default:
		return status.String()
	}
}

func validatorWithdrawalDashboardStateRank(status v1.ValidatorState) int {
	switch status {
	case v1.ValidatorStateActiveOngoing:
		return 0
	case v1.ValidatorStateActiveExiting:
		return 1
	case v1.ValidatorStateActiveSlashed:
		return 2
	case v1.ValidatorStatePendingInitialized, v1.ValidatorStatePendingQueued:
		return 3
	case v1.ValidatorStateExitedUnslashed:
		return 4
	case v1.ValidatorStateExitedSlashed:
		return 5
	case v1.ValidatorStateWithdrawalPossible:
		return 6
	case v1.ValidatorStateWithdrawalDone:
		return 7
	default:
		return 8
	}
}

func validatorsWithdrawalDashboardHealth(participationRate float64) string {
	if participationRate >= 95 {
		return "healthy"
	}
	if participationRate >= 90 {
		return "warning"
	}
	if participationRate > 0 {
		return "critical"
	}
	return "unknown"
}

func validatorsWithdrawalDashboardLivenessStartEpoch(currentEpoch phase0.Epoch) phase0.Epoch {
	if currentEpoch > validatorsWithdrawalDashboardLivenessEpochs {
		return currentEpoch - validatorsWithdrawalDashboardLivenessEpochs
	}
	return 0
}

func validatorsWithdrawalDashboardLivenessWindow(currentEpoch phase0.Epoch, startEpoch phase0.Epoch) (phase0.Epoch, bool, float64) {
	targetEpochs := phase0.Epoch(validatorsWithdrawalDashboardLivenessEpochs)
	_, oldestActivityEpoch := services.GlobalBeaconService.GetBeaconIndexer().GetValidatorActivityCount(0, startEpoch)
	if oldestActivityEpoch <= startEpoch {
		return targetEpochs, false, 100
	}
	if oldestActivityEpoch > currentEpoch {
		return 1, true, 0
	}

	readyEpochs := currentEpoch - oldestActivityEpoch
	if readyEpochs >= targetEpochs {
		return targetEpochs, false, 100
	}

	livenessEpochs := readyEpochs
	if livenessEpochs < 1 {
		livenessEpochs = 1
	}
	return livenessEpochs, true, float64(readyEpochs) * 100 / float64(targetEpochs)
}
