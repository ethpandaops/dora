package handlers

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/pk910/light-beaconchain-explorer/types/models"
	"github.com/pk910/light-beaconchain-explorer/utils"
	"github.com/sirupsen/logrus"
)

// Validators will return the main "validators" page using a go template
func Validators(w http.ResponseWriter, r *http.Request) {
	var validatorsTemplateFiles = append(layoutTemplateFiles,
		"validators/validators.html",
		"_svg/professor.html",
	)

	var pageTemplate = templates.GetTemplate(validatorsTemplateFiles...)

	w.Header().Set("Content-Type", "text/html")
	data := InitPageData(w, r, "validators", "/validators", "Validators", validatorsTemplateFiles)

	urlArgs := r.URL.Query()
	var firstIdx uint64 = 0
	if urlArgs.Has("s") {
		firstIdx, _ = strconv.ParseUint(urlArgs.Get("s"), 10, 64)
	}
	var pageSize uint64 = 50
	if urlArgs.Has("c") {
		pageSize, _ = strconv.ParseUint(urlArgs.Get("c"), 10, 64)
	}
	var filterPubKey string
	var filterIndex string
	var filterName string
	var filterStatus string
	if urlArgs.Has("f") {
		if urlArgs.Has("f.pubkey") {
			filterPubKey = urlArgs.Get("f.pubkey")
		}
		if urlArgs.Has("f.index") {
			filterIndex = urlArgs.Get("f.index")
		}
		if urlArgs.Has("f.name") {
			filterName = urlArgs.Get("f.name")
		}
		if urlArgs.Has("f.status") {
			filterStatus = strings.Join(urlArgs["f.status"], ",")
		}
	}
	var sortOrder string
	if urlArgs.Has("o") {
		sortOrder = urlArgs.Get("o")
	}
	data.Data = getValidatorsPageData(firstIdx, pageSize, sortOrder, filterPubKey, filterIndex, filterName, filterStatus)

	if handleTemplateError(w, r, "validators.go", "Validators", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getValidatorsPageData(firstValIdx uint64, pageSize uint64, sortOrder string, filterPubKey string, filterIndex string, filterName string, filterStatus string) *models.ValidatorsPageData {
	pageData := &models.ValidatorsPageData{}
	pageCacheKey := fmt.Sprintf("validators:%v:%v:%v:%v:%v:%v:%v", firstValIdx, pageSize, sortOrder, filterPubKey, filterIndex, filterName, filterStatus)
	pageData = services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildValidatorsPageData(firstValIdx, pageSize, sortOrder, filterPubKey, filterIndex, filterName, filterStatus)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	}).(*models.ValidatorsPageData)
	return pageData
}

