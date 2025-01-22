package db

// ConvertUint64ToInt64 converts a uint64 to an int64, supporting the full range of uint64,
// but the value is translated to the range of int64.
func ConvertUint64ToInt64(u uint64) int64 {
	// Subtract half of uint64 max value to center the range around 0
	// This maps:
	// uint64(0) -> int64.MinValue
	// uint64.MaxValue -> int64.MaxValue
	return int64(u - 1<<63)
}

// ConvertInt64ToUint64 converts an int64 to a uint64, supporting the full range of int64,
// but the value is translated to the range of uint64.
func ConvertInt64ToUint64(i int64) uint64 {
	// Add 2^63 to shift the range back to uint64
	// This maps:
	// int64.MinValue -> uint64(0)
	// int64.MaxValue -> uint64.MaxValue
	return uint64(i) + 1<<63
}
