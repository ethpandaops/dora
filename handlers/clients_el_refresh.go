package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

var (
	lastExecutionRefreshTime time.Time
	executionRefreshMutex    sync.Mutex
)

// ClientsELRefreshStatus returns the current cooldown status for execution clients
func ClientsELRefreshStatus(w http.ResponseWriter, r *http.Request) {
	executionRefreshMutex.Lock()
	defer executionRefreshMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")

	// Calculate dynamic cooldown based on number of connected execution nodes (3s per node, max 60s)
	executionClients := services.GlobalBeaconService.GetExecutionClients()
	onlineClientCount := 0
	for _, client := range executionClients {
		if client.GetStatus() == execution.ClientStatusOnline {
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

	if time.Since(lastExecutionRefreshTime) < cooldownDuration {
		remainingTime := int(cooldownDuration.Seconds()) - int(time.Since(lastExecutionRefreshTime).Seconds())
		response := fmt.Sprintf(`{"cooldown_active": true, "remaining_seconds": %d, "total_cooldown": %d, "online_clients": %d}`, remainingTime, int(cooldownDuration.Seconds()), onlineClientCount)
		w.Write([]byte(response))
	} else {
		response := fmt.Sprintf(`{"cooldown_active": false, "remaining_seconds": 0, "total_cooldown": %d, "online_clients": %d}`, int(cooldownDuration.Seconds()), onlineClientCount)
		w.Write([]byte(response))
	}
}

// ClientsELRefresh forces a refresh of peer data from all execution clients
func ClientsELRefresh(w http.ResponseWriter, r *http.Request) {
	executionRefreshMutex.Lock()
	defer executionRefreshMutex.Unlock()

	// Get all execution clients to calculate dynamic cooldown
	executionClients := services.GlobalBeaconService.GetExecutionClients()
	onlineClientCount := 0
	for _, client := range executionClients {
		if client.GetStatus() == execution.ClientStatusOnline {
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
	if time.Since(lastExecutionRefreshTime) < cooldownDuration {
		remainingTime := int(cooldownDuration.Seconds()) - int(time.Since(lastExecutionRefreshTime).Seconds())
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
	lastExecutionRefreshTime = time.Now()

	// Use dynamic timeout based on number of clients (min 60s, max 300s)
	timeoutDuration := time.Duration(60+onlineClientCount) * time.Second
	if timeoutDuration > 300*time.Second {
		timeoutDuration = 300 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeoutDuration)
	defer cancel()

	if len(executionClients) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"success": false, "message": "no execution clients available"}`))
		return
	}

	// Filter online clients (reuse the already calculated online clients)
	onlineClients := make([]*execution.Client, 0, onlineClientCount)
	for _, client := range executionClients {
		if client.GetStatus() == execution.ClientStatusOnline {
			onlineClients = append(onlineClients, client)
		}
	}

	if len(onlineClients) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"success": false, "message": "no online execution clients available for refresh"}`))
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

	// Force refresh peer data for all online execution clients in parallel
	for _, client := range onlineClients {
		wg.Add(1)
		go func(client *execution.Client) {
			defer wg.Done()

			// Force update peer information regardless of schedule
			if err := client.ForceUpdatePeerData(ctx); err != nil {
				logrus.WithFields(logrus.Fields{
					"client": client.GetName(),
					"error":  err,
				}).Warn("failed to force refresh peer data for execution client")
				resultsChan <- refreshResult{
					clientName: client.GetName(),
					success:    false,
					error:      err,
				}
			} else {
				logrus.WithFields(logrus.Fields{
					"client": client.GetName(),
				}).Info("successfully refreshed peer data for execution client")
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

	// Collect results (no need to close buffered channel, just read what's there)
	refreshedClients := 0
	failedClients := 0
	for i := 0; i < len(onlineClients); i++ {
		result := <-resultsChan
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
		response := fmt.Sprintf(`{"success": false, "message": "failed to refresh any execution clients", "failed_clients": %d}`, failedClients)
		w.Write([]byte(response))
	}
}
