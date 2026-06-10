package beacon

import (
	"errors"
	"fmt"

	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	dynssz "github.com/pk910/dynamic-ssz"
)

var jsonVersionFlag uint64 = 0x40000000
var compressionFlag uint64 = 0x20000000

// MarshalSignedBeaconBlockSSZ marshals a fork-agnostic signed beacon block
// to SSZ (or JSON when SSZ encoding is disabled at runtime). The returned
// version word stores the fork in the lower bits, with optional
// compression and JSON-format flags OR-ed in.
func MarshalSignedBeaconBlockSSZ(dynSsz *dynssz.DynSsz, block *all.SignedBeaconBlock, compress, forceSSZ bool) (uint64, []byte, error) {
	if block == nil {
		return 0, nil, errors.New("nil signed beacon block")
	}

	versionWord := uint64(block.Version)

	var (
		ssz []byte
		err error
	)

	if utils.Config.KillSwitch.DisableSSZEncoding && !forceSSZ {
		ssz, err = block.MarshalJSON()
		versionWord |= jsonVersionFlag
	} else {
		ssz, err = dynSsz.MarshalSSZ(block)
	}

	if err != nil {
		return 0, nil, err
	}

	if compress {
		ssz = compressBytes(ssz)
		versionWord |= compressionFlag
	}

	return versionWord, ssz, nil
}

// UnmarshalSignedBeaconBlockSSZ inverts MarshalSignedBeaconBlockSSZ.
func UnmarshalSignedBeaconBlockSSZ(dynSsz *dynssz.DynSsz, versionWord uint64, ssz []byte) (*all.SignedBeaconBlock, error) {
	if versionWord&compressionFlag != 0 {
		decompressed, err := decompressBytes(ssz)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress: %v", err)
		}
		ssz = decompressed
		versionWord &= ^compressionFlag
	}

	isJSON := versionWord&jsonVersionFlag != 0
	if isJSON {
		versionWord &= ^jsonVersionFlag
	}

	block := &all.SignedBeaconBlock{Version: spec.DataVersion(versionWord)}

	var err error
	if isJSON {
		err = block.UnmarshalJSON(ssz)
	} else {
		err = dynSsz.UnmarshalSSZ(block, ssz)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to decode signed beacon block (version %d): %v", block.Version, err)
	}

	return block, nil
}

// MarshalVersionedSignedExecutionPayloadEnvelopeSSZ marshals a signed execution payload envelope using SSZ encoding.
func MarshalVersionedSignedExecutionPayloadEnvelopeSSZ(dynSsz *dynssz.DynSsz, payload *all.SignedExecutionPayloadEnvelope, compress bool) (version uint64, ssz []byte, err error) {
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
func UnmarshalVersionedSignedExecutionPayloadEnvelopeSSZ(dynSsz *dynssz.DynSsz, version uint64, ssz []byte) (*all.SignedExecutionPayloadEnvelope, error) {
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

	// SSZ encoding
	payload := &all.SignedExecutionPayloadEnvelope{Version: spec.DataVersion(version)}
	if err := dynSsz.UnmarshalSSZ(payload, ssz); err != nil {
		return nil, fmt.Errorf("failed to decode signed execution payload envelope: %v", err)
	}

	return payload, nil
}

// marshalVersionedSignedExecutionPayloadEnvelopeJson marshals a versioned signed execution payload envelope using JSON encoding.
func marshalVersionedSignedExecutionPayloadEnvelopeJson(payload *all.SignedExecutionPayloadEnvelope) (version uint64, jsonRes []byte, err error) {
	version = uint64(payload.Version)
	jsonRes, err = payload.MarshalJSON()

	version |= jsonVersionFlag

	return
}

// unmarshalVersionedSignedExecutionPayloadEnvelopeJson unmarshals a versioned signed execution payload envelope using JSON encoding.
func unmarshalVersionedSignedExecutionPayloadEnvelopeJson(version uint64, ssz []byte) (*all.SignedExecutionPayloadEnvelope, error) {
	if version&jsonVersionFlag == 0 {
		return nil, fmt.Errorf("no json encoding")
	}

	payload := &all.SignedExecutionPayloadEnvelope{Version: spec.DataVersion(version - jsonVersionFlag)}
	if err := payload.UnmarshalJSON(ssz); err != nil {
		return nil, fmt.Errorf("failed to decode gloas signed execution payload envelope: %v", err)
	}
	return payload, nil
}

// blockAccessListFormatV1 is the version marker for a BAL stored as the raw
// EIP-7928 RLP bytes (optionally compressed via the shared compressionFlag).
// The BAL is persisted independently of the payload so that a re-delivered
// payload with a pruned (empty) BAL cannot overwrite a previously stored one —
// the blockdb write guard checks `BalVersion != 0 && len(BalData) > 0`.
const blockAccessListFormatV1 = uint64(1)

// MarshalBlockAccessList wraps raw EIP-7928 RLP BAL bytes for blockdb storage.
// Returns (0, nil, nil) for empty input so callers can hand the result straight
// to BlockData{BalVersion, BalData} and have the "BAL absent" case map to flags
// not setting BlockDataFlagBal.
func MarshalBlockAccessList(bal []byte, compress bool) (version uint64, data []byte, err error) {
	if len(bal) == 0 {
		return 0, nil, nil
	}

	version = blockAccessListFormatV1
	data = bal
	if compress {
		data = compressBytes(data)
		version |= compressionFlag
	}
	return
}

// UnmarshalBlockAccessList decodes a BAL byte slice previously produced by
// MarshalBlockAccessList, returning the raw RLP bytes.
func UnmarshalBlockAccessList(version uint64, data []byte) ([]byte, error) {
	if (version & compressionFlag) != 0 {
		decompressed, err := decompressBytes(data)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress BAL: %v", err)
		}
		data = decompressed
		version &= ^compressionFlag
	}

	if version != blockAccessListFormatV1 {
		return nil, fmt.Errorf("unknown BAL version: %d", version)
	}

	return data, nil
}

