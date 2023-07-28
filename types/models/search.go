package models

// SearchBlockResult is a struct to hold the search block result with a given graffiti
type SearchBlockResult struct {
	Slot     uint64 `json:"slot,omitempty"`
	Root     []byte `json:"root,omitempty"`
	Orphaned bool   `json:"orphaned,omitempty"`
}

// SearchGraffitiResult is a struct to hold the search block result with a given graffiti
type SearchGraffitiResult struct {
	Graffiti string `json:"graffiti,omitempty"`
}

// SearchAheadEpochsResult is a struct to hold the search ahead epochs results
type SearchAheadEpochsResult []struct {
	Epoch string `json:"epoch,omitempty"`
}

// SearchAheadSlotsResult is a struct to hold the search ahead slots results
type SearchAheadSlotsResult []struct {
	Slot     string `json:"slot,omitempty"`
	Root     string `json:"root,omitempty"`
	Orphaned bool   `json:"orphaned,omitempty"`
}

// SearchAheadGraffitiResult is a struct to hold the search ahead blocks results with a given graffiti
type SearchAheadGraffitiResult []struct {
	Graffiti string `json:"graffiti,omitempty"`
	Count    string `json:"count,omitempty"`
}
