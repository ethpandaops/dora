package dbtypes

type SearchBlockResult struct {
	Slot   uint64     `db:"slot"`
	Root   []byte     `db:"root"`
	Status SlotStatus `db:"status"`
}

type SearchGraffitiResult struct {
	Graffiti string `db:"graffiti"`
}

type SearchNameResult struct {
	Name string `db:"name"`
}

type SearchAheadEpochsResult []struct {
	Epoch uint64 `db:"epoch"`
}

type SearchAheadSlotsResult []struct {
	Slot   uint64     `db:"slot"`
	Root   []byte     `db:"root"`
	Status SlotStatus `db:"status"`
}

type SearchAheadExecBlocksResult []struct {
	Slot       uint64     `db:"slot"`
	Root       []byte     `db:"root"`
	ExecHash   []byte     `db:"eth_block_hash"`
	ExecNumber uint64     `db:"eth_block_number"`
	Status     SlotStatus `db:"status"`
}

type SearchAheadGraffitiResult []struct {
	Graffiti string `db:"graffiti"`
	Count    uint64 `db:"count"`
}

type SearchAheadValidatorNameResult []struct {
	Name  string `db:"name"`
	Count uint64 `db:"count"`
}
