package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// VoluntaryExits will return the filtered "voluntary_exits" page using a go template
func VoluntaryExits(w http.ResponseWriter, r *http.Request) {
	var templateFiles = append(layoutTemplateFiles,
		"voluntary_exits/voluntary_exits.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(templateFiles...)
	data := InitPageData(w, r, "validators", "/validators/voluntary_exits", "Voluntary Exits", templateFiles)

	urlArgs := r.URL.Query()
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	var pageIdx uint64 = 1
	if urlArgs.Has("p") {
		pageIdx, _ = strconv.ParseUint(urlArgs.Get("p"), 10, 64)
		if pageIdx < 1 {
			pageIdx = 1
		}
	}

	var minSlot uint64
	var maxSlot uint64
	var minIndex uint64
	var maxIndex uint64
	var vname string
	var withOrphaned uint64

	if urlArgs.Has("f") {
		if urlArgs.Has("f.mins") {
			minSlot, _ = strconv.ParseUint(urlArgs.Get("f.mins"), 10, 64)
		}
		if urlArgs.Has("f.maxs") {
			maxSlot, _ = strconv.ParseUint(urlArgs.Get("f.maxs"), 10, 64)
		}
		if urlArgs.Has("f.mini") {
			minIndex, _ = strconv.ParseUint(urlArgs.Get("f.mini"), 10, 64)
		}
		if urlArgs.Has("f.maxi") {
			maxIndex, _ = strconv.ParseUint(urlArgs.Get("f.maxi"), 10, 64)
		}
		if urlArgs.Has("f.vname") {
			vname = urlArgs.Get("f.vname")
		}
		if urlArgs.Has("f.orphaned") {
			withOrphaned, _ = strconv.ParseUint(urlArgs.Get("f.orphaned"), 10, 64)
		}
	} else {
		withOrphaned = 1
	}
	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getFilteredVoluntaryExitsPageData(pageIdx, pageSize, minSlot, maxSlot, minIndex, maxIndex, vname, uint8(withOrphaned))
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "voluntary_exits.go", "VoluntaryExits", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getFilteredVoluntaryExitsPageData(pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, minIndex uint64, maxIndex uint64, vname string, withOrphaned uint8) (*models.VoluntaryExitsPageData, error) {
	pageData := &models.VoluntaryExitsPageData{}
	pageCacheKey := fmt.Sprintf("voluntary_exits:%v:%v:%v:%v:%v:%v:%v:%v", pageIdx, pageSize, minSlot, maxSlot, minIndex, maxIndex, vname, withOrphaned)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		return buildFilteredVoluntaryExitsPageData(pageCall.CallCtx, pageIdx, pageSize, minSlot, maxSlot, minIndex, maxIndex, vname, withOrphaned)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.VoluntaryExitsPageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildFilteredVoluntaryExitsPageData(ctx context.Context, pageIdx uint64, pageSize uint64, minSlot uint64, maxSlot uint64, minIndex uint64, maxIndex uint64, vname string, withOrphaned uint8) *models.VoluntaryExitsPageData {
	filterArgs := url.Values{}
	if minSlot != 0 {
		filterArgs.Add("f.mins", fmt.Sprintf("%v", minSlot))
	}
	if maxSlot != 0 {
		filterArgs.Add("f.maxs", fmt.Sprintf("%v", maxSlot))
	}
	if minIndex != 0 {
		filterArgs.Add("f.mini", fmt.Sprintf("%v", minIndex))
	}
	if maxIndex != 0 {
		filterArgs.Add("f.maxi", fmt.Sprintf("%v", maxIndex))
	}
	if vname != "" {
		filterArgs.Add("f.vname", vname)
	}
	if withOrphaned != 0 {
		filterArgs.Add("f.orphaned", fmt.Sprintf("%v", withOrphaned))
	}

	pageData := &models.VoluntaryExitsPageData{
		FilterMinSlot:       minSlot,
		FilterMaxSlot:       maxSlot,
		FilterMinIndex:      minIndex,
		FilterMaxIndex:      maxIndex,
		FilterValidatorName: vname,
		FilterWithOrphaned:  withOrphaned,
	}
	logrus.Debugf("voluntary_exits page called: %v:%v [%v,%v,%v,%v,%v]", pageIdx, pageSize, minSlot, maxSlot, minIndex, maxIndex, vname)
	if pageIdx == 1 {
		pageData.IsDefaultPage = true
	}

	if pageSize > 100 {
		pageSize = 100
	}
	pageData.PageSize = pageSize
	pageData.TotalPages = pageIdx
	pageData.CurrentPageIndex = pageIdx
	if pageIdx > 1 {
		pageData.PrevPageIndex = pageIdx - 1
	}

	// load voluntary exits
	voluntaryExitFilter := &dbtypes.VoluntaryExitFilter{
		MinSlot:       minSlot,
		MaxSlot:       maxSlot,
		MinIndex:      minIndex,
		MaxIndex:      maxIndex,
		ValidatorName: vname,
		WithOrphaned:  withOrphaned,
	}

	dbVoluntaryExits, totalRows := services.GlobalBeaconService.GetVoluntaryExitsByFilter(ctx, voluntaryExitFilter, pageIdx-1, uint32(pageSize))

	chainState := services.GlobalBeaconService.GetChainState()

	for _, voluntaryExit := range dbVoluntaryExits {
		voluntaryExitData := &models.VoluntaryExitsPageDataExit{
			SlotNumber:      voluntaryExit.SlotNumber,
			SlotRoot:        voluntaryExit.SlotRoot,
			Time:            chainState.SlotToTime(phase0.Slot(voluntaryExit.SlotNumber)),
			Orphaned:        voluntaryExit.Orphaned,
			ValidatorStatus: "",
		}

		// Check if this is a builder exit (validator index has BuilderIndexFlag set)
		if voluntaryExit.ValidatorIndex&services.BuilderIndexFlag != 0 {
			builderIndex := voluntaryExit.ValidatorIndex &^ services.BuilderIndexFlag
			voluntaryExitData.IsBuilder = true
			voluntaryExitData.ValidatorIndex = builderIndex

			// Resolve builder name via validatornames service (with BuilderIndexFlag)
			voluntaryExitData.ValidatorName = services.GlobalBeaconService.GetValidatorName(voluntaryExit.ValidatorIndex)

			builder := services.GlobalBeaconService.GetBuilderByIndex(gloas.BuilderIndex(builderIndex))
			if builder == nil {
				voluntaryExitData.ValidatorStatus = "Unknown"
			} else {
				voluntaryExitData.PublicKey = builder.PublicKey[:]

				// Determine builder status
				currentEpoch := chainState.CurrentEpoch()
				if builder.WithdrawableEpoch <= currentEpoch {
					voluntaryExitData.ValidatorStatus = "Exited"
				} else {
					voluntaryExitData.ValidatorStatus = "Exiting"
				}
			}
		} else {
			// Regular validator exit
			voluntaryExitData.ValidatorIndex = voluntaryExit.ValidatorIndex
			voluntaryExitData.ValidatorName = services.GlobalBeaconService.GetValidatorName(voluntaryExit.ValidatorIndex)

			validator := services.GlobalBeaconService.GetValidatorByIndex(phase0.ValidatorIndex(voluntaryExit.ValidatorIndex), false)
			if validator == nil {
				voluntaryExitData.ValidatorStatus = "Unknown"
			} else {
				voluntaryExitData.PublicKey = validator.Validator.PublicKey[:]
				voluntaryExitData.WithdrawalCreds = validator.Validator.WithdrawalCredentials

				if strings.HasPrefix(validator.Status.String(), "pending") {
					voluntaryExitData.ValidatorStatus = "Pending"
				} else if validator.Status == v1.ValidatorStateActiveOngoing {
					voluntaryExitData.ValidatorStatus = "Active"
					voluntaryExitData.ShowUpcheck = true
				} else if validator.Status == v1.ValidatorStateActiveExiting {
					voluntaryExitData.ValidatorStatus = "Exiting"
					voluntaryExitData.ShowUpcheck = true
				} else if validator.Status == v1.ValidatorStateActiveSlashed {
					voluntaryExitData.ValidatorStatus = "Slashed"
					voluntaryExitData.ShowUpcheck = true
				} else if validator.Status == v1.ValidatorStateExitedUnslashed {
					voluntaryExitData.ValidatorStatus = "Exited"
				} else if validator.Status == v1.ValidatorStateExitedSlashed {
					voluntaryExitData.ValidatorStatus = "Slashed"
				} else {
					voluntaryExitData.ValidatorStatus = validator.Status.String()
				}

				if voluntaryExitData.ShowUpcheck {
					voluntaryExitData.UpcheckActivity = uint8(services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3))
					voluntaryExitData.UpcheckMaximum = uint8(3)
				}
			}
		}

		pageData.VoluntaryExits = append(pageData.VoluntaryExits, voluntaryExitData)
	}
	pageData.ExitCount = uint64(len(pageData.VoluntaryExits))

	if pageData.ExitCount > 0 {
		pageData.FirstIndex = pageData.VoluntaryExits[0].SlotNumber
		pageData.LastIndex = pageData.VoluntaryExits[pageData.ExitCount-1].SlotNumber
	}

	pageData.TotalPages = totalRows / pageSize
	if totalRows%pageSize > 0 {
		pageData.TotalPages++
	}
	pageData.LastPageIndex = pageData.TotalPages
	if pageIdx < pageData.TotalPages {
		pageData.NextPageIndex = pageIdx + 1
	}

	// Populate UrlParams for page jump functionality
	pageData.UrlParams = make(map[string]string)
	for key, values := range filterArgs {
		if len(values) > 0 {
			pageData.UrlParams[key] = values[0]
		}
	}
	pageData.UrlParams["c"] = fmt.Sprintf("%v", pageData.PageSize)

	pageData.FirstPageLink = fmt.Sprintf("/validators/voluntary_exits?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/validators/voluntary_exits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/validators/voluntary_exits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/validators/voluntary_exits?f&%v&c=%v&p=%v", filterArgs.Encode(), pageData.PageSize, pageData.LastPageIndex)

	return pageData
}
