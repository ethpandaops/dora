package models

import "time"

// ForksPageData is a struct to hold info for the forks page
type ForksPageData struct {
	Forks     []*ForksPageDataFork `json:"forks"`
	ForkCount uint64               `json:"fork_count"`
}

type ForksPageDataFork struct {
	HeadSlot    uint64                 `json:"head_slot"`
	HeadRoot    []byte                 `json:"head_root"`
	Clients     []*ForksPageDataClient `json:"clients"`
	ClientCount uint64                 `json:"client_count"`
}

type ForksPageDataClient struct {
	Index       int       `json:"index"`
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Status      string    `json:"status"`
	HeadSlot    uint64    `json:"head_slot"`
	Distance    uint64    `json:"distance"`
	LastRefresh time.Time `json:"refresh"`
	LastError   string    `json:"error"`
}
