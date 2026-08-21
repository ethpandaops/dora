package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/ethereum/go-ethereum/common"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
)

type faucetRequest struct {
	Address string `json:"address"`
}

type faucetResponse struct {
	Status  string `json:"status"`
	TxHash  string `json:"txhash,omitempty"`
	Message string `json:"message,omitempty"`
}

// SubmitDepositFaucet handles devnet faucet requests from the submit deposit pages.
func SubmitDepositFaucet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	sendFaucetError := func(status int, message string) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(faucetResponse{Status: "error", Message: message})
	}

	if !utils.Config.Frontend.ShowSubmitDeposit {
		sendFaucetError(http.StatusForbidden, "submit deposit is not enabled")
		return
	}

	faucetService := services.GetFaucetService()
	if !faucetService.IsEnabled() {
		sendFaucetError(http.StatusForbidden, "faucet is not enabled")
		return
	}

	var request faucetRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		sendFaucetError(http.StatusBadRequest, "failed to decode request body")
		return
	}

	if !common.IsHexAddress(request.Address) {
		sendFaucetError(http.StatusBadRequest, "invalid address")
		return
	}

	txHash, err := faucetService.RequestFunds(r.Context(), common.HexToAddress(request.Address))
	if err != nil {
		logrus.WithError(err).Warnf("faucet request for %v failed", request.Address)
		sendFaucetError(http.StatusServiceUnavailable, err.Error())
		return
	}

	json.NewEncoder(w).Encode(faucetResponse{Status: "ok", TxHash: txHash.String()})
}
