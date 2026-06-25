package models

import "time"

// TransferRow is a single token-transfer row, shared by the global transfers
// list and the token detail page.
type TransferRow struct {
	TxHash         []byte    `json:"tx_hash"`
	BlockNumber    uint64    `json:"block_number"`
	BlockRoot      []byte    `json:"block_root"`
	BlockOrphaned  bool      `json:"block_orphaned"`
	BlockTime      time.Time `json:"block_time"`
	MethodName     string    `json:"method_name"`
	FromAddr       []byte    `json:"from_addr"`
	FromIsContract bool      `json:"from_is_contract"`
	ToAddr         []byte    `json:"to_addr"`
	ToIsContract   bool      `json:"to_is_contract"`
	Contract       []byte    `json:"contract"`
	TokenName      string    `json:"token_name"`
	TokenSymbol    string    `json:"token_symbol"`
	TokenType      uint8     `json:"token_type"` // 1=ERC20, 2=ERC721, 3=ERC1155
	TokenIndex     []byte    `json:"token_index"`
	Amount         float64   `json:"amount"`
	AmountRaw      []byte    `json:"amount_raw"`
}

// TransfersPageData is the data for the global token-transfers list page. The
// list uses keyset pagination on (tx_uid, tx_idx).
type TransfersPageData struct {
	Transfers       []*TransferRow `json:"transfers"`
	PageSize        uint64         `json:"page_size"`
	HasMore         bool           `json:"has_more"`
	NextCursorTxUid uint64         `json:"next_cursor_tx_uid"`
	NextCursorTxIdx uint32         `json:"next_cursor_tx_idx"`
	IsDefaultPage   bool           `json:"is_default_page"`
	FirstPageLink   string         `json:"first_page_link"`
	NextPageLink    string         `json:"next_page_link"`
}
