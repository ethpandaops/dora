package indexer

import "github.com/pk910/light-beaconchain-explorer/rpctypes"

type BlockInfo struct {
	Header   *rpctypes.StandardV1BeaconHeaderResponse
	Block    *rpctypes.StandardV2BeaconBlockResponse
	Orphaned bool
}

func (blockInfo *BlockInfo) processAggregations() error {
	return nil
}
