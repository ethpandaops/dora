// Package blockdb re-exports the DXTX format types and functions from
// blockdb/types so that existing callers can continue using blockdb.XYZ.
package blockdb

import "github.com/ethpandaops/dora/blockdb/types"

// Section bitmap flags (re-exported from types).
const (
	ExecDataSectionEvents      = types.ExecDataSectionEvents
	ExecDataSectionCallTrace   = types.ExecDataSectionCallTrace
	ExecDataSectionStateChange = types.ExecDataSectionStateChange
)

// Type aliases for backward compatibility.
type (
	ExecDataObject        = types.ExecDataObject
	ExecDataTxEntry       = types.ExecDataTxEntry
	ExecDataTxSectionData = types.ExecDataTxSectionData
)

// Function wrappers for backward compatibility.
var (
	BuildExecDataObject   = types.BuildExecDataObject
	ParseExecDataIndex    = types.ParseExecDataIndex
	ParseExecDataTxCount  = types.ParseExecDataTxCount
	ExecDataIndexSize     = types.ExecDataIndexSize
	ExecDataMinHeaderSize = types.ExecDataMinHeaderSize
	ExtractSectionData    = types.ExtractSectionData
)
