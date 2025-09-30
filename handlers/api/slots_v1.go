package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// APISlotsResponse represents the response for slots list endpoint
type APISlotsResponse struct {
	Status string        `json:"status"`
	Data   *APISlotsData `json:"data"`
}

// APISlotsData represents the slots list data
type APISlotsData struct {
	Slots      []*APISlotListItem `json:"slots"`
	TotalCount int                `json:"total_count"`
	NextSlot   *uint64            `json:"next_slot,omitempty"`
}

// APISlotListItem represents a single slot in the list
type APISlotListItem struct {
	Slot                       uint64                       `json:"slot"`
	Epoch                      uint64                       `json:"epoch"`
	Time                       time.Time                    `json:"time"`
	Finalized                  bool                         `json:"finalized"`
	Scheduled                  bool                         `json:"scheduled"`
	Status                     string                       `json:"status"`
	Proposer                   uint64                       `json:"proposer"`
	ProposerName               string                       `json:"proposer_name"`
	AttestationCount           uint64                       `json:"attestation_count"`
	DepositCount               uint64                       `json:"deposit_count"`
	ExitCount                  uint64                       `json:"exit_count"`
	ProposerSlashingCount      uint64                       `json:"proposer_slashing_count"`
	AttesterSlashingCount      uint64                       `json:"attester_slashing_count"`
	SyncAggregateParticipation float64                      `json:"sync_aggregate_participation"`
	EthTransactionCount        uint64                       `json:"eth_transaction_count"`
	BlobCount                  uint64                       `json:"blob_count"`
	WithEthBlock               bool                         `json:"with_eth_block"`
	EthBlockNumber             *uint64                      `json:"eth_block_number,omitempty"`
	Graffiti                   string                       `json:"graffiti"`
	GraffitiText               string                       `json:"graffiti_text"`
	ElExtraData                string                       `json:"el_extra_data"`
	GasUsed                    uint64                       `json:"gas_used"`
	GasLimit                   uint64                       `json:"gas_limit"`
	BlockSize                  uint64                       `json:"block_size"`
	BlockRoot                  string                       `json:"block_root"`
	ParentRoot                 string                       `json:"parent_root"`
	StateRoot                  string                       `json:"state_root"`
	RecvDelay                  *int32                       `json:"recv_delay,omitempty"`
	MinExecTime                *uint32                      `json:"min_exec_time,omitempty"`
	MaxExecTime                *uint32                      `json:"max_exec_time,omitempty"`
	AvgExecTime                *uint32                      `json:"avg_exec_time,omitempty"`
	ExecutionTimes             []models.ExecutionTimeDetail `json:"execution_times,omitempty"`
	IsMevBlock                 bool                         `json:"is_mev_block"`
	MevBlockRelays             string                       `json:"mev_block_relays,omitempty"`
}

