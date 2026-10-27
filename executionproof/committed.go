package executionproof

import (
	"encoding/binary"
	"fmt"
)

// Committed is what can be read from a proof's bytes without verifying it: the
// public values the guest revealed (and the public input they decode to), and
// the zkVM-specific metadata around them. A verifier binds these values to the
// proof; reading them tells what the proof claims, not that the claim holds.
type Committed struct {
	// PublicValues are the raw public values the proof carries.
	PublicValues []byte
	// PublicInput is the decoded public input, nil if the public values do not
	// decode to one (DecodeError says why).
	PublicInput *PublicInput
	DecodeError string
	// ProofMode is the proof kind for zkVMs that carry one (SP1: compressed, ...).
	ProofMode string
	// SdkVersion is the zkVM SDK version for zkVMs that carry one (SP1).
	SdkVersion string
	// ProgramVK is the program verification key for zkVMs that carry it in the
	// proof (Zisk).
	ProgramVK []byte
}

// DecodeCommitted reads the committed values from a proof of the given zkVM.
func DecodeCommitted(zkvm ZkVM, proof []byte) (*Committed, error) {
	var committed *Committed
	var err error
	switch {
	case MockProofs:
		committed, err = decodeMock(proof)
	case zkvm == ZkVMSP1:
		committed, err = decodeSP1(proof)
	case zkvm == ZkVMZisk:
		committed, err = decodeZisk(proof)
	case zkvm == ZkVMOpenVM:
		committed, err = decodeOpenVM(proof)
	default:
		return nil, fmt.Errorf("unsupported zkvm: %s", zkvm)
	}
	if err != nil {
		return nil, err
	}

	publicInput, err := PublicInputFromPublicValues(zkvm, committed.PublicValues)
	if err != nil {
		committed.DecodeError = err.Error()
	} else {
		committed.PublicInput = publicInput
	}

	return committed, nil
}

// SP1 proofs are the bincode (legacy config) encoding of
// `ProofFromNetwork { proof: SP1Proof, public_values: Vec<u8>, sp1_version: String }`:
// the proof enum starts with its variant tag as u32, and the two trailing
// fields are each a u64 length followed by their bytes.
var sp1ProofModes = map[uint32]string{0: "core", 1: "compressed", 2: "plonk", 3: "groth16"}

const sp1MaxVersionLen = 64

func decodeSP1(proof []byte) (*Committed, error) {
	if len(proof) < 4+8+8 {
		return nil, fmt.Errorf("sp1 proof too short")
	}

	mode, ok := sp1ProofModes[binary.LittleEndian.Uint32(proof[0:4])]
	if !ok {
		return nil, fmt.Errorf("unknown sp1 proof mode")
	}

	// the version string is the last field; its length prefix identifies it
	for versionLen := 1; versionLen <= sp1MaxVersionLen; versionLen++ {
		versionPos := len(proof) - versionLen
		lenPos := versionPos - 8
		if lenPos < 8 {
			break
		}
		if binary.LittleEndian.Uint64(proof[lenPos:versionPos]) != uint64(versionLen) || !isVersionString(proof[versionPos:]) {
			continue
		}

		// the public values precede the version field, their own length
		// prefix precedes them: the serialized public input, or any other
		// length the prefix accounts for
		for _, valuesLen := range sp1PublicValuesLengths(lenPos) {
			valuesPos := lenPos - valuesLen
			if binary.LittleEndian.Uint64(proof[valuesPos-8:valuesPos]) != uint64(valuesLen) {
				continue
			}
			return &Committed{
				PublicValues: append([]byte(nil), proof[valuesPos:lenPos]...),
				ProofMode:    mode,
				SdkVersion:   string(proof[versionPos:]),
			}, nil
		}
		return nil, fmt.Errorf("sp1 public values not found")
	}

	return nil, fmt.Errorf("sp1 version string not found")
}

// sp1PublicValuesLengths returns the public values lengths to try before the
// given position: the public input size first, then everything the proof could
// hold up to the decoder's bound.
func sp1PublicValuesLengths(lenPos int) []int {
	lengths := make([]int, 0, 1<<12)
	for length := 1; length <= 1<<12 && length+8 <= lenPos; length++ {
		if length != PublicInputSize {
			lengths = append(lengths, length)
		}
	}
	if PublicInputSize+8 <= lenPos {
		lengths = append([]int{PublicInputSize}, lengths...)
	}
	return lengths
}