func buildValidatorsPageData(firstValIdx uint64, pageSize uint64, sortOrder string, filterPubKey string, filterIndex string, filterName string, filterStatus string) (*models.ValidatorsPageData, time.Duration) {
	logrus.Debugf("validators page called: %v:%v:%v:%v:%v:%v:%v", firstValIdx, pageSize, sortOrder, filterPubKey, filterIndex, filterName, filterStatus)
	pageData := &models.ValidatorsPageData{}
	cacheTime := 10 * time.Minute

	// get latest validator set
	var validatorSet []*rpctypes.ValidatorEntry
	validatorSetRsp := services.GlobalBeaconService.GetCachedValidatorSet()
	if validatorSetRsp == nil {
		cacheTime = 5 * time.Minute
		validatorSet = []*rpctypes.ValidatorEntry{}
	} else {
		validatorSet = validatorSetRsp.Data
	}

	// get status options
	statusMap := map[string]uint64{}
	for _, val := range validatorSet {
		statusMap[val.Status]++
	}
	pageData.FilterStatusOpts = make([]models.ValidatorsPageDataStatusOption, 0)
	for status, count := range statusMap {
		pageData.FilterStatusOpts = append(pageData.FilterStatusOpts, models.ValidatorsPageDataStatusOption{
			Status: status,
			Count:  count,
		})
	}
	sort.Slice(pageData.FilterStatusOpts, func(a, b int) bool {
		return strings.Compare(pageData.FilterStatusOpts[a].Status, pageData.FilterStatusOpts[b].Status) < 0
	})

	filterArgs := url.Values{}
	if filterPubKey != "" || filterIndex != "" || filterName != "" || filterStatus != "" {
		var filterPubKeyVal []byte
		var filterIndexVal uint64
		var filterStatusVal []string

		if filterPubKey != "" {
			filterArgs.Add("f.pubkey", filterPubKey)
			filterPubKeyVal, _ = hex.DecodeString(strings.Replace(filterPubKey, "0x", "", -1))
		}
		if filterIndex != "" {
			filterArgs.Add("f.index", filterIndex)
			filterIndexVal, _ = strconv.ParseUint(filterIndex, 10, 64)
		}
		if filterName != "" {
			filterArgs.Add("f.name", filterName)
		}
		if filterStatus != "" {
			filterArgs.Add("f.status", filterStatus)
			filterStatusVal = strings.Split(filterStatus, ",")
		}

		// apply filter
		filteredValidatorSet := make([]*rpctypes.ValidatorEntry, 0)
		for _, val := range validatorSet {
			if filterPubKey != "" && !bytes.Equal(filterPubKeyVal, val.Validator.PubKey) {
				continue
			}
			if filterIndex != "" && filterIndexVal != uint64(val.Index) {
				continue
			}
			if filterName != "" {
				valName := services.GlobalBeaconService.GetValidatorName(uint64(val.Index))
				if !strings.Contains(valName, filterName) {
					continue
				}
			}
			if filterStatus != "" && !utils.SliceContains(filterStatusVal, val.Status) {
				continue
			}
			filteredValidatorSet = append(filteredValidatorSet, val)
		}
		validatorSet = filteredValidatorSet
	}
	pageData.FilterPubKey = filterPubKey
	pageData.FilterIndex = filterIndex
	pageData.FilterName = filterName
	pageData.FilterStatus = filterStatus

	// apply sort order
	validatorSetLen := len(validatorSet)
	if sortOrder == "" {
		sortOrder = "index"
	}
	if sortOrder != "index" {
		sortedValidatorSet := make([]*rpctypes.ValidatorEntry, validatorSetLen)
		copy(sortedValidatorSet, validatorSet)

		switch sortOrder {
		case "index-d":
			sort.Slice(sortedValidatorSet, func(a, b int) bool {
				return sortedValidatorSet[a].Index > sortedValidatorSet[b].Index
			})
		case "pubkey":
			sort.Slice(sortedValidatorSet, func(a, b int) bool {
				return bytes.Compare(sortedValidatorSet[a].Validator.PubKey, sortedValidatorSet[b].Validator.PubKey) < 0
			})
		case "pubkey-d":
			sort.Slice(sortedValidatorSet, func(a, b int) bool {
				return bytes.Compare(sortedValidatorSet[a].Validator.PubKey, sortedValidatorSet[b].Validator.PubKey) > 0
			})
		case "balance":
			sort.Slice(sortedValidatorSet, func(a, b int) bool {
				return sortedValidatorSet[a].Balance < sortedValidatorSet[b].Balance
			})
		case "balance-d":
			sort.Slice(sortedValidatorSet, func(a, b int) bool {
				return sortedValidatorSet[a].Balance > sortedValidatorSet[b].Balance
			})
		case "activation":
			sort.Slice(sortedValidatorSet, func(a, b int) bool {
				return sortedValidatorSet[a].Validator.ActivationEpoch < sortedValidatorSet[b].Validator.ActivationEpoch
			})
		case "activation-d":
			sort.Slice(sortedValidatorSet, func(a, b int) bool {
				return sortedValidatorSet[a].Validator.ActivationEpoch > sortedValidatorSet[b].Validator.ActivationEpoch
			})
		case "exit":
			sort.Slice(sortedValidatorSet, func(a, b int) bool {
				return sortedValidatorSet[a].Validator.ExitEpoch < sortedValidatorSet[b].Validator.ExitEpoch
			})
		case "exit-d":
			sort.Slice(sortedValidatorSet, func(a, b int) bool {
				return sortedValidatorSet[a].Validator.ExitEpoch > sortedValidatorSet[b].Validator.ExitEpoch
			})
		}
		validatorSet = sortedValidatorSet
	} else {
		pageData.IsDefaultSorting = true
	}
	pageData.Sorting = sortOrder

	totalValidatorCount := uint64(validatorSetLen)
	if firstValIdx == 0 {
		pageData.IsDefaultPage = true
	} else if firstValIdx > totalValidatorCount {
		firstValIdx = totalValidatorCount
	}

	if pageSize > 100 {
		pageSize = 100
	}

	pagesBefore := firstValIdx / pageSize
	if (firstValIdx % pageSize) > 0 {
		pagesBefore++
	}
	pagesAfter := (totalValidatorCount - firstValIdx) / pageSize
	if ((totalValidatorCount - firstValIdx) % pageSize) > 0 {
		pagesAfter++
	}
	pageData.PageSize = pageSize
	pageData.TotalPages = pagesBefore + pagesAfter
	pageData.CurrentPageIndex = pagesBefore + 1
	pageData.CurrentPageValIdx = firstValIdx
	if pagesBefore > 0 {
		pageData.PrevPageIndex = pageData.CurrentPageIndex - 1
		pageData.PrevPageValIdx = pageData.CurrentPageValIdx - pageSize
	}
	if pagesAfter > 1 {
		pageData.NextPageIndex = pageData.CurrentPageIndex + 1
		pageData.NextPageValIdx = pageData.CurrentPageValIdx + pageSize
	}
	pageData.LastPageValIdx = totalValidatorCount - pageSize

	// load activity map
	activityMap, maxActivity := services.GlobalBeaconService.GetValidatorActivity()

	// get validators
	lastValIdx := firstValIdx + pageSize
	if lastValIdx >= totalValidatorCount {
		lastValIdx = totalValidatorCount
	}
	pageData.Validators = make([]*models.ValidatorsPageDataValidator, 0)

	for _, validator := range validatorSet[firstValIdx:lastValIdx] {
		validatorData := &models.ValidatorsPageDataValidator{
			Index:            uint64(validator.Index),
			Name:             services.GlobalBeaconService.GetValidatorName(uint64(validator.Index)),
			PublicKey:        validator.Validator.PubKey,
			Balance:          uint64(validator.Balance),
			EffectiveBalance: uint64(validator.Validator.EffectiveBalance),
		}
		if strings.HasPrefix(validator.Status, "pending") {
			validatorData.State = "Pending"
		} else if validator.Status == "active_ongoing" {
			validatorData.State = "Active"
			validatorData.ShowUpcheck = true
		} else if validator.Status == "active_exiting" {
			validatorData.State = "Exiting"
			validatorData.ShowUpcheck = true
		} else if validator.Status == "active_slashed" {
			validatorData.State = "Slashed"
			validatorData.ShowUpcheck = true
		} else if validator.Status == "exited_unslashed" {
			validatorData.State = "Exited"
		} else if validator.Status == "exited_slashed" {
			validatorData.State = "Slashed"
		} else {
			validatorData.State = validator.Status
		}

		if validatorData.ShowUpcheck {
			validatorData.UpcheckActivity = activityMap[uint64(validator.Index)]
			validatorData.UpcheckMaximum = uint8(maxActivity)
		}

		if validator.Validator.ActivationEpoch < 18446744073709551615 {
			validatorData.ShowActivation = true
			validatorData.ActivationEpoch = uint64(validator.Validator.ActivationEpoch)
			validatorData.ActivationTs = utils.EpochToTime(uint64(validator.Validator.ActivationEpoch))
		}
		if validator.Validator.ExitEpoch < 18446744073709551615 {
			validatorData.ShowExit = true
			validatorData.ExitEpoch = uint64(validator.Validator.ExitEpoch)
			validatorData.ExitTs = utils.EpochToTime(uint64(validator.Validator.ExitEpoch))
		}
		if validator.Validator.WithdrawalCredentials[0] == 0x01 {
			validatorData.ShowWithdrawAddress = true
			validatorData.WithdrawAddress = validator.Validator.WithdrawalCredentials[12:]
		}

		pageData.Validators = append(pageData.Validators, validatorData)
	}
	pageData.ValidatorCount = uint64(len(pageData.Validators))
	pageData.FirstValidator = firstValIdx
	pageData.LastValidator = lastValIdx
	pageData.FilteredPageLink = fmt.Sprintf("/validators?f&%v&c=%v", filterArgs.Encode(), pageData.PageSize)

	return pageData, cacheTime
}
