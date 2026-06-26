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
	MethodID       []byte    `json:"method_id"`
	TxHashRowspan  int       `json:"tx_hash_rowspan"` // >0 render the tx/block/age cells with rowspan; 0 skip
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
// list uses keyset pagination on (tx_uid, tx_idx) (see PagerData).
type TransfersPageData struct {
	Transfers []*TransferRow   `json:"transfers"`
	PageSize  uint64           `json:"page_size"`
	Filter    *TransfersFilter `json:"filter"`
	Pager     *PagerData       `json:"pager"`

	// Columns toggled via the column selector "d" bitmask. Hash, From and To
	// are always shown.
	ColumnMask       uint64 `json:"column_mask"`
	DisplayBlock     bool   `json:"display_block"`
	DisplayAge       bool   `json:"display_age"`
	DisplayMethod    bool   `json:"display_method"`
	DisplayAmount    bool   `json:"display_amount"`
	DisplayToken     bool   `json:"display_token"`
	DisplayStatus    bool   `json:"display_status"`
	DisplayTokenType bool   `json:"display_token_type"`
}

// TransfersFilter holds the current transfer filter values as strings so the
// filter form can repopulate them (token/from/to are contract/address hex).
type TransfersFilter struct {
	MinSlot     string `json:"min_slot"`
	MaxSlot     string `json:"max_slot"`
	Token       string `json:"token"`
	From        string `json:"from"`
	To          string `json:"to"`
	MinAmount   string `json:"min_amount"`
	MaxAmount   string `json:"max_amount"`
	Direction   string `json:"direction"` // "", "out", "in" (address page only)
	TypeERC20   bool   `json:"type_erc20"`
	TypeERC721  bool   `json:"type_erc721"`
	TypeERC1155 bool   `json:"type_erc1155"`
	Active      bool   `json:"active"`
}
