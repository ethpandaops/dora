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
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/utils"
	dynssz "github.com/pk910/dynamic-ssz"
)

var jsonVersionFlag uint64 = 0x40000000
var compressionFlag uint64 = 0x20000000

// MarshalVersionedSignedBeaconBlockSSZ marshals a versioned signed beacon block using SSZ encoding.
func MarshalVersionedSignedBeaconBlockSSZ(dynSsz *dynssz.DynSsz, block *spec.VersionedSignedBeaconBlock, compress bool, forceSSZ bool) (version uint64, ssz []byte, err error) {
	if utils.Config.KillSwitch.DisableSSZEncoding && !forceSSZ {
		// SSZ encoding disabled, use json instead
		version, ssz, err = MarshalVersionedSignedBeaconBlockJson(block)
	} else {
		// SSZ encoding
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
		case spec.DataVersionFulu:
			version = uint64(block.Version)
			ssz, err = dynSsz.MarshalSSZ(block.Fulu)
		case spec.DataVersionGloas:
			version = uint64(block.Version)
			ssz, err = dynSsz.MarshalSSZ(block.Gloas)
		default:
			err = fmt.Errorf("unknown block version")
		}
	}

	if compress {
		ssz = compressBytes(ssz)
		version |= compressionFlag
	}

	return
}

// UnmarshalVersionedSignedBeaconBlockSSZ unmarshals a versioned signed beacon block using SSZ encoding.
func UnmarshalVersionedSignedBeaconBlockSSZ(dynSsz *dynssz.DynSsz, version uint64, ssz []byte) (*spec.VersionedSignedBeaconBlock, error) {
	if (version & compressionFlag) != 0 {
		// decompress
		if d, err := decompressBytes(ssz); err != nil {
			return nil, fmt.Errorf("failed to decompress: %v", err)
		} else {
			ssz = d
			version &= ^compressionFlag
		}
	}

	if (version & jsonVersionFlag) != 0 {
		// JSON encoding
		return unmarshalVersionedSignedBeaconBlockJson(version, ssz)
	}

	// SSZ encoding
	block := &spec.VersionedSignedBeaconBlock{
		Version: spec.DataVersion(version),
	}

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
	case spec.DataVersionFulu:
		block.Fulu = &electra.SignedBeaconBlock{}
		if err := dynSsz.UnmarshalSSZ(block.Fulu, ssz); err != nil {
			return nil, fmt.Errorf("failed to decode fulu signed beacon block: %v", err)
		}
	case spec.DataVersionGloas:
		block.Gloas = &gloas.SignedBeaconBlock{}
		if err := dynSsz.UnmarshalSSZ(block.Gloas, ssz); err != nil {
			return nil, fmt.Errorf("failed to decode gloas signed beacon block: %v", err)
		}
	default:
		return nil, fmt.Errorf("unknown block version")
	}
	return block, nil
}

// MarshalVersionedSignedBeaconBlockJson marshals a versioned signed beacon block using JSON encoding.
func MarshalVersionedSignedBeaconBlockJson(block *spec.VersionedSignedBeaconBlock) (version uint64, jsonRes []byte, err error) {
	switch block.Version {
	case spec.DataVersionPhase0:
		version = uint64(block.Version)
		jsonRes, err = block.Phase0.MarshalJSON()
	case spec.DataVersionAltair:
		version = uint64(block.Version)
		jsonRes, err = block.Altair.MarshalJSON()
	case spec.DataVersionBellatrix:
		version = uint64(block.Version)
		jsonRes, err = block.Bellatrix.MarshalJSON()
	case spec.DataVersionCapella:
		version = uint64(block.Version)
		jsonRes, err = block.Capella.MarshalJSON()
	case spec.DataVersionDeneb:
		version = uint64(block.Version)
		jsonRes, err = block.Deneb.MarshalJSON()
	case spec.DataVersionElectra:
		version = uint64(block.Version)
		jsonRes, err = block.Electra.MarshalJSON()
	case spec.DataVersionFulu:
		version = uint64(block.Version)
		jsonRes, err = block.Fulu.MarshalJSON()
	case spec.DataVersionGloas:
		version = uint64(block.Version)
		jsonRes, err = block.Gloas.MarshalJSON()
	default:
		err = fmt.Errorf("unknown block version")
	}

	version |= jsonVersionFlag

	return
}

