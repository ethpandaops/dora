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

func GetValidatorChurnLimit(validatorCount uint64) uint64 {
	min := Config.Chain.Config.MinPerEpochChurnLimit
	adaptable := uint64(0)
	if validatorCount > 0 {
		adaptable = validatorCount / Config.Chain.Config.ChurnLimitQuotient
	}
	if min > adaptable {
		return min
	}
	return adaptable
}
