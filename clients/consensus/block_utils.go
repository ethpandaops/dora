package consensus

import (
	"errors"

	"github.com/attestantio/go-eth2-client/spec"
)

func GetExecutionExtraData(v *spec.VersionedSignedBeaconBlock) ([]byte, error) {
	//nolint:exhaustive // ignore
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

func GetBlockBody(v *spec.VersionedSignedBeaconBlock) any {
	//nolint:exhaustive // ignore
	switch v.Version {
	case spec.DataVersionPhase0:
		return v.Phase0
	case spec.DataVersionAltair:
		return v.Altair
	case spec.DataVersionBellatrix:
		return v.Bellatrix
	case spec.DataVersionCapella:
		return v.Capella
	case spec.DataVersionDeneb:
		return v.Deneb
	case spec.DataVersionElectra:
		return v.Electra
	default:
		return nil
	}
}
