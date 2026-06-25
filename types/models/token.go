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

	Transfers       []*TransferRow `json:"transfers"`
	PageSize        uint64         `json:"page_size"`
	HasMore         bool           `json:"has_more"`
	NextCursorTxUid uint64         `json:"next_cursor_tx_uid"`
	NextCursorTxIdx uint32         `json:"next_cursor_tx_idx"`
	IsDefaultPage   bool           `json:"is_default_page"`
	FirstPageLink   string         `json:"first_page_link"`
	NextPageLink    string         `json:"next_page_link"`
}
