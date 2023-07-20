package indexer

import "github.com/pk910/light-beaconchain-explorer/rpctypes"

type BlockInfo struct {
	header *rpctypes.StandardV1BeaconHeaderResponse
	block  *rpctypes.StandardV2BeaconBlockResponse
}

func (blockInfo *BlockInfo) processAggregations() error {
	return nil
}
