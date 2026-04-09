package models

import (
	"time"
)

// WithdrawalsListPageData is a struct to hold info for the withdrawals list page.
type WithdrawalsListPageData struct {
	FilterEntity        string `json:"filter_entity"` // "all", "validator", or "builder"
	FilterMinIndex      uint64 `json:"filter_mini"`
	FilterMaxIndex      uint64 `json:"filter_maxi"`
	FilterValidatorName string `json:"filter_vname"`
	FilterAddress       string `json:"filter_address"`
	FilterWithType      string `json:"filter_type"`
	FilterMinAmount     string `json:"filter_min_amount"`
	FilterMaxAmount     string `json:"filter_max_amount"`
	FilterWithOrphaned  uint8  `json:"filter_orphaned"`

	Withdrawals     []*WithdrawalsListPageDataWithdrawal `json:"withdrawals"`
	WithdrawalCount uint64                               `json:"withdrawal_count"`
	FirstIndex      uint64                               `json:"first_index"`
	LastIndex       uint64                               `json:"last_index"`

	IsDefaultPage    bool   `json:"default_page"`
	TotalPages       uint64 `json:"total_pages"`
	PageSize         uint64 `json:"page_size"`
	CurrentPageIndex uint64 `json:"page_index"`
	PrevPageIndex    uint64 `json:"prev_page_index"`
	NextPageIndex    uint64 `json:"next_page_index"`
	LastPageIndex    uint64 `json:"last_page_index"`

	FirstPageLink string `json:"first_page_link"`
	PrevPageLink  string `json:"prev_page_link"`
	NextPageLink  string `json:"next_page_link"`
	LastPageLink  string `json:"last_page_link"`

	UrlParams []UrlParam `json:"url_params"`
}

// WithdrawalsListPageDataWithdrawal represents a single withdrawal entry.
type WithdrawalsListPageDataWithdrawal struct {
	SlotNumber     uint64    `json:"slot"`
	BlockRoot      []byte    `json:"block_root" ssz-size:"32"`
	BlockNumber    uint64    `json:"block_number"`
	Time           time.Time `json:"time"`
	Orphaned       bool      `json:"orphaned"`
	Type           uint8     `json:"type"`
	HasValidator   bool      `json:"has_validator"`
	IsBuilder      bool      `json:"is_builder"`
	ValidatorIndex uint64    `json:"vindex"`
	ValidatorName  string    `json:"vname"`
	Address        []byte    `json:"address" ssz-size:"20"`
	Amount         uint64    `json:"amount"` // Gwei
	RefSlot        uint64    `json:"ref_slot"`
	RefSlotRoot    []byte    `json:"ref_slot_root" ssz-size:"32"`
}
