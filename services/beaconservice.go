package services

import (
	"github.com/pk910/light-beaconchain-explorer/rpc"
	"github.com/pk910/light-beaconchain-explorer/rpctypes"
	"github.com/pk910/light-beaconchain-explorer/utils"
)

type BeaconService struct {
	rpcClient *rpc.BeaconClient
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

	GlobalBeaconService = &BeaconService{
		rpcClient: rpcClient,
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
	return bs.rpcClient.GetEpochAssignments(epoch)
}
