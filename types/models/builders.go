package models

import (
	"time"
)

// BuildersPageData is a struct to hold info for the builders page
type BuildersPageData struct {
	FilterPubKey        string                         `json:"filter_pubkey"`
	FilterIndex         string                         `json:"filter_index"`
	FilterExecutionAddr string                         `json:"filter_execution_addr"`
	FilterStatus        string                         `json:"filter_status"`
	FilterStatusOpts    []BuildersPageDataStatusOption `json:"filter_status_opts"`

	Builders         []*BuildersPageDataBuilder `json:"builders"`
	BuilderCount     uint64                     `json:"builder_count"`
	FirstBuilder     uint64                     `json:"first_builder"`
	LastBuilder      uint64                     `json:"last_builder"`
	Sorting          string                     `json:"sorting"`
	IsDefaultSorting bool                       `json:"default_sorting"`
	IsDefaultPage    bool                       `json:"default_page"`
	TotalPages       uint64                     `json:"total_pages"`
	PageSize         uint64                     `json:"page_size"`
	CurrentPageIndex uint64                     `json:"page_index"`
	PrevPageIndex    uint64                     `json:"prev_page_index"`
	NextPageIndex    uint64                     `json:"next_page_index"`
	LastPageIndex    uint64                     `json:"last_page_index"`
	FilteredPageLink string                     `json:"filtered_page_link"`

	UrlParams map[string]string `json:"url_params"`
}

type BuildersPageDataStatusOption struct {
	Status string `json:"status"`
	Count  uint64 `json:"count"`
}

type BuildersPageDataBuilder struct {
	Index             uint64    `json:"index"`
	PublicKey         []byte    `json:"pubkey"`
	ExecutionAddress  []byte    `json:"execution_address"`
	Balance           uint64    `json:"balance"`
	State             string    `json:"state"`
	ShowDeposit       bool      `json:"show_deposit"`
	DepositTs         time.Time `json:"deposit_ts"`
	DepositEpoch      uint64    `json:"deposit_epoch"`
	ShowWithdrawable  bool      `json:"show_withdrawable"`
	WithdrawableTs    time.Time `json:"withdrawable_ts"`
	WithdrawableEpoch uint64    `json:"withdrawable_epoch"`
}

// BuilderPageData holds data for the builder details page
type BuilderPageData struct {
	CurrentEpoch     uint64 `json:"current_epoch"`
	Index            uint64 `json:"index"`
	Name             string `json:"name"`
	PublicKey        []byte `json:"pubkey"`
	Balance          uint64 `json:"balance"`
	ExecutionAddress []byte `json:"execution_address"`
	Version          uint8  `json:"version"`
	State            string `json:"state"` // "Active", "Exited", "Superseded"

	// Deposit lifecycle
	ShowDeposit  bool      `json:"show_deposit"`
	DepositEpoch uint64    `json:"deposit_epoch"`
	DepositTs    time.Time `json:"deposit_ts"`

	// Withdrawable lifecycle
	ShowWithdrawable  bool      `json:"show_withdrawable"`
	WithdrawableEpoch uint64    `json:"withdrawable_epoch"`
	WithdrawableTs    time.Time `json:"withdrawable_ts"`

	IsSuperseded bool `json:"is_superseded"`

	// Tab control
	TabView       string `json:"tab_view"`
	GloasIsActive bool   `json:"gloas_is_active"`

	// Tab data (loaded conditionally)
	RecentBlocks   []*BuilderPageDataBlock   `json:"recent_blocks"`
	RecentBids     []*BuilderPageDataBid     `json:"recent_bids"`
	RecentDeposits []*BuilderPageDataDeposit `json:"recent_deposits"`
}

// BuilderPageDataBlock represents a block/payload built by this builder
type BuilderPageDataBlock struct {
	Epoch        uint64    `json:"epoch"`
	Slot         uint64    `json:"slot"`
	Ts           time.Time `json:"ts"`
	BlockRoot    []byte    `json:"block_root"`
	BlockHash    []byte    `json:"block_hash"`
	Status       uint16    `json:"status"` // PayloadStatus
	FeeRecipient []byte    `json:"fee_recipient"`
	GasLimit     uint64    `json:"gas_limit"`
	Value        uint64    `json:"value"`
	ElPayment    uint64    `json:"el_payment"`
}

// BuilderPageDataBid represents a bid submitted by this builder
type BuilderPageDataBid struct {
	Slot         uint64    `json:"slot"`
	Ts           time.Time `json:"ts"`
	ParentRoot   []byte    `json:"parent_root"`
	ParentHash   []byte    `json:"parent_hash"`
	BlockHash    []byte    `json:"block_hash"`
	FeeRecipient []byte    `json:"fee_recipient"`
	GasLimit     uint64    `json:"gas_limit"`
	Value        uint64    `json:"value"`
	ElPayment    uint64    `json:"el_payment"`
	IsWinning    bool      `json:"is_winning"`
}

// BuilderPageDataDeposit represents a builder deposit or voluntary exit
type BuilderPageDataDeposit struct {
	Type       string    `json:"type"` // "exit"
	SlotNumber uint64    `json:"slot"`
	SlotRoot   []byte    `json:"slot_root"`
	Time       time.Time `json:"time"`
	Orphaned   bool      `json:"orphaned"`
}
