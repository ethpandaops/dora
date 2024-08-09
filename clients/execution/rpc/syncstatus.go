package rpc

type SyncStatus struct {
	IsSyncing     bool
	StartingBlock uint64
	CurrentBlock  uint64
	HighestBlock  uint64
}

func (s *SyncStatus) Percent() float64 {
	if !s.IsSyncing {
		return 100 //notlint:gomnd
	}

	return float64(s.CurrentBlock) / float64(s.HighestBlock) * 100 //notlint:gomnd // 100 will never change.
}
