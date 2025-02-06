package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/ethpandaops/dora/services"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type ApiWithdrawalCredentialsResponseV1 struct {
	PublicKey      string `json:"public_key"`
	ValidSignature bool   `json:"valid_signature"`
	ValidatorIndex uint64 `json:"validator_index"`
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
// @Router /api/v1/validator/withdrawalCredentials/{withdrawalCredentialsOrEth1address} [get]
func ApiWithdrawalCredentialsValidatorsV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	q := r.URL.Query()
	limitQuery := q.Get("limit")
	offsetQuery := q.Get("offset")

	limit, err := strconv.ParseInt(limitQuery, 10, 64)
	if err != nil {
		limit = 2000
	}

	offset, err := strconv.ParseInt(offsetQuery, 10, 64)
	if err != nil {
		offset = 0
	}

	if offset < 0 {
		offset = 0
	}

	if limit > (2000+offset) || limit <= 0 || limit <= offset {
		limit = 2000 + offset
	}

	vars := mux.Vars(r)
	search := vars["withdrawalCredentialsOrEth1address"]
	searchBytes, err := hex.DecodeString(strings.Replace(search, "0x", "", -1))
	if err != nil {
		sendBadRequestResponse(w, r.URL.String(), "invalid eth1 address provided")
		return
	}

	validatorSet := services.GlobalBeaconService.GetCachedValidatorSet(true)
	if validatorSet == nil {
		sendServerErrorResponse(w, r.URL.String(), "could not get validator set")
		return
	}

	searchEth1Address := len(searchBytes) == 20

	relevantValidators := []*ApiValidatorEth1ResponseV1{}
	for _, validator := range validatorSet {
		if validator.Validator.WithdrawalCredentials[0] != 0x01 && validator.Validator.WithdrawalCredentials[0] != 0x02 {
			continue
		}

		if searchEth1Address && (validator.Validator.WithdrawalCredentials[0] == 0x01 || validator.Validator.WithdrawalCredentials[0] == 0x02) {
			if bytes.Equal(validator.Validator.WithdrawalCredentials[12:], searchBytes) {
				relevantValidators = append(relevantValidators, &ApiValidatorEth1ResponseV1{
					PublicKey:      validator.Validator.PublicKey.String(),
					ValidSignature: true,
					ValidatorIndex: uint64(validator.Index),
				})
			}

		} else if !searchEth1Address && bytes.Equal(validator.Validator.WithdrawalCredentials, searchBytes) {
			relevantValidators = append(relevantValidators, &ApiValidatorEth1ResponseV1{
				PublicKey:      validator.Validator.PublicKey.String(),
				ValidSignature: true,
				ValidatorIndex: uint64(validator.Index),
			})
		}
	}

	if offset > 0 {
		if int(offset) > len(relevantValidators) {
			relevantValidators = relevantValidators[offset:]
		} else {
			relevantValidators = relevantValidators[:0]
		}
	}

	if limit > 0 {
		if int(limit) > len(relevantValidators) {
			relevantValidators = relevantValidators[:limit]
		}
	}

	j := json.NewEncoder(w)
	response := &ApiResponse{}
	response.Status = "OK"
	response.Data = relevantValidators

	err = j.Encode(response)
	if err != nil {
		sendServerErrorResponse(w, r.URL.String(), "could not serialize data results")
		logrus.Errorf("error serializing json data for API %v route: %v", r.URL, err)
	}
}
