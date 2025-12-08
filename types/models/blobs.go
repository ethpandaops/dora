package models

import (
	"time"
)

type BlobsPageData struct {
	TotalBlobs             uint64                 `json:"total_blobs"`
	TotalBlobsInBlocks     uint64                 `json:"total_blobs_in_blocks"`
	AvgBlobsPerBlock       float64                `json:"avg_blobs_per_block"`
	BlobsLast1h            uint64                 `json:"blobs_last_1h"`
	BlobsLast24h           uint64                 `json:"blobs_last_24h"`
	BlobsLast7d            uint64                 `json:"blobs_last_7d"`
	BlobsLast30d           uint64                 `json:"blobs_last_30d"`
	BlocksWithBlobsLast1h  uint64                 `json:"blocks_with_blobs_last_1h"`
	BlocksWithBlobsLast24h uint64                 `json:"blocks_with_blobs_last_24h"`
	BlocksWithBlobsLast7d  uint64                 `json:"blocks_with_blobs_last_7d"`
	BlocksWithBlobsLast18d uint64                 `json:"blocks_with_blobs_last_18d"`
	BlobGasLast1h          uint64                 `json:"blob_gas_last_1h"`
	BlobGasLast24h         uint64                 `json:"blob_gas_last_24h"`
	BlobGasLast7d          uint64                 `json:"blob_gas_last_7d"`
	BlobGasLast18d         uint64                 `json:"blob_gas_last_18d"`
	TotalBlobGasUsed       uint64                 `json:"total_blob_gas_used"`
	AvgBlobGasPerBlock     uint64                 `json:"avg_blob_gas_per_block"`
	LatestBlobBlocks       []*LatestBlobBlock     `json:"latest_blob_blocks"`
	StorageCalculator      *StorageCalculatorData `json:"storage_calculator"`
	DataLimitationNote     string                 `json:"data_limitation_note"`
}

type LatestBlobBlock struct {
	Slot         uint64    `json:"slot"`
	BlockNumber  uint64    `json:"block_number"`
	Timestamp    time.Time `json:"timestamp"`
	BlobCount    uint64    `json:"blob_count"`
	Proposer     uint64    `json:"proposer"`
	ProposerName string    `json:"proposer_name"`
	BlockRoot    []byte    `json:"block_root"`
	Finalized    bool      `json:"finalized"`
}

type StorageCalculatorData struct {
	MinEth             uint32  `json:"min_eth"`
	MaxEth             uint32  `json:"max_eth"`
	DefaultEth         uint32  `json:"default_eth"`
	ColumnSize         float64 `json:"column_size"`
	TotalColumns       uint32  `json:"total_columns"`
	MinColumnsNonVal   uint32  `json:"min_columns_non_val"`
	MinColumnsVal      uint32  `json:"min_columns_val"`
	FreeValidatorCount uint32  `json:"free_validator_count"`
}
