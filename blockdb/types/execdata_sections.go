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
	Data       []byte
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
	Status  uint8 // CallStatus* constants
	Input   []byte
	Output  []byte
	Error   string
	Logs    []CallFrameLog
}

// CallFrameLog is a log emitted within a call frame.
type CallFrameLog struct {
	Address [20]byte
	Topics  [][]byte `ssz-size:"?,32" ssz-max:"5"`
	Data    []byte
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
	PreCode  []byte
	PostCode []byte

	// Storage
	Slots []StateChangeSlot
}
