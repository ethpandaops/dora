package models

import "github.com/attestantio/go-eth2-client/spec/phase0"

// SearchBlockResult is a struct to hold the search block result with a given graffiti
type SearchBlockResult struct {
	Slot     uint64      `json:"slot,omitempty"`
	Root     phase0.Root `json:"root,omitempty"`
	Orphaned bool        `json:"orphaned,omitempty"`
}

// SearchGraffitiResult is a struct to hold the search block result with a given graffiti
type SearchGraffitiResult struct {
	Graffiti string `json:"graffiti,omitempty"`
}

// SearchAheadEpochsResult is a struct to hold the search ahead epochs results
type SearchAheadEpochsResult struct {
	Epoch string `json:"epoch,omitempty"`
}

// SearchAheadSlotsResult is a struct to hold the search ahead slots results
type SearchAheadSlotsResult struct {
	Slot     string      `json:"slot,omitempty"`
	Root     phase0.Root `json:"root,omitempty"`
	Orphaned bool        `json:"orphaned,omitempty"`
}

// SearchAheadExecBlocksResult is a struct to hold the search ahead execution blocks results
type SearchAheadExecBlocksResult struct {
	Slot       string        `json:"slot,omitempty"`
	Root       phase0.Root   `json:"root,omitempty"`
	ExecHash   phase0.Hash32 `json:"exec_hash,omitempty"`
	ExecNumber uint64        `json:"exec_number,omitempty"`
	Orphaned   bool          `json:"orphaned,omitempty"`
}

// SearchAheadGraffitiResult is a struct to hold the search ahead blocks results with a given graffiti
type SearchAheadGraffitiResult struct {
	Graffiti string `json:"graffiti,omitempty"`
	Count    string `json:"count,omitempty"`
}

// SearchAheadValidatorNameResult is a struct to hold the search ahead blocks results with a given graffiti
type SearchAheadValidatorNameResult struct {
	Name  string `json:"name,omitempty"`
	Count string `json:"count,omitempty"`
}

// SearchAheadValidatorResult is a struct to hold the search ahead validator results
type SearchAheadValidatorResult struct {
	Index  string `json:"index,omitempty"`
	Pubkey string `json:"pubkey,omitempty"`
	Name   string `json:"name,omitempty"`
}

// SearchAheadAddressResult is a struct to hold the search ahead address results
type SearchAheadAddressResult struct {
	Address    string `json:"address,omitempty"`
	IsContract bool   `json:"is_contract,omitempty"`
	HasData    bool   `json:"has_data,omitempty"`
}

// SearchAheadTransactionResult is a struct to hold the search ahead transaction results
type SearchAheadTransactionResult struct {
	TxHash      string `json:"tx_hash,omitempty"`
	BlockNumber uint64 `json:"block_number,omitempty"`
	Reverted    bool   `json:"reverted,omitempty"`
}
