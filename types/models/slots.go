package models

import (
	"time"
)

// SlotsPageData is a struct to hold info for the slots page
type SlotsPageData struct {
	Slots         []*SlotsPageDataSlot `json:"slots"`
	SlotCount     uint64               `json:"slot_count"`
	FirstSlot     uint64               `json:"first_slot"`
	LastSlot      uint64               `json:"last_slot"`
	ForkTreeWidth int                  `json:"forktree_width"`

	DisplayChain        bool   `json:"dp_chain"`
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
	DisplayGasUsage     bool   `json:"dp_gasusage"`
	DisplayGasLimit     bool   `json:"dp_gaslimit"`
	DisplayMevBlock     bool   `json:"dp_mevblock"`
	DisplayBlockSize    bool   `json:"dp_blocksize"`
	DisplayRecvDelay    bool   `json:"dp_recvdelay"`
	DisplayExecTime     bool   `json:"dp_exectime"`
	DisplayColCount     uint64 `json:"display_col_count"`

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

	UrlParams map[string]string `json:"url_params"`
	MaxSlot   uint64            `json:"max_slot"`
}

type SlotsPageDataSlot struct {
	Slot                  uint64                    `json:"slot"`
	Epoch                 uint64                    `json:"epoch"`
	Ts                    time.Time                 `json:"ts"`
	Finalized             bool                      `json:"scheduled"`
	Scheduled             bool                      `json:"finalized"`
	Status                uint8                     `json:"status"`
	Synchronized          bool                      `json:"synchronized"`
	Proposer              uint64                    `json:"proposer"`
	ProposerName          string                    `json:"proposer_name"`
	AttestationCount      uint64                    `json:"attestation_count"`
	DepositCount          uint64                    `json:"deposit_count"`
	ExitCount             uint64                    `json:"exit_count"`
	ProposerSlashingCount uint64                    `json:"proposer_slashing_count"`
	AttesterSlashingCount uint64                    `json:"attester_slashing_count"`
	SyncParticipation     float64                   `json:"sync_participation"`
	EthTransactionCount   uint64                    `json:"eth_transaction_count"`
	BlobCount             uint64                    `json:"blob_count"`
	WithEthBlock          bool                      `json:"with_eth_block"`
	EthBlockNumber        uint64                    `json:"eth_block_number"`
	Graffiti              []byte                    `json:"graffiti"`
	ElExtraData           []byte                    `json:"el_extra_data"`
	GasUsed               uint64                    `json:"gas_used"`
	GasLimit              uint64                    `json:"gas_limit"`
	BlockSize             uint64                    `json:"block_size"`
	BlockRoot             []byte                    `json:"block_root"`
	ParentRoot            []byte                    `json:"parent_root"`
	RecvDelay             int32                     `json:"recv_delay"`
	MinExecTime           uint32                    `json:"min_exec_time"`
	MaxExecTime           uint32                    `json:"max_exec_time"`
	AvgExecTime           uint32                    `json:"avg_exec_time"`
	ExecutionTimes        []ExecutionTimeDetail     `json:"execution_times"`
	ForkGraph             []*SlotsPageDataForkGraph `json:"fork_graph"`
	IsMevBlock            bool                      `json:"is_mev_block"`
	MevBlockRelays        string                    `json:"mev_block_relays"`
}

type SlotsPageDataForkGraph struct {
	Index int             `json:"index"`
	Left  int             `json:"left"`
	Tiles map[string]bool `json:"tiles"`
	Block bool            `json:"block"`
}

type ExecutionTimeDetail struct {
	ClientType string `json:"client_type"`
	MinTime    uint16 `json:"min_time"`
	MaxTime    uint16 `json:"max_time"`
	AvgTime    uint16 `json:"avg_time"`
	Count      uint16 `json:"count"`
}
