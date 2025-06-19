package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

var (
	lastRefreshTime time.Time
	refreshMutex    sync.Mutex
)

// ClientsCLRefreshStatus returns the current cooldown status
func ClientsCLRefreshStatus(w http.ResponseWriter, r *http.Request) {
	refreshMutex.Lock()
	defer refreshMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")

	// Calculate dynamic cooldown based on number of connected nodes (3s per node, max 60s)
	consensusClients := services.GlobalBeaconService.GetConsensusClients()
	onlineClientCount := 0
	for _, client := range consensusClients {
		if client.GetStatus() == consensus.ClientStatusOnline {
			onlineClientCount++
		}
	}
	
	cooldownDuration := time.Duration(onlineClientCount*3) * time.Second
	if cooldownDuration < 3*time.Second {
		cooldownDuration = 3 * time.Second // Minimum 3 seconds
	}
	if cooldownDuration > 60*time.Second {
		cooldownDuration = 60 * time.Second // Maximum 60 seconds
	}

	if time.Since(lastRefreshTime) < cooldownDuration {
		remainingTime := int(cooldownDuration.Seconds()) - int(time.Since(lastRefreshTime).Seconds())
		response := fmt.Sprintf(`{"cooldown_active": true, "remaining_seconds": %d, "total_cooldown": %d, "online_clients": %d}`, remainingTime, int(cooldownDuration.Seconds()), onlineClientCount)
		w.Write([]byte(response))
	} else {
		response := fmt.Sprintf(`{"cooldown_active": false, "remaining_seconds": 0, "total_cooldown": %d, "online_clients": %d}`, int(cooldownDuration.Seconds()), onlineClientCount)
		w.Write([]byte(response))
	}
}

// ClientsCLRefresh forces a refresh of ENR data from all consensus clients
func ClientsCLRefresh(w http.ResponseWriter, r *http.Request) {
	refreshMutex.Lock()
	defer refreshMutex.Unlock()

	// Get all consensus clients to calculate dynamic cooldown
	consensusClients := services.GlobalBeaconService.GetConsensusClients()
	onlineClientCount := 0
	for _, client := range consensusClients {
		if client.GetStatus() == consensus.ClientStatusOnline {
			onlineClientCount++
		}
	}
	
	// Calculate dynamic cooldown based on number of connected nodes (3s per node, max 60s)
	cooldownDuration := time.Duration(onlineClientCount*3) * time.Second
	if cooldownDuration < 3*time.Second {
		cooldownDuration = 3 * time.Second // Minimum 3 seconds
	}
	if cooldownDuration > 60*time.Second {
		cooldownDuration = 60 * time.Second // Maximum 60 seconds
	}

	// Check dynamic cooldown to prevent brute force attacks
	if time.Since(lastRefreshTime) < cooldownDuration {
		remainingTime := int(cooldownDuration.Seconds()) - int(time.Since(lastRefreshTime).Seconds())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		var message string
		if int(cooldownDuration.Seconds()) == 60 {
			message = fmt.Sprintf("refresh cooldown active, try again in %d seconds (%d clients × 3s, capped at 60s)", remainingTime, onlineClientCount)
		} else {
			message = fmt.Sprintf("refresh cooldown active, try again in %d seconds (%d clients × 3s)", remainingTime, onlineClientCount)
		}
		response := fmt.Sprintf(`{"success": false, "message": "%s", "remaining_seconds": %d, "total_cooldown": %d, "online_clients": %d}`, message, remainingTime, int(cooldownDuration.Seconds()), onlineClientCount)
		w.Write([]byte(response))
		return
	}

	// Check rate limiting
	err := services.GlobalCallRateLimiter.CheckCallLimit(r, 1)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"success": false, "message": "rate limit exceeded"}`))
		return
	}

	// Update last refresh time
	lastRefreshTime = time.Now()

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	if len(consensusClients) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"success": false, "message": "no consensus clients available"}`))
		return
	}

	// Filter online clients (reuse the already calculated online clients)
	onlineClients := make([]*consensus.Client, 0, onlineClientCount)
	for _, client := range consensusClients {
		if client.GetStatus() == consensus.ClientStatusOnline {
			onlineClients = append(onlineClients, client)
		}
	}

	if len(onlineClients) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"success": false, "message": "no online clients available for refresh"}`))
		return
	}

	// Create results channel and wait group for parallel processing
	type refreshResult struct {
		clientName string
		success    bool
		error      error
	}

	resultsChan := make(chan refreshResult, len(onlineClients))
	var wg sync.WaitGroup

	// Force refresh metadata for all online clients in parallel
	for _, client := range onlineClients {
		wg.Add(1)
		go func(client *consensus.Client) {
			defer wg.Done()

			// Force update node metadata (including ENRs) regardless of schedule
			if err := client.ForceUpdateNodeMetadata(ctx); err != nil {
				logrus.WithFields(logrus.Fields{
					"client": client.GetName(),
					"error":  err,
				}).Warn("failed to force refresh ENRs for client")
				resultsChan <- refreshResult{
					clientName: client.GetName(),
					success:    false,
					error:      err,
				}
			} else {
				logrus.WithFields(logrus.Fields{
					"client": client.GetName(),
				}).Info("successfully refreshed ENRs for client")
				resultsChan <- refreshResult{
					clientName: client.GetName(),
					success:    true,
					error:      nil,
				}
			}
		}(client)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(resultsChan)

	// Collect results
	refreshedClients := 0
	failedClients := 0
	for result := range resultsChan {
		if result.success {
			refreshedClients++
		} else {
			failedClients++
		}
	}

	// Note: Cache will be automatically invalidated on next page load due to fresh data

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	if refreshedClients > 0 {
		w.WriteHeader(http.StatusOK)
		response := fmt.Sprintf(`{"success": true, "refreshed_clients": %d, "failed_clients": %d}`, refreshedClients, failedClients)
		w.Write([]byte(response))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		response := fmt.Sprintf(`{"success": false, "message": "failed to refresh any clients", "failed_clients": %d}`, failedClients)
		w.Write([]byte(response))
	}
}
