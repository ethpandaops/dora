package models

// PagerData drives the shared pager component (templates/_shared/pager.html).
// It supports both keyset (boundary) pagination — First | < | Page N | > with
// no Last and no jump — and classic offset pagination — First | < | [input] / M
// | > | Last. Fields left at their zero value simply disable the corresponding
// control, so callers opt into Last / the page input as needed.
type PagerData struct {
	PageNum    uint64 `json:"page_num"`
	TotalPages uint64 `json:"total_pages"` // 0 = unknown (keyset); enables "/ M" + input max

	HasPrev bool `json:"has_prev"`
	HasNext bool `json:"has_next"`
	HasLast bool `json:"has_last"`

	FirstLink string `json:"first_link"`
	PrevLink  string `json:"prev_link"`
	NextLink  string `json:"next_link"`
	LastLink  string `json:"last_link"`

	// Page jump input (offset pages). When ShowInput is true the pager renders a
	// number input that GETs JumpAction with JumpParams as hidden fields and the
	// page number under JumpPageField.
	ShowInput     bool       `json:"show_input"`
	JumpAction    string     `json:"jump_action"`
	JumpParams    []UrlParam `json:"jump_params"`
	JumpPageField string     `json:"jump_page_field"`
}
