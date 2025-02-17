package models

import (
	"time"
)

// ElConsolidationsPageData is a struct to hold info for the el_consolidations page
type ElConsolidationsPageData struct {
	FilterMinSlot          uint64 `json:"filter_mins"`
	FilterMaxSlot          uint64 `json:"filter_maxs"`
	FilterAddress          string `json:"filter_address"`
	FilterPublicKey        string `json:"filter_pubkey"`
	FilterMinSrcIndex      uint64 `json:"filter_minsi"`
	FilterMaxSrcIndex      uint64 `json:"filter_maxsi"`
	FilterSrcValidatorName string `json:"filter_svname"`
	FilterMinTgtIndex      uint64 `json:"filter_minti"`
	FilterMaxTgtIndex      uint64 `json:"filter_maxti"`
	FilterTgtValidatorName string `json:"filter_tvname"`
	FilterWithOrphaned     uint8  `json:"filter_orphaned"`

	ElRequests   []*ElConsolidationsPageDataConsolidation `json:"consolidations"`
	RequestCount uint64                                   `json:"request_count"`
	FirstIndex   uint64                                   `json:"first_index"`
	LastIndex    uint64                                   `json:"last_index"`

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

type ElConsolidationsPageDataConsolidation struct {
	IsIncluded           bool      `json:"is_included"`
	SlotNumber           uint64    `json:"slot"`
	SlotRoot             []byte    `json:"slot_root"`
	Time                 time.Time `json:"time"`
	Status               uint64    `json:"status"`
	Result               uint8     `json:"result"`
	ResultMessage        string    `json:"result_message"`
	TxStatus             uint64    `json:"tx_status"`
	SourceAddr           []byte    `json:"src_addr"`
	SourceValidatorValid bool      `json:"src_vvalid"`
	SourceValidatorIndex uint64    `json:"src_vindex"`
	SourceValidatorName  string    `json:"src_vname"`
	SourcePublicKey      []byte    `json:"src_pubkey"`
	TargetValidatorValid bool      `json:"tgt_vvalid"`
	TargetValidatorIndex uint64    `json:"tgt_vindex"`
	TargetValidatorName  string    `json:"tgt_vname"`
	TargetPublicKey      []byte    `json:"tgt_pubkey"`
	LinkedTransaction    bool      `json:"linked_tx"`
	TransactionHash      []byte    `json:"tx_hash"`

	TransactionDetails *ElConsolidationsPageDataConsolidationTxDetails `json:"tx_details"`
}

type ElConsolidationsPageDataConsolidationTxDetails struct {
	BlockNumber uint64 `json:"block"`
	BlockHash   string `json:"block_hash"`
	BlockTime   uint64 `json:"block_time"`
	TxOrigin    string `json:"tx_origin"`
	TxTarget    string `json:"tx_target"`
	TxHash      string `json:"tx_hash"`
}
