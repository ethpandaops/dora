package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	v1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/types/models"
	"github.com/sirupsen/logrus"
)

// ValidatorsOffline will return the filtered "validators offline" page using a go template
func ValidatorsOffline(w http.ResponseWriter, r *http.Request) {
	var pageTemplateFiles = append(layoutTemplateFiles,
		"validators_offline/validators_offline.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(pageTemplateFiles...)
	data := InitPageData(w, r, "validators", "/validators/offline", "Offline Validators", pageTemplateFiles)

	urlArgs := r.URL.Query()
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	var pageIdx uint64 = 0
	if urlArgs.Has("s") {
		pageIdx, _ = strconv.ParseUint(urlArgs.Get("s"), 10, 64)
	}

	var sortOrder string
	if urlArgs.Has("o") {
		sortOrder = urlArgs.Get("o")
	}
	if sortOrder == "" {
		sortOrder = "index"
	}

	var groupBy uint64
	if urlArgs.Has("group") {
		groupBy, _ = strconv.ParseUint(urlArgs.Get("group"), 10, 64)
	}
	if groupBy == 0 {
		if services.GlobalBeaconService.GetValidatorNamesCount() > 0 {
			groupBy = 3
		} else {
			groupBy = 1
		}
	}

	var groupKey string
	if urlArgs.Has("key") {
		groupKey = urlArgs.Get("key")
	}

	var pageError error
	pageError = services.GlobalCallRateLimiter.CheckCallLimit(r, 2)
	if pageError == nil {
		data.Data, pageError = getValidatorsOfflinePageData(pageIdx, pageSize, sortOrder, groupBy, groupKey)
	}
	if pageError != nil {
		handlePageError(w, r, pageError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	if handleTemplateError(w, r, "validators_offline.go", "ValidatorsOffline", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getValidatorsOfflinePageData(pageIdx uint64, pageSize uint64, sortOrder string, groupBy uint64, groupKey string) (*models.ValidatorsOfflinePageData, error) {
	pageData := &models.ValidatorsOfflinePageData{}
	pageCacheKey := fmt.Sprintf("validators_offline:%v:%v:%v:%v:%v", pageIdx, pageSize, sortOrder, groupBy, groupKey)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(processingPage *services.FrontendCacheProcessingPage) interface{} {
		processingPage.CacheTimeout = 10 * time.Second
		return buildValidatorsOfflinePageData(pageIdx, pageSize, sortOrder, groupBy, groupKey)
	})
	if pageErr == nil && pageRes != nil {
		resData, resOk := pageRes.(*models.ValidatorsOfflinePageData)
		if !resOk {
			return nil, ErrInvalidPageModel
		}
		pageData = resData
	}
	return pageData, pageErr
}

func buildValidatorsOfflinePageData(pageIdx uint64, pageSize uint64, sortOrder string, groupBy uint64, groupKey string) *models.ValidatorsOfflinePageData {
	chainState := services.GlobalBeaconService.GetChainState()

	filterArgs := url.Values{}
	filterArgs.Add("group", fmt.Sprintf("%v", groupBy))
	filterArgs.Add("key", groupKey)

	pageData := &models.ValidatorsOfflinePageData{
		ViewOptionGroupBy: groupBy,
		GroupKey:          groupKey,
	}
	logrus.Debugf("validators_offline page called: %v:%v [%v:%v]", pageIdx, pageSize, groupBy, groupKey)
	if pageIdx == 0 {
		pageData.IsDefaultPage = true
	}

	if pageSize > 100 {
		pageSize = 100
	}
	pageData.PageSize = pageSize
	pageData.CurrentPageIndex = pageIdx + 1
	if pageIdx >= 1 {
		pageData.PrevPageIndex = pageIdx
	}
	pageData.LastPageIndex = 0

	// get group name
	var groupName string
	switch groupBy {
	case 1:
		groupIdx, _ := strconv.ParseUint(groupKey, 10, 64)
		groupName = fmt.Sprintf("%v - %v", groupIdx*100000, (groupIdx+1)*100000)
	case 2:
		groupIdx, _ := strconv.ParseUint(groupKey, 10, 64)
		groupName = fmt.Sprintf("%v - %v", groupIdx*10000, (groupIdx+1)*10000)
	case 3:
		groupName = groupKey
	}
	pageData.GroupName = groupName

	// collect offline validators
	offlineIndices := []phase0.ValidatorIndex{}
	currentEpoch := services.GlobalBeaconService.GetChainState().CurrentEpoch()

	services.GlobalBeaconService.StreamActiveValidatorData(true, func(index phase0.ValidatorIndex, validatorFlags uint16, activeData *beacon.ValidatorData, validator *phase0.Validator) error {
		var validatorGroupKey string

		switch groupBy {
		case 1:
			groupIdx := index / 100000
			validatorGroupKey = fmt.Sprintf("%06d", groupIdx)
		case 2:
			groupIdx := index / 10000
			validatorGroupKey = fmt.Sprintf("%06d", groupIdx)
		case 3:
			validatorGroupKey = strings.ToLower(services.GlobalBeaconService.GetValidatorName(uint64(index)))
		}

		if validatorGroupKey != groupKey {
			return nil
		}

		if validatorFlags&beacon.ValidatorStatusSlashed != 0 {
			return nil
		}

		if activeData != nil && activeData.ActivationEpoch <= currentEpoch {
			if activeData.ExitEpoch > currentEpoch {
				votingActivity := services.GlobalBeaconService.GetValidatorLiveness(index, 3)

				if votingActivity == 0 {
					// This is an offline validator
					offlineIndices = append(offlineIndices, index)
				}
			}
		}

		return nil
	})

	// load validator set for offline indices
	validatorFilter := dbtypes.ValidatorFilter{
		Limit:   pageSize,
		Offset:  pageIdx * pageSize,
		Indices: offlineIndices,
	}
	// apply sort order
	switch sortOrder {
	case "index-d":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderIndexDesc
	case "pubkey":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderPubKeyAsc
	case "pubkey-d":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderPubKeyDesc
	case "balance":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderBalanceAsc
	case "balance-d":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderBalanceDesc
	case "activation":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderActivationEpochAsc
	case "activation-d":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderActivationEpochDesc
	case "exit":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderExitEpochAsc
	case "exit-d":
		validatorFilter.OrderBy = dbtypes.ValidatorOrderExitEpochDesc
	default:
		validatorFilter.OrderBy = dbtypes.ValidatorOrderIndexAsc
		pageData.IsDefaultSorting = true
		sortOrder = "index"
	}
	pageData.Sorting = sortOrder

	// get validator set
	var validatorSet []v1.Validator
	validatorSetRsp, validatorSetLen := services.GlobalBeaconService.GetFilteredValidatorSet(&validatorFilter, true)
	if len(validatorSetRsp) == 0 {
		validatorSet = []v1.Validator{}
	} else {
		validatorSet = validatorSetRsp
	}

	pageData.Validators = make([]*models.ValidatorsPageDataValidator, 0)

	for _, validator := range validatorSet {
		if validator.Validator == nil {
			continue
		}

		validatorData := &models.ValidatorsPageDataValidator{
			Index:            uint64(validator.Index),
			Name:             services.GlobalBeaconService.GetValidatorName(uint64(validator.Index)),
			PublicKey:        validator.Validator.PublicKey[:],
			Balance:          uint64(validator.Balance),
			EffectiveBalance: uint64(validator.Validator.EffectiveBalance),
		}
		if strings.HasPrefix(validator.Status.String(), "pending") {
			validatorData.State = "Pending"
		} else if validator.Status == v1.ValidatorStateActiveOngoing {
			validatorData.State = "Active"
			validatorData.ShowUpcheck = true
		} else if validator.Status == v1.ValidatorStateActiveExiting {
			validatorData.State = "Exiting"
			validatorData.ShowUpcheck = true
		} else if validator.Status == v1.ValidatorStateActiveSlashed {
			validatorData.State = "Slashed"
			validatorData.ShowUpcheck = true
		} else if validator.Status == v1.ValidatorStateExitedUnslashed {
			validatorData.State = "Exited"
		} else if validator.Status == v1.ValidatorStateExitedSlashed {
			validatorData.State = "Slashed"
		} else {
			validatorData.State = validator.Status.String()
		}

		if validatorData.ShowUpcheck {
			validatorData.UpcheckActivity = 0
			validatorData.UpcheckMaximum = uint8(3)
		}

		if validator.Validator.ActivationEpoch < 18446744073709551615 {
			validatorData.ShowActivation = true
			validatorData.ActivationEpoch = uint64(validator.Validator.ActivationEpoch)
			validatorData.ActivationTs = chainState.EpochToTime(validator.Validator.ActivationEpoch)
		}
		if validator.Validator.ExitEpoch < 18446744073709551615 {
			validatorData.ShowExit = true
			validatorData.ExitEpoch = uint64(validator.Validator.ExitEpoch)
			validatorData.ExitTs = chainState.EpochToTime(validator.Validator.ExitEpoch)
		}
		if validator.Validator.WithdrawalCredentials[0] == 0x01 || validator.Validator.WithdrawalCredentials[0] == 0x02 {
			validatorData.ShowWithdrawAddress = true
			validatorData.WithdrawAddress = validator.Validator.WithdrawalCredentials[12:]
		}

		pageData.Validators = append(pageData.Validators, validatorData)
	}

	pageData.ValidatorCount = validatorSetLen
	pageData.TotalPages = validatorSetLen / pageSize
	if validatorSetLen%pageSize != 0 {
		pageData.TotalPages++
	}
	pageData.FirstValidator = pageIdx * pageSize
	pageData.LastValidator = pageData.FirstValidator + uint64(len(pageData.Validators))

	if pageData.LastValidator < validatorSetLen {
		pageData.NextPageIndex = pageIdx + 1
	}
	if pageData.TotalPages > 1 && pageData.LastValidator < validatorSetLen {
		pageData.LastPageIndex = pageData.TotalPages - 1
	}

	if pageIdx > 0 {
		pageData.PrevPageIndex = pageIdx - 1
	}

	sortingArg := ""
	if sortOrder != "index" {
		sortingArg = fmt.Sprintf("&o=%v", sortOrder)
	}

	pageData.ViewPageLink = fmt.Sprintf("/validators/offline?%v&c=%v", filterArgs.Encode(), pageData.PageSize)
	pageData.FirstPageLink = fmt.Sprintf("/validators/offline?%v%v&c=%v", filterArgs.Encode(), sortingArg, pageData.PageSize)
	pageData.PrevPageLink = fmt.Sprintf("/validators/offline?%v%v&c=%v&s=%v", filterArgs.Encode(), sortingArg, pageData.PageSize, pageData.PrevPageIndex)
	pageData.NextPageLink = fmt.Sprintf("/validators/offline?%v%v&c=%v&s=%v", filterArgs.Encode(), sortingArg, pageData.PageSize, pageData.NextPageIndex)
	pageData.LastPageLink = fmt.Sprintf("/validators/offline?%v%v&c=%v&s=%v", filterArgs.Encode(), sortingArg, pageData.PageSize, pageData.LastPageIndex)

	return pageData
}
