package models

import (
	"time"
)

// IndexPageData is a struct to hold info for the main web page
type IndexPageData struct {
	NetworkName           string `json:"networkName"`
	DepositContract       string `json:"depositContract"`
	ShowSyncingMessage    bool
	CurrentEpoch          uint64                 `json:"current_epoch"`
	CurrentFinalizedEpoch uint64                 `json:"current_finalized_epoch"`
	CurrentSlot           uint64                 `json:"current_slot"`
	RecentBlocks          []*IndexPageDataBlocks `json:"recent_blocks"`
	RecentEpochs          []*IndexPageDataEpochs `json:"recent_epochs"`
}

type IndexPageDataEpochs struct {
	Epoch             uint64    `json:"epoch"`
	Ts                time.Time `json:"ts"`
	Finalized         bool      `json:"finalized"`
	EligibleEther     uint64    `json:"eligibleether"`
	TargetVoted       uint64    `json:"target_voted"`
	HeadVoted         uint64    `json:"head_voted"`
	TotalVoted        uint64    `json:"total_voted"`
	VoteParticipation float32   `json:"vote_participation"`
}

type IndexPageDataBlocks struct {
	Epoch        uint64    `json:"epoch"`
	Slot         uint64    `json:"slot"`
	EthBlock     uint64    `json:"eth_block"`
	Ts           time.Time `json:"ts"`
	Proposer     uint64    `json:"proposer"`
	ProposerName string    `json:"proposer_name"`
	Status       uint64    `json:"status"`
	BlockRoot    string    `json:"block_root"`
}
