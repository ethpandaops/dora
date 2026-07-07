package models

// TokenPageData is the data for the EIP-3091 token detail page
// (/token/<contract>). It shows token metadata and a keyset-paginated list of
// transfers for the token.
type TokenPageData struct {
	Contract      []byte `json:"contract"`
	TokenType     uint8  `json:"token_type"`
	TokenTypeName string `json:"token_type_name"`
	Name          string `json:"name"`
	Symbol        string `json:"symbol"`
	Decimals      uint8  `json:"decimals"`

	Transfers []*TransferRow   `json:"transfers"`
	PageSize  uint64           `json:"page_size"`
	Filter    *TransfersFilter `json:"filter"`
	Pager     *PagerData       `json:"pager"`

	// Columns toggled via the "d" bitmask (Token/TokenType are fixed here so are
	// always omitted). Hash, From and To are always shown.
	ColumnMask    uint64 `json:"column_mask"`
	DisplayBlock  bool   `json:"display_block"`
	DisplayAge    bool   `json:"display_age"`
	DisplayMethod bool   `json:"display_method"`
	DisplayAmount bool   `json:"display_amount"`
	DisplayStatus bool   `json:"display_status"`

	EnsNameData
}
