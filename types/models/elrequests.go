package models

import (
	"time"
)

// ElRequestsPageData is a struct to hold info for the el_requests page
type ElRequestsPageData struct {
	FilterMinSlot        uint64 `json:"filter_mins"`
	FilterMaxSlot        uint64 `json:"filter_maxs"`
	FilterSourceAddress  string `json:"filter_src_addr"`
	FilterMinSourceIndex uint64 `json:"filter_src_min"`
	FilterMaxSourceIndex uint64 `json:"filter_src_max"`
	FilterSourceName     string `json:"filter_src_name"`
	FilterWithRequest    uint8  `json:"filter_req_type"`
	FilterWithOrphaned   uint8  `json:"filter_orphaned"`

	ElRequests   []*ElRequestsPageDataRequest `json:"requests"`
	RequestCount uint64                       `json:"request_count"`
	FirstIndex   uint64                       `json:"first_index"`
	LastIndex    uint64                       `json:"last_index"`

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

type ElRequestsPageDataRequest struct {
	SlotNumber       uint64    `json:"slot"`
	SlotRoot         []byte    `json:"slot_root"`
	Time             time.Time `json:"time"`
	Orphaned         bool      `json:"orphaned"`
	RequestType      uint8     `json:"req_type"`
	SourceAddress    []byte    `json:"src_address"`
	SourceIndex      uint64    `json:"src_index"`
	SourceIndexValid bool      `json:"src_valid"`
	SourceName       string    `json:"src_name"`
	SourcePubkey     []byte    `json:"src_pubkey"`
	TargetIndex      uint64    `json:"tgt_index"`
	TargetIndexValid bool      `json:"tgt_valid"`
	TargetName       string    `json:"tgt_name"`
	TargetPubkey     []byte    `json:"tgt_pubkey"`
	Amount           uint64    `json:"amount"`
	Valid            bool      `json:"valid"`
}
