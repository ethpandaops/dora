package utils

import (
	"encoding/binary"
	"math"
	"sort"

	"github.com/ethereum/go-ethereum/p2p/enode"
	"github.com/holiman/uint256"
	errors "github.com/pkg/errors"
	"github.com/prysmaticlabs/prysm/v5/crypto/hash"
	"github.com/prysmaticlabs/prysm/v5/encoding/bytesutil"
)

// Mainly from: prysm/beacon-chain/core/peerdas/helpers.go

var (
	// Custom errors
	errCustodySubnetCountTooLarge = errors.New("custody subnet count larger than data column sidecar subnet count")

	// maxUint256 is the maximum value of a uint256.
	maxUint256 = &uint256.Int{math.MaxUint64, math.MaxUint64, math.MaxUint64, math.MaxUint64}
)

func CustodyColumnSubnets(nodeId enode.ID, custodySubnetCount uint64, dataColumnSidecarSubnetCount uint64) (map[uint64]bool, error) {
	// Check if the custody subnet count is larger than the data column sidecar subnet count.
	if custodySubnetCount > dataColumnSidecarSubnetCount {
		return nil, errCustodySubnetCountTooLarge
	}

	// First, compute the subnet IDs that the node should participate in.
	subnetIds := make(map[uint64]bool, custodySubnetCount)

	one := uint256.NewInt(1)

	for currentId := new(uint256.Int).SetBytes(nodeId.Bytes()); uint64(len(subnetIds)) < custodySubnetCount; currentId.Add(currentId, one) {
		// Convert to big endian bytes.
		currentIdBytesBigEndian := currentId.Bytes32()

		// Convert to little endian.
		currentIdBytesLittleEndian := bytesutil.ReverseByteOrder(currentIdBytesBigEndian[:])

		// Hash the result.
		hashedCurrentId := hash.Hash(currentIdBytesLittleEndian)

		// Get the subnet ID.
		subnetId := binary.LittleEndian.Uint64(hashedCurrentId[:8]) % dataColumnSidecarSubnetCount

		// Add the subnet to the map.
		subnetIds[subnetId] = true

		// Overflow prevention.
		if currentId.Cmp(maxUint256) == 0 {
			currentId = uint256.NewInt(0)
		}
	}

	return subnetIds, nil
}

// CustodyColumns computes the columns the node should custody.
// https://github.com/ethereum/consensus-specs/blob/dev/specs/_features/eip7594/das-core.md#helper-functions
func CustodyColumns(nodeId enode.ID, custodySubnetCount uint64, numberOfColumns uint64, dataColumnSidecarSubnetCount uint64) (map[uint64]bool, error) {

	// Compute the custodied subnets.
	subnetIds, err := CustodyColumnSubnets(nodeId, custodySubnetCount, dataColumnSidecarSubnetCount)
	if err != nil {
		return nil, errors.Wrap(err, "custody subnets")
	}

	columnsPerSubnet := numberOfColumns / dataColumnSidecarSubnetCount

	// Knowing the subnet ID and the number of columns per subnet, select all the columns the node should custody.
	// Columns belonging to the same subnet are contiguous.
	columnIndices := make(map[uint64]bool, custodySubnetCount*columnsPerSubnet)
	for i := uint64(0); i < columnsPerSubnet; i++ {
		for subnetId := range subnetIds {
			columnIndex := dataColumnSidecarSubnetCount*i + subnetId
			columnIndices[columnIndex] = true
		}
	}

	return columnIndices, nil
}

func CustodyColumnsSlice(nodeId enode.ID, custodySubnetCount uint64, numberOfColumns uint64, dataColumnSidecarSubnetCount uint64) ([]uint64, error) {
	columns, err := CustodyColumns(nodeId, custodySubnetCount, numberOfColumns, dataColumnSidecarSubnetCount)
	if err != nil {
		return nil, err
	}
	columnsSlice := make([]uint64, 0, len(columns))
	for column := range columns {
		columnsSlice = append(columnsSlice, column)
	}

	sort.Slice(columnsSlice, func(i, j int) bool {
		return columnsSlice[i] < columnsSlice[j]
	})

	return columnsSlice, nil
}

func CustodyColumnSubnetsSlice(nodeId enode.ID, custodySubnetCount uint64, dataColumnSidecarSubnetCount uint64) ([]uint64, error) {
	subnets, err := CustodyColumnSubnets(nodeId, custodySubnetCount, dataColumnSidecarSubnetCount)
	if err != nil {
		return nil, err
	}
	subnetsSlice := make([]uint64, 0, len(subnets))
	for subnet := range subnets {
		subnetsSlice = append(subnetsSlice, subnet)
	}
	sort.Slice(subnetsSlice, func(i, j int) bool {
		return subnetsSlice[i] < subnetsSlice[j]
	})

	return subnetsSlice, nil
}
