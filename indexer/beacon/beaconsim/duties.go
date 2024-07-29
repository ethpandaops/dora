package beaconsim

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/ethpandaops/dora/clients/consensus"
)

const seedSize = int8(32)
const roundSize = int8(1)
const positionWindowSize = int8(4)
const pivotViewSize = seedSize + roundSize
const totalSize = seedSize + roundSize + positionWindowSize

var maxShuffleListSize uint64 = 1 << 40

type BeaconState interface {
	GetRandaoMixes() []phase0.Root
	GetEffectiveBalance(index phase0.ValidatorIndex) phase0.Gwei
}

func Hash(data []byte) phase0.Hash32 {
	return sha256.Sum256(data)
}

func UintToBytes(data any) []byte {
	var res []byte

	if d64, ok := data.(uint64); ok {
		res = make([]byte, 8)
		binary.LittleEndian.PutUint64(res, d64)
	} else if d32, ok := data.(uint32); ok {
		res = make([]byte, 4)
		binary.LittleEndian.PutUint32(res, d32)
	} else if d16, ok := data.(uint16); ok {
		res = make([]byte, 2)
		binary.LittleEndian.PutUint16(res, d16)
	} else if d8, ok := data.(uint8); ok {
		res = []byte{d8}
	}

	return res
}

func BytesToUint(data []byte) uint64 {
	return binary.LittleEndian.Uint64(data)
}

func SplitOffset(listSize, chunks, index uint64) uint64 {
	return (listSize * index) / chunks
}

func GetRandaoMix(spec *consensus.ChainSpec, state BeaconState, epoch phase0.Epoch) phase0.Hash32 {
	randaoMixes := state.GetRandaoMixes()
	index := int(epoch % phase0.Epoch(spec.EpochsPerHistoricalVector))
	return phase0.Hash32(randaoMixes[index])
}

func GetSeed(spec *consensus.ChainSpec, state BeaconState, epoch phase0.Epoch, domainType phase0.DomainType) phase0.Hash32 {
	mix := GetRandaoMix(spec, state, epoch+phase0.Epoch(spec.EpochsPerHistoricalVector-spec.MinSeedLookahead-1))

	data := []byte{}
	data = append(data, domainType[:]...)
	data = append(data, UintToBytes(uint64(epoch))...)
	data = append(data, mix[:]...)

	return Hash(data)
}

func ComputeShuffledIndex(spec *consensus.ChainSpec, index uint64, indexCount uint64, seed [32]byte, shuffle bool) (uint64, error) {
	if spec.ShuffleRoundCount == 0 {
		return index, nil
	}
	if uint64(index) >= indexCount {
		return 0, fmt.Errorf("input index %d out of bounds: %d",
			index, indexCount)
	}
	if indexCount > maxShuffleListSize {
		return 0, fmt.Errorf("list size %d out of bounds",
			indexCount)
	}
	rounds := uint8(spec.ShuffleRoundCount)
	round := uint8(0)
	if !shuffle {
		// Starting last round and iterating through the rounds in reverse, un-swaps everything,
		// effectively un-shuffling the list.
		round = rounds - 1
	}
	buf := make([]byte, totalSize)
	posBuffer := make([]byte, 8)

	// Seed is always the first 32 bytes of the hash input, we never have to change this part of the buffer.
	copy(buf[:32], seed[:])
	for {
		buf[seedSize] = round
		h := Hash(buf[:pivotViewSize])
		hash8 := h[:8]
		hash8Int := BytesToUint(hash8)
		pivot := hash8Int % indexCount
		flip := (pivot + indexCount - uint64(index)) % indexCount
		// Consider every pair only once by picking the highest pair index to retrieve randomness.
		position := uint64(index)
		if flip > position {
			position = flip
		}
		// Add position except its last byte to []buf for randomness,
		// it will be used later to select a bit from the resulting hash.
		binary.LittleEndian.PutUint64(posBuffer[:8], position>>8)
		copy(buf[pivotViewSize:], posBuffer[:4])
		source := Hash(buf)
		// Effectively keep the first 5 bits of the byte value of the position,
		// and use it to retrieve one of the 32 (= 2^5) bytes of the hash.
		byteV := source[(position&0xff)>>3]
		// Using the last 3 bits of the position-byte, determine which bit to get from the hash-byte (note: 8 bits = 2^3)
		bitV := (byteV >> (position & 0x7)) & 0x1
		// index = flip if bit else index
		if bitV == 1 {
			index = flip
		}
		if shuffle {
			round++
			if round == rounds {
				break
			}
		} else {
			if round == 0 {
				break
			}
			round--
		}
	}
	return index, nil
}

