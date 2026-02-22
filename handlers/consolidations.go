package handlers

import (
	"context"
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

// Consolidations will return the main "consolidations" page using a go template
func Consolidations(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"consolidations/consolidations.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/consolidations", "Consolidations", templateFiles)

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
		data.Data, pageError = getConsolidationsPageData(firstEpoch, pageSize, tabView)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")

	if r.URL.Query().Has("lazy") {
		// return the selected tab content only (lazy loaded)
		handleTemplateError(w, r, "consolidations.go", "Consolidations", "", pageTemplate.ExecuteTemplate(w, "lazyPage", data.Data))
	} else {
		handleTemplateError(w, r, "consolidations.go", "Consolidations", "", pageTemplate.ExecuteTemplate(w, "layout", data))
	}
}

func getConsolidationsPageData(firstEpoch uint64, pageSize uint64, tabView string) (*models.ConsolidationsPageData, error) {
	pageData := &models.ConsolidationsPageData{}
	pageCacheKey := fmt.Sprintf("consolidations:%v:%v:%v", firstEpoch, pageSize, tabView)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildConsolidationsPageData(pageCall.CallCtx, firstEpoch, pageSize, tabView)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ConsolidationsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildConsolidationsPageData(ctx context.Context, firstEpoch uint64, pageSize uint64, tabView string) (*models.ConsolidationsPageData, time.Duration) {
	logrus.Debugf("consolidations page called: %v:%v:%v", firstEpoch, pageSize, tabView)
	chainState := services.GlobalBeaconService.GetChainState()
	epochStats, _ := services.GlobalBeaconService.GetRecentEpochStats(nil)

	pageData := &models.ConsolidationsPageData{
		TabView: tabView,
	}

	// Get consolidation queue stats
	consolidationRequestFilter := &services.CombinedConsolidationRequestFilter{
		Filter: &dbtypes.ConsolidationRequestFilter{
			WithOrphaned: 0, // Only canonical requests
		},
	}

	// Get total consolidation count
	_, _, totalConsolidations := services.GlobalBeaconService.GetConsolidationRequestsByFilter(ctx, consolidationRequestFilter, 0, 1)
	pageData.TotalConsolidationCount = totalConsolidations

	// Get consolidation queue data for stats
	queueFilter := &services.ConsolidationQueueFilter{
		ReverseOrder: true,
	}
	queuedConsolidations, queuedConsolidationCount := services.GlobalBeaconService.GetConsolidationQueueByFilter(ctx, queueFilter, 0, 1)
	pageData.QueuedConsolidationCount = queuedConsolidationCount

	// Calculate consolidating validator count and amount
	if epochStats != nil {
		pageData.ConsolidatingValidatorCount = uint64(len(epochStats.PendingConsolidations))
		pageData.ConsolidatingAmount = uint64(epochStats.ConsolidatingBalance)
	}

	// Calculate queue duration estimation based on the last queued consolidation
	if len(queuedConsolidations) > 0 {
		lastQueueEntry := queuedConsolidations[0]
		if lastQueueEntry.SrcValidator != nil && lastQueueEntry.SrcValidator.Validator != nil {
			withdrawableEpoch := lastQueueEntry.SrcValidator.Validator.WithdrawableEpoch
			pageData.QueueDurationEstimate = chainState.EpochToTime(withdrawableEpoch)
			pageData.HasQueueDuration = true
		}
	}

	// Only load data for the selected tab
	switch tabView {
	case "recent":
		// Load recent consolidations (canonical only)
		consolidationFilter := &services.CombinedConsolidationRequestFilter{
			Filter: &dbtypes.ConsolidationRequestFilter{
				WithOrphaned: 0,
			},
		}

		dbConsolidations, _, _ := services.GlobalBeaconService.GetConsolidationRequestsByFilter(ctx, consolidationFilter, 0, uint32(20))
		for _, consolidation := range dbConsolidations {
			consolidationData := &models.ConsolidationsPageDataRecentConsolidation{
				SourceAddr:      consolidation.SourceAddress(),
				SourcePublicKey: consolidation.SourcePubkey(),
				TargetPublicKey: consolidation.TargetPubkey(),
			}

			if sourceIndex := consolidation.SourceIndex(); sourceIndex != nil {
				consolidationData.SourceValidatorIndex = *sourceIndex
				consolidationData.SourceValidatorName = services.GlobalBeaconService.GetValidatorName(*sourceIndex)
				consolidationData.SourceValidatorValid = true
			}

			if targetIndex := consolidation.TargetIndex(); targetIndex != nil {
				consolidationData.TargetValidatorIndex = *targetIndex
				consolidationData.TargetValidatorName = services.GlobalBeaconService.GetValidatorName(*targetIndex)
				consolidationData.TargetValidatorValid = true
			}

			if request := consolidation.Request; request != nil {
				consolidationData.IsIncluded = true
				consolidationData.SlotNumber = request.SlotNumber
				consolidationData.SlotRoot = request.SlotRoot
				consolidationData.Time = chainState.SlotToTime(phase0.Slot(request.SlotNumber))
				consolidationData.Status = uint64(1)
				consolidationData.Result = request.Result
				consolidationData.ResultMessage = getConsolidationResultMessage(request.Result, chainState.GetSpecs())
			}

			if transaction := consolidation.Transaction; transaction != nil {
				consolidationData.TransactionHash = transaction.TxHash
				consolidationData.LinkedTransaction = true
				consolidationData.TxStatus = uint64(1)
				if consolidation.TransactionOrphaned {
					consolidationData.TxStatus = uint64(2)
				}
			}

			pageData.RecentConsolidations = append(pageData.RecentConsolidations, consolidationData)
		}
		pageData.RecentConsolidationCount = uint64(len(pageData.RecentConsolidations))

	case "queue":
		// Load consolidation queue
		queueConsolidations, _ := services.GlobalBeaconService.GetConsolidationQueueByFilter(ctx, &services.ConsolidationQueueFilter{}, 0, 20)
		for _, queueEntry := range queueConsolidations {
			queueData := &models.ConsolidationsPageDataQueuedConsolidation{}

			if queueEntry.SrcValidator != nil {
				queueData.SourceValidatorExists = true
				queueData.SourceValidatorIndex = uint64(queueEntry.SrcValidator.Index)
				queueData.SourceValidatorName = queueEntry.SrcValidatorName
				queueData.SourceEffectiveBalance = uint64(queueEntry.SrcValidator.Validator.EffectiveBalance)

				validator := services.GlobalBeaconService.GetValidatorByIndex(queueEntry.SrcValidator.Index, false)
				if strings.HasPrefix(validator.Status.String(), "pending") {
					queueData.SourceValidatorStatus = "Pending"
				} else if validator.Status == v1.ValidatorStateActiveOngoing {
					queueData.SourceValidatorStatus = "Active"
					queueData.ShowUpcheck = true
				} else if validator.Status == v1.ValidatorStateActiveExiting {
					queueData.SourceValidatorStatus = "Exiting"
					queueData.ShowUpcheck = true
				} else if validator.Status == v1.ValidatorStateActiveSlashed {
					queueData.SourceValidatorStatus = "Slashed"
					queueData.ShowUpcheck = true
				} else if validator.Status == v1.ValidatorStateExitedUnslashed {
					queueData.SourceValidatorStatus = "Exited"
				} else if validator.Status == v1.ValidatorStateExitedSlashed {
					queueData.SourceValidatorStatus = "Slashed"
				} else {
					queueData.SourceValidatorStatus = validator.Status.String()
				}

				if queueData.ShowUpcheck {
					queueData.UpcheckActivity = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
					queueData.UpcheckMaximum = uint8(3)
				}

				// Get public key from validator
				queueData.SourcePublicKey = queueEntry.SrcValidator.Validator.PublicKey[:]

				if queueEntry.SrcValidator.Validator.WithdrawableEpoch != math.MaxUint64 {
					queueData.EstimatedTime = chainState.EpochToTime(queueEntry.SrcValidator.Validator.WithdrawableEpoch)
				} else {
					// WithdrawableEpoch not set yet for pending consolidation
					queueData.EstimatedTime = time.Time{}
				}
			}

			if queueEntry.TgtValidator != nil {
				queueData.TargetValidatorExists = true
				queueData.TargetValidatorIndex = uint64(queueEntry.TgtValidator.Index)
				queueData.TargetValidatorName = queueEntry.TgtValidatorName

				// Get public key from validator
				queueData.TargetPublicKey = queueEntry.TgtValidator.Validator.PublicKey[:]
			}

			pageData.QueuedConsolidations = append(pageData.QueuedConsolidations, queueData)
		}
		pageData.QueuedTabCount = uint64(len(pageData.QueuedConsolidations))
	}

	return pageData, 1 * time.Minute
}
