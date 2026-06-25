package models

import "time"

// TransactionsPageData is the data for the global execution transactions list
// page. The list uses keyset (boundary) pagination on tx_uid: NextCursor holds
// the tx_uid of the last row, and "older" pages are fetched with ?before=.
type TransactionsPageData struct {
	Transactions  []*TransactionsPageDataTransaction `json:"transactions"`
	PageSize      uint64                             `json:"page_size"`
	HasMore       bool                               `json:"has_more"`
	NextCursor    uint64                             `json:"next_cursor"`
	IsDefaultPage bool                               `json:"is_default_page"`

	// Filters (reflected back into pagination links).
	FilterReverted string `json:"filter_reverted"` // "", "1" (reverted), "0" (success)

	FirstPageLink string `json:"first_page_link"`
	NextPageLink  string `json:"next_page_link"`
}

// TransactionsPageDataTransaction is a single row of the transactions list.
type TransactionsPageDataTransaction struct {
	TxHash         []byte    `json:"tx_hash"`
	BlockNumber    uint64    `json:"block_number"`
	BlockRoot      []byte    `json:"block_root"`
	BlockOrphaned  bool      `json:"block_orphaned"`
	BlockTime      time.Time `json:"block_time"`
	FromAddr       []byte    `json:"from_addr"`
	FromIsContract bool      `json:"from_is_contract"`
	ToAddr         []byte    `json:"to_addr"`
	ToIsContract   bool      `json:"to_is_contract"`
	HasTo          bool      `json:"has_to"`
	IsCreate       bool      `json:"is_create"`
	Nonce          uint64    `json:"nonce"`
	Amount         float64   `json:"amount"`
	TxFee          float64   `json:"tx_fee"`
	Reverted       bool      `json:"reverted"`
	MethodID       []byte    `json:"method_id"`
	MethodName     string    `json:"method_name"`
}
