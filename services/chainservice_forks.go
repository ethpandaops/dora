package services

import (
	"fmt"
	"time"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// BpoForkInfo describes a BPO (blob parameter only) fork, normalized from either
// the EL genesis config (preferred, carries the enumerated BPO numbers) or the
// CL BLOB_SCHEDULE (fallback, deduplicated & non-enumerated).
type BpoForkInfo struct {
	Name             string
	Epoch            phase0.Epoch
	Time             time.Time
	MaxBlobsPerBlock uint64
	ForkDigest       phase0.ForkDigest
}

// GetBpoForks returns all BPO forks of the network. The EL genesis config is used
// as source if available, as it enumerates the BPO forks (bpo1Time, bpo2Time, ...),
// so the fork names stay correct even if the schedule doesn't start at BPO1 or
// earlier BPOs are collapsed into genesis. Without an EL genesis config, the forks
// are derived from the CL BLOB_SCHEDULE and numbered sequentially.
func (bs *ChainService) GetBpoForks() []*BpoForkInfo {
	chainState := bs.GetChainState()
	specs := chainState.GetSpecs()
	genesis := chainState.GetGenesis()
	if specs == nil || genesis == nil {
		return nil
	}

	forks := []*BpoForkInfo{}

	elBlobSchedule := bs.GetExecutionChainState().GetFullBlobSchedule()
	if len(elBlobSchedule) > 0 {
		for _, entry := range elBlobSchedule {
			if !entry.IsBpo {
				continue
			}

			epoch := phase0.Epoch(0)
			forkTime := entry.Timestamp
			if entry.Timestamp.After(genesis.GenesisTime) {
				epoch = chainState.EpochOfSlot(chainState.TimeToSlot(entry.Timestamp))
			} else {
				forkTime = genesis.GenesisTime
			}

			forkVersion := chainState.GetForkVersionAtEpoch(epoch)
			blobParams := &consensus.BlobScheduleEntry{
				Epoch:            uint64(epoch),
				MaxBlobsPerBlock: entry.Schedule.Max,
			}

			forks = append(forks, &BpoForkInfo{
				Name:             fmt.Sprintf("BPO%d", entry.BpoNumber),
				Epoch:            epoch,
				Time:             forkTime,
				MaxBlobsPerBlock: entry.Schedule.Max,
				ForkDigest:       chainState.GetForkDigest(forkVersion, blobParams),
			})
		}
	} else {
		for i, entry := range specs.BlobSchedule {
			forkVersion := chainState.GetForkVersionAtEpoch(phase0.Epoch(entry.Epoch))
			blobParams := &consensus.BlobScheduleEntry{
				Epoch:            entry.Epoch,
				MaxBlobsPerBlock: entry.MaxBlobsPerBlock,
			}

			forks = append(forks, &BpoForkInfo{
				Name:             fmt.Sprintf("BPO%d", i+1),
				Epoch:            phase0.Epoch(entry.Epoch),
				Time:             chainState.EpochToTime(phase0.Epoch(entry.Epoch)),
				MaxBlobsPerBlock: entry.MaxBlobsPerBlock,
				ForkDigest:       chainState.GetForkDigest(forkVersion, blobParams),
			})
		}
	}

	return forks
}