// APISlotsV1 returns a list of slots with filters
// @Summary Get filtered slots list
// @Description Returns a list of slots with various filtering options, sorted by slot number descending
// @Tags Slot
// @Produce json
// @Param graffiti query string false "Filter by graffiti"
// @Param graffiti_invert query bool false "Invert graffiti filter"
// @Param extra_data query string false "Filter by extra data"
// @Param extra_data_invert query bool false "Invert extra data filter"
// @Param proposer query string false "Filter by proposer index"
// @Param proposer_name query string false "Filter by proposer name"
// @Param proposer_invert query bool false "Invert proposer filter"
// @Param with_orphaned query int false "Include orphaned blocks (0=exclude, 1=include, 2=only orphaned)"
// @Param with_missing query int false "Include missing blocks (0=exclude, 1=include, 2=only missing)"
// @Param min_sync query float32 false "Minimum sync aggregate participation (0-100)"
// @Param max_sync query float32 false "Maximum sync aggregate participation (0-100)"
// @Param min_exec_time query int false "Minimum execution time in ms"
// @Param max_exec_time query int false "Maximum execution time in ms"
// @Param min_tx_count query int false "Minimum transaction count"
// @Param max_tx_count query int false "Maximum transaction count"
// @Param min_blob_count query int false "Minimum blob count"
// @Param max_blob_count query int false "Maximum blob count"
// @Param fork_ids query string false "Comma-separated list of fork IDs"
// @Param start_slot query int false "Start slot for pagination (inclusive)"
// @Param limit query int false "Number of results to return (max 100, default 100)"
// @Success 200 {object} APISlotsResponse
// @Failure 400 {object} map[string]string "Invalid parameters"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /v1/slots [get]
// @ID getSlots
func APISlotsV1(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Parse query parameters
	query := r.URL.Query()

	// Pagination parameters
	var startSlot *uint64
	if query.Has("start_slot") {
		slot, err := strconv.ParseUint(query.Get("start_slot"), 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid start_slot"}`, http.StatusBadRequest)
			return
		}
		startSlot = &slot
	}

	limit := uint64(100)
	if query.Has("limit") {
		parsedLimit, err := strconv.ParseUint(query.Get("limit"), 10, 64)
		if err != nil || parsedLimit == 0 {
			http.Error(w, `{"status": "ERROR: invalid limit"}`, http.StatusBadRequest)
			return
		}
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		limit = parsedLimit
	}

	// Build block filter
	blockFilter := &dbtypes.BlockFilter{
		WithOrphaned: 1, // Include all by default
		WithMissing:  1, // Include all by default
	}

	// Process filter parameters
	if query.Has("graffiti") {
		blockFilter.Graffiti = query.Get("graffiti")
	}
	if query.Has("graffiti_invert") {
		blockFilter.InvertGraffiti = query.Get("graffiti_invert") == "true"
	}
	if query.Has("extra_data") {
		blockFilter.ExtraData = query.Get("extra_data")
	}
	if query.Has("extra_data_invert") {
		blockFilter.InvertExtraData = query.Get("extra_data_invert") == "true"
	}
	if query.Has("proposer") {
		pidx, err := strconv.ParseUint(query.Get("proposer"), 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid proposer"}`, http.StatusBadRequest)
			return
		}
		blockFilter.ProposerIndex = &pidx
	}
	if query.Has("proposer_name") {
		blockFilter.ProposerName = query.Get("proposer_name")
	}
	if query.Has("proposer_invert") {
		blockFilter.InvertProposer = query.Get("proposer_invert") == "true"
	}
	if query.Has("with_orphaned") {
		val, err := strconv.ParseUint(query.Get("with_orphaned"), 10, 8)
		if err != nil || val > 2 {
			http.Error(w, `{"status": "ERROR: invalid with_orphaned"}`, http.StatusBadRequest)
			return
		}
		blockFilter.WithOrphaned = uint8(val)
	}
	if query.Has("with_missing") {
		val, err := strconv.ParseUint(query.Get("with_missing"), 10, 8)
		if err != nil || val > 2 {
			http.Error(w, `{"status": "ERROR: invalid with_missing"}`, http.StatusBadRequest)
			return
		}
		blockFilter.WithMissing = uint8(val)
	}
	if query.Has("min_sync") {
		minSync, err := strconv.ParseFloat(query.Get("min_sync"), 32)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid min_sync"}`, http.StatusBadRequest)
			return
		}
		minSyncFloat32 := float32(minSync / 100.0) // Convert percentage to ratio
		blockFilter.MinSyncParticipation = &minSyncFloat32
	}
	if query.Has("max_sync") {
		maxSync, err := strconv.ParseFloat(query.Get("max_sync"), 32)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid max_sync"}`, http.StatusBadRequest)
			return
		}
		maxSyncFloat32 := float32(maxSync / 100.0) // Convert percentage to ratio
		blockFilter.MaxSyncParticipation = &maxSyncFloat32
	}
	if query.Has("min_exec_time") {
		minExec, err := strconv.ParseUint(query.Get("min_exec_time"), 10, 32)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid min_exec_time"}`, http.StatusBadRequest)
			return
		}
		minExecUint32 := uint32(minExec)
		blockFilter.MinExecTime = &minExecUint32
	}
	if query.Has("max_exec_time") {
		maxExec, err := strconv.ParseUint(query.Get("max_exec_time"), 10, 32)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid max_exec_time"}`, http.StatusBadRequest)
			return
		}
		maxExecUint32 := uint32(maxExec)
		blockFilter.MaxExecTime = &maxExecUint32
	}
	if query.Has("min_tx_count") {
		minTx, err := strconv.ParseUint(query.Get("min_tx_count"), 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid min_tx_count"}`, http.StatusBadRequest)
			return
		}
		blockFilter.MinTxCount = &minTx
	}
	if query.Has("max_tx_count") {
		maxTx, err := strconv.ParseUint(query.Get("max_tx_count"), 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid max_tx_count"}`, http.StatusBadRequest)
			return
		}
		blockFilter.MaxTxCount = &maxTx
	}
	if query.Has("min_blob_count") {
		minBlob, err := strconv.ParseUint(query.Get("min_blob_count"), 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid min_blob_count"}`, http.StatusBadRequest)
			return
		}
		blockFilter.MinBlobCount = &minBlob
	}
	if query.Has("max_blob_count") {
		maxBlob, err := strconv.ParseUint(query.Get("max_blob_count"), 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid max_blob_count"}`, http.StatusBadRequest)
			return
		}
		blockFilter.MaxBlobCount = &maxBlob
	}
	if query.Has("fork_ids") {
		forkIdList := strings.Split(query.Get("fork_ids"), ",")
		parsedForkIds := make([]uint64, 0, len(forkIdList))
		for _, forkIdStr := range forkIdList {
			forkIdStr = strings.TrimSpace(forkIdStr)
			if forkIdStr != "" {
				forkId, err := strconv.ParseUint(forkIdStr, 10, 64)
				if err != nil {
					http.Error(w, `{"status": "ERROR: invalid fork_ids"}`, http.StatusBadRequest)
					return
				}
				parsedForkIds = append(parsedForkIds, forkId)
			}
		}
		if len(parsedForkIds) > 0 {
			blockFilter.ForkIds = parsedForkIds
		}
	}

	// Calculate page index based on start slot
	pageIdx := uint64(0)
	if startSlot != nil {
		// The page index is calculated based on how many pages would be before this slot
		// Since slots are sorted descending, we need to know how many slots are after this one
		chainState := services.GlobalBeaconService.GetChainState()
		currentSlot := uint64(chainState.CurrentSlot())
		if *startSlot <= currentSlot {
			pageIdx = currentSlot - *startSlot
		}
	}

	// Get blocks from database
	dbBlocks := services.GlobalBeaconService.GetDbBlocksByFilter(blockFilter, pageIdx, uint32(limit+1), 0)

	// Check if we have MEV blocks to fetch
	var mevBlocksMap map[string]*dbtypes.MevBlock
	var execBlockHashes [][]byte
	for _, dbBlock := range dbBlocks {
		if dbBlock.Block != nil && dbBlock.Block.Status > 0 && dbBlock.Block.EthBlockHash != nil {
			execBlockHashes = append(execBlockHashes, dbBlock.Block.EthBlockHash)
		}
	}
	if len(execBlockHashes) > 0 {
		mevBlocksMap = db.GetMevBlocksByBlockHashes(execBlockHashes)
	}

	// Get chain state for finalization check
	finalizedEpoch, _ := services.GlobalBeaconService.GetFinalizedEpoch()
	chainState := services.GlobalBeaconService.GetChainState()
	currentSlot := chainState.CurrentSlot()

	// Process results
	slots := make([]*APISlotListItem, 0, limit)
	var nextSlot *uint64

	for idx, dbBlock := range dbBlocks {
		if idx >= int(limit) {
			// We have more results, set next slot for pagination
			if dbBlock.Block != nil {
				next := dbBlock.Block.Slot
				nextSlot = &next
			} else {
				next := dbBlock.Slot
				nextSlot = &next
			}
			break
		}

		slot := phase0.Slot(dbBlock.Slot)
		slotItem := &APISlotListItem{
			Slot:         uint64(slot),
			Epoch:        uint64(chainState.EpochOfSlot(slot)),
			Time:         chainState.SlotToTime(slot),
			Finalized:    finalizedEpoch >= chainState.EpochOfSlot(slot),
			Scheduled:    slot >= currentSlot,
			Proposer:     dbBlock.Proposer,
			ProposerName: services.GlobalBeaconService.GetValidatorName(dbBlock.Proposer),
		}

		if dbBlock.Block != nil {
			if dbBlock.Block.Status != dbtypes.Missing {
				slotItem.Scheduled = false
			}

			// Set status
			switch dbBlock.Block.Status {
			case dbtypes.Missing:
				slotItem.Status = "Missing"
			case dbtypes.Canonical:
				slotItem.Status = "Canonical"
			case dbtypes.Orphaned:
				slotItem.Status = "Orphaned"
			default:
				slotItem.Status = "Unknown"
			}

			slotItem.AttestationCount = dbBlock.Block.AttestationCount
			slotItem.DepositCount = dbBlock.Block.DepositCount
			slotItem.ExitCount = dbBlock.Block.ExitCount
			slotItem.ProposerSlashingCount = dbBlock.Block.ProposerSlashingCount
			slotItem.AttesterSlashingCount = dbBlock.Block.AttesterSlashingCount
			slotItem.SyncAggregateParticipation = float64(dbBlock.Block.SyncParticipation) * 100
			slotItem.EthTransactionCount = dbBlock.Block.EthTransactionCount
			slotItem.BlobCount = dbBlock.Block.BlobCount
			slotItem.Graffiti = fmt.Sprintf("0x%x", dbBlock.Block.Graffiti)
			slotItem.GraffitiText = string(dbBlock.Block.Graffiti)
			slotItem.ElExtraData = fmt.Sprintf("0x%x", dbBlock.Block.EthBlockExtra)
			slotItem.GasUsed = dbBlock.Block.EthGasUsed
			slotItem.GasLimit = dbBlock.Block.EthGasLimit
			slotItem.BlockSize = dbBlock.Block.BlockSize
			slotItem.BlockRoot = fmt.Sprintf("0x%x", dbBlock.Block.Root)
			slotItem.ParentRoot = fmt.Sprintf("0x%x", dbBlock.Block.ParentRoot)
			slotItem.StateRoot = fmt.Sprintf("0x%x", dbBlock.Block.StateRoot)

			if dbBlock.Block.RecvDelay != 0 {
				slotItem.RecvDelay = &dbBlock.Block.RecvDelay
			}

			if dbBlock.Block.EthBlockNumber != nil {
				slotItem.WithEthBlock = true
				slotItem.EthBlockNumber = dbBlock.Block.EthBlockNumber
			}

			// Check for MEV block
			if dbBlock.Block.EthBlockHash != nil && mevBlocksMap != nil {
				if mevBlock, exists := mevBlocksMap[fmt.Sprintf("%x", dbBlock.Block.EthBlockHash)]; exists {
					slotItem.IsMevBlock = true

					var relays []string
					for _, relay := range utils.Config.MevIndexer.Relays {
						relayFlag := uint64(1) << uint64(relay.Index)
						if mevBlock.SeenbyRelays&relayFlag > 0 {
							relays = append(relays, relay.Name)
						}
					}
					slotItem.MevBlockRelays = strings.Join(relays, ", ")
				}
			}

			// Add execution times if available
			if dbBlock.Block.MinExecTime > 0 && dbBlock.Block.MaxExecTime > 0 {
				slotItem.MinExecTime = &dbBlock.Block.MinExecTime
				slotItem.MaxExecTime = &dbBlock.Block.MaxExecTime

				// Deserialize execution times if available
				if len(dbBlock.Block.ExecTimes) > 0 {
					execTimes := []beacon.ExecutionTime{}
					if err := services.GlobalBeaconService.GetBeaconIndexer().GetDynSSZ().UnmarshalSSZ(&execTimes, dbBlock.Block.ExecTimes); err == nil {
						slotItem.ExecutionTimes = make([]models.ExecutionTimeDetail, 0, len(execTimes))
						totalAvg := uint64(0)
						totalCount := uint64(0)

						for _, et := range execTimes {
							detail := models.ExecutionTimeDetail{
								ClientType: getClientTypeName(et.ClientType),
								MinTime:    et.MinTime,
								MaxTime:    et.MaxTime,
								AvgTime:    et.AvgTime,
								Count:      et.Count,
							}
							slotItem.ExecutionTimes = append(slotItem.ExecutionTimes, detail)
							totalAvg += uint64(et.AvgTime) * uint64(et.Count)
							totalCount += uint64(et.Count)
						}

						if totalCount > 0 {
							avgTime := uint32(totalAvg / totalCount)
							slotItem.AvgExecTime = &avgTime
						}
					}
				}

				// If we don't have detailed times, calculate average from min/max
				if slotItem.AvgExecTime == nil {
					avgTime := (*slotItem.MinExecTime + *slotItem.MaxExecTime) / 2
					slotItem.AvgExecTime = &avgTime
				}
			}
		} else {
			// Missing slot
			slotItem.Status = "Scheduled"
			if slot < currentSlot {
				slotItem.Status = "Missing"
			}
		}

		slots = append(slots, slotItem)
	}

	// Build response
	response := APISlotsResponse{
		Status: "OK",
		Data: &APISlotsData{
			Slots:      slots,
			TotalCount: len(slots),
			NextSlot:   nextSlot,
		},
	}

	// Send response
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode slots response")
		http.Error(w, `{"status": "ERROR: failed to encode response"}`, http.StatusInternalServerError)
		return
	}
}

func getClientTypeName(clientType uint8) string {
	if clientType > 0 {
		return execution.ClientType(clientType).String()
	}

	return fmt.Sprintf("Unknown(%d)", clientType)
}
