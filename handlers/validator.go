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

func getValidatorPageData(validatorIndex uint64) *models.ValidatorsPageData {
	pageData := &models.ValidatorsPageData{}
	pageCacheKey := fmt.Sprintf("validator:%v", validatorIndex)
	if !utils.Config.Frontend.Debug && services.GlobalBeaconService.GetFrontendCache(pageCacheKey, pageData) == nil {
		logrus.Printf("validator page served from cache: %v", validatorIndex)
		return pageData
	}
	logrus.Printf("validator page called: %v", validatorIndex)

	// TODO

	if pageCacheKey != "" {
		services.GlobalBeaconService.SetFrontendCache(pageCacheKey, pageData, 10*time.Minute)
	}
	return pageData
}
