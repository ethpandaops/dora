package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// ClientsCLRefresh forces a refresh of ENR data from all consensus clients
func ClientsCLRefresh(w http.ResponseWriter, r *http.Request) {
	// Check rate limiting
	err := services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if err != nil {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// Get all consensus clients
	consensusClients := services.GlobalBeaconService.GetConsensusClients()
	if len(consensusClients) == 0 {
		http.Error(w, "no consensus clients available", http.StatusServiceUnavailable)
		return
	}

	// Force refresh metadata for all clients
	refreshedClients := 0
	for _, client := range consensusClients {
		if client.GetStatus() == consensus.ClientStatusOnline {
			// Force update node metadata (including ENRs) regardless of schedule
			if err := client.ForceUpdateNodeMetadata(ctx); err != nil {
				logrus.WithFields(logrus.Fields{
					"client": client.GetName(),
					"error":  err,
				}).Warn("failed to force refresh ENRs for client")
			} else {
				refreshedClients++
				logrus.WithFields(logrus.Fields{
					"client": client.GetName(),
				}).Info("successfully refreshed ENRs for client")
			}
		}
	}

	// Note: Cache will be automatically invalidated on next page load due to fresh data

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	if refreshedClients > 0 {
		w.WriteHeader(http.StatusOK)
		response := fmt.Sprintf(`{"success": true, "refreshed_clients": %d}`, refreshedClients)
		w.Write([]byte(response))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"success": false, "message": "no online clients available for refresh"}`))
	}
}
