package models

import (
	"time"
)

// BlocksFilteredPageData is a struct to hold info for the filtered blocks page
type BlocksFilteredPageData struct {
	FilterExtraData       string `json:"filter_extra_data"`
	FilterInvertExtraData bool   `json:"filter_invert_extra_data"`
	FilterMinGasUsed      string `json:"filter_min_gas_used"`
	FilterMaxGasUsed      string `json:"filter_max_gas_used"`
	FilterMinGasLimit     string `json:"filter_min_gas_limit"`
	FilterMaxGasLimit     string `json:"filter_max_gas_limit"`
	FilterMinBlockSize    string `json:"filter_min_block_size"`
	FilterMaxBlockSize    string `json:"filter_max_block_size"`
	FilterWithMevBlock    uint8  `json:"filter_mev_block"`
	FilterMinTxCount      string `json:"filter_min_tx"`
	FilterMaxTxCount      string `json:"filter_max_tx"`
	FilterMinBlobCount    string `json:"filter_min_blob"`
	FilterMaxBlobCount    string `json:"filter_max_blob"`
	FilterMinEpoch        string `json:"filter_min_epoch"`
	FilterMaxEpoch        string `json:"filter_max_epoch"`

	DisplayEpoch        bool   `json:"dp_epoch"`
	DisplayBlockNumber  bool   `json:"dp_block_number"`
	DisplaySlot         bool   `json:"dp_slot"`
	DisplayStatus       bool   `json:"dp_status"`
	DisplayTime         bool   `json:"dp_time"`
	DisplayTxCount      bool   `json:"dp_txcount"`
	DisplayBlobCount    bool   `json:"dp_blobcount"`
	DisplayGasUsage     bool   `json:"dp_gasusage"`
	DisplayGasLimit     bool   `json:"dp_gaslimit"`
	DisplayMevBlock     bool   `json:"dp_mevblock"`
	DisplayBlockSize    bool   `json:"dp_blocksize"`
	DisplayElExtraData  bool   `json:"dp_elextra"`
	DisplayFeeRecipient bool   `json:"dp_feerecipient"`
	DisplayColCount     uint64 `json:"display_col_count"`

	Blocks     []*BlocksFilteredPageDataBlock `json:"blocks"`
	BlockCount uint64                         `json:"block_count"`
	FirstBlock uint64                         `json:"first_block"`
	LastBlock  uint64                         `json:"last_block"`

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
}

type BlocksFilteredPageDataBlock struct {
	Epoch               uint64    `json:"epoch"`
	Slot                uint64    `json:"slot"`
	EthBlockNumber      uint64    `json:"eth_block_number"`
	Ts                  time.Time `json:"ts"`
	Status              uint8     `json:"status"`
	EthTransactionCount uint64    `json:"eth_transaction_count"`
	BlobCount           uint64    `json:"blob_count"`
	ElExtraData         []byte    `json:"el_extra_data"`
	GasUsed             uint64    `json:"gas_used"`
	GasLimit            uint64    `json:"gas_limit"`
	BlockSize           uint64    `json:"block_size"`
	BlockRoot           []byte    `json:"block_root"`
	IsMevBlock          bool      `json:"is_mev_block"`
	MevBlockRelays      string    `json:"mev_block_relays"`
	FeeRecipient        []byte    `json:"fee_recipient"`
}
