package dbtypes

type SearchBlockResult struct {
	Slot     uint64 `db:"slot"`
	Root     []byte `db:"root"`
	Orphaned bool   `db:"orphaned"`
}

type SearchGraffitiResult struct {
	Graffiti string `db:"graffiti"`
}

type SearchAheadEpochsResult []struct {
	Epoch uint64 `db:"epoch"`
}

type SearchAheadSlotsResult []struct {
	Slot     uint64 `db:"slot"`
	Root     []byte `db:"root"`
	Orphaned bool   `db:"orphaned"`
}

type SearchAheadGraffitiResult []struct {
	Graffiti string `db:"graffiti"`
	Count    uint64 `db:"count"`
}
