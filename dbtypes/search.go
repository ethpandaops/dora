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

type SearchAheadValidatorResult []struct {
	Index  uint64 `db:"validator_index"`
	Pubkey []byte `db:"pubkey"`
	Name   string `db:"name"`
}

type SearchAheadAddressResult []struct {
	Address    []byte `db:"address"`
	IsContract bool   `db:"is_contract"`
}

type SearchAheadTransactionResult []struct {
	TxHash      []byte `db:"tx_hash"`
	BlockNumber uint64 `db:"block_number"`
	Reverted    bool   `db:"reverted"`
}
