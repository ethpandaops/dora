package ethtypes

import (
	"errors"
	"fmt"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/altair"
	"github.com/attestantio/go-eth2-client/spec/bellatrix"
	"github.com/attestantio/go-eth2-client/spec/capella"
	"github.com/attestantio/go-eth2-client/spec/deneb"
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

func VersionedSignedBeaconBlock_MarshalVersionedSSZ(block *spec.VersionedSignedBeaconBlock) (ver uint64, ssz []byte, err error) {
	switch block.Version {
	case spec.DataVersionPhase0:
		ver = uint64(block.Version)
		ssz, err = block.Phase0.MarshalSSZ()
	case spec.DataVersionAltair:
		ver = uint64(block.Version)
		ssz, err = block.Altair.MarshalSSZ()
	case spec.DataVersionBellatrix:
		ver = uint64(block.Version)
		ssz, err = block.Bellatrix.MarshalSSZ()
	case spec.DataVersionCapella:
		ver = uint64(block.Version)
		ssz, err = block.Capella.MarshalSSZ()
	case spec.DataVersionDeneb:
		ver = uint64(block.Version)
		ssz, err = block.Deneb.MarshalSSZ()
	default:
		err = fmt.Errorf("unknown block version")
	}
	return
}

func VersionedSignedBeaconBlock_UnmarshalVersionedSSZ(ver uint64, ssz []byte) (*spec.VersionedSignedBeaconBlock, error) {
	block := &spec.VersionedSignedBeaconBlock{
		Version: spec.DataVersion(ver),
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
	default:
		return nil, fmt.Errorf("unknown block version")
	}
	return block, nil
}

func VersionedSignedBeaconBlock_ExecutionBlockHash(v *spec.VersionedSignedBeaconBlock) (phase0.Hash32, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return phase0.Hash32{}, nil
	case spec.DataVersionAltair:
		return phase0.Hash32{}, nil
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.Message == nil || v.Bellatrix.Message.Body == nil || v.Bellatrix.Message.Body.ExecutionPayload == nil {
			return phase0.Hash32{}, errors.New("no bellatrix block")
		}
		return v.Bellatrix.Message.Body.ExecutionPayload.BlockHash, nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.Message == nil || v.Capella.Message.Body == nil || v.Capella.Message.Body.ExecutionPayload == nil {
			return phase0.Hash32{}, errors.New("no capella block")
		}
		return v.Capella.Message.Body.ExecutionPayload.BlockHash, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil || v.Deneb.Message.Body.ExecutionPayload == nil {
			return phase0.Hash32{}, errors.New("no deneb block")
		}
		return v.Deneb.Message.Body.ExecutionPayload.BlockHash, nil
	default:
		return phase0.Hash32{}, errors.New("unknown version")
	}
}

func VersionedSignedBeaconBlock_ExecutionBlockNumber(v *spec.VersionedSignedBeaconBlock) (uint64, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return 0, nil
	case spec.DataVersionAltair:
		return 0, nil
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.Message == nil || v.Bellatrix.Message.Body == nil || v.Bellatrix.Message.Body.ExecutionPayload == nil {
			return 0, errors.New("no bellatrix block")
		}
		return v.Bellatrix.Message.Body.ExecutionPayload.BlockNumber, nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.Message == nil || v.Capella.Message.Body == nil || v.Capella.Message.Body.ExecutionPayload == nil {
			return 0, errors.New("no capella block")
		}
		return v.Capella.Message.Body.ExecutionPayload.BlockNumber, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil || v.Deneb.Message.Body.ExecutionPayload == nil {
			return 0, errors.New("no deneb block")
		}
		return v.Deneb.Message.Body.ExecutionPayload.BlockNumber, nil
	default:
		return 0, errors.New("unknown version")
	}
}

func VersionedSignedBeaconBlock_RandaoReveal(v *spec.VersionedSignedBeaconBlock) (phase0.BLSSignature, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		if v.Phase0 == nil || v.Phase0.Message == nil || v.Phase0.Message.Body == nil {
			return phase0.BLSSignature{}, errors.New("no phase0 block")
		}
		return v.Phase0.Message.Body.RANDAOReveal, nil
	case spec.DataVersionAltair:
		if v.Altair == nil || v.Altair.Message == nil || v.Altair.Message.Body == nil {
			return phase0.BLSSignature{}, errors.New("no altair block")
		}
		return v.Altair.Message.Body.RANDAOReveal, nil
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.Message == nil || v.Bellatrix.Message.Body == nil {
			return phase0.BLSSignature{}, errors.New("no bellatrix block")
		}
		return v.Bellatrix.Message.Body.RANDAOReveal, nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.Message == nil || v.Capella.Message.Body == nil {
			return phase0.BLSSignature{}, errors.New("no capella block")
		}
		return v.Capella.Message.Body.RANDAOReveal, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil {
			return phase0.BLSSignature{}, errors.New("no deneb block")
		}
		return v.Deneb.Message.Body.RANDAOReveal, nil
	default:
		return phase0.BLSSignature{}, errors.New("unknown version")
	}
}

