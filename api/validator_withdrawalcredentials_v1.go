package api

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type ApiWithdrawalCredentialsResponseV1 struct {
	PublicKey      string `json:"publickey"`
	ValidatorIndex uint64 `json:"validatorindex"`
}

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

	filter := &dbtypes.ValidatorFilter{}

	if len(searchBytes) == 20 {
		filter.WithdrawalAddress = searchBytes
	} else {
		filter.WithdrawalCreds = searchBytes
	}

	relevantValidators, _ := services.GlobalBeaconService.GetFilteredValidatorSet(filter, true)

	if offset > 0 {
		if int(offset) > len(relevantValidators) {
			relevantValidators = relevantValidators[offset:]
		} else {
			relevantValidators = relevantValidators[:0]
		}
	}

	if limit > 0 {
		if len(relevantValidators) > int(limit) {
			relevantValidators = relevantValidators[:limit]
		}
	}

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
