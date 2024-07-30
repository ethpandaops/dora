package beacon

import (
	"errors"
	"fmt"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/altair"
	"github.com/attestantio/go-eth2-client/spec/bellatrix"
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/electra"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/utils"
	dynssz "github.com/pk910/dynamic-ssz"
	"gopkg.in/yaml.v3"
)

var staticConfigSpec map[string]any
var jsonVersionOffset uint64 = 0x70000000

func getConfigSpec(specs *consensus.ChainSpec) map[string]any {
	if staticConfigSpec != nil {
		return staticConfigSpec
	}

	staticConfigSpec = map[string]any{}
	specYaml, err := yaml.Marshal(specs)
	if err != nil {
		yaml.Unmarshal(specYaml, staticConfigSpec)
	}
	return staticConfigSpec
}

func MarshalVersionedSignedBeaconBlockSSZ(specs *consensus.ChainSpec, block *spec.VersionedSignedBeaconBlock) (version uint64, ssz []byte, err error) {
	if utils.Config.KillSwitch.DisableSSZEncoding {
		// SSZ encoding disabled, use json instead
		return marshalVersionedSignedBeaconBlockJson(block)
	}

	dynSsz := dynssz.NewDynSsz(getConfigSpec(specs))

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

func UnmarshalVersionedSignedBeaconBlockSSZ(specs *consensus.ChainSpec, version uint64, ssz []byte) (*spec.VersionedSignedBeaconBlock, error) {
	if version >= jsonVersionOffset {
		return unmarshalVersionedSignedBeaconBlockJson(version, ssz)
	}
	block := &spec.VersionedSignedBeaconBlock{
		Version: spec.DataVersion(version),
	}
	dynSsz := dynssz.NewDynSsz(getConfigSpec(specs))

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
		if err := dynSsz.UnmarshalSSZ(block.Electra, ssz); err != nil {
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

func getBlockExecutionExtraData(v *spec.VersionedSignedBeaconBlock) ([]byte, error) {
	switch v.Version {
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.Message == nil || v.Bellatrix.Message.Body == nil || v.Bellatrix.Message.Body.ExecutionPayload == nil {
			return nil, errors.New("no bellatrix block")
		}

		return v.Bellatrix.Message.Body.ExecutionPayload.ExtraData, nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.Message == nil || v.Capella.Message.Body == nil || v.Capella.Message.Body.ExecutionPayload == nil {
			return nil, errors.New("no capella block")
		}

		return v.Capella.Message.Body.ExecutionPayload.ExtraData, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil || v.Deneb.Message.Body.ExecutionPayload == nil {
			return nil, errors.New("no deneb block")
		}

		return v.Deneb.Message.Body.ExecutionPayload.ExtraData, nil
	case spec.DataVersionElectra:
		if v.Electra == nil || v.Electra.Message == nil || v.Electra.Message.Body == nil || v.Electra.Message.Body.ExecutionPayload == nil {
			return nil, errors.New("no electra block")
		}

		return v.Electra.Message.Body.ExecutionPayload.ExtraData, nil
	default:
		return nil, errors.New("unknown version")
	}
}

func getStateRandaoMixes(v *spec.VersionedBeaconState) ([]phase0.Root, error) {
	switch v.Version {
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.RANDAOMixes == nil {
			return nil, errors.New("no bellatrix block")
		}

		return v.Bellatrix.RANDAOMixes, nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.RANDAOMixes == nil {
			return nil, errors.New("no capella block")
		}

		return v.Capella.RANDAOMixes, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.RANDAOMixes == nil {
			return nil, errors.New("no deneb block")
		}

		return v.Deneb.RANDAOMixes, nil
	case spec.DataVersionElectra:
		if v.Electra == nil || v.Electra.RANDAOMixes == nil {
			return nil, errors.New("no electra block")
		}

		return v.Electra.RANDAOMixes, nil
	default:
		return nil, errors.New("unknown version")
	}
}

func getStateDepositIndex(state *spec.VersionedBeaconState) uint64 {
	switch state.Version {
	case spec.DataVersionPhase0:
		return state.Phase0.ETH1DepositIndex
	case spec.DataVersionAltair:
		return state.Altair.ETH1DepositIndex
	case spec.DataVersionBellatrix:
		return state.Bellatrix.ETH1DepositIndex
	case spec.DataVersionCapella:
		return state.Capella.ETH1DepositIndex
	case spec.DataVersionDeneb:
		return state.Deneb.ETH1DepositIndex
	case spec.DataVersionElectra:
		return state.Electra.ETH1DepositIndex
	}
	return 0
}
