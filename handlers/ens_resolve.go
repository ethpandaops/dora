package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"github.com/ethpandaops/dora/services"
)

type ensResolveMatch struct {
	Address string `json:"address"`
	Network string `json:"network"`
	Local   bool   `json:"local"`
}

type ensResolveResponse struct {
	Status  string            `json:"status"`
	Name    string            `json:"name"`
	Matches []ensResolveMatch `json:"matches"`
}

type ensLookupName struct {
	Name    string `json:"name"`
	Network string `json:"network"`
	Local   bool   `json:"local"`
}

type ensLookupResponse struct {
	Status  string          `json:"status"`
	Address string          `json:"address"`
	Names   []ensLookupName `json:"names"`
}

// EnsLookup reverse-resolves an execution address to its primary ENS names on all
// configured networks. Resolution is asynchronous - an address seen for the first
// time returns no names and gets queued, so callers may retry shortly after.
func EnsLookup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	address := r.URL.Query().Get("address")
	response := ensLookupResponse{Status: "ok", Address: address, Names: []ensLookupName{}}

	if common.IsHexAddress(address) {
		addr := common.HexToAddress(address)
		names := services.GlobalBeaconService.GetEnsResolver().ResolveNames(r.Context(), [][]byte{addr.Bytes()})
		for _, name := range names[strings.ToLower(addr.Hex())] {
			response.Names = append(response.Names, ensLookupName{
				Name:    name.Name,
				Network: name.Network,
				Local:   name.Local,
			})
		}
	}

	json.NewEncoder(w).Encode(response)
}

// EnsResolve forward-resolves an ENS name (EIP-137) on all configured networks.
// Used by the frontend forms to accept ENS names in address inputs.
func EnsResolve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	name := r.URL.Query().Get("name")
	matches := services.GlobalBeaconService.GetEnsResolver().ResolveEnsName(r.Context(), name)

	response := ensResolveResponse{
		Status:  "ok",
		Name:    name,
		Matches: make([]ensResolveMatch, 0, len(matches)),
	}
	for _, match := range matches {
		response.Matches = append(response.Matches, ensResolveMatch{
			Address: match.Address.Hex(),
			Network: match.Network,
			Local:   match.Local,
		})
	}

	json.NewEncoder(w).Encode(response)
}
