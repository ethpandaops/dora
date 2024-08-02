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

// loadHeader loads the block header from the client.
func loadHeader(ctx context.Context, client *Client, root phase0.Root) (*phase0.SignedBeaconBlockHeader, error) {
	ctx, cancel := context.WithTimeout(ctx, beaconHeaderRequestTimeout)
	defer cancel()

	header, err := client.client.GetRPCClient().GetBlockHeaderByBlockroot(ctx, root)
	if err != nil {
		return nil, err
	}

	return header.Header, nil
}

func loadHeaderBySlot(ctx context.Context, client *Client, slot phase0.Slot) (*phase0.SignedBeaconBlockHeader, error) {
	ctx, cancel := context.WithTimeout(ctx, beaconHeaderRequestTimeout)
	defer cancel()

	header, err := client.client.GetRPCClient().GetBlockHeaderBySlot(ctx, slot)
	if err != nil {
		return nil, err
	}

	return header.Header, nil
}

// loadBlock loads the block body from the RPC client.
func loadBlock(ctx context.Context, client *Client, root phase0.Root) (*spec.VersionedSignedBeaconBlock, error) {
	ctx, cancel := context.WithTimeout(ctx, beaconBodyRequestTimeout)
	defer cancel()

	body, err := client.client.GetRPCClient().GetBlockBodyByBlockroot(ctx, root)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func loadState(ctx context.Context, client *Client, root phase0.Root) (*spec.VersionedBeaconState, error) {
	ctx, cancel := context.WithTimeout(ctx, beaconStateRequestTimeout)
	defer cancel()

	resState, err := client.client.GetRPCClient().GetState(ctx, fmt.Sprintf("0x%x", root[:]))
	if err != nil {
		return nil, err
	}

	return resState, nil
}
