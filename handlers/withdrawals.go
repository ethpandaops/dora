package handlers

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// Withdrawals will return the main "withdrawals" page using a go template
func Withdrawals(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"withdrawals/withdrawals.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/withdrawals", "Withdrawals", templateFiles)

	urlArgs := r.URL.Query()
	var firstEpoch uint64 = math.MaxUint64
	if urlArgs.Has("epoch") {
		firstEpoch, _ = strconv.ParseUint(urlArgs.Get("epoch"), 10, 64)
	}
	var pageSize uint64 = 50
	if urlArgs.Has("count") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("count"), 10, 64)
	}

	// Get tab view from URL
	tabView := "recent"
	if urlArgs.Has("v") {
		tabView = urlArgs.Get("v")
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if pageError == nil {
		data.Data, pageError = getWithdrawalsPageData(firstEpoch, pageSize, tabView)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")

	if r.URL.Query().Has("lazy") {
		// return the selected tab content only (lazy loaded)
		handleTemplateError(w, r, "withdrawals.go", "Withdrawals", "", pageTemplate.ExecuteTemplate(w, "lazyPage", data.Data))
	} else {
		handleTemplateError(w, r, "withdrawals.go", "Withdrawals", "", pageTemplate.ExecuteTemplate(w, "layout", data))
	}
}

func getWithdrawalsPageData(firstEpoch uint64, pageSize uint64, tabView string) (*models.WithdrawalsPageData, error) {
	pageData := &models.WithdrawalsPageData{}
	pageCacheKey := fmt.Sprintf("withdrawals:%v:%v:%v", firstEpoch, pageSize, tabView)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildWithdrawalsPageData(firstEpoch, pageSize, tabView)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.WithdrawalsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildWithdrawalsPageData(firstEpoch uint64, pageSize uint64, tabView string) (*models.WithdrawalsPageData, time.Duration) {
	logrus.Debugf("withdrawals page called: %v:%v:%v", firstEpoch, pageSize, tabView)
	chainState := services.GlobalBeaconService.GetChainState()

	pageData := &models.WithdrawalsPageData{
		TabView: tabView,
	}

	// Get withdrawal queue data for stats
	queueFilter := &services.WithdrawalQueueFilter{}
	queuedWithdrawals, queuedWithdrawalCount, queuedAmount := services.GlobalBeaconService.GetWithdrawalQueueByFilter(queueFilter, 0, 1)
	pageData.QueuedWithdrawalCount = queuedWithdrawalCount
	pageData.WithdrawingValidatorCount = queuedWithdrawalCount
	pageData.WithdrawingAmount = uint64(queuedAmount)

	// Calculate queue duration estimation based on the last queued withdrawal
	if len(queuedWithdrawals) > 0 {
		lastQueueEntry := queuedWithdrawals[len(queuedWithdrawals)-1]
		pageData.QueueDurationEstimate = chainState.SlotToTime(lastQueueEntry.EstimatedWithdrawalTime)
		pageData.HasQueueDuration = true
	}

	zeroAmount := uint64(0)
	oneAmount := uint64(1)

	_, _, totalWithdrawals := services.GlobalBeaconService.GetWithdrawalRequestsByFilter(&services.CombinedWithdrawalRequestFilter{
		Filter: &dbtypes.WithdrawalRequestFilter{
			WithOrphaned: 1,
			MinAmount:    &oneAmount,
		},
	}, 0, 1)
	pageData.TotalWithdrawalCount = totalWithdrawals

	_, _, totalExits := services.GlobalBeaconService.GetWithdrawalRequestsByFilter(&services.CombinedWithdrawalRequestFilter{
		Filter: &dbtypes.WithdrawalRequestFilter{
			WithOrphaned: 1,

			MaxAmount: &zeroAmount,
		},
	}, 0, 1)
	pageData.TotalExitCount = totalExits

	// Only load data for the selected tab
	switch tabView {
	case "recent":
		withdrawalFilter := &services.CombinedWithdrawalRequestFilter{
			Filter: &dbtypes.WithdrawalRequestFilter{
				WithOrphaned: 1,
			},
		}

		dbWithdrawals, _, _ := services.GlobalBeaconService.GetWithdrawalRequestsByFilter(withdrawalFilter, 0, uint32(20))
		for _, withdrawal := range dbWithdrawals {
			withdrawalData := &models.WithdrawalsPageDataRecentWithdrawal{
				SourceAddr: withdrawal.SourceAddress(),
				Amount:     withdrawal.Amount(),
				PublicKey:  withdrawal.ValidatorPubkey(),
			}

			if validatorIndex := withdrawal.ValidatorIndex(); validatorIndex != nil {
				withdrawalData.ValidatorIndex = *validatorIndex
				withdrawalData.ValidatorName = services.GlobalBeaconService.GetValidatorName(*validatorIndex)
				withdrawalData.ValidatorValid = true
			}

			if request := withdrawal.Request; request != nil {
				withdrawalData.IsIncluded = true
				withdrawalData.SlotNumber = request.SlotNumber
				withdrawalData.SlotRoot = request.SlotRoot
				withdrawalData.Time = chainState.SlotToTime(phase0.Slot(request.SlotNumber))
				withdrawalData.Status = uint64(1)
				withdrawalData.Result = request.Result
				withdrawalData.ResultMessage = getWithdrawalResultMessage(request.Result, chainState.GetSpecs())
			}

			if transaction := withdrawal.Transaction; transaction != nil {
				withdrawalData.TransactionHash = transaction.TxHash
				withdrawalData.LinkedTransaction = true
				withdrawalData.TxStatus = uint64(1)
				if withdrawal.TransactionOrphaned {
					withdrawalData.TxStatus = uint64(2)
				}
			}

			pageData.RecentWithdrawals = append(pageData.RecentWithdrawals, withdrawalData)
		}
		pageData.RecentWithdrawalCount = uint64(len(pageData.RecentWithdrawals))

	case "queue":
		// Load withdrawal queue
		queueWithdrawals, _, _ := services.GlobalBeaconService.GetWithdrawalQueueByFilter(&services.WithdrawalQueueFilter{}, 0, 20)
		for _, queueEntry := range queueWithdrawals {
			queueData := &models.WithdrawalsPageDataQueuedWithdrawal{
				ValidatorIndex:    uint64(queueEntry.ValidatorIndex),
				ValidatorName:     queueEntry.ValidatorName,
				Amount:            uint64(queueEntry.Amount),
				WithdrawableEpoch: uint64(queueEntry.WithdrawableEpoch),
				PublicKey:         queueEntry.Validator.Validator.PublicKey[:],
			}

			if strings.HasPrefix(queueEntry.Validator.Status.String(), "pending") {
				queueData.ValidatorStatus = "Pending"
			} else if queueEntry.Validator.Status == v1.ValidatorStateActiveOngoing {
				queueData.ValidatorStatus = "Active"
				queueData.ShowUpcheck = true
			} else if queueEntry.Validator.Status == v1.ValidatorStateActiveExiting {
				queueData.ValidatorStatus = "Exiting"
				queueData.ShowUpcheck = true
			} else if queueEntry.Validator.Status == v1.ValidatorStateActiveSlashed {
				queueData.ValidatorStatus = "Slashed"
				queueData.ShowUpcheck = true
			} else if queueEntry.Validator.Status == v1.ValidatorStateExitedUnslashed {
				queueData.ValidatorStatus = "Exited"
			} else if queueEntry.Validator.Status == v1.ValidatorStateExitedSlashed {
				queueData.ValidatorStatus = "Slashed"
			} else {
				queueData.ValidatorStatus = queueEntry.Validator.Status.String()
			}

			if queueData.ShowUpcheck {
				queueData.UpcheckActivity = uint8(services.GlobalBeaconService.GetValidatorLiveness(queueEntry.Validator.Index, 3))
				queueData.UpcheckMaximum = uint8(3)
			}

			// Use the calculated EstimatedWithdrawalTime from the queue entry
			queueData.EstimatedTime = chainState.SlotToTime(queueEntry.EstimatedWithdrawalTime)

			pageData.QueuedWithdrawals = append(pageData.QueuedWithdrawals, queueData)
		}
		pageData.QueuedTabCount = uint64(len(pageData.QueuedWithdrawals))
	}

	return pageData, 1 * time.Minute
}
