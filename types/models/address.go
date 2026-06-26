package models

import (
	"time"
)

// AddressPageData is a struct to hold info for the address page
type AddressPageData struct {
	Address            []byte `json:"address"`
	AccountID          uint64 `json:"account_id"`
	IsContract         bool   `json:"is_contract"`
	IsToken            bool   `json:"is_token"`
	TokenName          string `json:"token_name"`
	TokenSymbol        string `json:"token_symbol"`
	TokenType          uint8  `json:"token_type"`
	FirstFunded        uint64 `json:"first_funded"`
	FundedBy           []byte `json:"funded_by"`
	FundedByID         uint64 `json:"funded_by_id"`
	FundedByIsContract bool   `json:"funded_by_is_contract"`
	LastNonce          uint64 `json:"last_nonce"`

	// ETH Balance (from el_balances with token_id = 0)
	EthBalance    float64 `json:"eth_balance"`
	EthBalanceRaw []byte  `json:"eth_balance_raw"`

	// Token balances for sidebar
	TokenBalances     []*AddressPageDataTokenBalance `json:"token_balances"`
	TokenBalanceCount uint64                         `json:"token_balance_count"`

	// Tab view
	TabView string `json:"tab_view"`

	// Tab visibility
	HasWithdrawals bool `json:"has_withdrawals"`
	HasBlockFees   bool `json:"has_block_fees"`

	// Contract tab (loaded on demand for contract addresses)
	ContractRpcUnavailable bool   `json:"contract_rpc_unavailable"` // EL RPC not reachable
	ContractBytecode       []byte `json:"contract_bytecode"`        // deployed (runtime) code

	// Transactions tab
	Transactions     []*AddressPageDataTransaction `json:"transactions"`
	TransactionCount uint64                        `json:"transaction_count"`
	TxCountCapped    bool                          `json:"tx_count_capped"`
	TxPageIndex      uint64                        `json:"tx_page_index"`
	TxPageSize       uint64                        `json:"tx_page_size"`
	TxTotalPages     uint64                        `json:"tx_total_pages"`
	TxFirstItem      uint64                        `json:"tx_first_item"`
	TxLastItem       uint64                        `json:"tx_last_item"`

	// ERC20 Token Transfers tab
	ERC20Transfers     []*AddressPageDataTokenTransfer `json:"erc20_transfers"`
	ERC20TransferCount uint64                          `json:"erc20_transfer_count"`
	ERC20CountCapped   bool                            `json:"erc20_count_capped"`
	ERC20PageIndex     uint64                          `json:"erc20_page_index"`
	ERC20PageSize      uint64                          `json:"erc20_page_size"`
	ERC20TotalPages    uint64                          `json:"erc20_total_pages"`
	ERC20FirstItem     uint64                          `json:"erc20_first_item"`
	ERC20LastItem      uint64                          `json:"erc20_last_item"`

	// NFT Transfers tab (ERC721/ERC1155)
	NFTTransfers     []*AddressPageDataTokenTransfer `json:"nft_transfers"`
	NFTTransferCount uint64                          `json:"nft_transfer_count"`
	NFTCountCapped   bool                            `json:"nft_count_capped"`
	NFTPageIndex     uint64                          `json:"nft_page_index"`
	NFTPageSize      uint64                          `json:"nft_page_size"`
	NFTTotalPages    uint64                          `json:"nft_total_pages"`
	NFTFirstItem     uint64                          `json:"nft_first_item"`
	NFTLastItem      uint64                          `json:"nft_last_item"`

	// Internal Transactions tab
	HasInternalTxs       bool                                  `json:"has_internal_txs"`
	InternalTxs          []*AddressPageDataInternalTransaction `json:"internal_txs"`
	InternalTxCount      uint64                                `json:"internal_tx_count"`
	InternalTxPageIndex  uint64                                `json:"internal_tx_page_index"`
	InternalTxPageSize   uint64                                `json:"internal_tx_page_size"`
	InternalTxTotalPages uint64                                `json:"internal_tx_total_pages"`
	InternalTxFirstItem  uint64                                `json:"internal_tx_first_item"`
	InternalTxLastItem   uint64                                `json:"internal_tx_last_item"`

	// Beacon Withdrawals tab
	Withdrawals     []*AddressPageDataWithdrawal `json:"withdrawals"`
	WithdrawalCount uint64                       `json:"withdrawal_count"`
	WdPageIndex     uint64                       `json:"wd_page_index"`
	WdPageSize      uint64                       `json:"wd_page_size"`
	WdTotalPages    uint64                       `json:"wd_total_pages"`
	WdFirstItem     uint64                       `json:"wd_first_item"`
	WdLastItem      uint64                       `json:"wd_last_item"`

	// Block Fees tab
	BlockFees     []*AddressPageDataBlockFee `json:"block_fees"`
	BlockFeeCount uint64                     `json:"block_fee_count"`
	BfPageIndex   uint64                     `json:"bf_page_index"`
	BfPageSize    uint64                     `json:"bf_page_size"`
	BfTotalPages  uint64                     `json:"bf_total_pages"`
	BfFirstItem   uint64                     `json:"bf_first_item"`
	BfLastItem    uint64                     `json:"bf_last_item"`
}

// AddressPageDataTokenBalance represents a token balance in the sidebar
type AddressPageDataTokenBalance struct {
	TokenID    uint64  `json:"token_id"`
	Contract   []byte  `json:"contract"`
	Name       string  `json:"name"`
	Symbol     string  `json:"symbol"`
	Decimals   uint8   `json:"decimals"`
	Balance    float64 `json:"balance"`
	BalanceRaw []byte  `json:"balance_raw"`
}

