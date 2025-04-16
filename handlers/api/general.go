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
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

type ApiResponse struct {
	Status string      `json:"status"`
	Data   interface{} `json:"data"`
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

func sendBadRequestResponse(w http.ResponseWriter, route, message string) {
	sendErrorWithCodeResponse(w, route, message, http.StatusBadRequest)
}

func sendServerErrorResponse(w http.ResponseWriter, route, message string) {
	sendErrorWithCodeResponse(w, route, message, http.StatusInternalServerError)
}

func sendErrorWithCodeResponse(w http.ResponseWriter, route, message string, errorcode int) {
	w.WriteHeader(errorcode)
	j := json.NewEncoder(w)
	response := &ApiResponse{}
	response.Status = "ERROR: " + message
	err := j.Encode(response)

	if err != nil {
		logrus.Errorf("error serializing json error for API %v route: %v", route, err)
	}
}

func SendOKResponse(j *json.Encoder, route string, data []interface{}) {
	response := &ApiResponse{}
	response.Status = "OK"

	if len(data) == 1 {
		response.Data = data[0]
	} else {
		response.Data = data
	}
	err := j.Encode(response)

	if err != nil {
		logrus.Errorf("error serializing json data for API %v route: %v", route, err)
	}
}
