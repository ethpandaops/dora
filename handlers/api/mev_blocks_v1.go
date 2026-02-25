package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// APIMevBlocksResponse represents the response structure for MEV blocks list
type APIMevBlocksResponse struct {
	Status string            `json:"status"`
	Data   *APIMevBlocksData `json:"data"`
}

// APIMevBlocksData contains the MEV blocks data
type APIMevBlocksData struct {
	MevBlocks  []*APIMevBlockInfo `json:"mev_blocks"`
	Count      uint64             `json:"count"`
	TotalCount uint64             `json:"total_count"`
}

// APIMevBlockInfo represents information about a single MEV block
type APIMevBlockInfo struct {
	SlotNumber     uint64                  `json:"slot_number"`
	BlockHash      string                  `json:"block_hash"`
	BlockNumber    uint64                  `json:"block_number"`
	Time           int64                   `json:"time"`
	ValidatorIndex uint64                  `json:"validator_index"`
	ValidatorName  string                  `json:"validator_name,omitempty"`
	BuilderPubkey  string                  `json:"builder_pubkey,omitempty"`
	Proposed       uint8                   `json:"proposed"` // 0=not proposed, 1=proposed, 2=orphaned
	Relays         []*APIMevBlockRelayInfo `json:"relays"`
	FeeRecipient   string                  `json:"fee_recipient"`
	TxCount        uint32                  `json:"tx_count"`
	BlobCount      uint64                  `json:"blob_count"`
	GasUsed        uint64                  `json:"gas_used"`
	BlockValue     uint64                  `json:"block_value_gwei"`
}

// APIMevBlockRelayInfo represents information about a MEV relay for a block
type APIMevBlockRelayInfo struct {
	RelayId   uint8  `json:"relay_id"`
	RelayName string `json:"relay_name"`
}