// AddressPageDataTransaction represents a transaction in the transactions tab
type AddressPageDataTransaction struct {
	TxHash         []byte    `json:"tx_hash"`
	BlockNumber    uint64    `json:"block_number"`
	BlockUid       uint64    `json:"block_uid"`
	BlockRoot      []byte    `json:"block_root"`     // For linking to /slot/{root}
	BlockOrphaned  bool      `json:"block_orphaned"` // True if block is orphaned
	BlockTime      time.Time `json:"block_time"`
	FromAddr       []byte    `json:"from_addr"`
	FromID         uint64    `json:"from_id"`
	FromIsContract bool      `json:"from_is_contract"`
	ToAddr         []byte    `json:"to_addr"`
	ToID           uint64    `json:"to_id"`
	ToIsContract   bool      `json:"to_is_contract"`
	HasTo          bool      `json:"has_to"` // false for contract creation
	IsOutgoing     bool      `json:"is_outgoing"`
	Nonce          uint64    `json:"nonce"`
	Amount         float64   `json:"amount"`
	AmountRaw      []byte    `json:"amount_raw"`
	TxFee          float64   `json:"tx_fee"` // Transaction fee in ETH
	Reverted       bool      `json:"reverted"`
	MethodID       []byte    `json:"method_id"`
	MethodName     string    `json:"method_name"`
}

// AddressPageDataTokenTransfer represents a token transfer
type AddressPageDataTokenTransfer struct {
	TxHash         []byte    `json:"tx_hash"`
	TxHashRowspan  int       `json:"tx_hash_rowspan"` // >0 means render with rowspan, 0 means skip cell
	BlockNumber    uint64    `json:"block_number"`
	BlockUid       uint64    `json:"block_uid"`
	BlockRoot      []byte    `json:"block_root"`     // For linking to /slot/{root}
	BlockOrphaned  bool      `json:"block_orphaned"` // True if block is orphaned
	BlockTime      time.Time `json:"block_time"`
	FromAddr       []byte    `json:"from_addr"`
	FromID         uint64    `json:"from_id"`
	FromIsContract bool      `json:"from_is_contract"`
	ToAddr         []byte    `json:"to_addr"`
	ToID           uint64    `json:"to_id"`
	ToIsContract   bool      `json:"to_is_contract"`
	IsOutgoing     bool      `json:"is_outgoing"`
	TokenID        uint64    `json:"token_id"`
	Contract       []byte    `json:"contract"`
	TokenName      string    `json:"token_name"`
	TokenSymbol    string    `json:"token_symbol"`
	Decimals       uint8     `json:"decimals"`
	TokenType      uint8     `json:"token_type"`  // 1=ERC20, 2=ERC721, 3=ERC1155
	TokenIndex     []byte    `json:"token_index"` // NFT token ID
	Amount         float64   `json:"amount"`      // For ERC20
	AmountRaw      []byte    `json:"amount_raw"`  // Raw amount
	MethodName     string    `json:"method_name"` // Function name if known
}

// AddressPageDataInternalTransaction is a per-transaction aggregate of
// internal calls touching the address. One row per tx (not per call).
type AddressPageDataInternalTransaction struct {
	TxHash        []byte                                       `json:"tx_hash" ssz-size:"32"`
	BlockNumber   uint64                                       `json:"block_number"`
	BlockUid      uint64                                       `json:"block_uid"`
	BlockRoot     []byte                                       `json:"block_root" ssz-size:"32"`
	BlockOrphaned bool                                         `json:"block_orphaned"`
	BlockTime     time.Time                                    `json:"block_time"`
	InCount       uint16                                       `json:"in_count"`   // calls where address was callee
	OutCount      uint16                                       `json:"out_count"`  // calls where address was caller
	CallTypes     []AddressPageDataInternalTransactionCallType `json:"call_types"` // pre-expanded incoming call types
	ValueIn       float64                                      `json:"value_in"`
	ValueOut      float64                                      `json:"value_out"`
	GasUsed       uint64                                       `json:"gas_used"`
}

// AddressPageDataInternalTransactionCallType is a single bit of CallTypeMask
// expanded for template rendering.
type AddressPageDataInternalTransactionCallType struct {
	Type uint8  `json:"type"` // 0=CALL, 1=STATICCALL, 2=DELEGATECALL, 3=CREATE, 4=CREATE2, 5=SELFDESTRUCT
	Name string `json:"name"`
}

// AddressPageDataWithdrawal represents a beacon withdrawal on the address page.
type AddressPageDataWithdrawal struct {
	BlockUid       uint64    `json:"block_uid"`
	BlockNumber    uint64    `json:"block_number"`
	BlockRoot      []byte    `json:"block_root" ssz-size:"32"`
	BlockOrphaned  bool      `json:"block_orphaned"`
	BlockTime      time.Time `json:"block_time"`
	Type           uint8     `json:"type"`   // 1=full, 2=sweep, 3=requested
	Amount         uint64    `json:"amount"` // Gwei
	ValidatorIndex uint64    `json:"validator_index"`
	ValidatorName  string    `json:"validator_name"`
	IsBuilder      bool      `json:"is_builder"`
}

// AddressPageDataBlockFee represents a block fee reward on the address page.
type AddressPageDataBlockFee struct {
	BlockUid      uint64    `json:"block_uid"`
	BlockNumber   uint64    `json:"block_number"`
	BlockRoot     []byte    `json:"block_root" ssz-size:"32"`
	BlockOrphaned bool      `json:"block_orphaned"`
	BlockTime     time.Time `json:"block_time"`
	Amount        float64   `json:"amount"`
	AmountRaw     []byte    `json:"amount_raw" ssz-size:"32"`
}
