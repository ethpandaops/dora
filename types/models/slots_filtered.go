package models

import (
	"time"
)

// SlotsPageData is a struct to hold info for the slots page
type SlotsFilteredPageData struct {
	FilterGraffiti     string `json:"filter_graffiti"`
	FilterExtraData    string `json:"filter_extra_data"`
	FilterProposer     string `json:"filter_proposer"`
	FilterProposerName string `json:"filter_pname"`
	FilterWithOrphaned uint8  `json:"filter_orphaned"`
	FilterWithMissing  uint8  `json:"filter_missing"`

	DisplayEpoch        bool   `json:"dp_epoch"`
	DisplaySlot         bool   `json:"dp_slot"`
	DisplayStatus       bool   `json:"dp_status"`
	DisplayTime         bool   `json:"dp_time"`
	DisplayProposer     bool   `json:"dp_proposer"`
	DisplayAttestations bool   `json:"dp_attestations"`
	DisplayDeposits     bool   `json:"dp_deposits"`
	DisplaySlashings    bool   `json:"dp_slashings"`
	DisplayTxCount      bool   `json:"dp_txcount"`
	DisplaySyncAgg      bool   `json:"dp_syncagg"`
	DisplayGraffiti     bool   `json:"dp_graffiti"`
	DisplayElExtraData  bool   `json:"dp_elextra"`
	DisplayColCount     uint64 `json:"display_col_count"`

	Slots     []*SlotsFilteredPageDataSlot `json:"slots"`
	SlotCount uint64                       `json:"slot_count"`
	FirstSlot uint64                       `json:"first_slot"`
	LastSlot  uint64                       `json:"last_slot"`

	IsDefaultPage    bool   `json:"default_page"`
	TotalPages       uint64 `json:"total_pages"`
	PageSize         uint64 `json:"page_size"`
	CurrentPageIndex uint64 `json:"page_index"`
	CurrentPageSlot  uint64 `json:"page_slot"`
	PrevPageIndex    uint64 `json:"prev_page_index"`
	PrevPageSlot     uint64 `json:"prev_page_slot"`
	NextPageIndex    uint64 `json:"next_page_index"`
	NextPageSlot     uint64 `json:"next_page_slot"`
	LastPageSlot     uint64 `json:"last_page_slot"`

	FirstPageLink string `json:"first_page_link"`
	PrevPageLink  string `json:"prev_page_link"`
	NextPageLink  string `json:"next_page_link"`
	LastPageLink  string `json:"last_page_link"`
}

type SlotsFilteredPageDataSlot struct {
	Slot                  uint64    `json:"slot"`
	Epoch                 uint64    `json:"epoch"`
	Ts                    time.Time `json:"ts"`
	Finalized             bool      `json:"scheduled"`
	Scheduled             bool      `json:"finalized"`
	Status                uint8     `json:"status"`
	PayloadStatus         uint8     `json:"payload_status"`
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
	WithEthBlock          bool      `json:"with_eth_block"`
	EthBlockNumber        uint64    `json:"eth_block_number"`
	Graffiti              []byte    `json:"graffiti"`
	ElExtraData           []byte    `json:"el_extra_data"`
	BlockRoot             []byte    `json:"block_root"`
	ParentRoot            []byte    `json:"parent_root"`
}
