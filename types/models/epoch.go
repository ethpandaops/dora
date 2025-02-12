package models

import (
	"time"
)

// EpochPageData is a struct to hold info for the epoch page
type EpochPageData struct {
	Epoch                   uint64               `json:"epoch"`
	PreviousEpoch           uint64               `json:"prev_epoch"`
	NextEpoch               uint64               `json:"next_epoch"`
	Ts                      time.Time            `json:"ts"`
	Synchronized            bool                 `json:"synchronized"`
	Finalized               bool                 `json:"finalized"`
	AttestationCount        uint64               `json:"attestation_count"`
	DepositCount            uint64               `json:"deposit_count"`
	ExitCount               uint64               `json:"exit_count"`
	WithdrawalCount         uint64               `json:"withdrawal_count"`
	WithdrawalAmount        uint64               `json:"withdrawal_amount"`
	ProposerSlashingCount   uint64               `json:"proposer_slashing_count"`
	AttesterSlashingCount   uint64               `json:"attester_slashing_count"`
	EligibleEther           uint64               `json:"eligibleether"`
	TargetVoted             uint64               `json:"target_voted"`
	HeadVoted               uint64               `json:"head_voted"`
	TotalVoted              uint64               `json:"total_voted"`
	TargetVoteParticipation float64              `json:"target_vote_participation"`
	HeadVoteParticipation   float64              `json:"head_vote_participation"`
	TotalVoteParticipation  float64              `json:"total_vote_participation"`
	SyncParticipation       float64              `json:"sync_participation"`
	ValidatorCount          uint64               `json:"validator_count"`
	AverageValidatorBalance uint64               `json:"avg_validator_balance"`
	BlockCount              uint64               `json:"block_count"`
	CanonicalCount          uint64               `json:"canonical_count"`
	MissedCount             uint64               `json:"missed_count"`
	ScheduledCount          uint64               `json:"scheduled_count"`
	OrphanedCount           uint64               `json:"orphaned_count"`
	EthTransactionCount     uint64               `json:"eth_transaction_count"`
	Slots                   []*EpochPageDataSlot `json:"slots"`
}

type EpochPageDataSlot struct {
	Slot                  uint64    `json:"slot"`
	Epoch                 uint64    `json:"epoch"`
	Ts                    time.Time `json:"ts"`
	Scheduled             bool      `json:"scheduled"`
	Status                uint8     `json:"status"`
	PayloadStatus         uint8     `json:"payload_status"`
	Proposer              uint64    `json:"proposer"`
	ProposerName          string    `json:"proposer_name"`
	AttestationCount      uint64    `json:"attestation_count"`
	DepositCount          uint64    `json:"deposit_count"`
	ExitCount             uint64    `json:"exit_count"`
	ProposerSlashingCount uint64    `json:"proposer_slashing_count"`
	AttesterSlashingCount uint64    `json:"attester_slashing_count"`
	SyncParticipation     float64   `json:"sync_participation"`
	EthTransactionCount   uint64    `json:"eth_transaction_count"`
	EthBlockNumber        uint64    `json:"eth_block_number"`
	WithEthBlock          bool      `json:"with_eth_block"`
	Graffiti              []byte    `json:"graffiti"`
	BlockRoot             []byte    `json:"block_root"`
}
