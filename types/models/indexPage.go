package models

import (
	"time"
)

// IndexPageData is a struct to hold info for the main web page
type IndexPageData struct {
	NetworkName             string    `json:"netname"`
	DepositContract         string    `json:"depaddr"`
	ShowSyncingMessage      bool      `json:"show_sync"`
	SlotsPerEpoch           uint64    `json:"slots_per_epoch"`
	CurrentEpoch            uint64    `json:"cur_epoch"`
	CurrentFinalizedEpoch   int64     `json:"finalized_epoch"`
	CurrentJustifiedEpoch   int64     `json:"justified_epoch"`
	CurrentSlot             uint64    `json:"cur_slot"`
	CurrentScheduledCount   uint64    `json:"cur_scheduled"`
	CurrentEpochProgress    float64   `json:"cur_epoch_prog"`
	ActiveValidatorCount    uint64    `json:"active_val"`
	EnteringValidatorCount  uint64    `json:"entering_val"`
	ExitingValidatorCount   uint64    `json:"exiting_val"`
	ValidatorsPerEpoch      uint64    `json:"churn_epoch"`
	ValidatorsPerDay        uint64    `json:"churn_day"`
	TotalEligibleEther      uint64    `json:"eligible"`
	AverageValidatorBalance uint64    `json:"avg_balance"`
	NewDepositProcessAfter  string    `json:"queue_delay"`
	GenesisTime             time.Time `json:"genesis_time"`
	GenesisForkVersion      []byte    `json:"genesis_version"`
	GenesisValidatorsRoot   []byte    `json:"genesis_valroot"`

	NetworkForks     []*IndexPageDataForks  `json:"forks"`
	RecentBlocks     []*IndexPageDataBlocks `json:"blocks"`
	RecentBlockCount uint64                 `json:"block_count"`
	RecentEpochs     []*IndexPageDataEpochs `json:"epochs"`
	RecentEpochCount uint64                 `json:"epoch_count"`
	RecentSlots      []*IndexPageDataSlots  `json:"slots"`
	RecentSlotCount  uint64                 `json:"slot_count"`
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
	Justified         bool      `json:"justified"`
	EligibleEther     uint64    `json:"eligible"`
	TargetVoted       uint64    `json:"voted"`
	VoteParticipation float64   `json:"votep"`
}

type IndexPageDataBlocks struct {
	Epoch         uint64    `json:"epoch"`
	Slot          uint64    `json:"slot"`
	WithEthBlock  bool      `json:"has_block"`
	EthBlock      uint64    `json:"eth_block"`
	EthBlockLink  string    `json:"eth_link"`
	Ts            time.Time `json:"ts"`
	Proposer      uint64    `json:"proposer"`
	ProposerName  string    `json:"proposer_name"`
	Status        uint64    `json:"status"`
	PayloadStatus uint8     `json:"payload_status"`
	BlockRoot     []byte    `json:"block_root"`
}

type IndexPageDataSlots struct {
	Epoch         uint64                    `json:"epoch"`
	Slot          uint64                    `json:"slot"`
	EthBlock      uint64                    `json:"eth_block"`
	Ts            time.Time                 `json:"ts"`
	Proposer      uint64                    `json:"proposer"`
	ProposerName  string                    `json:"proposer_name"`
	Status        uint64                    `json:"status"`
	PayloadStatus uint8                     `json:"payload_status"`
	BlockRoot     []byte                    `json:"block_root"`
	ParentRoot    []byte                    `json:"-"`
	ForkGraph     []*IndexPageDataForkGraph `json:"fork_graph"`
}

type IndexPageDataForkGraph struct {
	Index int             `json:"index"`
	Left  int             `json:"left"`
	Tiles map[string]bool `json:"tiles"`
	Block bool            `json:"block"`
}
