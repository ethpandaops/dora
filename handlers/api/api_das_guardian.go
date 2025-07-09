package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APIDasGuardianScanRequest represents the request body for DAS Guardian scan
type APIDasGuardianScanRequest struct {
	ENR string `json:"enr"`
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
}

// APIDasGuardianMetadata represents the beacon node metadata
type APIDasGuardianMetadata struct {
	SeqNumber   uint64 `json:"seq_number"`
	Attnets     string `json:"attnets"`
	Syncnets    string `json:"syncnets"`
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

	// Check rate limit (5 calls per minute per IP)
	if err := services.GlobalCallRateLimiter.CheckCallLimit(r, 5); err != nil {
		http.Error(w, `{"error": "rate limit exceeded"}`, http.StatusTooManyRequests)
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

	// Perform scan
	scanResult, scanErr := dasGuardian.ScanNode(ctx, req.ENR)

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
		if scanResult.RemoteStatus != nil {
			result.RemoteStatus = &APIDasGuardianStatus{
				ForkDigest:     fmt.Sprintf("0x%x", scanResult.RemoteStatus.ForkDigest),
				FinalizedRoot:  fmt.Sprintf("0x%x", scanResult.RemoteStatus.FinalizedRoot),
				FinalizedEpoch: uint64(scanResult.RemoteStatus.FinalizedEpoch),
				HeadRoot:       fmt.Sprintf("0x%x", scanResult.RemoteStatus.HeadRoot),
				HeadSlot:       uint64(scanResult.RemoteStatus.HeadSlot),
			}
		}

		// Map metadata
		if scanResult.RemoteMetadata != nil {
			result.RemoteMetadata = &APIDasGuardianMetadata{
				SeqNumber:         scanResult.RemoteMetadata.SeqNumber,
				Attnets:           fmt.Sprintf("0x%x", scanResult.RemoteMetadata.Attnets),
				Syncnets:          fmt.Sprintf("0x%x", scanResult.RemoteMetadata.Syncnets),
				CustodyGroupCount: uint64(scanResult.RemoteMetadata.CustodyGroupCount),
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