// unmarshalVersionedSignedBeaconBlockJson unmarshals a versioned signed beacon block using JSON encoding.
func unmarshalVersionedSignedBeaconBlockJson(version uint64, ssz []byte) (*spec.VersionedSignedBeaconBlock, error) {
	if version&jsonVersionFlag == 0 {
		return nil, fmt.Errorf("no json encoding")
	}
	block := &spec.VersionedSignedBeaconBlock{
		Version: spec.DataVersion(version - jsonVersionFlag),
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
	case spec.DataVersionFulu:
		block.Fulu = &electra.SignedBeaconBlock{}
		if err := block.Fulu.UnmarshalJSON(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode fulu signed beacon block: %v", err)
		}
	case spec.DataVersionGloas:
		block.Gloas = &gloas.SignedBeaconBlock{}
		if err := block.Gloas.UnmarshalJSON(ssz); err != nil {
			return nil, fmt.Errorf("failed to decode gloas signed beacon block: %v", err)
		}
	default:
		return nil, fmt.Errorf("unknown block version")
	}
	return block, nil
}

// MarshalVersionedSignedExecutionPayloadEnvelopeSSZ marshals a signed execution payload envelope using SSZ encoding.
func MarshalVersionedSignedExecutionPayloadEnvelopeSSZ(dynSsz *dynssz.DynSsz, payload *gloas.SignedExecutionPayloadEnvelope, compress bool) (version uint64, ssz []byte, err error) {
	if utils.Config.KillSwitch.DisableSSZEncoding {
		// SSZ encoding disabled, use json instead
		version, ssz, err = marshalVersionedSignedExecutionPayloadEnvelopeJson(payload)
	} else {
		// SSZ encoding
		version = uint64(spec.DataVersionGloas)
		ssz, err = dynSsz.MarshalSSZ(payload)
	}

	if compress {
		ssz = compressBytes(ssz)
		version |= compressionFlag
	}

	return
}

// UnmarshalVersionedSignedExecutionPayloadEnvelopeSSZ unmarshals a versioned signed execution payload envelope using SSZ encoding.
func UnmarshalVersionedSignedExecutionPayloadEnvelopeSSZ(dynSsz *dynssz.DynSsz, version uint64, ssz []byte) (*gloas.SignedExecutionPayloadEnvelope, error) {
	if (version & compressionFlag) != 0 {
		// decompress
		if d, err := decompressBytes(ssz); err != nil {
			return nil, fmt.Errorf("failed to decompress: %v", err)
		} else {
			ssz = d
			version &= ^compressionFlag
		}
	}

	if (version & jsonVersionFlag) != 0 {
		// JSON encoding
		return unmarshalVersionedSignedExecutionPayloadEnvelopeJson(version, ssz)
	}

	if version != uint64(spec.DataVersionGloas) {
		return nil, fmt.Errorf("unknown version")
	}

	// SSZ encoding
	payload := &gloas.SignedExecutionPayloadEnvelope{}
	if err := dynSsz.UnmarshalSSZ(payload, ssz); err != nil {
		return nil, fmt.Errorf("failed to decode gloas signed execution payload envelope: %v", err)
	}

	return payload, nil
}

// marshalVersionedSignedExecutionPayloadEnvelopeJson marshals a versioned signed execution payload envelope using JSON encoding.
func marshalVersionedSignedExecutionPayloadEnvelopeJson(payload *gloas.SignedExecutionPayloadEnvelope) (version uint64, jsonRes []byte, err error) {
	version = uint64(spec.DataVersionGloas)
	jsonRes, err = payload.MarshalJSON()

	version |= jsonVersionFlag

	return
}

// unmarshalVersionedSignedExecutionPayloadEnvelopeJson unmarshals a versioned signed execution payload envelope using JSON encoding.
func unmarshalVersionedSignedExecutionPayloadEnvelopeJson(version uint64, ssz []byte) (*gloas.SignedExecutionPayloadEnvelope, error) {
	if version&jsonVersionFlag == 0 {
		return nil, fmt.Errorf("no json encoding")
	}

	if version-jsonVersionFlag != uint64(spec.DataVersionGloas) {
		return nil, fmt.Errorf("unknown version")
	}

	payload := &gloas.SignedExecutionPayloadEnvelope{}
	if err := payload.UnmarshalJSON(ssz); err != nil {
		return nil, fmt.Errorf("failed to decode gloas signed execution payload envelope: %v", err)
	}
	return payload, nil
}

// getBlockExecutionExtraData returns the extra data from the execution payload of a versioned signed beacon block.
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
	case spec.DataVersionGloas:
		return nil, nil
	default:
		return nil, errors.New("unknown version")
	}
}

