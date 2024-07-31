package dbtypes

type IndexerSyncState struct {
	Epoch uint64 `json:"epoch"`
}

type IndexerPruneState struct {
	Epoch uint64 `json:"epoch"`
}

type DepositIndexerState struct {
	FinalBlock   uint64 `json:"final_block"`
	HeadBlock    uint64 `json:"head_block"`
	DepositIndex uint64 `json:"deposit_index"`
}
