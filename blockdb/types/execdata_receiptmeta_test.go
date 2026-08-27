package types

import (
	"testing"
)

func sampleReceiptMeta() *ReceiptMetaData {
	meta := &ReceiptMetaData{
		Version:           ReceiptMetaVersion1,
		Status:            1,
		TxType:            6,
		CumulativeGasUsed: 0x4e86,
		GasUsed:           0x4e86,
	}
	meta.EffectiveGasPrice.SetUint64(0x773642f2)
	copy(meta.From[:], []byte{0x7a, 0x11})
	copy(meta.To[:], []byte{0x30, 0x59})

	return meta
}

// A receipt with no frame content encodes exactly as it always did, so objects written
// before frame transactions existed and objects written after are the same shape.
func TestReceiptMetaSectionWithoutFramesIsUnchanged(t *testing.T) {
	meta := sampleReceiptMeta()

	encoded, err := EncodeReceiptMetaSection(meta, nil)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	if len(encoded) != receiptMetaSize {
		t.Fatalf("encoded %d bytes, want the fixed %d", len(encoded), receiptMetaSize)
	}

	decoded, frames, err := DecodeReceiptMetaSection(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if frames != nil {
		t.Error("a receipt with no frame content must decode without any")
	}

	if decoded.Version != ReceiptMetaVersion1 {
		t.Errorf("version = %d, want %d", decoded.Version, ReceiptMetaVersion1)
	}

	if decoded.GasUsed != meta.GasUsed || decoded.Status != meta.Status {
		t.Error("receipt metadata did not survive the round trip")
	}
}

func TestReceiptMetaSectionRoundTripsFrames(t *testing.T) {
	meta := sampleReceiptMeta()

	frames := &FrameReceiptData{
		Frames: []FrameReceiptEntry{
			{Status: 1, ExecGasUsed: 0x33, StateGasUsed: 0, LogCount: 0},
			{Status: 2, ExecGasUsed: 0, StateGasUsed: 0, LogCount: 0},
			{Status: 1, ExecGasUsed: 21000, StateGasUsed: 5, LogCount: 3},
		},
	}
	copy(frames.Payer[:], []byte{0x6d, 0xf3, 0x54, 0x38})

	encoded, err := EncodeReceiptMetaSection(meta, frames)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	if len(encoded) <= receiptMetaSize {
		t.Fatalf("encoded %d bytes, want more than the fixed %d", len(encoded), receiptMetaSize)
	}

	// The version has to say the tail is there, or a reader will not look for it.
	if meta.Version != ReceiptMetaVersion2 {
		t.Errorf("version = %d, want %d", meta.Version, ReceiptMetaVersion2)
	}

	decodedMeta, decodedFrames, err := DecodeReceiptMetaSection(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if decodedMeta.GasUsed != meta.GasUsed {
		t.Error("receipt metadata did not survive alongside the frames")
	}

	if decodedFrames == nil {
		t.Fatal("frame content was lost")
	}

	if decodedFrames.Payer != frames.Payer {
		t.Errorf("payer = %x, want %x", decodedFrames.Payer, frames.Payer)
	}

	if len(decodedFrames.Frames) != len(frames.Frames) {
		t.Fatalf("frames = %d, want %d", len(decodedFrames.Frames), len(frames.Frames))
	}

	for i := range frames.Frames {
		if decodedFrames.Frames[i] != frames.Frames[i] {
			t.Errorf("frame %d = %+v, want %+v", i, decodedFrames.Frames[i], frames.Frames[i])
		}
	}
}

// A section written before frame transactions existed carries version 1 and no tail. It
// must keep decoding, which is the whole reason the frames go behind a version rather
// than into a new section of the object.
func TestReceiptMetaSectionDecodesLegacySections(t *testing.T) {
	meta := sampleReceiptMeta()
	meta.Version = ReceiptMetaVersion1

	legacy, err := meta.MarshalSSZ()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	decoded, frames, err := DecodeReceiptMetaSection(legacy)
	if err != nil {
		t.Fatalf("decode of a legacy section failed: %v", err)
	}

	if frames != nil {
		t.Error("a legacy section has no frame content")
	}

	if decoded.CumulativeGasUsed != meta.CumulativeGasUsed {
		t.Error("legacy receipt metadata did not decode")
	}
}

func TestReceiptMetaSectionRejectsTruncatedInput(t *testing.T) {
	if _, _, err := DecodeReceiptMetaSection(make([]byte, receiptMetaSize-1)); err == nil {
		t.Error("a section shorter than the fixed metadata must not decode")
	}
}
