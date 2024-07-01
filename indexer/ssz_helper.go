package indexer

import (
	"fmt"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/altair"
	"github.com/attestantio/go-eth2-client/spec/bellatrix"
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/utils"
	dynssz "github.com/pk910/dynamic-ssz"
	"gopkg.in/yaml.v3"
)

var staticConfigSpec map[string]any
var jsonVersionOffset uint64 = 0x70000000

func getConfigSpec() map[string]any {
	if staticConfigSpec != nil {
		return staticConfigSpec
	}

	staticConfigSpec = map[string]any{}
	specYaml, err := yaml.Marshal(utils.Config.Chain.Config)
	if err != nil {
		yaml.Unmarshal(specYaml, staticConfigSpec)
	}
	return staticConfigSpec
}

func MarshalVersionedSignedBeaconBlockSSZ(block *spec.VersionedSignedBeaconBlock) (version uint64, ssz []byte, err error) {
	if utils.Config.KillSwitch.DisableSSZEncoding {
		// SSZ encoding disabled, use json instead
		return marshalVersionedSignedBeaconBlockJson(block)
	}

	dynSsz := dynssz.NewDynSsz(getConfigSpec())

	switch block.Version {
	case spec.DataVersionPhase0:
		version = uint64(block.Version)
		ssz, err = dynSsz.MarshalSSZ(block.Phase0)
	case spec.DataVersionAltair:
		version = uint64(block.Version)
		ssz, err = dynSsz.MarshalSSZ(block.Altair)
	case spec.DataVersionBellatrix:
		version = uint64(block.Version)
		ssz, err = dynSsz.MarshalSSZ(block.Bellatrix)
	case spec.DataVersionCapella:
		version = uint64(block.Version)
		ssz, err = dynSsz.MarshalSSZ(block.Capella)
	case spec.DataVersionDeneb:
		version = uint64(block.Version)
		ssz, err = dynSsz.MarshalSSZ(block.Deneb)
	case spec.DataVersionElectra:
		version = uint64(block.Version)
		ssz, err = dynSsz.MarshalSSZ(block.Electra)
	default:
		err = fmt.Errorf("unknown block version")
	}
	return
}

func UnmarshalVersionedSignedBeaconBlockSSZ(version uint64, ssz []byte) (*spec.VersionedSignedBeaconBlock, error) {
	if version >= jsonVersionOffset {
		return unmarshalVersionedSignedBeaconBlockJson(version, ssz)
	}
	block := &spec.VersionedSignedBeaconBlock{
		Version: spec.DataVersion(version),
	}
	dynSsz := dynssz.NewDynSsz(getConfigSpec())

	switch block.Version {
	case spec.DataVersionPhase0:
		block.Phase0 = &phase0.SignedBeaconBlock{}
		if err := dynSsz.UnmarshalSSZ(block.Phase0, ssz); err != nil {
			return nil, fmt.Errorf("failed to decode phase0 signed beacon block: %v", err)
		}
	case spec.DataVersionAltair:
		block.Altair = &altair.SignedBeaconBlock{}
		if err := dynSsz.UnmarshalSSZ(block.Altair, ssz); err != nil {
			return nil, fmt.Errorf("failed to decode altair signed beacon block: %v", err)
		}
	case spec.DataVersionBellatrix:
		block.Bellatrix = &bellatrix.SignedBeaconBlock{}
		if err := dynSsz.UnmarshalSSZ(block.Bellatrix, ssz); err != nil {
			return nil, fmt.Errorf("failed to decode bellatrix signed beacon block: %v", err)
		}
	case spec.DataVersionCapella:
		block.Capella = &capella.SignedBeaconBlock{}
		if err := dynSsz.UnmarshalSSZ(block.Capella, ssz); err != nil {
			return nil, fmt.Errorf("failed to decode capella signed beacon block: %v", err)
		}
	case spec.DataVersionDeneb:
		block.Deneb = &deneb.SignedBeaconBlock{}
		if err := dynSsz.UnmarshalSSZ(block.Deneb, ssz); err != nil {
			return nil, fmt.Errorf("failed to decode deneb signed beacon block: %v", err)
		}
	case spec.DataVersionElectra:
		block.Electra = &electra.SignedBeaconBlock{}
		if err := dynSsz.UnmarshalSSZ(block.Deneb, ssz); err != nil {
			return nil, fmt.Errorf("failed to decode electra signed beacon block: %v", err)
		}
	default:
		return nil, fmt.Errorf("unknown block version")
	}
	return block, nil
}

func marshalVersionedSignedBeaconBlockJson(block *spec.VersionedSignedBeaconBlock) (version uint64, jsonRes []byte, err error) {
	switch block.Version {
	case spec.DataVersionPhase0:
		version = uint64(block.Version) + jsonVersionOffset
		jsonRes, err = block.Phase0.MarshalJSON()
	case spec.DataVersionAltair:
		version = uint64(block.Version) + jsonVersionOffset
		jsonRes, err = block.Altair.MarshalJSON()
	case spec.DataVersionBellatrix:
		version = uint64(block.Version) + jsonVersionOffset
		jsonRes, err = block.Bellatrix.MarshalJSON()
	case spec.DataVersionCapella:
		version = uint64(block.Version) + jsonVersionOffset
		jsonRes, err = block.Capella.MarshalJSON()
	case spec.DataVersionDeneb:
		version = uint64(block.Version) + jsonVersionOffset
		jsonRes, err = block.Deneb.MarshalJSON()
	case spec.DataVersionElectra:
		version = uint64(block.Version) + jsonVersionOffset
		jsonRes, err = block.Electra.MarshalJSON()
	default:
		err = fmt.Errorf("unknown block version")
	}
	return
}

func unmarshalVersionedSignedBeaconBlockJson(version uint64, ssz []byte) (*spec.VersionedSignedBeaconBlock, error) {
	if version < jsonVersionOffset {
		return nil, fmt.Errorf("no json encoding")
	}
	block := &spec.VersionedSignedBeaconBlock{
		Version: spec.DataVersion(version - jsonVersionOffset),
	}
	switch block.Version {
	case spec.DataVersionPhase0:
		block.Phase0 = &phase0.SignedBeaconBlock{}
		if err := block.Phase0.UnmarshalJSON(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode phase0 signed beacon block: %v", err)
		}
	case spec.DataVersionAltair:
		block.Altair = &altair.SignedBeaconBlock{}
		if err := block.Altair.UnmarshalJSON(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode altair signed beacon block: %v", err)
		}
	case spec.DataVersionBellatrix:
		block.Bellatrix = &bellatrix.SignedBeaconBlock{}
		if err := block.Bellatrix.UnmarshalJSON(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode bellatrix signed beacon block: %v", err)
		}
	case spec.DataVersionCapella:
		block.Capella = &capella.SignedBeaconBlock{}
		if err := block.Capella.UnmarshalJSON(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode capella signed beacon block: %v", err)
		}
	case spec.DataVersionDeneb:
		block.Deneb = &deneb.SignedBeaconBlock{}
		if err := block.Deneb.UnmarshalJSON(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode deneb signed beacon block: %v", err)
		}
	case spec.DataVersionElectra:
		block.Electra = &electra.SignedBeaconBlock{}
		if err := block.Electra.UnmarshalJSON(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode electra signed beacon block: %v", err)
		}
	default:
		return nil, fmt.Errorf("unknown block version")
	}
	return block, nil
}
