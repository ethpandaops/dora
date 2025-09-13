package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
	dasguardian "github.com/probe-lab/eth-das-guardian"
	"github.com/sirupsen/logrus"
)

// APIDasGuardianScanRequest represents the request body for DAS Guardian scan
type APIDasGuardianScanRequest struct {
	ENR         string   `json:"enr"`
	Slots       []uint64 `json:"slots,omitempty"`        // Optional slot numbers to scan
	RandomMode  string   `json:"random_mode,omitempty"`  // Random slot selection mode: "non_missed", "with_blobs", "available"
	RandomCount int32    `json:"random_count,omitempty"` // Number of random slots to select (default: 4)
}

// APIDasGuardianScanResponse represents the response from DAS Guardian scan
type APIDasGuardianScanResponse struct {
	Success bool                      `json:"success"`
	Error   string                    `json:"error,omitempty"`
	Result  *APIDasGuardianScanResult `json:"result,omitempty"`
}

// APIDasGuardianScanResult represents the scan result details
type APIDasGuardianScanResult struct {
	// P2P Information
	Libp2pInfo map[string]interface{} `json:"libp2p_info"`

	// Status Information (from RemoteStatus)
	RemoteStatus *APIDasGuardianStatus `json:"remote_status,omitempty"`

	// Metadata (from RemoteMetadata)
	RemoteMetadata *APIDasGuardianMetadata `json:"remote_metadata,omitempty"`

	// DAS Evaluation Result
	EvalResult *APIDasGuardianEvalResult `json:"eval_result,omitempty"`
}

// APIDasGuardianStatus represents the beacon node status
type APIDasGuardianStatus struct {
	ForkDigest     string `json:"fork_digest"`
	FinalizedRoot  string `json:"finalized_root"`
	FinalizedEpoch uint64 `json:"finalized_epoch"`
	HeadRoot       string `json:"head_root"`
	HeadSlot       uint64 `json:"head_slot"`
	EarliestSlot   uint64 `json:"earliest_slot"`
}

// APIDasGuardianMetadata represents the beacon node metadata
type APIDasGuardianMetadata struct {
	SeqNumber         uint64 `json:"seq_number"`
	Attnets           string `json:"attnets"`
	Syncnets          string `json:"syncnets"`
	CustodyGroupCount uint64 `json:"custody_group_count"`
}

// APIDasGuardianEvalResult represents the DAS evaluation results
type APIDasGuardianEvalResult struct {
	NodeID           string     `json:"node_id"`
	Slots            []uint64   `json:"slots"`
	ColumnIdx        []uint64   `json:"column_idx"`
	DownloadedResult [][]string `json:"downloaded_result"`
	ValidKzg         [][]string `json:"valid_kzg"`
	ValidColumn      [][]bool   `json:"valid_column"`
	ValidSlot        []bool     `json:"valid_slot"`
	Error            string     `json:"error,omitempty"`
}

