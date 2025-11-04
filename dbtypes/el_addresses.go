package dbtypes

// ElAddress represents an Ethereum address with metadata and stats
type ElAddress struct {
	Address []byte `db:"address"`

	// Contract info
	IsContract         bool   `db:"is_contract"`
	ContractCreator    []byte `db:"contract_creator"`
	CreationTxHash     []byte `db:"creation_tx_hash"`
	CreationBlockNumber *uint64 `db:"creation_block_number"`
	CreationTimestamp  *uint64 `db:"creation_timestamp"`

	// Transaction stats
	FirstSeenBlock     uint64 `db:"first_seen_block"`
	FirstSeenTimestamp uint64 `db:"first_seen_timestamp"`
	LastSeenBlock      uint64 `db:"last_seen_block"`
	LastSeenTimestamp  uint64 `db:"last_seen_timestamp"`
	TxCount            uint64 `db:"tx_count"`
	InTxCount          uint64 `db:"in_tx_count"`
	OutTxCount         uint64 `db:"out_tx_count"`

	// Balance
	Balance            []byte `db:"balance"` // Wei as bytes
	BalanceUpdatedBlock uint64 `db:"balance_updated_block"`

	// Naming
	EnsName      *string `db:"ens_name"`
	EnsUpdatedAt *uint64 `db:"ens_updated_at"`
	CustomName   *string `db:"custom_name"`

	// Contract metadata
	ContractBytecodeHash []byte `db:"contract_bytecode_hash"`
	ContractVerified     bool   `db:"contract_verified"`
}

// ElAddressFilter represents filter options for address queries
type ElAddressFilter struct {
	IsContract   *bool
	MinBalance   []byte
	MinTxCount   *uint64
	WithENSName  bool
	SearchName   string
}
