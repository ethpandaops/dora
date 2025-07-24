package api

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
	dasguardian "github.com/probe-lab/eth-das-guardian"
	"github.com/sirupsen/logrus"
)

// APIDasGuardianMassScanRequest represents the request body for mass DAS Guardian scan
type APIDasGuardianMassScanRequest struct {
	Slots       []uint64 `json:"slots,omitempty"`        // Optional slot numbers to scan
	RandomMode  string   `json:"random_mode,omitempty"`  // Random slot selection mode: "non_missed", "with_blobs", "available"
	RandomCount int32    `json:"random_count,omitempty"` // Number of random slots to select (default: 4)
}

// APIDasGuardianMassScanResponse represents the response from mass DAS Guardian scan
type APIDasGuardianMassScanResponse struct {
	Success bool                          `json:"success"`
	Error   string                        `json:"error,omitempty"`
	Slots   []uint64                      `json:"slots,omitempty"`   // The slots that were scanned
	Results map[string]*APIMassNodeResult `json:"results,omitempty"` // ENR -> node result
}

// APIMassNodeResult represents the scan result for a single node in mass scan
type APIMassNodeResult struct {
	Success           bool                   `json:"success"`
	Error             string                 `json:"error,omitempty"`
	NodeAlias         string                 `json:"node_alias,omitempty"`
	ValidColumns      [][]bool               `json:"valid_columns,omitempty"`   // Per-slot array of column validity
	TotalColumns      int                    `json:"total_columns"`             // Total number of columns per slot
	SlotResults       map[uint64]*SlotResult `json:"slot_results,omitempty"`    // Slot -> result details
	CustodyGroupCount uint64                 `json:"custody_group_count"`       // CGC from node metadata
	CustodyColumns    []uint64               `json:"custody_columns,omitempty"` // Custody column indices
	EarliestSlot      uint64                 `json:"earliest_slot"`             // Earliest available slot from node status
}

// SlotResult represents the scan result for a single slot
type SlotResult struct {
	ValidColumnCount int    `json:"valid_column_count"`
	TotalColumns     int    `json:"total_columns"`
	Error            string `json:"error,omitempty"`
}

