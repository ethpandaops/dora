package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type ApiValidatorEth1ResponseV1 struct {
	PublicKey      string `json:"public_key"`
	ValidSignature bool   `json:"valid_signature"`
	ValidatorIndex uint64 `json:"validator_index"`
}

// ApiValidatorByEth1Address godoc
// @Summary Get all validators that belong to an eth1 address
// @Tags Validator
// @Produce  json
// @Param  eth1address path string true "Eth1 address from which the validator deposits were sent".
// @Param limit query string false "Limit the number of results (default: 2000)"
// @Param offset query string false "Offset the results (default: 0)"
// @Success 200 {object} ApiResponse{data=[]ApiValidatorEth1ResponseV1}
// @Failure 400 {object} ApiResponse
// @Router /api/v1/validator/eth1/{eth1address} [get]
func ApiValidatorByEth1AddressV1(w http.ResponseWriter, r *http.Request) {
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
	search := vars["eth1address"]
	eth1Address, err := hex.DecodeString(strings.Replace(search, "0x", "", -1))
	if err != nil {
		sendBadRequestResponse(w, r.URL.String(), "invalid eth1 address provided")
		return
	}

	deposits, _, err := db.GetDepositTxsFiltered(uint64(offset), uint32(limit), 0, &dbtypes.DepositTxFilter{
		Address: eth1Address,
	})
	if err != nil {
		sendServerErrorResponse(w, r.URL.String(), "could not get deposit txs")
		return
	}

	data := []*ApiValidatorEth1ResponseV1{}
	for _, deposit := range deposits {
		index, _ := services.GlobalBeaconService.GetValidatorIndexByPubkey(phase0.BLSPubKey(deposit.PublicKey))
		data = append(data, &ApiValidatorEth1ResponseV1{
			PublicKey:      fmt.Sprintf("0x%x", deposit.PublicKey),
			ValidSignature: deposit.ValidSignature,
			ValidatorIndex: uint64(index),
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
