package models

type BlockAccessListEntry struct {
	Address      []byte   `json:"address"`
	StorageSlots [][]byte `json:"storage_slots"`
}
