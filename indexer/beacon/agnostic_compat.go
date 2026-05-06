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
	"github.com/ethpandaops/go-eth2-client/spec/version"
)

// Transitional shims that bridge the new fork-agnostic types
// (*all.SignedBeaconBlock, *all.BeaconState) returned by the new
// AgnosticSignedBeaconBlock / AgnosticBeaconState API surface back to the
// legacy *spec.VersionedSignedBeaconBlock / *spec.VersionedBeaconState
// wrappers that the rest of dora still operates on.
//
// These helpers are used during the in-progress migration of dora to the
// fork-agnostic types. They will be removed in a later phase once every
// consumer reads directly from *all.* values.

// AgnosticToVersionedSignedBeaconBlock builds a *spec.VersionedSignedBeaconBlock
// from a fork-agnostic *all.SignedBeaconBlock. The conversion goes through
// (*all.SignedBeaconBlock).ToView() so the per-fork view machinery in the
// upstream library is the single source of truth for fork-specific types.
//
// Fulu shares its block schema with Electra and is not enumerated in the
// agnostic block view-type switch, so callers receive an *electra.SignedBeaconBlock
// after a temporary version flip and the result is stored in versioned.Fulu.
func AgnosticToVersionedSignedBeaconBlock(block *all.SignedBeaconBlock) (*spec.VersionedSignedBeaconBlock, error) {
	if block == nil {
		return nil, nil
	}

	if block.Version == spec.DataVersionFulu {
		eb, err := agnosticToElectraSignedBlockView(block)
		if err != nil {
			return nil, err
		}

		return &spec.VersionedSignedBeaconBlock{
			Version: spec.DataVersionFulu,
			Fulu:    eb,
		}, nil
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
		versioned.Electra = v
	case *gloas.SignedBeaconBlock:
		versioned.Gloas = v
	case *heze.SignedBeaconBlock:
		versioned.Heze = v
	default:
		return nil, fmt.Errorf("unsupported signed beacon block view type %T", view)
	}

	return versioned, nil
}

// agnosticToElectraSignedBlockView extracts an *electra.SignedBeaconBlock view
// from an agnostic block whose Version is Fulu. It temporarily flips the
// Version on the agnostic block and its Versioned children so the upstream
// ToView() dispatch reaches the Electra arms, then restores the Fulu version
// on return.
func agnosticToElectraSignedBlockView(block *all.SignedBeaconBlock) (*electra.SignedBeaconBlock, error) {
	type versioned struct {
		set  func(version.DataVersion)
		orig version.DataVersion
	}

	flips := []versioned{
		{set: func(v version.DataVersion) { block.Version = v }, orig: block.Version},
	}

	if block.Message != nil {
		flips = append(flips, versioned{set: func(v version.DataVersion) { block.Message.Version = v }, orig: block.Message.Version})

		if block.Message.Body != nil {
			body := block.Message.Body
			flips = append(flips, versioned{set: func(v version.DataVersion) { body.Version = v }, orig: body.Version})

			if body.ExecutionPayload != nil {
				ep := body.ExecutionPayload
				flips = append(flips, versioned{set: func(v version.DataVersion) { ep.Version = v }, orig: ep.Version})
			}
		}
	}

	for _, f := range flips {
		f.set(version.DataVersionElectra)
	}

	defer func() {
		for _, f := range flips {
			f.set(f.orig)
		}
	}()

	view, err := block.ToView()
	if err != nil {
		return nil, fmt.Errorf("convert agnostic fulu signed beacon block to electra view: %w", err)
	}

	eb, ok := view.(*electra.SignedBeaconBlock)
	if !ok {
		return nil, fmt.Errorf("unexpected fulu signed beacon block view type %T", view)
	}

	return eb, nil
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