// getBlockPayloadBuilderIndex returns the builder index from the execution payload of a versioned signed beacon block.
func getBlockPayloadBuilderIndex(v *spec.VersionedSignedBeaconBlock) (gloas.BuilderIndex, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return 0, errors.New("no builder index in phase0 block")
	case spec.DataVersionAltair:
		return 0, errors.New("no builder index in altair block")
	case spec.DataVersionBellatrix:
		return 0, errors.New("no builder index in bellatrix block")
	case spec.DataVersionCapella:
		return 0, errors.New("no builder index in capella block")
	case spec.DataVersionDeneb:
		return 0, errors.New("no builder index in deneb block")
	case spec.DataVersionElectra:
		return 0, errors.New("no builder index in electra block")
	case spec.DataVersionGloas:
		if v.Gloas == nil || v.Gloas.Message == nil || v.Gloas.Message.Body == nil || v.Gloas.Message.Body.SignedExecutionPayloadBid == nil || v.Gloas.Message.Body.SignedExecutionPayloadBid.Message == nil {
			return 0, errors.New("no gloas block")
		}

		return v.Gloas.Message.Body.SignedExecutionPayloadBid.Message.BuilderIndex, nil
	default:
		return 0, errors.New("unknown version")
	}
}

// getStateRandaoMixes returns the RANDAO mixes from a versioned beacon state.
func getStateRandaoMixes(v *spec.VersionedBeaconState) ([]phase0.Root, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		if v.Phase0 == nil || v.Phase0.RANDAOMixes == nil {
			return nil, errors.New("no phase0 block")
		}

		return v.Phase0.RANDAOMixes, nil
	case spec.DataVersionAltair:
		if v.Altair == nil || v.Altair.RANDAOMixes == nil {
			return nil, errors.New("no altair block")
		}

		return v.Altair.RANDAOMixes, nil
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
	case spec.DataVersionFulu:
		if v.Fulu == nil || v.Fulu.RANDAOMixes == nil {
			return nil, errors.New("no fulu block")
		}

		return v.Fulu.RANDAOMixes, nil
	case spec.DataVersionGloas:
		if v.Gloas == nil || v.Gloas.RANDAOMixes == nil {
			return nil, errors.New("no gloas block")
		}

		return v.Gloas.RANDAOMixes, nil
	default:
		return nil, errors.New("unknown version")
	}
}

// getStateDepositIndex returns the deposit index from a versioned beacon state.
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
	case spec.DataVersionFulu:
		return state.Fulu.ETH1DepositIndex
	case spec.DataVersionGloas:
		return state.Gloas.ETH1DepositIndex
	}
	return 0
}

// getStateCurrentSyncCommittee returns the current sync committee from a versioned beacon state.
func getStateCurrentSyncCommittee(v *spec.VersionedBeaconState) ([]phase0.BLSPubKey, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return nil, errors.New("no sync committee in phase0")
	case spec.DataVersionAltair:
		if v.Altair == nil || v.Altair.CurrentSyncCommittee == nil {
			return nil, errors.New("no altair block")
		}

		return v.Altair.CurrentSyncCommittee.Pubkeys, nil
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.CurrentSyncCommittee == nil {
			return nil, errors.New("no bellatrix block")
		}

		return v.Bellatrix.CurrentSyncCommittee.Pubkeys, nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.CurrentSyncCommittee == nil {
			return nil, errors.New("no capella block")
		}

		return v.Capella.CurrentSyncCommittee.Pubkeys, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.CurrentSyncCommittee == nil {
			return nil, errors.New("no deneb block")
		}

		return v.Deneb.CurrentSyncCommittee.Pubkeys, nil
	case spec.DataVersionElectra:
		if v.Electra == nil || v.Electra.CurrentSyncCommittee == nil {
			return nil, errors.New("no electra block")
		}

		return v.Electra.CurrentSyncCommittee.Pubkeys, nil
	case spec.DataVersionFulu:
		if v.Fulu == nil || v.Fulu.CurrentSyncCommittee == nil {
			return nil, errors.New("no fulu block")
		}

		return v.Fulu.CurrentSyncCommittee.Pubkeys, nil
	case spec.DataVersionGloas:
		if v.Gloas == nil || v.Gloas.CurrentSyncCommittee == nil {
			return nil, errors.New("no gloas block")
		}

		return v.Gloas.CurrentSyncCommittee.Pubkeys, nil
	default:
		return nil, errors.New("unknown version")
	}
}

