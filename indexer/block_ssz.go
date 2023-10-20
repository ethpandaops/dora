package indexer

import (
	"fmt"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/altair"
	"github.com/attestantio/go-eth2-client/spec/bellatrix"
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/attestantio/go-eth2-client/spec/verkle"
)

func MarshalVersionedSignedBeaconBlockSSZ(block *spec.VersionedSignedBeaconBlock) (version uint64, ssz []byte, err error) {
	switch block.Version {
	case spec.DataVersionPhase0:
		version = uint64(block.Version)
		ssz, err = block.Phase0.MarshalSSZ()
	case spec.DataVersionAltair:
		version = uint64(block.Version)
		ssz, err = block.Altair.MarshalSSZ()
	case spec.DataVersionBellatrix:
		version = uint64(block.Version)
		ssz, err = block.Bellatrix.MarshalSSZ()
	case spec.DataVersionCapella:
		version = uint64(block.Version)
		ssz, err = block.Capella.MarshalSSZ()
	case spec.DataVersionDeneb:
		version = uint64(block.Version)
		ssz, err = block.Deneb.MarshalSSZ()
	case spec.DataVersionVerkle:
		version = uint64(block.Version)
		ssz, err = block.Verkle.MarshalSSZ()
	default:
		err = fmt.Errorf("unknown block version")
	}
	return
}

func UnmarshalVersionedSignedBeaconBlockSSZ(version uint64, ssz []byte) (*spec.VersionedSignedBeaconBlock, error) {
	block := &spec.VersionedSignedBeaconBlock{
		Version: spec.DataVersion(version),
	}
	switch block.Version {
	case spec.DataVersionPhase0:
		block.Phase0 = &phase0.SignedBeaconBlock{}
		if err := block.Phase0.UnmarshalSSZ(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode phase0 signed beacon block: %v", err)
		}
	case spec.DataVersionAltair:
		block.Altair = &altair.SignedBeaconBlock{}
		if err := block.Altair.UnmarshalSSZ(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode altair signed beacon block: %v", err)
		}
	case spec.DataVersionBellatrix:
		block.Bellatrix = &bellatrix.SignedBeaconBlock{}
		if err := block.Bellatrix.UnmarshalSSZ(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode bellatrix signed beacon block: %v", err)
		}
	case spec.DataVersionCapella:
		block.Capella = &capella.SignedBeaconBlock{}
		if err := block.Capella.UnmarshalSSZ(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode capella signed beacon block: %v", err)
		}
	case spec.DataVersionDeneb:
		block.Deneb = &deneb.SignedBeaconBlock{}
		if err := block.Deneb.UnmarshalSSZ(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode deneb signed beacon block: %v", err)
		}
	case spec.DataVersionVerkle:
		block.Verkle = &verkle.SignedBeaconBlock{}
		if err := block.Verkle.UnmarshalSSZ(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode verkle signed beacon block: %v", err)
		}
	default:
		return nil, fmt.Errorf("unknown block version")
	}
	return block, nil
}
