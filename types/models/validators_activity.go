package models

// ValidatorsActivityPageData is a struct to hold info for the validators activity page
type ValidatorsActivityPageData struct {
	ViewOptionGroupBy uint64 `json:"vopt_groupby"`

	Groups         []*ValidatorsActiviyPageDataGroup `json:"validators"`
	GroupCount     uint64                            `json:"group_count"`
	FirstValidator uint64                            `json:"first_validx"`
	LastValidator  uint64                            `json:"last_validx"`
	Sorting        string                            `json:"sorting"`

	FirstGroup uint64 `json:"first_group"`
	LastGroup  uint64 `json:"last_group"`

	IsDefaultPage    bool   `json:"default_page"`
	IsDefaultSorting bool   `json:"default_sorting"`
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
	ViewPageLink  string `json:"view_page_link"`
}

type ValidatorsActiviyPageDataGroup struct {
	Group      string `json:"group"`
	GroupLower string `json:"group_lower"`
	Validators uint64 `json:"validators"`
	Activated  uint64 `json:"activated"`
	Online     uint64 `json:"online"`
	Offline    uint64 `json:"offline"`
	Exited     uint64 `json:"exited"`
	Slashed    uint64 `json:"slashed"`
}