func isVersionString(s []byte) bool {
	if len(s) < 2 || s[0] != 'v' || s[1] < '0' || s[1] > '9' {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'z') && c != '.' && c != '-' {
			return false
		}
	}
	return true
}

// Zisk proofs are the bincode (legacy config) encoding of
// `VadcopFinalProof { proof: Vec<u64>, public_values: Vec<u64>, compressed: bool, hash: String }`.
// The public values hold the program verification key (4 Goldilocks words) and
// the 64 user public value words, each a u32 of the public values bytes; newer
// zisk versions prepend a final-proof flag word.
const (
	ziskProgramVKWords   = 4
	ziskPublicValueWords = 64
	ziskFinalProofFlag   = 0x44
)

func decodeZisk(proof []byte) (*Committed, error) {
	if len(proof) < 16 {
		return nil, fmt.Errorf("zisk proof too short")
	}

	proofWords := binary.LittleEndian.Uint64(proof[0:8])
	pos := uint64(8) + proofWords*8
	if proofWords > uint64(len(proof))/8 || pos+8 > uint64(len(proof)) {
		return nil, fmt.Errorf("invalid zisk proof length: %d words", proofWords)
	}

	valueWords := binary.LittleEndian.Uint64(proof[pos : pos+8])
	pos += 8
	if valueWords > uint64(len(proof))/8 || pos+valueWords*8 > uint64(len(proof)) {
		return nil, fmt.Errorf("invalid zisk public values length: %d words", valueWords)
	}
	words := make([]uint64, valueWords)
	for i := range words {
		words[i] = binary.LittleEndian.Uint64(proof[pos+uint64(i)*8:])
	}

	// with or without the leading final-proof flag
	switch {
	case len(words) == ziskProgramVKWords+ziskPublicValueWords:
	case len(words) == 1+ziskProgramVKWords+ziskPublicValueWords && words[0] == ziskFinalProofFlag:
		words = words[1:]
	default:
		return nil, fmt.Errorf("unexpected zisk public values layout: %d words", len(words))
	}

	programVK := make([]byte, 0, ziskProgramVKWords*8)
	for _, word := range words[:ziskProgramVKWords] {
		programVK = binary.LittleEndian.AppendUint64(programVK, word)
	}
	publicValues := make([]byte, 0, ziskPublicValueWords*4)
	for _, word := range words[ziskProgramVKWords:] {
		if word > 0xffffffff {
			return nil, fmt.Errorf("invalid zisk public value word")
		}
		publicValues = binary.LittleEndian.AppendUint32(publicValues, uint32(word))
	}

	return &Committed{PublicValues: publicValues, ProgramVK: programVK}, nil
}

// OpenVM proofs are the stark-backend codec encoding of `VmStarkProof`; the
// trailing `UserPublicValuesProof` ends with the public values (u32 count, then
// one u32 field element per value, each holding two bytes), the 8-element
// public values commitment and a byte telling whether deferral proofs follow.
const (
	openVMDigestWords      = 8
	openVMPublicValueCount = 128
)

func decodeOpenVM(proof []byte) (*Committed, error) {
	trailer := 1 + openVMDigestWords*4
	valuesLen := 4 + openVMPublicValueCount*4
	if len(proof) < trailer+valuesLen {
		return nil, fmt.Errorf("openvm proof too short")
	}
	if proof[len(proof)-1] != 0 {
		return nil, fmt.Errorf("openvm proof with deferral proofs is not supported")
	}

	pos := len(proof) - trailer - valuesLen
	if binary.LittleEndian.Uint32(proof[pos:pos+4]) != openVMPublicValueCount {
		return nil, fmt.Errorf("unexpected openvm public value count")
	}
	pos += 4

	publicValues := make([]byte, 0, openVMPublicValueCount*2)
	for range openVMPublicValueCount {
		element := binary.LittleEndian.Uint32(proof[pos : pos+4])
		if element > 0xffff {
			return nil, fmt.Errorf("invalid openvm public value element")
		}
		publicValues = binary.LittleEndian.AppendUint16(publicValues, uint16(element))
		pos += 4
	}

	return &Committed{PublicValues: publicValues}, nil
}
