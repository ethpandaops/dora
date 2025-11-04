package dbtypes

// ElTokenContract represents a token contract (ERC20, ERC721, ERC1155)
type ElTokenContract struct {
	Address    []byte `db:"address"`
	TokenType  string `db:"token_type"` // "ERC20", "ERC721", "ERC1155"

	Name        *string `db:"name"`
	Symbol      *string `db:"symbol"`
	Decimals    *uint8  `db:"decimals"`
	TotalSupply []byte  `db:"total_supply"`

	DiscoveredBlock     uint64 `db:"discovered_block"`
	DiscoveredTimestamp uint64 `db:"discovered_timestamp"`

	HolderCount      uint64 `db:"holder_count"`
	TransferCount    uint64 `db:"transfer_count"`
	LastUpdatedBlock uint64 `db:"last_updated_block"`
}

// ElTokenBalance represents a token balance for an address
type ElTokenBalance struct {
	Address          []byte `db:"address"`
	TokenAddress     []byte `db:"token_address"`
	Balance          []byte `db:"balance"`
	UpdatedBlock     uint64 `db:"updated_block"`
	UpdatedTimestamp uint64 `db:"updated_timestamp"`
}

// ElTokenTransfer represents a token transfer event
type ElTokenTransfer struct {
	Id              uint64 `db:"id"`
	TransactionHash []byte `db:"transaction_hash"`
	BlockNumber     uint64 `db:"block_number"`
	BlockTimestamp  uint64 `db:"block_timestamp"`
	LogIndex        uint   `db:"log_index"`

	TokenAddress []byte `db:"token_address"`
	TokenType    string `db:"token_type"`

	FromAddress []byte  `db:"from_address"`
	ToAddress   []byte  `db:"to_address"`
	Value       []byte  `db:"value"`    // For ERC20
	TokenId     []byte  `db:"token_id"` // For ERC721/ERC1155
}

// ElTokenFilter represents filter options for token queries
type ElTokenFilter struct {
	TokenType     string
	MinHolders    *uint64
	MinTransfers  *uint64
	SearchSymbol  string
	SearchName    string
}

// ElTokenTransferFilter represents filter options for token transfer queries
type ElTokenTransferFilter struct {
	TokenAddress []byte
	FromAddress  []byte
	ToAddress    []byte
	MinTimestamp *uint64
	MaxTimestamp *uint64
}

// ElIndexerState tracks the indexer progress per fork
type ElIndexerState struct {
	ForkId               uint64 `db:"fork_id"`
	LastIndexedBlock     uint64 `db:"last_indexed_block"`
	LastIndexedTimestamp uint64 `db:"last_indexed_timestamp"`
}
