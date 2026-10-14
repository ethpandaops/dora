// Package types: execution proof sections of the per-slot meta object.
//
// The object of slot S holds the EIP-8025 execution proofs published for the
// blocks of slot S (keyed by block root inside the object, so proofs of orphaned
// blocks are kept as well). A proof is identified by (block root, proof type);
// there is at most one proof per type and block.
//
// The proofs are stored in two sections of the meta object:
//
// EXECUTION PROOFS section: the proof index, small enough for the prefix read:
// uint16 count, per proof: uint16 record length + record:
//
//	├── BlockRoot:      32 bytes
//	├── ProofType:      uint8
//	├── Flags:          uint8 (ExecutionProofFlag* constants)
//	├── DataOffset:     uint32 (offset of the proof bytes in the data section)
//	├── DataSize:       uint32
//	├── ValidatorIndex: uint64 (zero unless signed)
//	└── Signature:      96 bytes (zero unless signed)
//
// EXECUTION PROOF DATA section (snappy compressed): the proof bytes of all
// proofs of the slot, concatenated in index order. Proofs can be large (up to
// MAX_PROOF_SIZE = 4 MiB each), so the section is stored last and only loaded
// when SlotMetaFlagExecutionProofData is selected.
package types

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/golang/snappy"
)

const (
	// ExecutionProofFlagSignatureValid and ExecutionProofFlagProverActive hold the
	// outcome of the prover signature check done against the validator set of the
	// proof's block when the proof was collected.
	ExecutionProofFlagSignatureValid uint8 = 1
	ExecutionProofFlagProverActive   uint8 = 2

	// executionProofRecordSize is the size of a proof index record.
	executionProofRecordSize = 32 + 1 + 1 + 4 + 4 + 8 + 96
)

// SlotExecutionProof is one execution proof of a block in the slot.
type SlotExecutionProof struct {
	BlockRoot      [32]byte
	ProofType      uint8
	ValidatorIndex uint64
	Signature      [96]byte
	// SignatureValid and ProverActive are the outcome of the prover signature
	// check done when the proof was collected.
	SignatureValid bool
	ProverActive   bool

	// DataSize is the proof size in bytes. It is known from the index even when
	// the proof bytes were not loaded.
	DataSize uint32
	// ProofData holds the proof bytes, nil when the data section was not loaded.
	ProofData []byte
}

// Key returns the string key identifying this proof across objects.
func (p *SlotExecutionProof) Key() string {
	return string(p.BlockRoot[:]) + string([]byte{p.ProofType})
}

// encodeExecutionProofSections encodes the proof index and data sections.
func encodeExecutionProofSections(proofs []*SlotExecutionProof) (*SlotMetaSection, *SlotMetaSection, error) {
	if len(proofs) > 0xffff {
		return nil, nil, fmt.Errorf("too many execution proofs: %d", len(proofs))
	}

	var indexBuf, dataBuf bytes.Buffer
	_ = binary.Write(&indexBuf, binary.BigEndian, uint16(len(proofs)))

	for _, proof := range proofs {
		if proof.ProofData == nil {
			return nil, nil, fmt.Errorf("execution proof data not loaded: block 0x%x type %d", proof.BlockRoot, proof.ProofType)
		}
		if dataBuf.Len()+len(proof.ProofData) > 0xffffffff {
			return nil, nil, fmt.Errorf("execution proof data too large")
		}

		var flags uint8
		if proof.SignatureValid {
			flags |= ExecutionProofFlagSignatureValid
		}
		if proof.ProverActive {
			flags |= ExecutionProofFlagProverActive
		}

		_ = binary.Write(&indexBuf, binary.BigEndian, uint16(executionProofRecordSize))
		indexBuf.Write(proof.BlockRoot[:])
		indexBuf.WriteByte(proof.ProofType)
		indexBuf.WriteByte(flags)
		_ = binary.Write(&indexBuf, binary.BigEndian, uint32(dataBuf.Len()))
		_ = binary.Write(&indexBuf, binary.BigEndian, uint32(len(proof.ProofData)))
		_ = binary.Write(&indexBuf, binary.BigEndian, proof.ValidatorIndex)
		indexBuf.Write(proof.Signature[:])

		dataBuf.Write(proof.ProofData)
	}

	indexSection := &SlotMetaSection{Type: MetaSectionExecutionProofs, Data: indexBuf.Bytes()}
	dataSection := &SlotMetaSection{
		Type:  MetaSectionExecutionProofData,
		Flags: MetaSectionFlagSnappy,
		Data:  snappy.Encode(nil, dataBuf.Bytes()),
	}

	return indexSection, dataSection, nil
}

// decodeExecutionProofSections decodes the proof index and, when present, slices
// the proof bytes out of the data section.
func decodeExecutionProofSections(indexSection *SlotMetaSection, dataSection *SlotMetaSection) ([]*SlotExecutionProof, error) {
	index, err := indexSection.payload()
	if err != nil {
		return nil, err
	}
	if len(index) < 2 {
		return nil, fmt.Errorf("truncated execution proofs section")
	}

	var data []byte
	if dataSection != nil {
		if data, err = dataSection.payload(); err != nil {
			return nil, err
		}
	}

	count := int(binary.BigEndian.Uint16(index[0:2]))
	pos := 2
	proofs := make([]*SlotExecutionProof, 0, count)
	for range count {
		if pos+2 > len(index) {
			return nil, fmt.Errorf("truncated execution proof record length")
		}
		recordLen := int(binary.BigEndian.Uint16(index[pos : pos+2]))
		pos += 2
		if recordLen < executionProofRecordSize || pos+recordLen > len(index) {
			return nil, fmt.Errorf("truncated execution proof record")
		}
		record := index[pos : pos+recordLen]
		pos += recordLen

		proof := &SlotExecutionProof{
			ProofType:      record[32],
			SignatureValid: record[33]&ExecutionProofFlagSignatureValid != 0,
			ProverActive:   record[33]&ExecutionProofFlagProverActive != 0,
			DataSize:       binary.BigEndian.Uint32(record[38:42]),
			ValidatorIndex: binary.BigEndian.Uint64(record[42:50]),
		}
		copy(proof.BlockRoot[:], record[0:32])
		copy(proof.Signature[:], record[50:146])

		if dataSection != nil {
			offset := int(binary.BigEndian.Uint32(record[34:38]))
			size := int(proof.DataSize)
			if offset+size > len(data) {
				return nil, fmt.Errorf("execution proof data out of range: %d+%d > %d", offset, size, len(data))
			}
			proof.ProofData = bytes.Clone(data[offset : offset+size])
		}

		proofs = append(proofs, proof)
	}

	return proofs, nil
}

// mergeSlotExecutionProofs unifies the proofs of two objects, keyed by block root
// and proof type. For proofs present in both the second (newer) wins.
func mergeSlotExecutionProofs(a, b []*SlotExecutionProof) []*SlotExecutionProof {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}

	merged := make([]*SlotExecutionProof, 0, len(a)+len(b))
	index := make(map[string]int, len(a)+len(b))
	for _, src := range [][]*SlotExecutionProof{a, b} {
		for _, proof := range src {
			key := proof.Key()
			if idx, ok := index[key]; ok {
				merged[idx] = proof
				continue
			}
			index[key] = len(merged)
			merged = append(merged, proof)
		}
	}

	return merged
}
