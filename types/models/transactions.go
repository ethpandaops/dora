package models

import "time"

// TransactionsPageData is the data for the global execution transactions list
// page. The list uses keyset (boundary) pagination on tx_uid (see PagerData).
type TransactionsPageData struct {
	Transactions []*TransactionsPageDataTransaction `json:"transactions"`
	PageSize     uint64                             `json:"page_size"`
	Filter       *TransactionsFilter                `json:"filter"`
	Pager        *PagerData                         `json:"pager"`

	// Columns toggled via the column selector "d" bitmask. Hash, From and To
	// are always shown; everything below is selectable.
	ColumnMask     uint64 `json:"column_mask"`
	DisplayBlock   bool   `json:"display_block"`
	DisplayAge     bool   `json:"display_age"`
	DisplayMethod  bool   `json:"display_method"`
	DisplayValue   bool   `json:"display_value"`
	DisplayFee     bool   `json:"display_fee"`
	DisplayGasUsed bool   `json:"display_gas_used"`
	DisplayType    bool   `json:"display_type"`
	DisplayNonce   bool   `json:"display_nonce"`
}

// TransactionsFilter holds the current filter values as strings so the filter
// form can repopulate them. Empty fields render as empty inputs.
type TransactionsFilter struct {
	MinSlot   string `json:"min_slot"`
	MaxSlot   string `json:"max_slot"`
	From      string `json:"from"`
	To        string `json:"to"`
	MinAmount string `json:"min_amount"`
	MaxAmount string `json:"max_amount"`
	MinGas    string `json:"min_gas"`
	MaxGas    string `json:"max_gas"`
	MinTip    string `json:"min_tip"`
	MaxTip    string `json:"max_tip"`
	Reverted  string `json:"reverted"` // "", "0" (success), "1" (reverted)
	Type0     bool   `json:"type0"`
	Type1     bool   `json:"type1"`
	Type2     bool   `json:"type2"`
	Type3     bool   `json:"type3"`
	Type4     bool   `json:"type4"`
	Active    bool   `json:"active"` // any filter set (controls panel open state)
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
	GasUsed        uint64    `json:"gas_used"`
	TxType         uint8     `json:"tx_type"`
	Reverted       bool      `json:"reverted"`
	MethodID       []byte    `json:"method_id"`
	MethodName     string    `json:"method_name"`
}
