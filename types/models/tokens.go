package models

// TokensPageData is the data for the detected-tokens list page (/tokens).
// el_tokens is small, so this uses offset pagination with a total count.
type TokensPageData struct {
	Tokens      []*TokensPageDataToken `json:"tokens"`
	SearchQuery string                 `json:"search_query"`

	PageSize   uint64 `json:"page_size"`
	PageIndex  uint64 `json:"page_index"`
	TotalPages uint64 `json:"total_pages"`
	TotalCount uint64 `json:"total_count"`
	FirstItem  uint64 `json:"first_item"`
	LastItem   uint64 `json:"last_item"`

	FirstPageLink string `json:"first_page_link"`
	PrevPageLink  string `json:"prev_page_link"`
	NextPageLink  string `json:"next_page_link"`
	LastPageLink  string `json:"last_page_link"`
}

// TokensPageDataToken is a single row of the tokens list.
type TokensPageDataToken struct {
	Contract      []byte `json:"contract"`
	TokenType     uint8  `json:"token_type"`
	TokenTypeName string `json:"token_type_name"`
	Name          string `json:"name"`
	Symbol        string `json:"symbol"`
	Decimals      uint8  `json:"decimals"`
}