func GetProposerIndex(spec *consensus.ChainSpec, state BeaconState, activeIndices []phase0.ValidatorIndex, slot phase0.Slot) (phase0.ValidatorIndex, error) {
	epoch := phase0.Epoch(slot / phase0.Slot(spec.SlotsPerEpoch))

	seedData := []byte{}
	seedHash := GetSeed(spec, state, epoch, spec.DomainBeaconProposer)
	seedData = append(seedData, seedHash[:]...)
	seedData = append(seedData, UintToBytes(uint64(slot))...)

	seed := Hash(seedData)

	maxEffectiveBalance := spec.MaxEffectiveBalance
	if phase0.Epoch(slot/phase0.Slot(spec.SlotsPerEpoch)) >= phase0.Epoch(spec.ElectraForkEpoch) {
		maxEffectiveBalance = spec.MaxEffectiveBalanceElectra
	}

	length := uint64(len(activeIndices))
	if length == 0 {
		return 0, fmt.Errorf("empty active indices list")
	}
	maxRandomByte := uint64(1<<8 - 1)

	for i := uint64(0); ; i++ {
		candidateIndex, err := ComputeShuffledIndex(spec, i%length, length, seed, true)
		if err != nil {
			return 0, err
		}
		candidateIndex = uint64(activeIndices[candidateIndex])
		b := append(seed[:], UintToBytes(i/32)...)
		randomByte := Hash(b)[i%32]

		effectiveBal := uint64(state.GetEffectiveBalance(phase0.ValidatorIndex(candidateIndex)))

		if effectiveBal*maxRandomByte >= maxEffectiveBalance*uint64(randomByte) {
			return phase0.ValidatorIndex(candidateIndex), nil
		}
	}
}

func GetBeaconCommittees(spec *consensus.ChainSpec, state BeaconState, activeIndices []phase0.ValidatorIndex, slot phase0.Slot) ([][]phase0.ValidatorIndex, error) {
	epoch := phase0.Epoch(slot / phase0.Slot(spec.SlotsPerEpoch))
	seed := GetSeed(spec, state, epoch, spec.DomainBeaconAttester)

	committeesPerSlot := SlotCommitteeCount(spec, uint64(len(activeIndices)))
	committeesCount := committeesPerSlot * spec.SlotsPerEpoch
	committees := [][]phase0.ValidatorIndex{}

	for committeeIndex := uint64(0); committeeIndex < committeesPerSlot; committeeIndex++ {
		indexOffset := committeeIndex + ((uint64(slot) % spec.SlotsPerEpoch) * committeesPerSlot)

		committee, err := computeCommittee(spec, activeIndices, seed, indexOffset, committeesCount)
		if err != nil {
			return nil, fmt.Errorf("failed computing committee %v:%v: %v", slot, committeeIndex, err)
		}

		committees = append(committees, committee)
	}

	return committees, nil
}

func SlotCommitteeCount(spec *consensus.ChainSpec, activeValidatorCount uint64) uint64 {
	var committeesPerSlot = activeValidatorCount / spec.SlotsPerEpoch / spec.TargetCommitteeSize

	if committeesPerSlot > spec.MaxCommitteesPerSlot {
		return spec.MaxCommitteesPerSlot
	}
	if committeesPerSlot == 0 {
		return 1
	}

	return committeesPerSlot
}

