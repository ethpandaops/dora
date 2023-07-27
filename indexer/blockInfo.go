package indexer

import (
	"encoding/json"

	"github.com/pk910/light-beaconchain-explorer/dbtypes"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
)

type BlockInfo struct {
	Header   *rpctypes.StandardV1BeaconHeaderResponse
	Block    *rpctypes.StandardV2BeaconBlockResponse
	Orphaned bool
}

func BuildOrphanedBlock(block *BlockInfo) *dbtypes.OrphanedBlock {
	headerJson, err := json.Marshal(block.Header)
	if err != nil {
		return nil
	}
	blockJson, err := json.Marshal(block.Block)
	if err != nil {
		return nil
	}
	return &dbtypes.OrphanedBlock{
		Root:   block.Header.Data.Root,
		Header: string(headerJson),
		Block:  string(blockJson),
	}
}

func ParseOrphanedBlock(blockData *dbtypes.OrphanedBlock) *BlockInfo {
	var header rpctypes.StandardV1BeaconHeaderResponse
	err := json.Unmarshal([]byte(blockData.Header), &header)
	if err != nil {
		logger.Warnf("Error parsing orphaned block header from db: %v", err)
		return nil
	}
	var block rpctypes.StandardV2BeaconBlockResponse
	err = json.Unmarshal([]byte(blockData.Block), &block)
	if err != nil {
		logger.Warnf("Error parsing orphaned block body from db: %v", err)
		return nil
	}
	return &BlockInfo{
		Header:   &header,
		Block:    &block,
		Orphaned: true,
	}
}
