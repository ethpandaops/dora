package models

// ClientsPageData is a struct to hold info for the clients page
type ClientsPageData struct {
	Clients     []*ClientsPageDataClient `json:"clients"`
	ClientCount uint64                   `json:"client_count"`
}

type ClientsPageDataClient struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	HeadSlot uint64 `json:"head_slot"`
	HeadRoot []byte `json:"head_root"`
	Status   string `json:"status"`
}
