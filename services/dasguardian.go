package services

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethereum/go-ethereum/common/hexutil"
	dasguardian "github.com/probe-lab/eth-das-guardian"
	"github.com/probe-lab/eth-das-guardian/api"
	"github.com/sirupsen/logrus"
)

type DasGuardian struct {
	guardian *dasguardian.DasGuardian
}

func NewDasGuardian(ctx context.Context, logger logrus.FieldLogger) (*DasGuardian, error) {
	guardianApi := &dasGuardianAPI{}
	opts := &dasguardian.DasGuardianConfig{
		Logger:            logger,
		Libp2pHost:        "127.0.0.1",
		BeaconAPI:         guardianApi,
		ConnectionRetries: 3,
		ConnectionTimeout: 10 * time.Second,
		InitTimeout:       1 * time.Second,
	}

	guardian, err := dasguardian.NewDASGuardian(ctx, opts)
	if err != nil {
		return nil, err
	}

	return &DasGuardian{
		guardian: guardian,
	}, nil
}

func (d *DasGuardian) ScanNode(ctx context.Context, nodeEnr string, slots []uint64) (*dasguardian.DasGuardianScanResult, error) {
	node, err := dasguardian.ParseNode(nodeEnr)
	if err != nil {
		return nil, err
	}

	// Select appropriate slot selector based on input
	var slotSelector dasguardian.SlotSelector
	if len(slots) == 0 {
		// No slots specified - scan metadata only
		slotSelector = dasguardian.WithNoSlots()
	} else {
		// Specific slots requested
		slotSelector = dasguardian.WithCustomSlots(slots)
	}

	// Scan can return both result and error (partial results)
	res, err := d.guardian.Scan(ctx, node, slotSelector)

	// Return both - the handler will deal with partial results
	return res, err
}

// dasGuardianAPI is the beacon api interface for the DAS Guardian.
type dasGuardianAPI struct {
}

func (d *dasGuardianAPI) Init(ctx context.Context) error {
	return nil
}

func (d *dasGuardianAPI) GetStateVersion() string {
	fuluForkEpoch := d.GetFuluForkEpoch()
	currentEpoch := GlobalBeaconService.GetChainState().CurrentEpoch()

	if currentEpoch >= phase0.Epoch(fuluForkEpoch) {
		return "fulu"
	}

	return "electra"
}

func (d *dasGuardianAPI) GetForkDigest(slot uint64) ([]byte, error) {
	chainState := GlobalBeaconService.GetChainState()
	forkDigest := chainState.GetForkDigestForEpoch(chainState.EpochOfSlot(phase0.Slot(slot)))
	return forkDigest[:], nil
}

func (d *dasGuardianAPI) GetFinalizedCheckpoint() *phase0.Checkpoint {
	epoch, root := GlobalBeaconService.GetChainState().GetFinalizedCheckpoint()
	return &phase0.Checkpoint{
		Epoch: epoch,
		Root:  root,
	}
}

func (d *dasGuardianAPI) GetLatestBlockHeader() *phase0.BeaconBlockHeader {
	headBlock := GlobalBeaconService.GetBeaconIndexer().GetCanonicalHead(nil)
	header := headBlock.GetHeader()
	return header.Message
}

func (d *dasGuardianAPI) GetFuluForkEpoch() uint64 {
	specs := GlobalBeaconService.GetChainState().GetSpecs()
	if specs == nil {
		return 0
	}

	if specs.FuluForkEpoch == nil {
		return math.MaxInt64
	}

	return *specs.FuluForkEpoch
}

func (d *dasGuardianAPI) GetNodeIdentity(ctx context.Context) (*api.NodeIdentity, error) {
	// Get the first available consensus client
	consensusClients := GlobalBeaconService.GetConsensusClients()
	if len(consensusClients) == 0 {
		return nil, fmt.Errorf("no consensus clients available")
	}

	// Use the first available client
	client := consensusClients[0]
	localNodeIdentity := client.GetNodeIdentity()
	if localNodeIdentity == nil {
		return nil, fmt.Errorf("node identity not available from consensus client")
	}

	// Convert from local rpc.NodeIdentity to api.NodeIdentity
	nodeIdentity := &api.NodeIdentity{}
	nodeIdentity.Data.PeerID = localNodeIdentity.PeerID
	nodeIdentity.Data.Enr = localNodeIdentity.Enr
	nodeIdentity.Data.Maddrs = localNodeIdentity.P2PAddresses
	nodeIdentity.Data.DiscvAddrs = localNodeIdentity.DiscoveryAddresses

	// Convert metadata
	nodeIdentity.Data.Metadata.SeqNum = fmt.Sprintf("%v", localNodeIdentity.Metadata.SeqNumber)

	// Convert attnets from string to hexutil.Bytes
	if localNodeIdentity.Metadata.Attnets != "" {
		attnets, err := hexutil.Decode(localNodeIdentity.Metadata.Attnets)
		if err != nil {
			return nil, fmt.Errorf("failed to decode attnets: %v", err)
		}
		nodeIdentity.Data.Metadata.Attnets = attnets
	}

	// Convert syncnets from string to hexutil.Bytes
	if localNodeIdentity.Metadata.Syncnets != "" {
		syncnets, err := hexutil.Decode(localNodeIdentity.Metadata.Syncnets)
		if err != nil {
			return nil, fmt.Errorf("failed to decode syncnets: %v", err)
		}
		nodeIdentity.Data.Metadata.Syncnets = syncnets
	}

	// Convert custody group count
	nodeIdentity.Data.Metadata.Cgc = fmt.Sprintf("%v", localNodeIdentity.Metadata.CustodyGroupCount)

	return nodeIdentity, nil
}

func (d *dasGuardianAPI) GetBeaconBlock(ctx context.Context, slot uint64) (*spec.VersionedSignedBeaconBlock, error) {
	block, err := GlobalBeaconService.GetSlotDetailsBySlot(ctx, phase0.Slot(slot))
	if err != nil {
		return nil, err
	}

	if block == nil {
		return nil, fmt.Errorf("block not found for slot %d", slot)
	}

	return block.Block, nil
}

func (d *dasGuardianAPI) ReadSpecParameter(key string) (any, bool) {
	specs := GlobalBeaconService.GetChainState().GetSpecs()
	if specs == nil {
		return nil, false
	}

	switch key {
	case "FULU_FORK_EPOCH":
		if specs.FuluForkEpoch != nil {
			return *specs.FuluForkEpoch, true
		}
		return nil, false
	default:
		return nil, false
	}
}
