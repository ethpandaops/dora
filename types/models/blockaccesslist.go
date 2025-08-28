package models

import "fmt"

// BlockAccessList represents the complete block-level access list as defined in EIP-7928
type BlockAccessList struct {
	Hash            []byte            `json:"hash"`
	AccountChanges  []*AccountChanges `json:"account_changes"`
	TotalSlots      int               `json:"total_slots"`
	TotalAccounts   int               `json:"total_accounts"`
	TotalStorageOps int               `json:"total_storage_ops"`
}

// AccountChanges represents all changes for a single account
type AccountChanges struct {
	Address        []byte           `json:"address"`
	StorageChanges []*SlotChanges   `json:"storage_changes,omitempty"`
	StorageReads   [][]byte         `json:"storage_reads,omitempty"`
	BalanceChanges []*BalanceChange `json:"balance_changes,omitempty"`
	NonceChanges   []*NonceChange   `json:"nonce_changes,omitempty"`
	CodeChanges    []*CodeChange    `json:"code_changes,omitempty"`
}

// SlotChanges represents all changes to a single storage slot
type SlotChanges struct {
	Slot    []byte           `json:"slot"`
	Changes []*StorageChange `json:"changes"`
}

// StorageChange represents a single storage change at a block access index
type StorageChange struct {
	BlockAccessIndex uint16 `json:"block_access_index"`
	NewValue         []byte `json:"new_value"`
	DisplayIndex     string `json:"display_index"`
}

// BalanceChange represents a balance change at a block access index
type BalanceChange struct {
	BlockAccessIndex uint16 `json:"block_access_index"`
	PostBalance      uint64 `json:"post_balance"`
	DisplayIndex     string `json:"display_index"`
}

// NonceChange represents a nonce change at a block access index
type NonceChange struct {
	BlockAccessIndex uint16 `json:"block_access_index"`
	NewNonce         uint64 `json:"new_nonce"`
	DisplayIndex     string `json:"display_index"`
}

// CodeChange represents a code change at a block access index
type CodeChange struct {
	BlockAccessIndex uint16 `json:"block_access_index"`
	NewCode          []byte `json:"new_code"`
	DisplayIndex     string `json:"display_index"`
}

// GetDisplayIndex returns a human-readable string for the block access index
func GetDisplayIndex(index uint16, txCount int) string {
	if index == 0 {
		return "pre-execution"
	}
	if int(index) <= txCount {
		return formatTxIndex(int(index))
	}
	return "post-execution"
}

func formatTxIndex(index int) string {
	return fmt.Sprintf("tx-%d", index)
}
