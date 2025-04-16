package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type ApiValidatorDepositsResponseV1 struct {
	Amount                uint64 `json:"amount"`
	BlockNumber           uint64 `json:"block_number"`
	BlockTS               uint64 `json:"block_ts"`
	FromAddress           string `json:"from_address"`
	MerkleTreeIndex       string `json:"merkletree_index"`
	PublicKey             string `json:"publickey"`
	Removed               bool   `json:"removed"`
	Signature             string `json:"signature"`
	TxHash                string `json:"tx_hash"`
	TxIndex               uint64 `json:"tx_index"`
	TxInput               string `json:"tx_input"`
	ValidSignature        bool   `json:"valid_signature"`
	WithdrawalCredentials string `json:"withdrawal_credentials"`
}

// ApiValidatorDepositsV1 godoc
// @Summary Get validator execution layer deposits
// @Description Get all eth1 deposits for up to 100 validators
// @Tags Validators
// @Produce  json
// @Param  indexOrPubkey path string true "Up to 100 validator indicesOrPubkeys, comma separated"
// @Success 200 {object} types.ApiResponse{data=[]types.ApiValidatorDepositsResponseV1}
// @Failure 400 {object} types.ApiResponse
// @Router /api/v1/validator/{indexOrPubkey}/deposits [get]
func ApiValidatorDepositsV1(w http.ResponseWriter, r *http.Request) {
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

	pubkeys := make([][]byte, 0, len(relevantValidators))
	for _, relevantValidator := range relevantValidators {
		pubkey := relevantValidator.Validator.PublicKey[:]

		pubkeys = append(pubkeys, pubkey)
	}

	canonicalForkIds := services.GlobalBeaconService.GetCanonicalForkIds()

	deposits, _, err := db.GetDepositTxsFiltered(0, 1000, canonicalForkIds, &dbtypes.DepositTxFilter{
		PublicKeys:   pubkeys,
		WithOrphaned: 0,
	})
	if err != nil {
		sendServerErrorResponse(w, r.URL.String(), err.Error())
		return
	}

	data := []*ApiValidatorDepositsResponseV1{}
	for _, deposit := range deposits {
		data = append(data, &ApiValidatorDepositsResponseV1{
			Amount:                uint64(deposit.Amount),
			BlockNumber:           uint64(deposit.BlockNumber),
			BlockTS:               uint64(deposit.BlockTime),
			FromAddress:           common.Address(deposit.TxSender).Hex(),
			MerkleTreeIndex:       fmt.Sprintf("0x%x", deposit.BlockRoot),
			PublicKey:             fmt.Sprintf("0x%x", deposit.PublicKey),
			Removed:               deposit.Orphaned,
			Signature:             fmt.Sprintf("0x%x", deposit.Signature),
			TxHash:                fmt.Sprintf("0x%x", deposit.TxHash),
			TxIndex:               uint64(deposit.Index),
			TxInput:               fmt.Sprintf("0x%x", deposit.TxTarget),
			ValidSignature:        deposit.ValidSignature != 0,
			WithdrawalCredentials: fmt.Sprintf("0x%x", deposit.WithdrawalCredentials),
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
