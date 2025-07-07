package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type ApiValidatorResponseV1 struct {
	Activationeligibilityepoch uint64 `json:"activationeligibilityepoch"`
	Activationepoch            uint64 `json:"activationepoch"`
	Balance                    uint64 `json:"balance"`
	Effectivebalance           uint64 `json:"effectivebalance"`
	Exitepoch                  uint64 `json:"exitepoch"`
	Isonline                   bool   `json:"isonline"`
	Name                       string `json:"name"`
	Pubkey                     string `json:"pubkey"`
	Slashed                    bool   `json:"slashed"`
	Status                     string `json:"status"`
	Validatorindex             uint64 `json:"validatorindex"`
	Withdrawableepoch          uint64 `json:"withdrawableepoch"`
	Withdrawalcredentials      string `json:"withdrawalcredentials"`
}

type ApiValidatorRequestV1 struct {
	IndicesOrPubKey string `json:"indicesOrPubkey"`
}

// ApiValidator godoc
// @Summary Get up to 100 validators
// @Tags Validator
// @Description Searching for too many validators based on their pubkeys will lead to a "URI too long" error
// @Produce  json
// @Param  indexOrPubkey path string true "Up to 100 validator indicesOrPubkeys, comma separated"
// @Success 200 {object} ApiResponse{data=[]ApiValidatorResponseV1}
// @Failure 400 {object} ApiResponse
// @Router /validator/{indexOrPubkey} [get]
func ApiValidatorGetV1(w http.ResponseWriter, r *http.Request) {
	getApiValidator(w, r)
}

// ApiValidator godoc
// @Summary Get up to 100 validators
// @Tags Validator
// @Description This POST endpoint exists because the GET endpoint can lead to a "URI too long" error when searching for too many validators based on their pubkeys.
// @Produce  json
// @Param  indexOrPubkey body ApiValidatorRequestV1 true "Up to 100 validator indicesOrPubkeys, comma separated"
// @Success 200 {object} ApiResponse{data=[]ApiValidatorResponseV1}
// @Failure 400 {object} ApiResponse
// @Router /validator [post]
func ApiValidatorPostV1(w http.ResponseWriter, r *http.Request) {
	getApiValidator(w, r)
}

// This endpoint supports both GET and POST but requires different swagger descriptions based on the type
func getApiValidator(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	var param string
	if r.Method == http.MethodGet {
		// Get the validators from the URL
		param = vars["indexOrPubkey"]
	} else {
		// Get the validators from the request body
		decoder := json.NewDecoder(r.Body)
		req := &struct {
			IndicesOrPubKey string `json:"indicesOrPubkey"`
		}{}

		err := decoder.Decode(req)
		if err != nil {
			sendBadRequestResponse(w, r.URL.String(), "error decoding request body")
			return
		}
		param = req.IndicesOrPubKey
	}

	queryIndices, err := parseValidatorParamsToIndices(param, 100)
	if err != nil {
		sendBadRequestResponse(w, r.URL.String(), err.Error())
		return
	}

	relevantValidators, _ := services.GlobalBeaconService.GetFilteredValidatorSet(&dbtypes.ValidatorFilter{
		Indices: queryIndices,
	}, true)

	data := []*ApiValidatorResponseV1{}
	for _, validator := range relevantValidators {
		isOnline := services.GlobalBeaconService.GetValidatorLiveness(validator.Index, 3) > 0
		data = append(data, &ApiValidatorResponseV1{
			Activationeligibilityepoch: uint64(validator.Validator.ActivationEligibilityEpoch),
			Activationepoch:            uint64(validator.Validator.ActivationEpoch),
			Balance:                    uint64(validator.Balance),
			Effectivebalance:           uint64(validator.Validator.EffectiveBalance),
			Exitepoch:                  uint64(validator.Validator.ExitEpoch),
			Isonline:                   isOnline,
			Name:                       services.GlobalBeaconService.GetValidatorName(uint64(validator.Index)),
			Pubkey:                     validator.Validator.PublicKey.String(),
			Slashed:                    validator.Validator.Slashed,
			Status:                     validator.Status.String(),
			Validatorindex:             uint64(validator.Index),
			Withdrawableepoch:          uint64(validator.Validator.WithdrawableEpoch),
			Withdrawalcredentials:      fmt.Sprintf("0x%x", validator.Validator.WithdrawalCredentials),
		})
	}

	j := json.NewEncoder(w)
	response := &ApiResponse{}
	response.Status = "OK"

	if len(data) == 1 {
		response.Data = data[0]
	} else {
		response.Data = data
	}

	err = j.Encode(response)
	if err != nil {
		sendServerErrorResponse(w, r.URL.String(), "could not serialize data results")
		logrus.Errorf("error serializing json data for API %v route: %v", r.URL, err)
	}
}
