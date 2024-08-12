package services

import (
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
)

func (bs *ChainService) GetDbEpochs(firstEpoch uint64, limit uint32) []*dbtypes.Epoch {
	resEpochs := make([]*dbtypes.Epoch, limit)
	resIdx := 0

	dbEpochs := db.GetEpochs(firstEpoch, limit)
	dbIdx := 0
	dbCnt := len(dbEpochs)

	chainState := bs.consensusPool.GetChainState()
	finalizedEpoch, _ := bs.beaconIndexer.GetBlockCacheState()
	currentEpoch := chainState.CurrentEpoch()

	lastEpoch := int64(firstEpoch) - int64(limit)
	if lastEpoch < 0 {
		lastEpoch = 0
	}
	for epochIdx := int64(firstEpoch); epochIdx >= lastEpoch && resIdx < int(limit); epochIdx-- {
		epoch := phase0.Epoch(epochIdx)
		var resEpoch *dbtypes.Epoch
		if dbIdx < dbCnt && dbEpochs[dbIdx].Epoch == uint64(epoch) {
			resEpoch = dbEpochs[dbIdx]
			dbIdx++
		}
		if epoch >= finalizedEpoch && epoch <= currentEpoch {
			if epochStats := bs.beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
				resEpoch = epochStats.GetDbEpoch(bs.beaconIndexer, nil)
			}
		}
		if resEpoch == nil {
			resEpoch = &dbtypes.Epoch{
				Epoch: uint64(epoch),
			}
		}

		resEpochs[resIdx] = resEpoch
		resIdx++
	}

	return resEpochs
}
