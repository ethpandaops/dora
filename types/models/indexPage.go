package models

import (
	"time"
)

// IndexPageData is a struct to hold info for the main web page
type IndexPageData struct {
	NetworkName             string    `json:"networkName"`
	DepositContract         string    `json:"depositContract"`
	ShowSyncingMessage      bool      `json:"show_sync_message"`
	CurrentEpoch            uint64    `json:"current_epoch"`
	CurrentFinalizedEpoch   int64     `json:"current_finalized_epoch"`
	CurrentSlot             uint64    `json:"current_slot"`
	CurrentSlotIndex        uint64    `json:"current_slot_index"`
	CurrentScheduledCount   uint64    `json:"current_scheduled_count"`
	CurrentEpochProgress    float64   `json:"current_epoch_progress"`
	ActiveValidatorCount    uint64    `json:"active_validator_count"`
	EnteringValidatorCount  uint64    `json:"entering_validator_count"`
	ExitingValidatorCount   uint64    `json:"exiting_validator_count"`
	ValidatorsPerEpoch      uint64    `json:"validators_per_epoch"`
	ValidatorsPerDay        uint64    `json:"validators_per_day"`
	TotalEligibleEther      uint64    `json:"total_eligible_ether"`
	AverageValidatorBalance uint64    `json:"avg_validator_balance"`
	NewDepositProcessAfter  string    `json:"deposit_queue_delay"`
	GenesisTime             time.Time `json:"genesis_time"`
	GenesisForkVersion      []byte    `json:"genesis_version"`
	GenesisValidatorsRoot   []byte    `json:"genesis_valroot"`

	NetworkForks     []*IndexPageDataForks  `json:"network_forks"`
	RecentBlocks     []*IndexPageDataBlocks `json:"recent_blocks"`
	RecentBlockCount uint64                 `json:"recent_block_count"`
	RecentEpochs     []*IndexPageDataEpochs `json:"recent_epochs"`
	RecentEpochCount uint64                 `json:"recent_epoch_count"`
	RecentSlots      []*IndexPageDataSlots  `json:"recent_slots"`
	RecentSlotCount  uint64                 `json:"recent_slot_count"`
	ForkTreeWidth    int                    `json:"forktree_width"`
}

type IndexPageDataForks struct {
	Name    string `json:"name"`
	Epoch   uint64 `json:"epoch"`
	Version []byte `json:"version"`
	Active  bool   `json:"active"`
}

type IndexPageDataEpochs struct {
	Epoch             uint64    `json:"epoch"`
	Ts                time.Time `json:"ts"`
	Finalized         bool      `json:"finalized"`
	EligibleEther     uint64    `json:"eligibleether"`
	TargetVoted       uint64    `json:"target_voted"`
	HeadVoted         uint64    `json:"head_voted"`
	TotalVoted        uint64    `json:"total_voted"`
	VoteParticipation float64   `json:"vote_participation"`
}

type IndexPageDataBlocks struct {
	Epoch        uint64    `json:"epoch"`
	Slot         uint64    `json:"slot"`
	EthBlock     uint64    `json:"eth_block"`
	Ts           time.Time `json:"ts"`
	Proposer     uint64    `json:"proposer"`
	ProposerName string    `json:"proposer_name"`
	Status       uint64    `json:"status"`
	BlockRoot    []byte    `json:"block_root"`
}

type IndexPageDataSlots struct {
	Epoch        uint64                    `json:"epoch"`
	Slot         uint64                    `json:"slot"`
	EthBlock     uint64                    `json:"eth_block"`
	Ts           time.Time                 `json:"ts"`
	Proposer     uint64                    `json:"proposer"`
	ProposerName string                    `json:"proposer_name"`
	Status       uint64                    `json:"status"`
	BlockRoot    []byte                    `json:"block_root"`
	ParentRoot   []byte                    `json:"parent_root"`
	ForkGraph    []*IndexPageDataForkGraph `json:"fork_graph"`
}

type IndexPageDataForkGraph struct {
	Index int             `json:"index"`
	Left  int             `json:"left"`
	Tiles map[string]bool `json:"tiles"`
	Block bool            `json:"block"`
}