// APIDasGuardianMassScan performs a DAS Guardian scan on all available nodes
// @Summary Scan all nodes using DAS Guardian
// @Description Performs DAS Guardian scans on all available consensus client nodes in parallel
// @Tags das-guardian
// @Accept json
// @Produce json
// @Param request body APIDasGuardianMassScanRequest true "Mass scan parameters"
// @Success 200 {object} APIDasGuardianMassScanResponse
// @Failure 400 {object} map[string]string "Invalid request"
// @Failure 429 {object} map[string]string "Rate limit exceeded"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/das-guardian/mass-scan [post]
func APIDasGuardianMassScan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check if DAS Guardian check is disabled (which also disables mass scan)
	if utils.Config.Frontend.DisableDasGuardianCheck {
		http.Error(w, `{"error": "DAS Guardian check is disabled"}`, http.StatusForbidden)
		return
	}

	// Check if DAS Guardian mass scan is disabled
	if !utils.Config.Frontend.EnableDasGuardianMassScan {
		http.Error(w, `{"error": "DAS Guardian mass scan is disabled"}`, http.StatusForbidden)
		return
	}

	// Check rate limit (1 call per 5 minutes per IP for mass scans)
	if err := services.GlobalCallRateLimiter.CheckCallLimit(r, 1); err != nil {
		http.Error(w, `{"error": "rate limit exceeded"}`, http.StatusTooManyRequests)
		return
	}

	// Parse request
	var req APIDasGuardianMassScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request body"}`, http.StatusBadRequest)
		return
	}

	// Get all consensus clients with ENRs
	consensusClients := services.GlobalBeaconService.GetConsensusClients()
	if len(consensusClients) == 0 {
		http.Error(w, `{"error": "no consensus clients available"}`, http.StatusInternalServerError)
		return
	}

	// Filter clients that have ENRs
	var availableNodes []struct {
		ENR   string
		Alias string
	}

	for _, client := range consensusClients {
		identity := client.GetNodeIdentity()
		if identity != nil && identity.Enr != "" {
			availableNodes = append(availableNodes, struct {
				ENR   string
				Alias string
			}{
				ENR:   identity.Enr,
				Alias: client.GetName(),
			})
		}
	}

	if len(availableNodes) == 0 {
		http.Error(w, `{"error": "no nodes with ENRs available"}`, http.StatusInternalServerError)
		return
	}

	// Create temporary DAS Guardian instance
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute) // Extended timeout for mass scans
	defer cancel()

	dasGuardian, err := services.NewDasGuardian(ctx, logrus.WithField("component", "das-guardian-mass-api"))
	if err != nil {
		logrus.WithError(err).Error("failed to create DAS Guardian instance")
		http.Error(w, `{"error": "failed to initialize DAS Guardian"}`, http.StatusInternalServerError)
		return
	}

	defer dasGuardian.Close()

	// Pre-select slots if using random mode
	var selectedSlots []uint64
	if req.RandomMode != "" {
		randomCount := req.RandomCount
		if randomCount <= 0 {
			randomCount = 4 // Default to 4 slots
		}

		// Select random slots once for all nodes to ensure comparability
		var err error
		selectedSlots, err = selectRandomSlotsForMassScan(req.RandomMode, int(randomCount))
		if err != nil {
			logrus.WithError(err).Error("failed to select random slots for mass scan")
			http.Error(w, `{"error": "failed to select random slots"}`, http.StatusInternalServerError)
			return
		}

		if len(selectedSlots) == 0 {
			http.Error(w, `{"error": "no suitable slots found for the specified criteria"}`, http.StatusBadRequest)
			return
		}
	} else if len(req.Slots) > 0 {
		// Use manually provided slots
		selectedSlots = req.Slots
	}

	// Perform parallel scans
	results := make(map[string]*APIMassNodeResult)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var scannedSlots []uint64
	slotsSet := false

	// Channel to limit concurrent scans
	semaphore := make(chan struct{}, 10) // Limit to 10 concurrent scans

	for _, node := range availableNodes {
		wg.Add(1)
		go func(nodeENR, nodeAlias string) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			var scanResult *dasguardian.DasGuardianScanResult
			var scanErr error

			// Use pre-selected slots for all nodes
			if len(selectedSlots) > 0 {
				// Scan with the same slots for all nodes
				scanResult, scanErr = dasGuardian.ScanNode(ctx, nodeENR, selectedSlots)
			} else {
				// Metadata-only scan
				scanResult, scanErr = dasGuardian.ScanNode(ctx, nodeENR, nil)
			}

			// Process results
			nodeResult := &APIMassNodeResult{
				Success:   scanErr == nil && scanResult != nil,
				NodeAlias: nodeAlias,
			}

			if scanErr != nil {
				nodeResult.Error = scanErr.Error()
			}

			if scanResult != nil {
				// Extract CGC and earliest slot from scan results
				if scanResult.RemoteStatusV2 != nil {
					nodeResult.EarliestSlot = scanResult.RemoteStatusV2.EarliestAvailableSlot
				}

				if scanResult.RemoteMetadataV3 != nil {
					nodeResult.CustodyGroupCount = uint64(scanResult.RemoteMetadataV3.CustodyGroupCount)
				}

				// Extract custody columns from evaluation result
				if scanResult.EvalResult.ColumnIdx != nil {
					nodeResult.CustodyColumns = make([]uint64, len(scanResult.EvalResult.ColumnIdx))
					copy(nodeResult.CustodyColumns, scanResult.EvalResult.ColumnIdx)
				}

				if scanResult.EvalResult.Slots != nil {
					// Set scanned slots (should be same for all nodes)
					mu.Lock()
					if !slotsSet {
						scannedSlots = make([]uint64, len(scanResult.EvalResult.Slots))
						copy(scannedSlots, scanResult.EvalResult.Slots)
						sort.Slice(scannedSlots, func(i, j int) bool {
							return scannedSlots[i] < scannedSlots[j]
						})
						slotsSet = true
					}
					mu.Unlock()

					// Process per-slot results
					nodeResult.SlotResults = make(map[uint64]*SlotResult)
					nodeResult.ValidColumns = make([][]bool, len(scanResult.EvalResult.Slots))

					for slotIdx, slot := range scanResult.EvalResult.Slots {
						if slotIdx < len(scanResult.EvalResult.ValidColumn) {
							validCols := scanResult.EvalResult.ValidColumn[slotIdx]
							nodeResult.ValidColumns[slotIdx] = validCols
							nodeResult.TotalColumns = len(validCols)

							// Count valid columns
							validCount := 0
							for _, valid := range validCols {
								if valid {
									validCount++
								}
							}

							nodeResult.SlotResults[slot] = &SlotResult{
								ValidColumnCount: validCount,
								TotalColumns:     len(validCols),
							}
						}
					}
				}
			}

			// Store result
			mu.Lock()
			results[nodeENR] = nodeResult
			mu.Unlock()

		}(node.ENR, node.Alias)
	}

	// Wait for all scans to complete
	wg.Wait()

	// Build response
	response := APIDasGuardianMassScanResponse{
		Success: true,
		Slots:   scannedSlots,
		Results: results,
	}

	// Send response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode mass scan response")
		http.Error(w, `{"error": "failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}

// selectRandomSlotsForMassScan selects random slots for mass scanning (same slots for all nodes)
func selectRandomSlotsForMassScan(mode string, count int) ([]uint64, error) {
	chainState := services.GlobalBeaconService.GetChainState()
	currentSlot := uint64(chainState.CurrentSlot())

	// Calculate the valid slot range for DAS data availability
	specs := chainState.GetSpecs()
	currentEpoch := uint64(chainState.CurrentEpoch())

	// Start with Fulu activation as the absolute minimum
	var startSlot uint64 = 0
	if specs != nil && specs.FuluForkEpoch != nil {
		startSlot = uint64(chainState.EpochToSlot(phase0.Epoch(*specs.FuluForkEpoch)))
	}

	// Apply data column sidecar availability limit
	if specs != nil && specs.MinEpochsForDataColumnSidecars > 0 && currentEpoch > specs.MinEpochsForDataColumnSidecars {
		// Data columns are only available for the last MinEpochsForDataColumnSidecars epochs
		dataAvailabilityEpoch := currentEpoch - specs.MinEpochsForDataColumnSidecars
		dataAvailabilitySlot := uint64(chainState.EpochToSlot(phase0.Epoch(dataAvailabilityEpoch)))

		// Use the more restrictive limit (later slot)
		if dataAvailabilitySlot > startSlot {
			startSlot = dataAvailabilitySlot
		}
	}

	endSlot := currentSlot

	// If no valid range, return empty
	if startSlot >= endSlot {
		return []uint64{}, nil
	}

	// Collect valid slots by random sampling
	var validSlots []uint64
	maxPolls := count * 3
	polled := 0

	// Keep track of already checked slots to avoid duplicates
	checkedSlots := make(map[uint64]bool)

	for polled < maxPolls && len(validSlots) < count {
		polled++

		// Generate random slot in range
		randomSlot := startSlot + uint64(rand.Int63n(int64(endSlot-startSlot)))

		// Skip if already checked
		if checkedSlots[randomSlot] {
			continue
		}
		checkedSlots[randomSlot] = true

		// Check slot using specific slot filter
		filter := &dbtypes.BlockFilter{
			Slot:         &randomSlot,
			WithOrphaned: 1,
			WithMissing:  1,
		}

		blocks := services.GlobalBeaconService.GetDbBlocksByFilter(filter, 0, 1, 0)

		// Check conditions based on mode
		switch mode {
		case "non_missed":
			// Accept if slot has a proposed block (not missing)
			if len(blocks) > 0 && blocks[0].Block != nil {
				validSlots = append(validSlots, randomSlot)
			}
		case "with_blobs":
			// Accept if slot has a proposed block with blobs
			if len(blocks) > 0 && blocks[0].Block != nil && blocks[0].Block.BlobCount > 0 {
				validSlots = append(validSlots, randomSlot)
			}
		case "available":
			// Accept any slot (including missing ones)
			validSlots = append(validSlots, randomSlot)
		}
	}

	return validSlots, nil
}
