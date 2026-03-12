package types

// BlockDataFlags specifies which components to load from storage.
type BlockDataFlags uint8

const (
	// BlockDataFlagHeader requests the block header data.
	BlockDataFlagHeader BlockDataFlags = 1 << iota // 0x01
	// BlockDataFlagBody requests the block body data.
	BlockDataFlagBody // 0x02
	// BlockDataFlagPayload requests the execution payload data.
	BlockDataFlagPayload // 0x04
	// BlockDataFlagBal requests the block access list data.
	BlockDataFlagBal // 0x08

	// BlockDataFlagAll requests all block components.
	BlockDataFlagAll = BlockDataFlagHeader | BlockDataFlagBody | BlockDataFlagPayload | BlockDataFlagBal
)

// Has returns true if the flag set contains the specified flag.
func (f BlockDataFlags) Has(flag BlockDataFlags) bool {
	return f&flag == flag
}

// HasAny returns true if the flag set contains any of the specified flags.
func (f BlockDataFlags) HasAny(flags BlockDataFlags) bool {
	return f&flags != 0
}

// Add returns a new flag set with the specified flag added.
func (f BlockDataFlags) Add(flag BlockDataFlags) BlockDataFlags {
	return f | flag
}

// Remove returns a new flag set with the specified flag removed.
func (f BlockDataFlags) Remove(flag BlockDataFlags) BlockDataFlags {
	return f &^ flag
}
