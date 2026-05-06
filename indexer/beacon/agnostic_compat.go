package beacon

import (
	"fmt"

	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/altair"
	"github.com/ethpandaops/go-eth2-client/spec/bellatrix"
	"github.com/ethpandaops/go-eth2-client/spec/capella"
	"github.com/ethpandaops/go-eth2-client/spec/deneb"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/heze"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// AgnosticToVersionedSignedBeaconBlock builds a *spec.VersionedSignedBeaconBlock
// from a fork-agnostic *all.SignedBeaconBlock by going through ToView() so the
// per-fork view machinery in go-eth2-client is the single source of truth for
// the fork-specific block layouts.
//
// dora itself operates on *all.SignedBeaconBlock everywhere; this helper only
// exists for boundaries that hand the block to external libraries still keyed
// on the legacy versioned wrapper (currently eth-das-guardian).
//
// Fulu shares its block schema with Electra, so an agnostic block with
// Version Fulu yields an *electra.SignedBeaconBlock view that lands in
// versioned.Fulu.
func AgnosticToVersionedSignedBeaconBlock(block *all.SignedBeaconBlock) (*spec.VersionedSignedBeaconBlock, error) {
	if block == nil {
		return nil, nil
	}

	view, err := block.ToView()
	if err != nil {
		return nil, fmt.Errorf("convert agnostic signed beacon block to view: %w", err)
	}

	versioned := &spec.VersionedSignedBeaconBlock{
		Version: block.Version,
	}

	switch v := view.(type) {
	case *phase0.SignedBeaconBlock:
		versioned.Phase0 = v
	case *altair.SignedBeaconBlock:
		versioned.Altair = v
	case *bellatrix.SignedBeaconBlock:
		versioned.Bellatrix = v
	case *capella.SignedBeaconBlock:
		versioned.Capella = v
	case *deneb.SignedBeaconBlock:
		versioned.Deneb = v
	case *electra.SignedBeaconBlock:
		// Electra schema is shared with Fulu; the legacy wrapper exposes
		// distinct fields for both, picked by the recorded version.
		if block.Version == spec.DataVersionFulu {
			versioned.Fulu = v
		} else {
			versioned.Electra = v
		}
	case *gloas.SignedBeaconBlock:
		versioned.Gloas = v
	case *heze.SignedBeaconBlock:
		versioned.Heze = v
	default:
		return nil, fmt.Errorf("unsupported signed beacon block view type %T", view)
	}

	return versioned, nil
}
