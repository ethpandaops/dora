package utils

import (
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

// FindMatchingIndices returns indices that appear in both slices
// assumes both slices contain uint64 values
func FindMatchingIndices(a, b []uint64) []uint64 {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}

	// Use map for O(1) lookups
	lookup := make(map[uint64]struct{}, len(a))
	for _, v := range a {
		lookup[v] = struct{}{}
	}

	// Find matches
	result := make([]uint64, 0, min(len(a), len(b)))
	for _, v := range b {
		if _, ok := lookup[v]; ok {
			result = append(result, v)
		}
	}

	return result
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

func SyncCommitteeParticipation(bits []byte, syncCommitteeSize uint64) float64 {
	participating := 0
	for i := 0; i < int(syncCommitteeSize); i++ {
		if BitAtVector(bits, i) {
			participating++
		}
	}
	return float64(participating) / float64(syncCommitteeSize)
}
