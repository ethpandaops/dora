package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type ApiWithdrawalCredentialsResponseV1 struct {
	PublicKey      string `json:"publickey"`
	ValidatorIndex uint64 `json:"validatorindex"`
}

// ApiWithdrawalCredentialsValidators godoc
// @Summary Get all validators that have a specific withdrawal credentials
// @Tags Validator
// @Produce  json
// @Param withdrawalCredentialsOrEth1address path string true "Provide a withdrawal credential or an eth1 address with an optional 0x prefix". It can also be a valid ENS name.
// @Param  limit query int false "Limit the number of results, maximum: 200" default(10)
// @Param offset query int false "Offset the number of results" default(0)
// @Success 200 {object} ApiResponse{data=[]ApiWithdrawalCredentialsResponseV1}
// @Failure 400 {object} ApiResponse
// @Router /v1/validator/withdrawalCredentials/{withdrawalCredentialsOrEth1address} [get]
// @ID getWithdrawalCredentialsValidators
func ApiWithdrawalCredentialsValidatorsV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query()
	limitQuery := q.Get("limit")
	offsetQuery := q.Get("offset")

	limit, err := strconv.ParseUint(limitQuery, 10, 64)
	if err != nil {
		limit = 2000
	}

	offset, err := strconv.ParseUint(offsetQuery, 10, 64)
	if err != nil {
		offset = 0
	}

	if limit == 0 || limit > 2000 {
		limit = 2000
	}

	vars := mux.Vars(r)
	search := vars["withdrawalCredentialsOrEth1address"]
	withdrawalAddress, withdrawalCreds, err := utils.ParseWithdrawalAddressOrCredentials(search)
	if err != nil {
		sendBadRequestResponse(w, r.URL.String(), "invalid withdrawal address or credentials provided")
		return
	}

	filter := &dbtypes.ValidatorFilter{
		WithdrawalAddress: withdrawalAddress,
		WithdrawalCreds:   withdrawalCreds,
		Limit:             limit,
		Offset:            offset,
	}

	relevantValidators, _ := services.GlobalBeaconService.GetValidatorsByWithdrawalFilter(*filter, true)

	data := []*ApiWithdrawalCredentialsResponseV1{}
	for _, validator := range relevantValidators {
		data = append(data, &ApiWithdrawalCredentialsResponseV1{
			PublicKey:      validator.Validator.PublicKey.String(),
			ValidatorIndex: uint64(validator.Index),
		})
	}

	j := json.NewEncoder(w)
	response := &ApiResponse{}
	response.Status = "OK"
	response.Data = data

	err = j.Encode(response)
	if err != nil {
		sendServerErrorResponse(w, r.URL.String(), "could not serialize data results")
		logrus.Errorf("error serializing json data for API %v route: %v", r.URL, err)
	}
}
