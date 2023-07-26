package models

import (
	"time"
)

// SlotsPageData is a struct to hold info for the slots page
type SlotsPageData struct {
	Slots          []*SlotsPageDataSlot `json:"slots"`
	SlotCount      uint64
	FirstSlot      uint64
	LastSlot       uint64
	GraffitiFilter string

	IsDefaultPage    bool
	TotalPages       uint64
	PageSize         uint64
	CurrentPageIndex uint64
	CurrentPageSlot  uint64
	PrevPageIndex    uint64
	PrevPageSlot     uint64
	NextPageIndex    uint64
	NextPageSlot     uint64
	LastPageSlot     uint64
}

type SlotsPageDataSlot struct {
	Slot                  uint64    `json:"slot"`
	Epoch                 uint64    `json:"epoch"`
	Ts                    time.Time `json:"ts"`
	Finalized             bool      `json:"scheduled"`
	Scheduled             bool      `json:"finalized"`
	Status                uint8     `json:"status"`
	Synchronized          bool      `json:"synchronized"`
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
	Graffiti              []byte    `json:"graffiti"`
	BlockRoot             []byte    `json:"block_root"`
}
