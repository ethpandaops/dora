package types

import (
	"fmt"
)

// receiptMetaSize is the encoded size of ReceiptMetaData, which is fixed. A version 2
// section is exactly this many bytes followed by an encoded FrameReceiptData.
var receiptMetaSize = (&ReceiptMetaData{}).SizeSSZ()

// EncodeReceiptMetaSection encodes the receipt metadata section.
//
// Frame content is appended after the fixed metadata rather than stored in a section of
// its own: the per-transaction index entry has a fixed width, so a new section pointer
// would change the object format and make every object already written unreadable. The
// version field on the metadata says whether the tail is there.
func EncodeReceiptMetaSection(meta *ReceiptMetaData, frames *FrameReceiptData) ([]byte, error) {
	if frames == nil {
		meta.Version = ReceiptMetaVersion1
	} else {
		meta.Version = ReceiptMetaVersion2
	}

	encoded, err := meta.MarshalSSZ()
	if err != nil {
		return nil, fmt.Errorf("marshal receipt metadata: %w", err)
	}

	if frames == nil {
		return encoded, nil
	}

	frameData, err := frames.MarshalSSZ()
	if err != nil {
		return nil, fmt.Errorf("marshal frame receipt data: %w", err)
	}

	return append(encoded, frameData...), nil
}

// DecodeReceiptMetaSection decodes a receipt metadata section, returning the frame
// content when the section carries any. Sections written before frame transactions
// existed decode with a nil frame result.
func DecodeReceiptMetaSection(raw []byte) (*ReceiptMetaData, *FrameReceiptData, error) {
	if len(raw) < receiptMetaSize {
		return nil, nil, fmt.Errorf("receipt metadata truncated: need %d bytes, got %d", receiptMetaSize, len(raw))
	}

	meta := &ReceiptMetaData{}
	if err := meta.UnmarshalSSZ(raw[:receiptMetaSize]); err != nil {
		return nil, nil, fmt.Errorf("unmarshal receipt metadata: %w", err)
	}

	tail := raw[receiptMetaSize:]
	if meta.Version < ReceiptMetaVersion2 || len(tail) == 0 {
		return meta, nil, nil
	}

	frames := &FrameReceiptData{}
	if err := frames.UnmarshalSSZ(tail); err != nil {
		return nil, nil, fmt.Errorf("unmarshal frame receipt data: %w", err)
	}

	return meta, frames, nil
}
