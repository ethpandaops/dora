package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/sirupsen/logrus"
)

// APINetworkSplitsResponse represents the response structure for network splits
type APINetworkSplitsResponse struct {
	Status string                `json:"status"`
	Data   *APINetworkSplitsData `json:"data"`
}

// APINetworkSplitsData contains information about active network forks/splits
type APINetworkSplitsData struct {
	CurrentEpoch   uint64                 `json:"current_epoch"`
	CurrentSlot    uint64                 `json:"current_slot"`
	FinalizedEpoch int64                  `json:"finalized_epoch"`
	FinalizedRoot  string                 `json:"finalized_root"`
	Splits         []*APINetworkSplitInfo `json:"splits"`
}

// APINetworkSplitInfo represents information about a single network split/fork
type APINetworkSplitInfo struct {
	ForkId                 string    `json:"fork_id"`
	HeadSlot               uint64    `json:"head_slot"`
	HeadRoot               string    `json:"head_root"`
	HeadBlockHash          string    `json:"head_block_hash,omitempty"`
	HeadExecutionNumber    uint64    `json:"head_execution_number,omitempty"`
	TotalChainWeight       uint64    `json:"total_chain_weight"`
	LastEpochVotes         []uint64  `json:"last_epoch_votes"`
	LastEpochParticipation []float64 `json:"last_epoch_participation"`
	IsCanonical            bool      `json:"is_canonical"`
}

// APINetworkSplitsV1 returns information about active network splits/forks
// @Summary Get network splits
// @Description Returns information about active network forks/splits, their participation rates, and head blocks
// @Tags network
// @Accept json
// @Produce json
// @Param with_stale query bool false "Include stale forks with no voting weight (default: false)"
// @Success 200 {object} APINetworkSplitsResponse
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/network/splits [get]
func APINetworkSplitsV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Parse query parameters
	withStale := false
	if r.URL.Query().Get("with_stale") == "true" {
		withStale = true
	}

	chainState := services.GlobalBeaconService.GetChainState()
	if chainState == nil {
		http.Error(w, `{"status": "ERROR: chain state not available"}`, http.StatusInternalServerError)
		return
	}

	currentEpoch := chainState.CurrentEpoch()
	currentSlot := chainState.CurrentSlot()
	finalizedEpoch, finalizedRoot := chainState.GetFinalizedCheckpoint()

	// Get chain heads from the beacon indexer
	beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
	if beaconIndexer == nil {
		http.Error(w, `{"status": "ERROR: beacon indexer not available"}`, http.StatusInternalServerError)
		return
	}

	chainHeads := beaconIndexer.GetChainHeads()
	if len(chainHeads) == 0 {
		http.Error(w, `{"status": "ERROR: no chain heads available"}`, http.StatusInternalServerError)
		return
	}

	// Build splits information
	splits := make([]*APINetworkSplitInfo, 0, len(chainHeads))

	// The first chain head is considered canonical
	for i, chainHead := range chainHeads {
		block := chainHead.HeadBlock
		if block == nil {
			continue
		}

		// Filter out stale forks if with_stale is false
		if !withStale && chainHead.AggregatedHeadVotes == 0 {
			continue
		}

		// Get block details
		var blockHash string
		var executionNumber uint64

		if blockIndex := block.GetBlockIndex(); blockIndex != nil {
			blockHash = fmt.Sprintf("0x%x", blockIndex.ExecutionHash)
			executionNumber = blockIndex.ExecutionNumber
		}

		// Convert epoch votes to uint64 array
		epochVotes := make([]uint64, len(chainHead.PerEpochVotes))
		for j, vote := range chainHead.PerEpochVotes {
			epochVotes[j] = uint64(vote)
		}

		split := &APINetworkSplitInfo{
			ForkId:                 fmt.Sprintf("%x", block.GetForkId()),
			HeadSlot:               uint64(block.Slot),
			HeadRoot:               fmt.Sprintf("0x%x", block.Root),
			HeadBlockHash:          blockHash,
			HeadExecutionNumber:    executionNumber,
			LastEpochVotes:         epochVotes,
			LastEpochParticipation: chainHead.PerEpochVotingPercent,
			TotalChainWeight:       uint64(chainHead.AggregatedHeadVotes),
			IsCanonical:            i == 0, // First chain head is canonical
		}

		splits = append(splits, split)
	}

	data := &APINetworkSplitsData{
		CurrentEpoch:   uint64(currentEpoch),
		CurrentSlot:    uint64(currentSlot),
		FinalizedEpoch: int64(finalizedEpoch),
		FinalizedRoot:  fmt.Sprintf("0x%x", finalizedRoot),
		Splits:         splits,
	}

	response := APINetworkSplitsResponse{
		Status: "OK",
		Data:   data,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode network splits response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}

// Helper function to get IndexerBeacon interface methods we need
type IndexerBeacon interface {
	GetChainHeads() []*beacon.ChainHead
	GetBlocksByForkId(forkId beacon.ForkKey) []*beacon.Block
}
