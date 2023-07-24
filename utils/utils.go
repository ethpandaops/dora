package utils

import (
	"encoding/binary"
	"encoding/hex"
	"strings"

	log "github.com/sirupsen/logrus"
)

// sliceContains reports whether the provided string is present in the given slice of strings.
func SliceContains(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

// MustParseHex will parse a string into hex
func MustParseHex(hexString string) []byte {
	data, err := hex.DecodeString(strings.Replace(hexString, "0x", "", -1))
	if err != nil {
		log.Fatal(err)
	}
	return data
}

func BitAtVector(b []byte, i int) bool {
	bb := b[i/8]
	return (bb & (1 << uint(i%8))) > 0
}

func BitAtVectorReversed(b []byte, i int) bool {
	bb := b[i/8]
	return (bb & (1 << uint(7-(i%8)))) > 0
}

func SyncCommitteeParticipation(bits []byte) float64 {
	participating := 0
	for i := 0; i < int(Config.Chain.Config.SyncCommitteeSize); i++ {
		if BitAtVector(bits, i) {
			participating++
		}
	}
	return float64(participating) / float64(Config.Chain.Config.SyncCommitteeSize)
}

func MissingDutiesToBytes(missingDuties map[uint8]uint64) []byte {
	bytes := make([]byte, 0)
	for slotIdx := uint8(0); slotIdx < uint8(Config.Chain.Config.SlotsPerEpoch); slotIdx++ {
		validator := missingDuties[slotIdx]
		if validator > 0 {
			bytes = append(bytes, slotIdx)
			bytes = binary.LittleEndian.AppendUint64(bytes, validator)
		}
	}
	return bytes
}

func BytesToMissingDuties(bytes []byte) map[uint8]uint64 {
	missingDuties := make(map[uint8]uint64)
	bIdx := 0
	bLen := len(bytes)
	for bIdx < bLen-8 {
		slotIdx := uint8(bytes[bIdx])
		if slotIdx == 0xff {
			break
		}
		missingDuties[slotIdx] = binary.LittleEndian.Uint64(bytes[(bIdx + 1):(bIdx + 9)])
		bIdx += 9
	}
	return missingDuties
}
