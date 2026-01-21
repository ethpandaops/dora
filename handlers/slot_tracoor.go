package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// SlotTracoor handles the Tracoor data request for a slot
func SlotTracoor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	tracoorUrl := utils.Config.Frontend.TracoorUrl
	if tracoorUrl == "" {
		http.Error(w, `{"error": "Tracoor not configured"}`, http.StatusNotFound)
		return
	}

	// Remove trailing slash from tracoorUrl
	tracoorUrl = strings.TrimSuffix(tracoorUrl, "/")

	tracoorNetwork := utils.Config.Frontend.TracoorNetwork
	if tracoorNetwork == "" {
		tracoorNetwork = "mainnet"
	}

	// Get parameters from query string
	query := r.URL.Query()
	blockRoot := query.Get("blockRoot")
	stateRoot := query.Get("stateRoot")
	executionBlockHash := query.Get("executionBlockHash")

	// Validate input - at least one of the parameters should be provided
	if blockRoot == "" && stateRoot == "" && executionBlockHash == "" {
		http.Error(w, `{"error": "missing required parameters"}`, http.StatusBadRequest)
		return
	}

	result := models.SlotTracoorData{
		TracoorUrl:           tracoorUrl,
		BeaconBlocks:         []models.TracoorBeaconBlock{},
		BeaconStates:         []models.TracoorBeaconState{},
		ExecutionBlockTraces: []models.TracoorExecutionBlockTrace{},
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	// Fetch beacon block traces if blockRoot is provided
	if blockRoot != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			blockResp, err := fetchTracoorBeaconBlocks(client, tracoorUrl, tracoorNetwork, blockRoot)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.BeaconBlocksError = err.Error()
				logrus.WithError(err).WithField("blockRoot", blockRoot).Debug("error fetching Tracoor beacon blocks")
			} else {
				result.BeaconBlocks = blockResp.BeaconBlocks
			}
		}()
	}

	// Fetch beacon state traces if stateRoot is provided
	if stateRoot != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stateResp, err := fetchTracoorBeaconStates(client, tracoorUrl, tracoorNetwork, stateRoot)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.BeaconStatesError = err.Error()
				logrus.WithError(err).WithField("stateRoot", stateRoot).Debug("error fetching Tracoor beacon states")
			} else {
				result.BeaconStates = stateResp.BeaconStates
			}
		}()
	}

	// Fetch execution block traces if executionBlockHash is provided
	if executionBlockHash != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			execResp, err := fetchTracoorExecutionBlockTraces(client, tracoorUrl, tracoorNetwork, executionBlockHash)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				result.ExecutionBlockTraceErr = err.Error()
				logrus.WithError(err).WithField("executionBlockHash", executionBlockHash).Debug("error fetching Tracoor execution block traces")
			} else {
				result.ExecutionBlockTraces = execResp.ExecutionBlockTraces
			}
		}()
	}

	wg.Wait()

	if err := json.NewEncoder(w).Encode(result); err != nil {
		logrus.WithError(err).Error("error encoding Tracoor response")
		http.Error(w, `{"error": "internal server error"}`, http.StatusInternalServerError)
	}
}

func fetchTracoorBeaconBlocks(client *http.Client, tracoorUrl, network, blockRoot string) (*models.TracoorBeaconBlockResponse, error) {
	reqBody := map[string]any{
		"network":    network,
		"block_root": blockRoot,
		"pagination": map[string]any{
			"limit":    100,
			"offset":   0,
			"order_by": "fetched_at DESC",
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", tracoorUrl+"/v1/api/list-beacon-block", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var result models.TracoorBeaconBlockResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

func fetchTracoorBeaconStates(client *http.Client, tracoorUrl, network, stateRoot string) (*models.TracoorBeaconStateResponse, error) {
	reqBody := map[string]any{
		"network":    network,
		"state_root": stateRoot,
		"pagination": map[string]any{
			"limit":    100,
			"offset":   0,
			"order_by": "fetched_at DESC",
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", tracoorUrl+"/v1/api/list-beacon-state", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var result models.TracoorBeaconStateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

func fetchTracoorExecutionBlockTraces(client *http.Client, tracoorUrl, network, blockHash string) (*models.TracoorExecutionBlockTraceResponse, error) {
	reqBody := map[string]any{
		"network":    network,
		"block_hash": blockHash,
		"pagination": map[string]any{
			"limit":    100,
			"offset":   0,
			"order_by": "fetched_at DESC",
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", tracoorUrl+"/v1/api/list-execution-block-trace", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var result models.TracoorExecutionBlockTraceResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}
