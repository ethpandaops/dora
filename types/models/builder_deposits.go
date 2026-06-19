package models

import (
	"time"
)

// BuilderDepositsPageData is a struct to hold info for the builder deposits page
type BuilderDepositsPageData struct {
	FilterMinSlot   uint64 `json:"filter_mins"`
	FilterMaxSlot   uint64 `json:"filter_maxs"`
	FilterPubKey    string `json:"filter_pubkey"`
	FilterMinIndex  uint64 `json:"filter_mini"`
	FilterMaxIndex  uint64 `json:"filter_maxi"`
	FilterMinAmount uint64 `json:"filter_mina"`
	FilterMaxAmount uint64 `json:"filter_maxa"`

	Deposits     []*BuilderDepositsPageDataDeposit `json:"deposits"`
	DepositCount uint64                            `json:"deposit_count"`
	FirstIndex   uint64                            `json:"first_index"`
	LastIndex    uint64                            `json:"last_index"`

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

type BuilderDepositsPageDataDeposit struct {
	IsIncluded            bool                             `json:"is_included"` // included in a block (CL request) vs pending tx only
	SlotNumber            uint64                           `json:"slot"`
	SlotRoot              []byte                           `json:"slot_root" ssz-size:"32"`
	Time                  time.Time                        `json:"time"`
	Orphaned              bool                             `json:"orphaned"`
	PublicKey             []byte                           `json:"pubkey" ssz-size:"48"`
	WithdrawalCredentials []byte                           `json:"wdcreds" ssz-size:"32"`
	Amount                uint64                           `json:"amount"`
	HasBuilderIndex       bool                             `json:"has_builder_index"`
	BuilderIndex          uint64                           `json:"builder_index"`
	Result                uint8                            `json:"result"`
	HasTransaction        bool                             `json:"has_transaction"`
	TransactionHash       []byte                           `json:"tx_hash" ssz-size:"32"`
	TransactionDetails    *BuilderPageDataDepositTxDetails `json:"tx_details"`
	TransactionOrphaned   bool                             `json:"tx_orphaned"`
	BlockNumber           uint64                           `json:"block_number"`
}
