package models

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
