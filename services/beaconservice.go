package services

import (
	"fmt"

	"github.com/pk910/light-beaconchain-explorer/cache"
	"github.com/pk910/light-beaconchain-explorer/indexer"
	"github.com/pk910/light-beaconchain-explorer/rpc"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

type BeaconService struct {
	rpcClient   *rpc.BeaconClient
	tieredCache *cache.TieredCache
	indexer     *indexer.Indexer
}

var GlobalBeaconService *BeaconService

// StartBeaconService is used to start the global beaconchain service
func StartBeaconService() error {
	if GlobalBeaconService != nil {
		return nil
	}
	rpcClient, err := rpc.NewBeaconClient(utils.Config.BeaconApi.Endpoint)
	if err != nil {
		return err
	}

	cachePrefix := fmt.Sprintf("%srpc-", utils.Config.BeaconApi.RedisCachePrefix)
	tieredCache, err := cache.NewTieredCache(utils.Config.BeaconApi.LocalCacheSize, utils.Config.BeaconApi.RedisCacheAddr, cachePrefix)
	if err != nil {
		return err
	}

	inMemoryEpochs := utils.Config.BeaconApi.InMemoryEpochs
	if inMemoryEpochs < 2 {
		inMemoryEpochs = 2
	}
	epochProcessingDelay := utils.Config.BeaconApi.EpochProcessingDelay
	if epochProcessingDelay < 2 {
		epochProcessingDelay = 2
	} else if epochProcessingDelay > inMemoryEpochs {
		inMemoryEpochs = epochProcessingDelay
	}
	indexer, err := indexer.NewIndexer(rpcClient, inMemoryEpochs, epochProcessingDelay, !utils.Config.BeaconApi.DisableIndexWriter)
	if err != nil {
		return err
	}
	err = indexer.Start()
	if err != nil {
		return err
	}

	GlobalBeaconService = &BeaconService{
		rpcClient:   rpcClient,
		tieredCache: tieredCache,
		indexer:     indexer,
	}
	return nil
}

func (bs *BeaconService) GetFinalizedBlockHead() (*rpctypes.StandardV1BeaconHeaderResponse, error) {
	return bs.rpcClient.GetFinalizedBlockHead()
}

func (bs *BeaconService) GetSlotDetailsByBlockroot(blockroot []byte) (*rpctypes.CombinedBlockResponse, error) {
	header, err := bs.rpcClient.GetBlockHeaderByBlockroot(blockroot)
	if err != nil {
		return nil, err
	}
	if header == nil {
		return nil, nil
	}
	block, err := bs.rpcClient.GetBlockBodyByBlockroot(header.Data.Root)
	if err != nil {
		return nil, err
	}
	return &rpctypes.CombinedBlockResponse{
		Header: header,
		Block:  block,
	}, nil
}

func (bs *BeaconService) GetSlotDetailsBySlot(slot uint64) (*rpctypes.CombinedBlockResponse, error) {
	header, err := bs.rpcClient.GetBlockHeaderBySlot(slot)
	if err != nil {
		return nil, err
	}
	if header == nil {
		return nil, nil
	}
	block, err := bs.rpcClient.GetBlockBodyByBlockroot(header.Data.Root)
	if err != nil {
		return nil, err
	}
	return &rpctypes.CombinedBlockResponse{
		Header: header,
		Block:  block,
	}, nil
}

func (bs *BeaconService) GetEpochAssignments(epoch uint64) (*rpctypes.EpochAssignments, error) {
	wanted := &rpctypes.EpochAssignments{}
	cacheKey := fmt.Sprintf("epochduties-%v", epoch)
	if wanted, err := bs.tieredCache.GetWithLocalTimeout(cacheKey, 0, wanted); err == nil {
		return wanted.(*rpctypes.EpochAssignments), nil
	}
	wanted, err := bs.rpcClient.GetEpochAssignments(epoch)
	if err != nil {
		return nil, err
	}
	err = bs.tieredCache.Set(cacheKey, wanted, 0)
	if err != nil {
		return nil, err
	}
	return wanted, nil
}
