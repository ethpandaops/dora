package models

// ValidatorsOfflinePageData is a struct to hold info for the validators offline page
type ValidatorsOfflinePageData struct {
	ViewOptionGroupBy uint64 `json:"vopt_groupby"`
	GroupKey          string `json:"group_key"`
	GroupName         string `json:"group_name"`

	Validators       []*ValidatorsPageDataValidator `json:"validators"`
	ValidatorCount   uint64                         `json:"validator_count"`
	FirstValidator   uint64                         `json:"first_validx"`
	LastValidator    uint64                         `json:"last_validx"`
	Sorting          string                         `json:"sorting"`
	IsDefaultSorting bool                           `json:"default_sorting"`
	IsDefaultPage    bool                           `json:"default_page"`
	TotalPages       uint64                         `json:"total_pages"`
	PageSize         uint64                         `json:"page_size"`
	CurrentPageIndex uint64                         `json:"page_index"`
	PrevPageIndex    uint64                         `json:"prev_page_index"`
	NextPageIndex    uint64                         `json:"next_page_index"`
	LastPageIndex    uint64                         `json:"last_page_index"`

	FirstPageLink string `json:"first_page_link"`
	PrevPageLink  string `json:"prev_page_link"`
	NextPageLink  string `json:"next_page_link"`
	LastPageLink  string `json:"last_page_link"`
	ViewPageLink  string `json:"view_page_link"`
}
