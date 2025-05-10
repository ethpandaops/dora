package utils

import (
	"encoding/binary"
	"math/big"
)

// Uint256ToUint64 converts a [32]byte representation of a uint256 to a uint64
func Uint256ToUint64(bytes [32]byte) uint64 {
	// Little-endian byte order
	if bytes[8] != 0 || bytes[9] != 0 || bytes[10] != 0 || bytes[11] != 0 ||
		bytes[12] != 0 || bytes[13] != 0 || bytes[14] != 0 || bytes[15] != 0 ||
		bytes[16] != 0 || bytes[17] != 0 || bytes[18] != 0 || bytes[19] != 0 ||
		bytes[20] != 0 || bytes[21] != 0 || bytes[22] != 0 || bytes[23] != 0 ||
		bytes[24] != 0 || bytes[25] != 0 || bytes[26] != 0 || bytes[27] != 0 ||
		bytes[28] != 0 || bytes[29] != 0 || bytes[30] != 0 || bytes[31] != 0 {
		return 0xFFFFFFFFFFFFFFFF // Return max uint64 if value overflows
	}

	return binary.LittleEndian.Uint64(bytes[:8])
}

// AlternateUint256ToUint64 converts a [32]byte representation of a uint256 to a uint64 using big.Int
func AlternateUint256ToUint64(bytes [32]byte) uint64 {
	// Create a big.Int from the bytes (big-endian)
	bigInt := new(big.Int).SetBytes(bytes[:])

	// Check if it exceeds uint64 max
	if bigInt.Cmp(new(big.Int).SetUint64(0xFFFFFFFFFFFFFFFF)) > 0 {
		return 0xFFFFFFFFFFFFFFFF // Return max uint64 if value overflows
	}

	return bigInt.Uint64()
}

// GetBaseFeeAsUint64 is a generic function that converts any BaseFeePerGas type to uint64
// It handles both [32]byte and objects with Uint64() method
func GetBaseFeeAsUint64(baseFee interface{}) uint64 {
	switch fee := baseFee.(type) {
	case [32]byte:
		return AlternateUint256ToUint64(fee)
	default:
		// Try to use the Uint64 method if available
		if feeWithUint64, ok := fee.(interface{ Uint64() uint64 }); ok {
			return feeWithUint64.Uint64()
		}
		// If we can't handle the type, return 0
		return 0
	}
}
