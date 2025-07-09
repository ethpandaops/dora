package services

import (
	"context"
	"math"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
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
		Logger:     logger,
		Libp2pHost: "127.0.0.1",
		BeaconAPI:  guardianApi,
	}

	guardian, err := dasguardian.NewDASGuardian(ctx, opts)
	if err != nil {
		return nil, err
	}

	return &DasGuardian{
		guardian: guardian,
	}, nil
}

func (d *DasGuardian) ScanNode(ctx context.Context, nodeEnr string) (*dasguardian.DASEvaluationResult, error) {
	node, err := dasguardian.ParseNode(nodeEnr)
	if err != nil {
		return nil, err
	}

	res, err := d.guardian.Scan(ctx, node)
	if err != nil {
		return nil, err
	}

	return &res, nil
}

// dasGuardianAPI is the beacon api interface for the DAS Guardian.
type dasGuardianAPI struct {
}

func (d *dasGuardianAPI) Init(ctx context.Context) error {
	return nil
}

func (d *dasGuardianAPI) GetForkDigest() ([]byte, error) {
	forkDigest := GlobalBeaconService.GetChainState().GetCurrentForkDigest()
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

func (d *dasGuardianAPI) GetFuluForkEpoch() int {
	specs := GlobalBeaconService.GetChainState().GetSpecs()
	if specs == nil {
		return 0
	}

	if specs.FuluForkEpoch == nil {
		return math.MaxInt
	}

	return int(*specs.FuluForkEpoch)
}

func (d *dasGuardianAPI) GetNodeIdentity(ctx context.Context) (*api.NodeIdentity, error) {
	// not needed for now
	return nil, nil
}

func (d *dasGuardianAPI) GetBeaconBlock(ctx context.Context, slot uint64) (*spec.VersionedSignedBeaconBlock, error) {
	block, err := GlobalBeaconService.GetSlotDetailsBySlot(ctx, phase0.Slot(slot))
	if err != nil {
		return nil, err
	}

	return block.Block, nil
}