func VersionedSignedBeaconBlock_ETH1Data(v *spec.VersionedSignedBeaconBlock) (*phase0.ETH1Data, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		if v.Phase0 == nil || v.Phase0.Message == nil || v.Phase0.Message.Body == nil {
			return nil, errors.New("no phase0 block")
		}
		return v.Phase0.Message.Body.ETH1Data, nil
	case spec.DataVersionAltair:
		if v.Altair == nil || v.Altair.Message == nil || v.Altair.Message.Body == nil {
			return nil, errors.New("no altair block")
		}
		return v.Altair.Message.Body.ETH1Data, nil
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.Message == nil || v.Bellatrix.Message.Body == nil {
			return nil, errors.New("no bellatrix block")
		}
		return v.Bellatrix.Message.Body.ETH1Data, nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.Message == nil || v.Capella.Message.Body == nil {
			return nil, errors.New("no capella block")
		}
		return v.Capella.Message.Body.ETH1Data, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil {
			return nil, errors.New("no deneb block")
		}
		return v.Deneb.Message.Body.ETH1Data, nil
	default:
		return nil, errors.New("unknown version")
	}
}

func VersionedSignedBeaconBlock_ExecutionTransactions(v *spec.VersionedSignedBeaconBlock) ([]bellatrix.Transaction, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return nil, errors.New("phase0 block does not have execution transactions")
	case spec.DataVersionAltair:
		return nil, errors.New("altair block does not have execution transactions")
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.Message == nil || v.Bellatrix.Message.Body == nil || v.Bellatrix.Message.Body.ExecutionPayload == nil {
			return nil, errors.New("no bellatrix block")
		}
		return v.Bellatrix.Message.Body.ExecutionPayload.Transactions, nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.Message == nil || v.Capella.Message.Body == nil || v.Capella.Message.Body.ExecutionPayload == nil {
			return nil, errors.New("no capella block")
		}
		return v.Capella.Message.Body.ExecutionPayload.Transactions, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil || v.Deneb.Message.Body.ExecutionPayload == nil {
			return nil, errors.New("no deneb block")
		}
		return v.Deneb.Message.Body.ExecutionPayload.Transactions, nil
	default:
		return nil, errors.New("unknown version")
	}
}

func VersionedSignedBeaconBlock_Graffiti(v *spec.VersionedSignedBeaconBlock) ([]byte, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		if v.Phase0 == nil || v.Phase0.Message == nil || v.Phase0.Message.Body == nil {
			return nil, errors.New("no phase0 block")
		}
		return v.Phase0.Message.Body.Graffiti[:], nil
	case spec.DataVersionAltair:
		if v.Altair == nil || v.Altair.Message == nil || v.Altair.Message.Body == nil {
			return nil, errors.New("no altair block")
		}
		return v.Altair.Message.Body.Graffiti[:], nil
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.Message == nil || v.Bellatrix.Message.Body == nil {
			return nil, errors.New("no bellatrix block")
		}
		return v.Bellatrix.Message.Body.Graffiti[:], nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.Message == nil || v.Capella.Message.Body == nil {
			return nil, errors.New("no capella block")
		}
		return v.Capella.Message.Body.Graffiti[:], nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil {
			return nil, errors.New("no deneb block")
		}
		return v.Deneb.Message.Body.Graffiti[:], nil
	default:
		return nil, errors.New("unknown version")
	}
}

func VersionedSignedBeaconBlock_Deposits(v *spec.VersionedSignedBeaconBlock) ([]*phase0.Deposit, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		if v.Phase0 == nil || v.Phase0.Message == nil || v.Phase0.Message.Body == nil {
			return nil, errors.New("no phase0 block")
		}
		return v.Phase0.Message.Body.Deposits, nil
	case spec.DataVersionAltair:
		if v.Altair == nil || v.Altair.Message == nil || v.Altair.Message.Body == nil {
			return nil, errors.New("no altair block")
		}
		return v.Altair.Message.Body.Deposits, nil
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.Message == nil || v.Bellatrix.Message.Body == nil {
			return nil, errors.New("no bellatrix block")
		}
		return v.Bellatrix.Message.Body.Deposits, nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.Message == nil || v.Capella.Message.Body == nil {
			return nil, errors.New("no capella block")
		}
		return v.Capella.Message.Body.Deposits, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil {
			return nil, errors.New("no deneb block")
		}
		return v.Deneb.Message.Body.Deposits, nil
	default:
		return nil, errors.New("unknown version")
	}
}

