package models

import (
	"time"
)

// ElWithdrawalsPageData is a struct to hold info for the el_withdrawals page
type ElWithdrawalsPageData struct {
	FilterMinSlot       uint64 `json:"filter_mins"`
	FilterMaxSlot       uint64 `json:"filter_maxs"`
	FilterAddress       string `json:"filter_address"`
	FilterMinIndex      uint64 `json:"filter_mini"`
	FilterMaxIndex      uint64 `json:"filter_maxi"`
	FilterValidatorName string `json:"filter_vname"`
	FilterWithOrphaned  uint8  `json:"filter_orphaned"`
	FilterWithType      uint8  `json:"filter_type"`
	FilterPublicKey     string `json:"filter_pubkey"`

	ElRequests   []*ElWithdrawalsPageDataWithdrawal `json:"withdrawals"`
	RequestCount uint64                             `json:"request_count"`
	FirstIndex   uint64                             `json:"first_index"`
	LastIndex    uint64                             `json:"last_index"`

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

type ElWithdrawalsPageDataWithdrawal struct {
	SlotNumber        uint64    `json:"slot"`
	SlotRoot          []byte    `json:"slot_root"`
	Time              time.Time `json:"time"`
	Orphaned          bool      `json:"orphaned"`
	SourceAddr        []byte    `json:"source_addr"`
	Amount            uint64    `json:"amount"`
	ValidatorValid    bool      `json:"vvalid"`
	ValidatorIndex    uint64    `json:"vindex"`
	ValidatorName     string    `json:"vname"`
	PublicKey         []byte    `json:"pubkey"`
	LinkedTransaction bool      `json:"linked_tx"`
	TransactionHash   []byte    `json:"tx_hash"`

	TransactionDetails *ElWithdrawalsPageDataWithdrawalTxDetails `json:"tx_details"`
}

type ElWithdrawalsPageDataWithdrawalTxDetails struct {
	BlockNumber uint64 `json:"block"`
	BlockHash   string `json:"block_hash"`
	BlockTime   uint64 `json:"block_time"`
	TxOrigin    string `json:"tx_origin"`
	TxTarget    string `json:"tx_target"`
	TxHash      string `json:"tx_hash"`
}