// getStateDepositBalanceToConsume returns the deposit balance to consume from a versioned beacon state.
func getStateDepositBalanceToConsume(v *spec.VersionedBeaconState) (phase0.Gwei, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return 0, errors.New("no pending deposits in phase0")
	case spec.DataVersionAltair:
		return 0, errors.New("no pending deposits in altair")
	case spec.DataVersionBellatrix:
		return 0, errors.New("no pending deposits in bellatrix")
	case spec.DataVersionCapella:
		return 0, errors.New("no pending deposits in capella")
	case spec.DataVersionDeneb:
		return 0, errors.New("no pending deposits in deneb")
	case spec.DataVersionElectra:
		if v.Electra == nil {
			return 0, errors.New("no electra block")
		}

		return v.Electra.DepositBalanceToConsume, nil
	case spec.DataVersionFulu:
		if v.Fulu == nil {
			return 0, errors.New("no fulu block")
		}

		return v.Fulu.DepositBalanceToConsume, nil
	case spec.DataVersionGloas:
		if v.Gloas == nil {
			return 0, errors.New("no gloas block")
		}

		return v.Gloas.DepositBalanceToConsume, nil
	default:
		return 0, errors.New("unknown version")
	}
}

// getStatePendingDeposits returns the pending deposits from a versioned beacon state.
func getStatePendingDeposits(v *spec.VersionedBeaconState) ([]*electra.PendingDeposit, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return nil, errors.New("no pending deposits in phase0")
	case spec.DataVersionAltair:
		return nil, errors.New("no pending deposits in altair")
	case spec.DataVersionBellatrix:
		return nil, errors.New("no pending deposits in bellatrix")
	case spec.DataVersionCapella:
		return nil, errors.New("no pending deposits in capella")
	case spec.DataVersionDeneb:
		return nil, errors.New("no pending deposits in deneb")
	case spec.DataVersionElectra:
		if v.Electra == nil {
			return nil, errors.New("no electra block")
		}

		return v.Electra.PendingDeposits, nil
	case spec.DataVersionFulu:
		if v.Fulu == nil {
			return nil, errors.New("no fulu block")
		}

		return v.Fulu.PendingDeposits, nil
	case spec.DataVersionGloas:
		if v.Gloas == nil {
			return nil, errors.New("no gloas block")
		}

		return v.Gloas.PendingDeposits, nil
	default:
		return nil, errors.New("unknown version")
	}
}

// getStatePendingWithdrawals returns the pending withdrawals from a versioned beacon state.
func getStatePendingWithdrawals(v *spec.VersionedBeaconState) ([]*electra.PendingPartialWithdrawal, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return nil, errors.New("no pending withdrawals in phase0")
	case spec.DataVersionAltair:
		return nil, errors.New("no pending withdrawals in altair")
	case spec.DataVersionBellatrix:
		return nil, errors.New("no pending withdrawals in bellatrix")
	case spec.DataVersionCapella:
		return nil, errors.New("no pending withdrawals in capella")
	case spec.DataVersionDeneb:
		return nil, errors.New("no pending withdrawals in deneb")
	case spec.DataVersionElectra:
		if v.Electra == nil {
			return nil, errors.New("no electra block")
		}

		return v.Electra.PendingPartialWithdrawals, nil
	case spec.DataVersionFulu:
		if v.Fulu == nil {
			return nil, errors.New("no fulu block")
		}

		return v.Fulu.PendingPartialWithdrawals, nil
	case spec.DataVersionGloas:
		if v.Gloas == nil {
			return nil, errors.New("no gloas block")
		}

		return v.Gloas.PendingPartialWithdrawals, nil
	default:
		return nil, errors.New("unknown version")
	}
}

// getStatePendingConsolidations returns the pending consolidations from a versioned beacon state.
func getStatePendingConsolidations(v *spec.VersionedBeaconState) ([]*electra.PendingConsolidation, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return nil, errors.New("no pending consolidations in phase0")
	case spec.DataVersionAltair:
		return nil, errors.New("no pending consolidations in altair")
	case spec.DataVersionBellatrix:
		return nil, errors.New("no pending consolidations in bellatrix")
	case spec.DataVersionCapella:
		return nil, errors.New("no pending consolidations in capella")
	case spec.DataVersionDeneb:
		return nil, errors.New("no pending consolidations in deneb")
	case spec.DataVersionElectra:
		if v.Electra == nil {
			return nil, errors.New("no electra block")
		}

		return v.Electra.PendingConsolidations, nil
	case spec.DataVersionFulu:
		if v.Fulu == nil {
			return nil, errors.New("no fulu block")
		}

		return v.Fulu.PendingConsolidations, nil
	case spec.DataVersionGloas:
		if v.Gloas == nil {
			return nil, errors.New("no gloas block")
		}

		return v.Gloas.PendingConsolidations, nil
	default:
		return nil, errors.New("unknown version")
	}
}

