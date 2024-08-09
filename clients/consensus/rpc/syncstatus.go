package rpc

import v1 "github.com/attestantio/go-eth2-client/api/v1"

type SyncStatus struct {
	IsSyncing                bool
	IsOptimistic             bool
	HeadSlot                 uint64
	EstimatedHighestHeadSlot uint64
	SyncDistance             uint64
}

func NewSyncStatus(state *v1.SyncState) SyncStatus {
	return SyncStatus{
		IsSyncing:                state.IsSyncing,
		IsOptimistic:             state.IsOptimistic,
		HeadSlot:                 uint64(state.HeadSlot),
		EstimatedHighestHeadSlot: uint64(state.SyncDistance) + uint64(state.HeadSlot),
		SyncDistance:             uint64(state.SyncDistance),
	}
}

func (s *SyncStatus) Percent() float64 {
	percent := (float64(s.HeadSlot) / float64(s.EstimatedHighestHeadSlot) * 100)
	if !s.IsSyncing {
		percent = 100
	}

	return percent
}
