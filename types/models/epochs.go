package models

import (
	"time"
)

// EpochsPageData is a struct to hold info for the epochs page
type EpochsPageData struct {
	Epochs     []*EpochsPageDataEpoch `json:"epochs"`
	EpochCount uint64
}

type EpochsPageDataEpoch struct {
	Epoch                   uint64    `json:"epoch"`
	Ts                      time.Time `json:"ts"`
	Finalized               bool      `json:"finalized"`
	Synchronized            bool      `json:"synchronized"`
	CanonicalBlockCount     uint64    `json:"canonical_block_count"`
	OrphanedBlockCount      uint64    `json:"orphaned_block_count"`
	AttestationCount        uint64    `json:"attestation_count"`
	DepositCount            uint64    `json:"deposit_count"`
	ExitCount               uint64    `json:"exit_count"`
	ProposerSlashingCount   uint64    `json:"proposer_slashing_count"`
	AttesterSlashingCount   uint64    `json:"attester_slashing_count"`
	EligibleEther           uint64    `json:"eligibleether"`
	TargetVoted             uint64    `json:"target_voted"`
	HeadVoted               uint64    `json:"head_voted"`
	TotalVoted              uint64    `json:"total_voted"`
	TargetVoteParticipation float64   `json:"target_vote_participation"`
	HeadVoteParticipation   float64   `json:"head_vote_participation"`
	TotalVoteParticipation  float64   `json:"total_vote_participation"`
	EthTransactionCount     uint64    `json:"eth_transaction_count"`
}
