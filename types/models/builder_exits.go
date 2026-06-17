package models

import (
	"time"
)

// BuilderExitsPageData is a struct to hold info for the builder exits page
type BuilderExitsPageData struct {
	FilterMinSlot    uint64 `json:"filter_mins"`
	FilterMaxSlot    uint64 `json:"filter_maxs"`
	FilterPubKey     string `json:"filter_pubkey"`
	FilterSourceAddr string `json:"filter_source"`
	FilterMinIndex   uint64 `json:"filter_mini"`
	FilterMaxIndex   uint64 `json:"filter_maxi"`

	Exits      []*BuilderExitsPageDataExit `json:"exits"`
	ExitCount  uint64                      `json:"exit_count"`
	FirstIndex uint64                      `json:"first_index"`
	LastIndex  uint64                      `json:"last_index"`

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

type BuilderExitsPageDataExit struct {
	IsIncluded          bool      `json:"is_included"`
	SlotNumber          uint64    `json:"slot"`
	SlotRoot            []byte    `json:"slot_root" ssz-size:"32"`
	Time                time.Time `json:"time"`
	Orphaned            bool      `json:"orphaned"`
	SourceAddress       []byte    `json:"source_address" ssz-size:"20"`
	PublicKey           []byte    `json:"pubkey" ssz-size:"48"`
	HasBuilderIndex     bool      `json:"has_builder_index"`
	BuilderIndex        uint64    `json:"builder_index"`
	Result              uint8     `json:"result"`
	HasTransaction      bool      `json:"has_transaction"`
	TransactionHash     []byte    `json:"tx_hash" ssz-size:"32"`
	TransactionOrphaned bool      `json:"tx_orphaned"`
	BlockNumber         uint64    `json:"block_number"`
}