func VersionedSignedBeaconBlock_VoluntaryExits(v *spec.VersionedSignedBeaconBlock) ([]*phase0.SignedVoluntaryExit, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		if v.Phase0 == nil || v.Phase0.Message == nil || v.Phase0.Message.Body == nil {
			return nil, errors.New("no phase0 block")
		}
		return v.Phase0.Message.Body.VoluntaryExits, nil
	case spec.DataVersionAltair:
		if v.Altair == nil || v.Altair.Message == nil || v.Altair.Message.Body == nil {
			return nil, errors.New("no altair block")
		}
		return v.Altair.Message.Body.VoluntaryExits, nil
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.Message == nil || v.Bellatrix.Message.Body == nil {
			return nil, errors.New("no bellatrix block")
		}
		return v.Bellatrix.Message.Body.VoluntaryExits, nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.Message == nil || v.Capella.Message.Body == nil {
			return nil, errors.New("no capella block")
		}
		return v.Capella.Message.Body.VoluntaryExits, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil {
			return nil, errors.New("no deneb block")
		}
		return v.Deneb.Message.Body.VoluntaryExits, nil
	default:
		return nil, errors.New("unknown version")
	}
}

func VersionedSignedBeaconBlock_SyncAggregate(v *spec.VersionedSignedBeaconBlock) (*altair.SyncAggregate, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return nil, errors.New("phase0 block does not have sync aggregate")
	case spec.DataVersionAltair:
		if v.Altair == nil || v.Altair.Message == nil || v.Altair.Message.Body == nil {
			return nil, errors.New("no altair block")
		}
		return v.Altair.Message.Body.SyncAggregate, nil
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.Message == nil || v.Bellatrix.Message.Body == nil {
			return nil, errors.New("no bellatrix block")
		}
		return v.Bellatrix.Message.Body.SyncAggregate, nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.Message == nil || v.Capella.Message.Body == nil {
			return nil, errors.New("no capella block")
		}
		return v.Capella.Message.Body.SyncAggregate, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil {
			return nil, errors.New("no deneb block")
		}
		return v.Deneb.Message.Body.SyncAggregate, nil
	default:
		return nil, errors.New("unknown version")
	}
}

func VersionedSignedBeaconBlock_BLSToExecutionChanges(v *spec.VersionedSignedBeaconBlock) ([]*capella.SignedBLSToExecutionChange, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return nil, errors.New("phase0 block does not have bls to execution changes")
	case spec.DataVersionAltair:
		return nil, errors.New("altair block does not have bls to execution changes")
	case spec.DataVersionBellatrix:
		return nil, errors.New("bellatrix block does not have bls to execution changes")
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.Message == nil || v.Capella.Message.Body == nil {
			return nil, errors.New("no capella block")
		}
		return v.Capella.Message.Body.BLSToExecutionChanges, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil {
			return nil, errors.New("no deneb block")
		}
		return v.Deneb.Message.Body.BLSToExecutionChanges, nil
	default:
		return nil, errors.New("unknown version")
	}
}

func VersionedSignedBeaconBlock_Withdrawals(v *spec.VersionedSignedBeaconBlock) ([]*capella.Withdrawal, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return nil, errors.New("phase0 block does not have execution withdrawals")
	case spec.DataVersionAltair:
		return nil, errors.New("altair block does not have execution withdrawals")
	case spec.DataVersionBellatrix:
		return nil, errors.New("bellatrix block does not have execution withdrawals")
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.Message == nil || v.Capella.Message.Body == nil || v.Capella.Message.Body.ExecutionPayload == nil {
			return nil, errors.New("no capella block")
		}
		return v.Capella.Message.Body.ExecutionPayload.Withdrawals, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil || v.Deneb.Message.Body.ExecutionPayload == nil {
			return nil, errors.New("no deneb block")
		}
		return v.Deneb.Message.Body.ExecutionPayload.Withdrawals, nil
	default:
		return nil, errors.New("unknown version")
	}
}

func VersionedSignedBeaconBlock_BlobKzgCommitments(v *spec.VersionedSignedBeaconBlock) ([]deneb.KzgCommitment, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return nil, errors.New("phase0 block does not have kzg commitments")
	case spec.DataVersionAltair:
		return nil, errors.New("altair block does not have kzg commitments")
	case spec.DataVersionBellatrix:
		return nil, errors.New("bellatrix block does not have kzg commitments")
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.Message == nil || v.Deneb.Message.Body == nil {
			return nil, errors.New("no deneb block")
		}
		return v.Deneb.Message.Body.BlobKzgCommitments, nil
	default:
		return nil, errors.New("unknown version")
	}
}
