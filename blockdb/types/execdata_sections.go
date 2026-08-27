package types

import (
	"github.com/holiman/uint256"
)

// Call type constants matching the callTracer output.
const (
	CallTypeCall         = 0
	CallTypeStaticCall   = 1
	CallTypeDelegateCall = 2
	CallTypeCreate       = 3
	CallTypeCreate2      = 4
	CallTypeSelfDestruct = 5
	CallTypeFrame        = 6
)

// Call status constants for binary encoding.
const (
	CallStatusSuccess  = 0
	CallStatusReverted = 1
	CallStatusError    = 2
)

// EventData holds the data for a single event log to be encoded into the
// events section of the execution data object.
type EventData struct {
	EventIndex uint32
	Source     [20]byte
	Topics     [][]byte `ssz-size:"?,32" ssz-max:"5"`
	Data       []byte   `ssz-max:"10485760"`
}

// EventDataList is a list of EventData.
type EventDataList []EventData

// FlatCallFrame is a single call frame in a flattened depth-first call trace.
type FlatCallFrame struct {
	Depth   uint16
	Type    uint8 // CallType* constants
	From    [20]byte
	To      [20]byte
	Value   uint256.Int // nil or zero means no value
	Gas     uint64
	GasUsed uint64
	Status  uint8  // CallStatus* constants
	Input   []byte `ssz-max:"10485760"`
	Output  []byte `ssz-max:"10485760"`
	Error   string `ssz-max:"10485760"`
}

// TracePayloadLimit is the number of payload bytes kept per call frame for the
// Input and Output fields. A payload longer than the limit is captured as its
// first TracePayloadLimit+1 bytes, so a stored length above the limit is itself
// the marker that the value was truncated - no separate flag is needed on disk.
//
// Contracts can turn a single transaction's gas into hundreds of megabytes of
// tracer output by looping over calls that pass large memory buffers around
// (the identity precompile being the cheapest vehicle), so payloads are pruned
// while the tracer response is being read rather than after it is decoded.
const TracePayloadLimit = 16384

// TrimPrunedPayload splits a stored call frame payload into the bytes that are
// safe to display and whether the value was truncated when it was captured.
func TrimPrunedPayload(data []byte) (visible []byte, pruned bool) {
	if len(data) > TracePayloadLimit {
		return data[:TracePayloadLimit], true
	}

	return data, false
}

// State change section version.
const (
	StateChangesVersion1 = 1
)

// State change flags per account (bitmask).
const (
	StateChangeFlagBalanceChanged = 0x01
	StateChangeFlagNonceChanged   = 0x02
	StateChangeFlagCodeChanged    = 0x04
	StateChangeFlagStorageChanged = 0x08
	StateChangeFlagAccountCreated = 0x10 // exists only in post
	StateChangeFlagAccountKilled  = 0x20 // exists only in pre
)

// StateChangeSlot is a single changed storage slot.
type StateChangeSlot struct {
	Slot      [32]byte
	PreValue  [32]byte
	PostValue [32]byte
}

// StateChangeAccount is the normalized per-account state diff representation
// used by EncodeStateChangesSection.
type StateChangeAccount struct {
	Address [20]byte
	Flags   uint8

	// Balance
	PreBalance  uint256.Int
	PostBalance uint256.Int

	// Nonce
	PreNonce  uint64
	PostNonce uint64

	// Code
	PreCode  []byte `ssz-max:"10485760"`
	PostCode []byte `ssz-max:"10485760"`

	// Storage
	Slots []StateChangeSlot `ssz-max:"10485760"`
}

// Block receipt metadata version. Bump when adding new block-wide fields.
const (
	BlockReceiptMetaVersion1 = 1
)

// BlockReceiptMeta holds block-wide receipt metadata needed to reconstruct
// full receipts. Stored as a versioned section so new fields can be added
// without changing the DXTX format version.
type BlockReceiptMeta struct {
	Version      uint16 // Schema version for forward compatibility
	BlobGasPrice uint64 // Block-wide blob gas price in wei (EIP-4844), 0 if not applicable
}

// Receipt metadata version. Bump when adding new fields.
const (
	ReceiptMetaVersion1 = 1

	// ReceiptMetaVersion2 marks a section that carries frame content after the fixed
	// metadata: the payer and per-frame results of an EIP-8141 frame transaction, which
	// a receipt reports and no other section holds.
	ReceiptMetaVersion2 = 2
)

// FrameReceiptEntry is the result a receipt reports for one frame.
type FrameReceiptEntry struct {
	// Status is EIP-8141's per-frame status: 0 failed, 1 success, 2 skipped. A skipped
	// frame is neither - an earlier frame in its atomic batch failed and it never ran.
	Status uint8

	// EIP-8037 accounts for gas in two dimensions, and the receipt reports both per
	// frame. State gas is a final attribution rather than a running total: a later frame
	// can retroactively reduce an earlier one through a state-gas refill.
	ExecGasUsed  uint64
	StateGasUsed uint64

	// LogCount is how many logs the frame emitted. The transaction's logs are the
	// per-frame lists concatenated in frame order, so these counts partition the events
	// section and attribute each log to the frame that emitted it.
	LogCount uint32
}

// FrameReceiptData is the frame-transaction content of a receipt.
//
// A frame transaction's own fields - its targets, values, calldata and gas budgets - are
// recoverable from the transaction in the beacon block. What only the receipt holds is
// who paid and what each frame did, so that is what is kept here.
type FrameReceiptData struct {
	// Payer settled the transaction's fee. For a sponsored transaction it is a paymaster
	// rather than the sender, which is the point of the field.
	Payer [20]byte

	Frames []FrameReceiptEntry `ssz-max:"64"`
}

// ReceiptMetaData holds per-transaction receipt metadata needed to
// reconstruct a full eth_getTransactionReceipt JSON response.
// Stored in the ReceiptMeta section (bitmap flag 0x08) of the execution
// data object.
type ReceiptMetaData struct {
	Version           uint16      // Schema version for forward compatibility
	Status            uint8       // 0=failure, 1=success
	TxType            uint8       // Transaction type (0=legacy, 1=access list, 2=dynamic fee, 3=blob, 4=set code)
	CumulativeGasUsed uint64      // Cumulative gas used in block up to and including this tx
	GasUsed           uint64      // Gas used by this specific transaction
	EffectiveGasPrice uint256.Int // Actual gas price paid (in wei)
	BlobGasUsed       uint64      // Blob gas used (EIP-4844), 0 otherwise
	LogsBloom         [256]byte   // Bloom filter for this receipt's logs
	From              [20]byte    // Sender address
	To                [20]byte    // Receiver address (zero for contract creation)
	ContractAddress   [20]byte    // Created contract address (zero if not creation)
	HasContractAddr   bool        // Whether ContractAddress is valid (contract creation tx)
}