// getStateProposerLookahead returns the proposer lookahead from a versioned beacon state.
func getStateProposerLookahead(v *spec.VersionedBeaconState) ([]phase0.ValidatorIndex, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return nil, errors.New("no proposer lookahead in phase0")
	case spec.DataVersionAltair:
		return nil, errors.New("no proposer lookahead in altair")
	case spec.DataVersionBellatrix:
		return nil, errors.New("no proposer lookahead in bellatrix")
	case spec.DataVersionCapella:
		return nil, errors.New("no proposer lookahead in capella")
	case spec.DataVersionDeneb:
		return nil, errors.New("no proposer lookahead in deneb")
	case spec.DataVersionElectra:
		return nil, errors.New("no proposer lookahead in electra")
	case spec.DataVersionFulu:
		if v.Fulu == nil {
			return nil, errors.New("no fulu block")
		}

		return v.Fulu.ProposerLookahead, nil
	case spec.DataVersionGloas:
		if v.Gloas == nil {
			return nil, errors.New("no gloas block")
		}

		return v.Gloas.ProposerLookahead, nil
	default:
		return nil, errors.New("unknown version")
	}
}

// getStateProposerLookahead returns the proposer lookahead from a versioned beacon state.
func getStateBlockRoots(v *spec.VersionedBeaconState) ([]phase0.Root, error) {
	switch v.Version {

	case spec.DataVersionPhase0:
		if v.Phase0 == nil || v.Phase0.BlockRoots == nil {
			return nil, errors.New("no phase0 block")
		}

		return v.Phase0.BlockRoots, nil
	case spec.DataVersionAltair:
		if v.Altair == nil || v.Altair.BlockRoots == nil {
			return nil, errors.New("no altair block")
		}

		return v.Altair.BlockRoots, nil
	case spec.DataVersionBellatrix:
		if v.Bellatrix == nil || v.Bellatrix.BlockRoots == nil {
			return nil, errors.New("no bellatrix block")
		}

		return v.Bellatrix.BlockRoots, nil
	case spec.DataVersionCapella:
		if v.Capella == nil || v.Capella.BlockRoots == nil {
			return nil, errors.New("no capella block")
		}

		return v.Capella.BlockRoots, nil
	case spec.DataVersionDeneb:
		if v.Deneb == nil || v.Deneb.BlockRoots == nil {
			return nil, errors.New("no deneb block")
		}

		return v.Deneb.BlockRoots, nil
	case spec.DataVersionElectra:
		if v.Electra == nil || v.Electra.BlockRoots == nil {
			return nil, errors.New("no electra block")
		}

		return v.Electra.BlockRoots, nil
	case spec.DataVersionFulu:
		if v.Fulu == nil || v.Fulu.BlockRoots == nil {
			return nil, errors.New("no fulu block")
		}

		return v.Fulu.BlockRoots, nil
	case spec.DataVersionGloas:
		if v.Gloas == nil || v.Gloas.BlockRoots == nil {
			return nil, errors.New("no gloas block")
		}

		return v.Gloas.BlockRoots, nil
	default:
		return nil, errors.New("unknown version")
	}
}

// getBlockSize returns the block size from a versioned beacon block.
func getBlockSize(dynSsz *dynssz.DynSsz, block *spec.VersionedSignedBeaconBlock) (int, error) {
	switch block.Version {
	case spec.DataVersionPhase0:
		return dynSsz.SizeSSZ(block.Phase0)
	case spec.DataVersionAltair:
		return dynSsz.SizeSSZ(block.Altair)
	case spec.DataVersionBellatrix:
		return dynSsz.SizeSSZ(block.Bellatrix)
	case spec.DataVersionCapella:
		return dynSsz.SizeSSZ(block.Capella)
	case spec.DataVersionDeneb:
		return dynSsz.SizeSSZ(block.Deneb)
	case spec.DataVersionElectra:
		return dynSsz.SizeSSZ(block.Electra)
	case spec.DataVersionFulu:
		return dynSsz.SizeSSZ(block.Fulu)
	case spec.DataVersionGloas:
		return dynSsz.SizeSSZ(block.Gloas)
	default:
		return 0, errors.New("unknown version")
	}
}