// APIDasGuardianScan performs a DAS Guardian scan on a given ENR
// @Summary Scan a node using DAS Guardian
// @Description Performs a comprehensive scan of a beacon node using eth-das-guardian to check P2P connectivity, fork digest validity, head accuracy, and custody information
// @Tags das-guardian
// @Accept json
// @Produce json
// @Param request body APIDasGuardianScanRequest true "Node ENR to scan"
// @Success 200 {object} APIDasGuardianScanResponse
// @Failure 400 {object} map[string]string "Invalid request"
// @Failure 429 {object} map[string]string "Rate limit exceeded"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/das-guardian/scan [post]
func APIDasGuardianScan(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check if DAS Guardian check is disabled
	if utils.Config.Frontend.DisableDasGuardianCheck {
		http.Error(w, `{"error": "DAS Guardian check is disabled"}`, http.StatusForbidden)
		return
	}

	// Parse request
	var req APIDasGuardianScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.ENR == "" {
		http.Error(w, `{"error": "enr field is required"}`, http.StatusBadRequest)
		return
	}

	// Create temporary DAS Guardian instance
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	dasGuardian, err := services.NewDasGuardian(ctx, logrus.WithField("component", "das-guardian-api"))
	if err != nil {
		logrus.WithError(err).Error("failed to create DAS Guardian instance")
		http.Error(w, `{"error": "failed to initialize DAS Guardian"}`, http.StatusInternalServerError)
		return
	}

	defer dasGuardian.Close()

	// Perform scan
	var scanResult *dasguardian.DasGuardianScanResult
	var scanErr error

	// Handle slot selection
	if req.RandomMode != "" {
		// Use callback-based random slot selection
		randomCount := req.RandomCount
		if randomCount <= 0 {
			randomCount = 4 // Default to 4 slots
		}

		// Create callback that will be called with node status
		slotCallback := func(nodeStatus *dasguardian.StatusV2) ([]uint64, error) {
			return selectRandomSlotsWithNodeStatus(req.RandomMode, int(randomCount), nodeStatus)
		}

		// Scan with callback
		scanResult, scanErr = dasGuardian.ScanNodeWithCallback(ctx, req.ENR, slotCallback)
	} else {
		// Use manually provided slots or metadata-only scan
		scanResult, scanErr = dasGuardian.ScanNode(ctx, req.ENR, req.Slots)
	}

	// Build response - success is true only if no error AND we have results
	response := APIDasGuardianScanResponse{
		Success: scanErr == nil && scanResult != nil,
	}

	// Always include error if present
	if scanErr != nil {
		response.Error = scanErr.Error()
	}

	// Always try to map scan results if available (even with errors - partial results)
	if scanResult != nil {
		result := &APIDasGuardianScanResult{
			Libp2pInfo: scanResult.Libp2pInfo,
		}

		// Map status information
		if scanResult.RemoteStatusV1 != nil {
			result.RemoteStatus = &APIDasGuardianStatus{
				ForkDigest:     fmt.Sprintf("0x%x", scanResult.RemoteStatusV1.ForkDigest),
				FinalizedRoot:  fmt.Sprintf("0x%x", scanResult.RemoteStatusV1.FinalizedRoot),
				FinalizedEpoch: uint64(scanResult.RemoteStatusV1.FinalizedEpoch),
				HeadRoot:       fmt.Sprintf("0x%x", scanResult.RemoteStatusV1.HeadRoot),
				HeadSlot:       uint64(scanResult.RemoteStatusV1.HeadSlot),
			}
		} else if scanResult.RemoteStatusV2 != nil {
			result.RemoteStatus = &APIDasGuardianStatus{
				ForkDigest:     fmt.Sprintf("0x%x", scanResult.RemoteStatusV2.ForkDigest),
				FinalizedRoot:  fmt.Sprintf("0x%x", scanResult.RemoteStatusV2.FinalizedRoot),
				FinalizedEpoch: uint64(scanResult.RemoteStatusV2.FinalizedEpoch),
				HeadRoot:       fmt.Sprintf("0x%x", scanResult.RemoteStatusV2.HeadRoot),
				HeadSlot:       uint64(scanResult.RemoteStatusV2.HeadSlot),
				EarliestSlot:   uint64(scanResult.RemoteStatusV2.EarliestAvailableSlot),
			}
		}

		// Map metadata
		if scanResult.RemoteMetadataV2 != nil {
			result.RemoteMetadata = &APIDasGuardianMetadata{
				SeqNumber: scanResult.RemoteMetadataV2.SeqNumber,
				Attnets:   fmt.Sprintf("0x%x", scanResult.RemoteMetadataV2.Attnets),
				Syncnets:  fmt.Sprintf("0x%x", scanResult.RemoteMetadataV2.Syncnets),
			}
		} else if scanResult.RemoteMetadataV3 != nil {
			result.RemoteMetadata = &APIDasGuardianMetadata{
				SeqNumber:         scanResult.RemoteMetadataV3.SeqNumber,
				Attnets:           fmt.Sprintf("0x%x", scanResult.RemoteMetadataV3.Attnets),
				Syncnets:          fmt.Sprintf("0x%x", scanResult.RemoteMetadataV3.Syncnets),
				CustodyGroupCount: uint64(scanResult.RemoteMetadataV3.CustodyGroupCount),
			}
		}

		// Map evaluation result (always present since it's not a pointer)
		result.EvalResult = &APIDasGuardianEvalResult{
			NodeID:           scanResult.EvalResult.NodeID,
			Slots:            scanResult.EvalResult.Slots,
			ColumnIdx:        scanResult.EvalResult.ColumnIdx,
			DownloadedResult: scanResult.EvalResult.DownloadedResult,
			ValidKzg:         scanResult.EvalResult.ValidKzg,
			ValidColumn:      scanResult.EvalResult.ValidColumn,
			ValidSlot:        scanResult.EvalResult.ValidSlot,
		}
		if scanResult.EvalResult.Error != nil {
			result.EvalResult.Error = scanResult.EvalResult.Error.Error()
		}

		response.Result = result
	}

	// Send response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode response")
		http.Error(w, `{"error": "failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}

// selectRandomSlotsWithNodeStatus selects random slots based on the specified mode and node status
func selectRandomSlotsWithNodeStatus(mode string, count int, nodeStatus *dasguardian.StatusV2) ([]uint64, error) {
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

	// Use the node's earliest available slot if it's higher than our calculated minimum
	if nodeStatus != nil && nodeStatus.EarliestAvailableSlot > startSlot {
		startSlot = nodeStatus.EarliestAvailableSlot
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