// getBlockExecutionExtraData returns the extra data from the in-block
// execution payload (pre-EIP-7732). Returns (nil, nil) when the block has
// no in-block execution payload (Gloas+ moved this into a separate envelope).
func getBlockExecutionExtraData(b *all.SignedBeaconBlock) ([]byte, error) {
	if b == nil || b.Message == nil || b.Message.Body == nil {
		return nil, errors.New("nil block body")
	}

	switch b.Version {
	case spec.DataVersionPhase0, spec.DataVersionAltair:
		return nil, errors.New("no execution payload in pre-bellatrix block")
	case spec.DataVersionGloas, spec.DataVersionHeze:
		// Execution payload is delivered in a separate envelope.
		return nil, nil
	}

	if b.Message.Body.ExecutionPayload == nil {
		return nil, errors.New("no execution payload")
	}

	return b.Message.Body.ExecutionPayload.ExtraData, nil
}

// getBlockPayloadBuilderIndex returns the builder index from the in-block
// execution payload bid. Only Gloas+ blocks carry one.
func getBlockPayloadBuilderIndex(b *all.SignedBeaconBlock) (gloas.BuilderIndex, error) {
	if b == nil || b.Message == nil || b.Message.Body == nil {
		return 0, errors.New("nil block body")
	}

	if b.Version < spec.DataVersionGloas {
		return 0, errors.New("no builder index in pre-gloas block")
	}

	bid := b.Message.Body.SignedExecutionPayloadBid
	if bid == nil || bid.Message == nil {
		return 0, errors.New("no payload bid")
	}

	return bid.Message.BuilderIndex, nil
}

// getBlockPayloadBidValue returns the bid value from the in-block execution
// payload bid. Only Gloas+ blocks carry one; self-builds always bid 0.
func getBlockPayloadBidValue(b *all.SignedBeaconBlock) (phase0.Gwei, error) {
	if b == nil || b.Message == nil || b.Message.Body == nil {
		return 0, errors.New("nil block body")
	}

	if b.Version < spec.DataVersionGloas {
		return 0, errors.New("no payload bid in pre-gloas block")
	}

	bid := b.Message.Body.SignedExecutionPayloadBid
	if bid == nil || bid.Message == nil {
		return 0, errors.New("no payload bid")
	}

	return bid.Message.Value, nil
}

// getBlockExecutionParentHash returns the parent block hash for the
// execution payload referenced by this block. For Bellatrix..Electra blocks
// the payload is in-block; for Gloas+ it is referenced via the payload bid.
func getBlockExecutionParentHash(b *all.SignedBeaconBlock) (phase0.Hash32, error) {
	if b == nil || b.Message == nil || b.Message.Body == nil {
		return phase0.Hash32{}, errors.New("nil block body")
	}

	switch {
	case b.Version < spec.DataVersionBellatrix:
		return phase0.Hash32{}, errors.New("no execution parent hash in pre-bellatrix block")
	case b.Version >= spec.DataVersionGloas:
		bid := b.Message.Body.SignedExecutionPayloadBid
		if bid == nil || bid.Message == nil {
			return phase0.Hash32{}, errors.New("no payload bid")
		}

		return bid.Message.ParentBlockHash, nil
	default:
		if b.Message.Body.ExecutionPayload == nil {
			return phase0.Hash32{}, errors.New("no execution payload")
		}

		return b.Message.Body.ExecutionPayload.ParentHash, nil
	}
}

// getBlockExecutionBlockHash returns the committed execution block hash.
// For Gloas+ it is sourced from the bid (always present in the body),
// not from the separately gossiped envelope.
func getBlockExecutionBlockHash(b *all.SignedBeaconBlock) (phase0.Hash32, error) {
	if b == nil || b.Message == nil || b.Message.Body == nil {
		return phase0.Hash32{}, errors.New("nil block body")
	}

	switch {
	case b.Version < spec.DataVersionBellatrix:
		return phase0.Hash32{}, errors.New("no execution block hash in pre-bellatrix block")
	case b.Version >= spec.DataVersionGloas:
		bid := b.Message.Body.SignedExecutionPayloadBid
		if bid == nil || bid.Message == nil {
			return phase0.Hash32{}, errors.New("no payload bid")
		}

		return bid.Message.BlockHash, nil
	default:
		if b.Message.Body.ExecutionPayload == nil {
			return phase0.Hash32{}, errors.New("no execution payload")
		}

		return b.Message.Body.ExecutionPayload.BlockHash, nil
	}
}

// getBlockSize returns the SSZ-encoded byte size of a fork-agnostic signed
// beacon block.
func getBlockSize(dynSsz *dynssz.DynSsz, block *all.SignedBeaconBlock) (int, error) {
	if block == nil {
		return 0, errors.New("nil block")
	}

	return dynSsz.SizeSSZ(block)
}