func computeCommittee(
	spec *consensus.ChainSpec,
	indices []phase0.ValidatorIndex,
	seed [32]byte,
	index, count uint64,
) ([]phase0.ValidatorIndex, error) {
	validatorCount := uint64(len(indices))
	start := SplitOffset(validatorCount, count, index)
	end := SplitOffset(validatorCount, count, index+1)

	if start > validatorCount || end > validatorCount {
		return nil, errors.New("index out of range")
	}

	// Save the shuffled indices in cache, this is only needed once per epoch or once per new committee index.
	shuffledIndices := make([]phase0.ValidatorIndex, len(indices))
	copy(shuffledIndices, indices)
	// UnshuffleList is used here as it is an optimized implementation created
	// for fast computation of committees.
	// Reference implementation: https://github.com/protolambda/eth2-shuffle
	shuffledList, err := UnshuffleList(spec, shuffledIndices, seed)
	if err != nil {
		return nil, err
	}

	return shuffledList[start:end], nil
}

func ShuffleList(spec *consensus.ChainSpec, input []phase0.ValidatorIndex, seed [32]byte) ([]phase0.ValidatorIndex, error) {
	return innerShuffleList(spec, input, seed, true /* shuffle */)
}

// UnshuffleList un-shuffles the list by running backwards through the round count.
func UnshuffleList(spec *consensus.ChainSpec, input []phase0.ValidatorIndex, seed [32]byte) ([]phase0.ValidatorIndex, error) {
	return innerShuffleList(spec, input, seed, false /* un-shuffle */)
}

// shuffles or unshuffles, shuffle=false to un-shuffle.
func innerShuffleList(spec *consensus.ChainSpec, input []phase0.ValidatorIndex, seed [32]byte, shuffle bool) ([]phase0.ValidatorIndex, error) {
	if len(input) <= 1 {
		return input, nil
	}
	if uint64(len(input)) > maxShuffleListSize {
		return nil, fmt.Errorf("list size %d out of bounds",
			len(input))
	}
	rounds := uint8(spec.ShuffleRoundCount)
	if rounds == 0 {
		return input, nil
	}
	listSize := uint64(len(input))
	buf := make([]byte, totalSize)
	r := uint8(0)
	if !shuffle {
		r = rounds - 1
	}
	copy(buf[:seedSize], seed[:])
	for {
		buf[seedSize] = r
		ph := Hash(buf[:pivotViewSize])
		pivot := binary.LittleEndian.Uint64(ph[:8]) % listSize
		mirror := (pivot + 1) >> 1
		binary.LittleEndian.PutUint32(buf[pivotViewSize:], uint32(pivot>>8))
		source := Hash(buf)
		byteV := source[(pivot&0xff)>>3]
		for i, j := uint64(0), pivot; i < mirror; i, j = i+1, j-1 {
			byteV, source = swapOrNot(buf, byteV, phase0.ValidatorIndex(i), input, phase0.ValidatorIndex(j), source)
		}
		// Now repeat, but for the part after the pivot.
		mirror = (pivot + listSize + 1) >> 1
		end := listSize - 1
		binary.LittleEndian.PutUint32(buf[pivotViewSize:], uint32(end>>8))
		source = Hash(buf)
		byteV = source[(end&0xff)>>3]
		for i, j := pivot+1, end; i < mirror; i, j = i+1, j-1 {
			byteV, source = swapOrNot(buf, byteV, phase0.ValidatorIndex(i), input, phase0.ValidatorIndex(j), source)
		}
		if shuffle {
			r++
			if r == rounds {
				break
			}
		} else {
			if r == 0 {
				break
			}
			r--
		}
	}
	return input, nil
}

// swapOrNot describes the main algorithm behind the shuffle where we swap bytes in the inputted value
// depending on if the conditions are met.
func swapOrNot(buf []byte, byteV byte, i phase0.ValidatorIndex, input []phase0.ValidatorIndex, j phase0.ValidatorIndex, source [32]byte) (byte, [32]byte) {
	if j&0xff == 0xff {
		// just overwrite the last part of the buffer, reuse the start (seed, round)
		binary.LittleEndian.PutUint32(buf[pivotViewSize:], uint32(j>>8))
		source = Hash(buf)
	}
	if j&0x7 == 0x7 {
		byteV = source[(j&0xff)>>3]
	}
	bitV := (byteV >> (j & 0x7)) & 0x1

	if bitV == 1 {
		input[i], input[j] = input[j], input[i]
	}
	return byteV, source
}
