package beacon

import (
	"context"
	"fmt"
	"time"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// BeaconHeaderRequestTimeout is the timeout duration for beacon header requests.
const beaconHeaderRequestTimeout time.Duration = 30 * time.Second

// BeaconBodyRequestTimeout is the timeout duration for beacon body requests.
const beaconBodyRequestTimeout time.Duration = 30 * time.Second

// BeaconStateRequestTimeout is the timeout duration for beacon state requests.
const beaconStateRequestTimeout time.Duration = 600 * time.Second

const beaconStateRetryCount = 10

// LoadBeaconHeader loads the block header from the client.
func LoadBeaconHeader(ctx context.Context, client *Client, root phase0.Root) (*phase0.SignedBeaconBlockHeader, error) {
	ctx, cancel := context.WithTimeout(ctx, beaconHeaderRequestTimeout)
	defer cancel()

	header, err := client.client.GetRPCClient().GetBlockHeaderByBlockroot(ctx, root)
	if err != nil {
		return nil, err
	}

	return header.Header, nil
}

// LoadBeaconHeaderBySlot loads the block header with given slot number from the client.
func LoadBeaconHeaderBySlot(ctx context.Context, client *Client, slot phase0.Slot) (*phase0.SignedBeaconBlockHeader, phase0.Root, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, beaconHeaderRequestTimeout)
	defer cancel()

	header, err := client.client.GetRPCClient().GetBlockHeaderBySlot(ctx, slot)
	if err != nil {
		return nil, phase0.Root{}, false, err
	}

	if header == nil {
		return nil, phase0.Root{}, false, nil
	}

	return header.Header, header.Root, !header.Canonical, nil
}

// LoadBeaconBlock loads the block body from the RPC client.
func LoadBeaconBlock(ctx context.Context, client *Client, root phase0.Root) (*spec.VersionedSignedBeaconBlock, error) {
	ctx, cancel := context.WithTimeout(ctx, beaconBodyRequestTimeout)
	defer cancel()

	body, err := client.client.GetRPCClient().GetBlockBodyByBlockroot(ctx, root)
	if err != nil {
		return nil, err
	}

	body.Root()

	return body, nil
}

// LoadBeaconState loads the beacon state from the client.
func LoadBeaconState(ctx context.Context, client *Client, root phase0.Root) (*spec.VersionedBeaconState, error) {
	ctx, cancel := context.WithTimeout(ctx, beaconStateRequestTimeout)
	defer cancel()

	resState, err := client.client.GetRPCClient().GetState(ctx, fmt.Sprintf("0x%x", root[:]))
	if err != nil {
		return nil, err
	}

	return resState, nil
}

func GetDynamicBlockRoot(indexer *Indexer, v *spec.VersionedSignedBeaconBlock) (phase0.Root, error) {
	var blockObj any

	switch v.Version {
	case spec.DataVersionPhase0:
		if v.Phase0 == nil {
			return phase0.Root{}, fmt.Errorf("no phase0 block")
		}

		blockObj = v.Phase0.Message
	case spec.DataVersionAltair:
		if v.Altair == nil {
			return phase0.Root{}, fmt.Errorf("no altair block")
		}

		blockObj = v.Altair.Message
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil {
			return phase0.Root{}, fmt.Errorf("no bellatrix block")
		}

		blockObj = v.Bellatrix.Message
	case spec.DataVersionCapella:
		if v.Capella == nil {
			return phase0.Root{}, fmt.Errorf("no capella block")
		}

		blockObj = v.Capella.Message
	case spec.DataVersionDeneb:
		if v.Deneb == nil {
			return phase0.Root{}, fmt.Errorf("no deneb block")
		}

		blockObj = v.Deneb.Message
	case spec.DataVersionElectra:
		if v.Electra == nil {
			return phase0.Root{}, fmt.Errorf("no electra block")
		}

		blockObj = v.Electra.Message
	default:
		return phase0.Root{}, fmt.Errorf("unknown version")
	}

	return indexer.dynSsz.HashTreeRoot(blockObj)
}

func GetDynamicStateRoot(indexer *Indexer, v *spec.VersionedBeaconState) (phase0.Root, error) {
	var stateObj any

	switch v.Version {
	case spec.DataVersionPhase0:
		if v.Phase0 == nil {
			return phase0.Root{}, fmt.Errorf("no phase0 state")
		}

		stateObj = v.Phase0
	case spec.DataVersionAltair:
		if v.Altair == nil {
			return phase0.Root{}, fmt.Errorf("no altair state")
		}

		stateObj = v.Altair
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil {
			return phase0.Root{}, fmt.Errorf("no bellatrix state")
		}

		stateObj = v.Bellatrix
	case spec.DataVersionCapella:
		if v.Capella == nil {
			return phase0.Root{}, fmt.Errorf("no capella state")
		}

		stateObj = v.Capella
	case spec.DataVersionDeneb:
		if v.Deneb == nil {
			return phase0.Root{}, fmt.Errorf("no deneb state")
		}

		stateObj = v.Deneb
	case spec.DataVersionElectra:
		if v.Electra == nil {
			return phase0.Root{}, fmt.Errorf("no electra state")
		}

		stateObj = v.Electra
	default:
		return phase0.Root{}, fmt.Errorf("unknown version")
	}

	return indexer.dynSsz.HashTreeRoot(stateObj)
}
