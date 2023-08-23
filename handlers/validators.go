package handlers

import (
	"fmt"
	"net/http"
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
	var stateFilter string
	if urlArgs.Has("q") {
		stateFilter = urlArgs.Get("q")
	}
	data.Data = getValidatorsPageData(firstIdx, pageSize, stateFilter)

	if handleTemplateError(w, r, "validators.go", "Validators", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getValidatorsPageData(firstValIdx uint64, pageSize uint64, stateFilter string) *models.ValidatorsPageData {
	pageData := &models.ValidatorsPageData{}
	pageCacheKey := fmt.Sprintf("validators:%v:%v:%v", firstValIdx, pageSize, stateFilter)
	pageData = services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildValidatorsPageData(firstValIdx, pageSize, stateFilter)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	}).(*models.ValidatorsPageData)
	return pageData
}

func buildValidatorsPageData(firstValIdx uint64, pageSize uint64, stateFilter string) (*models.ValidatorsPageData, time.Duration) {
	logrus.Printf("validators page called: %v:%v:%v", firstValIdx, pageSize, stateFilter)
	pageData := &models.ValidatorsPageData{}
	cacheTime := 10 * time.Minute

	// get latest validator set
	var validatorSet []rpctypes.ValidatorEntry
	validatorSetRsp := services.GlobalBeaconService.GetCachedValidatorSet()
	if validatorSetRsp == nil {
		cacheTime = 5 * time.Minute
		validatorSet = []rpctypes.ValidatorEntry{}
	} else {
		validatorSet = validatorSetRsp.Data
	}

	//if stateFilter != "" {
	// TODO: apply filter
	//}

	totalValidatorCount := uint64(len(validatorSet))
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

	return pageData, cacheTime
}
