package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type ApiValidatorResponseV1 struct {
	Activationeligibilityepoch int64  `json:"activationeligibilityepoch"`
	Activationepoch            int64  `json:"activationepoch"`
	Balance                    int64  `json:"balance"`
	Effectivebalance           int64  `json:"effectivebalance"`
	Exitepoch                  int64  `json:"exitepoch"`
	Isonline                   bool   `json:"isonline"`
	Name                       string `json:"name"`
	Pubkey                     string `json:"pubkey"`
	Slashed                    bool   `json:"slashed"`
	Status                     string `json:"status"`
	Validatorindex             int64  `json:"validatorindex"`
	Withdrawableepoch          int64  `json:"withdrawableepoch"`
	Withdrawalcredentials      string `json:"withdrawalcredentials"`
}

// ApiValidator godoc
// @Summary Get up to 100 validators
// @Tags Validator
// @Description Searching for too many validators based on their pubkeys will lead to a "URI too long" error
// @Produce  json
// @Param  indexOrPubkey path string true "Up to 100 validator indicesOrPubkeys, comma separated"
// @Success 200 {object} ApiResponse{data=[]ApiValidatorResponseV1}
// @Failure 400 {object} ApiResponse
// @Router /api/v1/validator/{indexOrPubkey} [get]
func ApiValidatorGetV1(w http.ResponseWriter, r *http.Request) {
	getApiValidator(w, r)
}

// ApiValidator godoc
// @Summary Get up to 100 validators
// @Tags Validator
// @Description This POST endpoint exists because the GET endpoint can lead to a "URI too long" error when searching for too many validators based on their pubkeys.
// @Produce  json
// @Param  indexOrPubkey body types.DashboardRequest true "Up to 100 validator indicesOrPubkeys, comma separated"
// @Success 200 {object} ApiResponse{data=[]ApiValidatorResponseV1}
// @Failure 400 {object} ApiResponse
// @Router /api/v1/validator [post]
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
			Activationeligibilityepoch: int64(validator.Validator.ActivationEligibilityEpoch),
			Activationepoch:            int64(validator.Validator.ActivationEpoch),
			Balance:                    int64(validator.Balance),
			Effectivebalance:           int64(validator.Validator.EffectiveBalance),
			Exitepoch:                  int64(validator.Validator.ExitEpoch),
			Isonline:                   isOnline,
			Name:                       services.GlobalBeaconService.GetValidatorName(uint64(validator.Index)),
			Pubkey:                     validator.Validator.PublicKey.String(),
			Slashed:                    validator.Validator.Slashed,
			Status:                     validator.Status.String(),
			Validatorindex:             int64(validator.Index),
			Withdrawableepoch:          int64(validator.Validator.WithdrawableEpoch),
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

func parseValidatorParamsToIndices(origParam string, limit int) (indices []phase0.ValidatorIndex, err error) {
	params := strings.Split(origParam, ",")
	if len(params) > limit {
		return nil, fmt.Errorf("only a maximum of %d query parameters are allowed", limit)
	}
	for _, param := range params {
		if strings.Contains(param, "0x") || len(param) == 96 {
			pubkey, err := hex.DecodeString(strings.Replace(param, "0x", "", -1))
			if err != nil {
				return nil, fmt.Errorf("invalid validator-parameter")
			}

			indice, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(pubkey))
			if !found {
				return nil, fmt.Errorf("validator pubkey %s not found", param)
			}
			indices = append(indices, indice)
		} else {
			index, err := strconv.ParseUint(param, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid validator-parameter: %v", param)
			}
			if index < math.MaxInt64 {
				indices = append(indices, phase0.ValidatorIndex(index))
			}
		}
	}

	return
}
