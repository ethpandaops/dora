package handlers

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/services"
	"github.com/pk910/light-beaconchain-explorer/templates"
	"github.com/pk910/light-beaconchain-explorer/types/models"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

// Validator will return the main "validator" page using a go template
func Validator(w http.ResponseWriter, r *http.Request) {
	var validatorTemplateFiles = append(layoutTemplateFiles,
		"validator/validator.html",
		"validator/recentBlocks.html",
		"_svg/timeline.html",
	)
	var notfoundTemplateFiles = append(layoutTemplateFiles,
		"validator/notfound.html",
	)

	var pageTemplate = templates.GetTemplate(validatorTemplateFiles...)

	w.Header().Set("Content-Type", "text/html")
	data := InitPageData(w, r, "validators", "/validator", "Validator", validatorTemplateFiles)

	validatorSetRsp := services.GlobalBeaconService.GetCachedValidatorSet()
	var validator *rpctypes.ValidatorEntry
	if validatorSetRsp != nil {
		vars := mux.Vars(r)
		idxOrPubKey := strings.Replace(vars["idxOrPubKey"], "0x", "", -1)
		validatorPubKey, err := hex.DecodeString(idxOrPubKey)
		if err != nil || len(validatorPubKey) != 48 {
			// search by index^
			validatorIndex, err := strconv.ParseUint(vars["idxOrPubKey"], 10, 64)
			if err == nil && validatorIndex < uint64(len(validatorSetRsp.Data)) {
				validator = &validatorSetRsp.Data[validatorIndex]
			}
		} else {
			// search by pubkey
			for _, val := range validatorSetRsp.Data {
				if bytes.Equal(val.Validator.PubKey, validatorPubKey) {
					validator = &val
					break
				}
			}
		}
	}

	if validator == nil {
		data := InitPageData(w, r, "blockchain", "/validator", "Validator not found", notfoundTemplateFiles)
		if handleTemplateError(w, r, "validator.go", "Validator", "", templates.GetTemplate(notfoundTemplateFiles...).ExecuteTemplate(w, "layout", data)) != nil {
			return // an error has occurred and was processed
		}
		return
	}

	data.Data = getValidatorPageData(uint64(validator.Index))

	if handleTemplateError(w, r, "validators.go", "Validators", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return // an error has occurred and was processed
	}
}

func getValidatorPageData(validatorIndex uint64) *models.ValidatorPageData {
	pageData := &models.ValidatorPageData{}
	pageCacheKey := fmt.Sprintf("validator:%v", validatorIndex)
	pageData = services.GlobalFrontendCache.ProcessCachedPage(pageCacheKey, true, pageData, func(pageCall *services.FrontendCacheProcessingPage) interface{} {
		pageData, cacheTimeout := buildValidatorPageData(validatorIndex)
		pageCall.CacheTimeout = cacheTimeout
		return pageData
	}).(*models.ValidatorPageData)
	return pageData
}

func buildValidatorPageData(validatorIndex uint64) (*models.ValidatorPageData, time.Duration) {
	logrus.Printf("validator page called: %v", validatorIndex)

	validatorSetRsp := services.GlobalBeaconService.GetCachedValidatorSet()
	validator := validatorSetRsp.Data[validatorIndex]

	pageData := &models.ValidatorPageData{
		CurrentEpoch:        uint64(utils.TimeToEpoch(time.Now())),
		Index:               uint64(validator.Index),
		Name:                services.GlobalBeaconService.GetValidatorName(uint64(validator.Index)),
		PublicKey:           validator.Validator.PubKey,
		Balance:             uint64(validator.Balance),
		EffectiveBalance:    uint64(validator.Validator.EffectiveBalance),
		BeaconState:         validator.Status,
		WithdrawCredentials: validator.Validator.WithdrawalCredentials,
	}
	if strings.HasPrefix(validator.Status, "pending") {
		pageData.State = "Pending"
	} else if validator.Status == "active_ongoing" {
		pageData.State = "Active"
		pageData.IsActive = true
	} else if validator.Status == "active_exiting" {
		pageData.State = "Exiting"
		pageData.IsActive = true
	} else if validator.Status == "active_slashed" {
		pageData.State = "Slashed"
		pageData.IsActive = true
	} else if validator.Status == "exited_unslashed" {
		pageData.State = "Exited"
	} else if validator.Status == "exited_slashed" {
		pageData.State = "Slashed"
	} else {
		pageData.State = validator.Status
	}

	if pageData.IsActive {
		// load activity map
		activityMap, maxActivity := services.GlobalBeaconService.GetValidatorActivity()
		pageData.UpcheckActivity = activityMap[uint64(validator.Index)]
		pageData.UpcheckMaximum = uint8(maxActivity)
	}

	if validator.Validator.ActivationEligibilityEpoch < 18446744073709551615 {
		pageData.ShowEligible = true
		pageData.EligibleEpoch = uint64(validator.Validator.ActivationEligibilityEpoch)
		pageData.EligibleTs = utils.EpochToTime(uint64(validator.Validator.ActivationEligibilityEpoch))
	}
	if validator.Validator.ActivationEpoch < 18446744073709551615 {
		pageData.ShowActivation = true
		pageData.ActivationEpoch = uint64(validator.Validator.ActivationEpoch)
		pageData.ActivationTs = utils.EpochToTime(uint64(validator.Validator.ActivationEpoch))
	}
	if validator.Validator.ExitEpoch < 18446744073709551615 {
		pageData.ShowExit = true
		pageData.WasActive = true
		pageData.ExitEpoch = uint64(validator.Validator.ExitEpoch)
		pageData.ExitTs = utils.EpochToTime(uint64(validator.Validator.ExitEpoch))
	}
	if validator.Validator.WithdrawalCredentials[0] == 0x01 {
		pageData.ShowWithdrawAddress = true
		pageData.WithdrawAddress = validator.Validator.WithdrawalCredentials[12:]
	}

	// load latest blocks
	pageData.RecentBlocks = make([]*models.ValidatorPageDataBlocks, 0)
	blocksData := services.GlobalBeaconService.GetDbBlocksByProposer(validatorIndex, 0, 10, true, true)
	for _, blockData := range blocksData {
		blockStatus := 1
		if blockData.Block == nil {
			blockStatus = 0
		} else if blockData.Block.Orphaned {
			blockStatus = 2
		}
		blockEntry := models.ValidatorPageDataBlocks{
			Epoch:  utils.EpochOfSlot(blockData.Slot),
			Slot:   blockData.Slot,
			Ts:     utils.SlotToTime(blockData.Slot),
			Status: uint64(blockStatus),
		}
		if blockData.Block != nil {
			blockEntry.Graffiti = blockData.Block.Graffiti
			blockEntry.EthBlock = blockData.Block.EthBlockNumber
			blockEntry.BlockRoot = fmt.Sprintf("0x%x", blockData.Block.Root)
		}
		pageData.RecentBlocks = append(pageData.RecentBlocks, &blockEntry)
	}
	pageData.RecentBlockCount = uint64(len(pageData.RecentBlocks))

	return pageData, 10 * time.Minute
}
