package services

import (
	"fmt"

	"github.com/pk910/light-beaconchain-explorer/cache"
	"github.com/pk910/light-beaconchain-explorer/rpc"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

type BeaconService struct {
	rpcClient   *rpc.BeaconClient
	tieredCache *cache.TieredCache
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

	GlobalBeaconService = &BeaconService{
		rpcClient:   rpcClient,
		tieredCache: tieredCache,
	}
	return nil
}

func (bs *BeaconService) GetFinalizedBlockHead() (*rpctypes.StandardV1BeaconHeaderResponse, error) {
	return bs.rpcClient.GetFinalizedBlockHead()
}

func (bs *BeaconService) GetSlotDetailsByBlockroot(blockroot []byte) (*rpctypes.CombinedBlockResponse, error) {
	return bs.rpcClient.GetBlockByBlockroot(blockroot)
}

func (bs *BeaconService) GetSlotDetailsBySlot(slot uint64) (*rpctypes.CombinedBlockResponse, error) {
	return bs.rpcClient.GetBlockBySlot(slot)
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