// APIMevBlocksV1 returns a list of MEV blocks with filters
// @Summary Get MEV blocks
// @Description Returns a list of MEV blocks with relay information and comprehensive filtering
// @Tags mev_blocks
// @Accept json
// @Produce json
// @Param limit query int false "Number of MEV blocks to return (max 100)"
// @Param offset query int false "Offset for pagination"
// @Param min_slot query int false "Minimum slot number"
// @Param max_slot query int false "Maximum slot number"
// @Param min_index query int false "Minimum proposer validator index"
// @Param max_index query int false "Maximum proposer validator index"
// @Param validator_name query string false "Filter by proposer validator name"
// @Param relays query string false "Filter by MEV relays (comma-separated relay IDs)"
// @Param proposed query string false "Filter by proposed status (comma-separated: 0=not proposed, 1=proposed, 2=orphaned)"
// @Success 200 {object} APIMevBlocksResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/mev_blocks [get]
// @ID getMevBlocks
func APIMevBlocksV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	query := r.URL.Query()

	// Parse limit parameter
	limit := uint32(50)
	limitStr := query.Get("limit")
	if limitStr != "" {
		parsedLimit, err := strconv.ParseUint(limitStr, 10, 32)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid limit parameter"}`, http.StatusBadRequest)
			return
		}
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		if parsedLimit > 0 {
			limit = uint32(parsedLimit)
		}
	}

	// Parse offset parameter
	offset := uint64(0)
	offsetStr := query.Get("offset")
	if offsetStr != "" {
		parsedOffset, err := strconv.ParseUint(offsetStr, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid offset parameter"}`, http.StatusBadRequest)
			return
		}
		offset = parsedOffset
	}

	// Parse filter parameters
	var minSlot, maxSlot, minIndex, maxIndex uint64
	var proposerName string
	var relayIds []uint8
	var proposedStatuses []uint8

	if minSlotStr := query.Get("min_slot"); minSlotStr != "" {
		if parsed, err := strconv.ParseUint(minSlotStr, 10, 64); err == nil {
			minSlot = parsed
		}
	}

	if maxSlotStr := query.Get("max_slot"); maxSlotStr != "" {
		if parsed, err := strconv.ParseUint(maxSlotStr, 10, 64); err == nil {
			maxSlot = parsed
		}
	}

	if minIndexStr := query.Get("min_index"); minIndexStr != "" {
		if parsed, err := strconv.ParseUint(minIndexStr, 10, 64); err == nil {
			minIndex = parsed
		}
	}

	if maxIndexStr := query.Get("max_index"); maxIndexStr != "" {
		if parsed, err := strconv.ParseUint(maxIndexStr, 10, 64); err == nil {
			maxIndex = parsed
		}
	}

	proposerName = query.Get("validator_name")

	// Parse relay IDs from comma-separated string
	if relaysStr := query.Get("relays"); relaysStr != "" {
		for _, relayStr := range strings.Split(relaysStr, ",") {
			relayStr = strings.TrimSpace(relayStr)
			if relayId, err := strconv.ParseUint(relayStr, 10, 8); err == nil {
				relayIds = append(relayIds, uint8(relayId))
			}
		}
	}

	// Parse proposed statuses from comma-separated string
	if proposedStr := query.Get("proposed"); proposedStr != "" {
		for _, statusStr := range strings.Split(proposedStr, ",") {
			statusStr = strings.TrimSpace(statusStr)
			if status, err := strconv.ParseUint(statusStr, 10, 8); err == nil {
				proposedStatuses = append(proposedStatuses, uint8(status))
			}
		}
	}

	// Build MEV block filter with correct field names
	mevBlockFilter := &dbtypes.MevBlockFilter{
		MinSlot:      minSlot,
		MaxSlot:      maxSlot,
		MinIndex:     minIndex,
		MaxIndex:     maxIndex,
		ProposerName: proposerName,
		MevRelay:     relayIds,
		Proposed:     proposedStatuses,
	}

	// Get MEV blocks from database
	dbMevBlocks, totalCount, err := db.GetMevBlocksFiltered(r.Context(), offset, limit, mevBlockFilter)
	if err != nil {
		logrus.WithError(err).Error("failed to get MEV blocks")
		http.Error(w, `{"status": "ERROR: failed to get MEV blocks"}`, http.StatusInternalServerError)
		return
	}

	// Get blob count information for all blocks
	blockBlobCountMap := map[string]uint64{}
	if len(dbMevBlocks) > 0 {
		blockHashes := make([][]byte, 0, len(dbMevBlocks))
		for _, mevBlock := range dbMevBlocks {
			blockHashes = append(blockHashes, mevBlock.BlockHash)
		}
		blobCounts := db.GetSlotBlobCountByExecutionHashes(r.Context(), blockHashes)
		for i, blobCount := range blobCounts {
			if i < len(blockHashes) && blobCount != nil {
				blockBlobCountMap[string(blockHashes[i])] = blobCount.BlobCount
			}
		}
	}

	chainState := services.GlobalBeaconService.GetChainState()
	var mevBlocks []*APIMevBlockInfo
	for _, mevBlock := range dbMevBlocks {
		blockInfo := &APIMevBlockInfo{
			SlotNumber:     mevBlock.SlotNumber,
			BlockHash:      fmt.Sprintf("0x%x", mevBlock.BlockHash),
			BlockNumber:    mevBlock.BlockNumber,
			Time:           chainState.SlotToTime(phase0.Slot(mevBlock.SlotNumber)).Unix(),
			ValidatorIndex: mevBlock.ProposerIndex,
			ValidatorName:  services.GlobalBeaconService.GetValidatorName(mevBlock.ProposerIndex),
			BuilderPubkey:  fmt.Sprintf("0x%x", mevBlock.BuilderPubkey),
			Proposed:       mevBlock.Proposed,
			FeeRecipient:   fmt.Sprintf("0x%x", mevBlock.FeeRecipient),
			TxCount:        uint32(mevBlock.TxCount),
			BlobCount:      blockBlobCountMap[string(mevBlock.BlockHash)],
			GasUsed:        mevBlock.GasUsed,
			BlockValue:     mevBlock.BlockValueGwei,
		}

		// Get relay information from SeenbyRelays bitmask
		blockInfo.Relays = []*APIMevBlockRelayInfo{}
		for _, relay := range utils.Config.MevIndexer.Relays {
			if mevBlock.SeenbyRelays&(1<<relay.Index) != 0 {
				blockInfo.Relays = append(blockInfo.Relays, &APIMevBlockRelayInfo{
					RelayId:   relay.Index,
					RelayName: relay.Name,
				})
			}
		}

		mevBlocks = append(mevBlocks, blockInfo)
	}

	response := APIMevBlocksResponse{
		Status: "OK",
		Data: &APIMevBlocksData{
			MevBlocks:  mevBlocks,
			Count:      uint64(len(mevBlocks)),
			TotalCount: totalCount,
		},
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode MEV blocks response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}
