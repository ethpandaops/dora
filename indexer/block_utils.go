package indexer

import (
	"errors"

	"github.com/attestantio/go-eth2-client/spec"
	"github.com/attestantio/go-eth2-client/spec/electra"
)

func GetExecutionExtraData(v *spec.VersionedSignedBeaconBlock) ([]byte, error) {
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

// Consolidations returns the consolidations of the beacon block.
func GetBlockConsolidations(v *spec.VersionedSignedBeaconBlock) ([]*electra.SignedConsolidation, error) {
	switch v.Version {
	case spec.DataVersionPhase0:
		return nil, errors.New("consolidations not available in phase0 block")
	case spec.DataVersionAltair:
		return nil, errors.New("consolidations not available in altair block")
	case spec.DataVersionBellatrix:
		return nil, errors.New("consolidations not available in bellatrix block")
	case spec.DataVersionCapella:
		return nil, errors.New("consolidations not available in capella block")
	case spec.DataVersionDeneb:
		return nil, errors.New("consolidations not available in deneb block")
	case spec.DataVersionElectra:
		if v.Electra == nil || v.Electra.Message == nil || v.Electra.Message.Body == nil {
			return nil, errors.New("no electra block")
		}

		return v.Electra.Message.Body.Consolidations, nil
	default:
		return nil, errors.New("unknown version")
	}
}
