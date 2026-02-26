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
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// Exits will return the main "exits" page using a go template
func Exits(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"exits/exits.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/exits", "Exits", templateFiles)

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
		data.Data, pageError = getExitsPageData(firstEpoch, pageSize, tabView)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")

	if r.URL.Query().Has("lazy") {
		// return the selected tab content only (lazy loaded)
		handleTemplateError(w, r, "exits.go", "Exits", "", pageTemplate.ExecuteTemplate(w, "lazyPage", data.Data))
	} else {
		handleTemplateError(w, r, "exits.go", "Exits", "", pageTemplate.ExecuteTemplate(w, "layout", data))
	}
}

func getExitsPageData(firstEpoch uint64, pageSize uint64, tabView string) (*models.ExitsPageData, error) {
	pageData := &models.ExitsPageData{}
	pageCacheKey := fmt.Sprintf("exits:%v:%v:%v", firstEpoch, pageSize, tabView)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildExitsPageData(pageCall.CallCtx, firstEpoch, pageSize, tabView)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ExitsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildExitsPageData(ctx context.Context, firstEpoch uint64, pageSize uint64, tabView string) (*models.ExitsPageData, time.Duration) {
	logrus.Debugf("exits page called: %v:%v:%v", firstEpoch, pageSize, tabView)
	chainState := services.GlobalBeaconService.GetChainState()

	pageData := &models.ExitsPageData{
		TabView: tabView,
	}

	// Get total exit count from voluntary exits
	voluntaryExitFilter := &dbtypes.VoluntaryExitFilter{
		WithOrphaned: 0, // Only canonical exits
	}
	_, totalExits := services.GlobalBeaconService.GetVoluntaryExitsByFilter(ctx, voluntaryExitFilter, 0, 1)
	pageData.TotalVoluntaryExitCount = totalExits

	// Get total exit count from requested exits
	zeroAmount := uint64(0)
	requestedExitFilter := &services.CombinedWithdrawalRequestFilter{
		Filter: &dbtypes.WithdrawalRequestFilter{
			MaxAmount:    &zeroAmount,
			WithOrphaned: 0,
		},
	}
	_, _, totalRequestedExits := services.GlobalBeaconService.GetWithdrawalRequestsByFilter(ctx, requestedExitFilter, 0, 1)
	pageData.TotalRequestedExitCount = totalRequestedExits

	// Get exiting validators (excluding consolidating validators)
	validatorFilter := &dbtypes.ValidatorFilter{
		Status: []v1.ValidatorState{
			v1.ValidatorStateActiveExiting,
		},
	}

	exitingValidators, _ := services.GlobalBeaconService.GetFilteredValidatorSet(ctx, validatorFilter, false)

	// Filter out consolidating validators and calculate stats
	nonConsolidatingValidators := make([]*v1.Validator, 0)
	var exitingAmount uint64
	var latestExitEpoch phase0.Epoch

	epochStats, _ := services.GlobalBeaconService.GetRecentEpochStats(nil)
	consolidatingValidators := make(map[phase0.ValidatorIndex]bool)
	if epochStats != nil {
		for _, consolidation := range epochStats.PendingConsolidations {
			consolidatingValidators[consolidation.SourceIndex] = true
		}
	}

	for _, validator := range exitingValidators {
		// Skip if this validator is consolidating
		if consolidatingValidators[validator.Index] {
			continue
		}

		nonConsolidatingValidators = append(nonConsolidatingValidators, &validator)
		exitingAmount += uint64(validator.Validator.EffectiveBalance)

		if validator.Validator.ExitEpoch > latestExitEpoch {
			latestExitEpoch = validator.Validator.ExitEpoch
		}
	}

	pageData.ExitingValidatorCount = uint64(len(nonConsolidatingValidators))
	pageData.ExitingAmount = exitingAmount

	// Calculate queue duration estimation based on the latest exit epoch
	if len(nonConsolidatingValidators) > 0 && latestExitEpoch != math.MaxUint64 {
		pageData.QueueDurationEstimate = chainState.EpochToTime(latestExitEpoch)
		pageData.HasQueueDuration = true
	}

	// Only load data for the selected tab
	switch tabView {
	case "recent":
		// Load recent exits (canonical only)
		dbVoluntaryExits, _ := services.GlobalBeaconService.GetVoluntaryExitsByFilter(ctx, voluntaryExitFilter, 0, uint32(20))
		for _, voluntaryExit := range dbVoluntaryExits {
			exitData := &models.ExitsPageDataRecentExit{
				SlotNumber: voluntaryExit.SlotNumber,
				SlotRoot:   voluntaryExit.SlotRoot,
				Time:       chainState.SlotToTime(phase0.Slot(voluntaryExit.SlotNumber)),
				Orphaned:   voluntaryExit.Orphaned,
			}

			// Check if this is a builder exit (validator index has BuilderIndexFlag set)
			if voluntaryExit.ValidatorIndex&services.BuilderIndexFlag != 0 {
				builderIndex := voluntaryExit.ValidatorIndex &^ services.BuilderIndexFlag
				exitData.IsBuilder = true
				exitData.ValidatorIndex = builderIndex

				// Resolve builder name via validatornames service (with BuilderIndexFlag)
				exitData.ValidatorName = services.GlobalBeaconService.GetValidatorName(voluntaryExit.ValidatorIndex)

				builder := services.GlobalBeaconService.GetBuilderByIndex(gloas.BuilderIndex(builderIndex))
				if builder == nil {
					exitData.ValidatorStatus = "Unknown"
				} else {
					exitData.PublicKey = builder.PublicKey[:]

					// Determine builder status
					currentEpoch := chainState.CurrentEpoch()
					if builder.WithdrawableEpoch <= currentEpoch {
						exitData.ValidatorStatus = "Exited"
					} else {
						exitData.ValidatorStatus = "Exiting"
					}
				}
			} else {
				// Regular validator exit
				exitData.ValidatorIndex = voluntaryExit.ValidatorIndex
				exitData.ValidatorName = services.GlobalBeaconService.GetValidatorName(voluntaryExit.ValidatorIndex)

				validator := services.GlobalBeaconService.GetValidatorByIndex(phase0.ValidatorIndex(voluntaryExit.ValidatorIndex), false)
				if validator == nil {
					exitData.ValidatorStatus = "Unknown"
				} else {
					exitData.PublicKey = validator.Validator.PublicKey[:]
					exitData.WithdrawalCreds = validator.Validator.WithdrawalCredentials

					if strings.HasPrefix(validator.Status.String(), "pending") {
						exitData.ValidatorStatus = "Pending"
					} else if validator.Status == v1.ValidatorStateActiveOngoing {
						exitData.ValidatorStatus = "Active"
						exitData.ShowUpcheck = true
					} else if validator.Status == v1.ValidatorStateActiveExiting {
						exitData.ValidatorStatus = "Exiting"
						exitData.ShowUpcheck = true
					} else if validator.Status == v1.ValidatorStateActiveSlashed {
						exitData.ValidatorStatus = "Slashed"
						exitData.ShowUpcheck = true
					} else if validator.Status == v1.ValidatorStateExitedUnslashed {
						exitData.ValidatorStatus = "Exited"
					} else if validator.Status == v1.ValidatorStateExitedSlashed {
						exitData.ValidatorStatus = "Slashed"
					} else {
						exitData.ValidatorStatus = validator.Status.String()
					}

					if exitData.ShowUpcheck {
						exitData.UpcheckActivity = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
						exitData.UpcheckMaximum = uint8(3)
					}
				}
			}

			pageData.RecentExits = append(pageData.RecentExits, exitData)
		}
		pageData.RecentExitCount = uint64(len(pageData.RecentExits))

	case "exiting":
		// Load exiting validators (limit to first 20 for tab)
		count := uint64(len(nonConsolidatingValidators))
		if count > 20 {
			count = 20
		}

		for i := uint64(0); i < count; i++ {
			validator := nonConsolidatingValidators[i]

			exitingData := &models.ExitsPageDataExitingValidator{
				ValidatorIndex:   uint64(validator.Index),
				ValidatorName:    services.GlobalBeaconService.GetValidatorName(uint64(validator.Index)),
				PublicKey:        validator.Validator.PublicKey[:],
				WithdrawalCreds:  validator.Validator.WithdrawalCredentials,
				EffectiveBalance: uint64(validator.Validator.EffectiveBalance),
				ExitEpoch:        uint64(validator.Validator.ExitEpoch),
			}

			if strings.HasPrefix(validator.Status.String(), "pending") {
				exitingData.ValidatorStatus = "Pending"
			} else if validator.Status == v1.ValidatorStateActiveOngoing {
				exitingData.ValidatorStatus = "Active"
				exitingData.ShowUpcheck = true
			} else if validator.Status == v1.ValidatorStateActiveExiting {
				exitingData.ValidatorStatus = "Exiting"
				exitingData.ShowUpcheck = true
			} else if validator.Status == v1.ValidatorStateActiveSlashed {
				exitingData.ValidatorStatus = "Slashed"
				exitingData.ShowUpcheck = true
			} else if validator.Status == v1.ValidatorStateExitedUnslashed {
				exitingData.ValidatorStatus = "Exited"
			} else if validator.Status == v1.ValidatorStateExitedSlashed {
				exitingData.ValidatorStatus = "Slashed"
			} else {
				exitingData.ValidatorStatus = validator.Status.String()
			}

			if exitingData.ShowUpcheck {
				exitingData.UpcheckActivity = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
				exitingData.UpcheckMaximum = uint8(3)
			}

			if validator.Validator.ExitEpoch != math.MaxUint64 {
				exitingData.EstimatedTime = chainState.EpochToTime(validator.Validator.ExitEpoch)
			}

			pageData.ExitingValidators = append(pageData.ExitingValidators, exitingData)
		}
		pageData.ExitingValidatorTabCount = uint64(len(pageData.ExitingValidators))
	}

	return pageData, 1 * time.Minute
}
