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
	"github.com/ethpandaops/go-eth2-client/spec/fulu"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/heze"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// Transitional shims that bridge the new fork-agnostic types
// (*all.SignedBeaconBlock, *all.BeaconState) to the legacy
// *spec.VersionedSignedBeaconBlock / *spec.VersionedBeaconState wrappers
// used by code paths that have not yet been migrated (statetransition,
// blockdb body cast, etc.).
//
// These helpers will be removed in a later phase once every consumer reads
// directly from *all.* values.

// AgnosticToVersionedSignedBeaconBlock builds a *spec.VersionedSignedBeaconBlock
// from a fork-agnostic *all.SignedBeaconBlock. The conversion goes through
// (*all.SignedBeaconBlock).ToView() so the per-fork view machinery in the
// upstream library is the single source of truth for fork-specific types.
//
// Fulu shares its block schema with Electra, so an agnostic block with
// Version Fulu (or Electra) yields an *electra.SignedBeaconBlock view; both
// land in the appropriate field of the legacy wrapper.
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
		// Electra schema is shared with Fulu — the legacy wrapper exposes
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

// VersionedToAgnosticSignedBeaconBlock builds a *all.SignedBeaconBlock from
// a legacy *spec.VersionedSignedBeaconBlock by delegating to FromView with
// the populated fork-specific block.
func VersionedToAgnosticSignedBeaconBlock(versioned *spec.VersionedSignedBeaconBlock) (*all.SignedBeaconBlock, error) {
	if versioned == nil {
		return nil, nil
	}

	var view any
	switch versioned.Version {
	case spec.DataVersionPhase0:
		view = versioned.Phase0
	case spec.DataVersionAltair:
		view = versioned.Altair
	case spec.DataVersionBellatrix:
		view = versioned.Bellatrix
	case spec.DataVersionCapella:
		view = versioned.Capella
	case spec.DataVersionDeneb:
		view = versioned.Deneb
	case spec.DataVersionElectra:
		view = versioned.Electra
	case spec.DataVersionFulu:
		view = versioned.Fulu
	case spec.DataVersionGloas:
		view = versioned.Gloas
	case spec.DataVersionHeze:
		view = versioned.Heze
	default:
		return nil, fmt.Errorf("unsupported versioned signed beacon block version %d", versioned.Version)
	}

	if view == nil {
		return nil, fmt.Errorf("versioned signed beacon block has no body for version %d", versioned.Version)
	}

	block := &all.SignedBeaconBlock{Version: versioned.Version}
	if err := block.FromView(view); err != nil {
		return nil, fmt.Errorf("populate agnostic signed beacon block from view: %w", err)
	}

	return block, nil
}

// AgnosticToVersionedBeaconState builds a *spec.VersionedBeaconState from a
// fork-agnostic *all.BeaconState. The conversion goes through
// (*all.BeaconState).ToView() so the upstream view machinery owns all
// fork-specific assembly.
func AgnosticToVersionedBeaconState(state *all.BeaconState) (*spec.VersionedBeaconState, error) {
	if state == nil {
		return nil, nil
	}

	view, err := state.ToView()
	if err != nil {
		return nil, fmt.Errorf("convert agnostic beacon state to view: %w", err)
	}

	versioned := &spec.VersionedBeaconState{
		Version: state.Version,
	}

	switch v := view.(type) {
	case *phase0.BeaconState:
		versioned.Phase0 = v
	case *altair.BeaconState:
		versioned.Altair = v
	case *bellatrix.BeaconState:
		versioned.Bellatrix = v
	case *capella.BeaconState:
		versioned.Capella = v
	case *deneb.BeaconState:
		versioned.Deneb = v
	case *electra.BeaconState:
		versioned.Electra = v
	case *fulu.BeaconState:
		versioned.Fulu = v
	case *gloas.BeaconState:
		versioned.Gloas = v
	case *heze.BeaconState:
		versioned.Heze = v
	default:
		return nil, fmt.Errorf("unsupported beacon state view type %T", view)
	}

	return versioned, nil
}
