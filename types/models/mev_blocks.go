package models

import (
	"time"
)

// MevBlocksPageData is a struct to hold info for the mev_blocks page
type MevBlocksPageData struct {
	FilterMinSlot       uint64                    `json:"filter_mins"`
	FilterMaxSlot       uint64                    `json:"filter_maxs"`
	FilterMinIndex      uint64                    `json:"filter_mini"`
	FilterMaxIndex      uint64                    `json:"filter_maxi"`
	FilterValidatorName string                    `json:"filter_vname"`
	FilterRelays        map[uint8]bool            `json:"filter_relays"`
	FilterRelayOpts     []*MevBlocksPageDataRelay `json:"filter_relay_opts"`
	FilterProposed      map[uint8]bool            `json:"filter_proposed"`

	MevBlocks  []*MevBlocksPageDataBlock `json:"exits"`
	BlockCount uint64                    `json:"block_count"`
	FirstIndex uint64                    `json:"first_index"`
	LastIndex  uint64                    `json:"last_index"`

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
}

type MevBlocksPageDataBlock struct {
	SlotNumber     uint64                    `json:"slot"`
	BlockHash      []byte                    `json:"block_hash"`
	BlockNumber    uint64                    `json:"block_number"`
	Time           time.Time                 `json:"time"`
	ValidatorIndex uint64                    `json:"vindex"`
	ValidatorName  string                    `json:"vname"`
	BuilderPubkey  []byte                    `json:"builder"`
	Proposed       uint8                     `json:"proposed"`
	Relays         []*MevBlocksPageDataRelay `json:"relays"`
	RelayCount     uint64                    `json:"relay_count"`
	FeeRecipient   []byte                    `json:"fee_recipient"`
	TxCount        uint64                    `json:"tx_count"`
	GasUsed        uint64                    `json:"gas_used"`
	BlockValue     uint64                    `json:"block_value"`
	BlockValueStr  string                    `json:"block_value_str"`
}

type MevBlocksPageDataRelay struct {
	Index uint64 `json:"index"`
	Name  string `json:"name"`
}
