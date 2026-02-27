package utils

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// DecodedCalldataParam represents a single decoded parameter from calldata.
type DecodedCalldataParam struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// CallTargetResolution holds the result of resolving a call target type
// and method information for a transaction.
type CallTargetResolution struct {
	CallType        string                  // "call", "deploy", "precompile", "system"
	CallName        string                  // Precompile or system contract name
	MethodName      string                  // Resolved method name
	MethodID        []byte                  // 4-byte method selector (if applicable)
	MethodSignature string                  // Full function signature (if resolved)
	DecodedCalldata []*DecodedCalldataParam // Decoded calldata params (if available)
}

// SignatureLookupResult holds the result of a function signature lookup.
type SignatureLookupResult struct {
	Name      string
	Signature string
	Found     bool
}

// SignatureLookupFunc is a callback for looking up function signatures by their
// 4-byte selector. Implementations typically query a signature database.
type SignatureLookupFunc func(methodID [4]byte) *SignatureLookupResult

// ResolveCallTargetAndMethod determines the call target type and resolves the
// method name. For deployments, precompiles, and non-deposit system contracts,
// the function signature lookup is skipped. For normal contract calls, the
// signature is looked up via the provided callback and calldata is optionally
// ABI-decoded.
func ResolveCallTargetAndMethod(
	toAddr []byte,
	isCreate bool,
	inputData []byte,
	methodID []byte,
	sysContracts map[common.Address]string,
	lookupSignature SignatureLookupFunc,
) *CallTargetResolution {
	res := &CallTargetResolution{}

	// Contract creation
	if isCreate {
		res.CallType = "deploy"
		res.MethodName = "deploy"
		return res
	}

	// Precompile
	if precompileInfo := GetPrecompileInfo(toAddr); precompileInfo != nil {
		res.CallType = "precompile"
		res.CallName = precompileInfo.Name
		res.MethodName = precompileInfo.Name
		if len(inputData) > 0 {
			res.DecodedCalldata = DecodePrecompileInput(precompileInfo.Index, inputData)
		}
		return res
	}

	// Non-deposit system contract
	if len(toAddr) == 20 {
		var addr common.Address
		copy(addr[:], toAddr)
		if sysName, ok := sysContracts[addr]; ok && sysName != "Deposit" {
			res.CallType = "system"
			res.CallName = sysName
			res.MethodName = sysName
			if len(inputData) > 0 {
				switch sysName {
				case "Withdrawal Request":
					res.DecodedCalldata = DecodeWithdrawalRequestInput(inputData)
				case "Consolidation Request":
					res.DecodedCalldata = DecodeConsolidationRequestInput(inputData)
				}
			}
			return res
		}
	}

	// Normal call
	res.CallType = "call"
	if len(methodID) < 4 {
		if len(inputData) == 0 {
			res.MethodName = "transfer"
		}
		return res
	}

	res.MethodID = methodID[:4]
	if lookupSignature != nil {
		var sigBytes [4]byte
		copy(sigBytes[:], methodID[:4])
		if result := lookupSignature(sigBytes); result != nil {
			if result.Found {
				res.MethodName = result.Name
				res.MethodSignature = result.Signature
				if len(inputData) > 4 && result.Signature != "" {
					res.DecodedCalldata = DecodeCalldata(result.Signature, inputData)
				}
			} else {
				res.MethodName = "call?"
			}
		}
	}

	return res
}

// PrecompileInfo describes a known precompile contract.
type PrecompileInfo struct {
	Index uint8
	Name  string
}

// precompileNames maps the precompile byte index to its human-readable name.
var precompileNames = map[uint8]string{
	0x01: "ecRecover",
	0x02: "SHA2-256",
	0x03: "RIPEMD-160",
	0x04: "Identity",
	0x05: "ModExp",
	0x06: "BN256Add",
	0x07: "BN256ScalarMul",
	0x08: "BN256Pairing",
	0x09: "BLAKE2f",
	0x0a: "KZG Point Eval",
	0x0b: "BLS12-381 G1Add",
	0x0c: "BLS12-381 G1MSM",
	0x0d: "BLS12-381 G2Add",
	0x0e: "BLS12-381 G2MSM",
}

// maxPrecompileIndex is the highest known precompile address byte.
const maxPrecompileIndex = 0x0e

// ShouldSkipSignatureLookup checks if a function signature lookup should be
// skipped for the given target address. Returns (shouldSkip, alternativeName).
// The sysContracts map should be obtained from ChainService.GetSystemContractAddresses().
func ShouldSkipSignatureLookup(toAddr []byte, isCreate bool, sysContracts map[common.Address]string) (bool, string) {
	if isCreate {
		return true, "deploy"
	}

	if info := GetPrecompileInfo(toAddr); info != nil {
		return true, info.Name
	}

	if len(toAddr) == 20 {
		var addr common.Address
		copy(addr[:], toAddr)

		if sysName, ok := sysContracts[addr]; ok && sysName != "Deposit" {
			return true, sysName
		}
	}

	return false, ""
}

// GetPrecompileInfo checks if the given 20-byte address is a precompile.
// A precompile address has all-zero bytes except the last byte, which
// is in range [0x01, maxPrecompileIndex].
func GetPrecompileInfo(addr []byte) *PrecompileInfo {
	if len(addr) != 20 {
		return nil
	}

	// Check that the first 19 bytes are zero
	for i := 0; i < 19; i++ {
		if addr[i] != 0 {
			return nil
		}
	}

	idx := addr[19]
	if idx == 0 || idx > maxPrecompileIndex {
		return nil
	}

	name, ok := precompileNames[idx]
	if !ok {
		name = fmt.Sprintf("Precompile 0x%02x", idx)
	}

	return &PrecompileInfo{Index: idx, Name: name}
}

// DecodeCalldata decodes calldata using a full function signature
// (e.g., "transfer(address,uint256)"). Returns nil if decoding fails.
func DecodeCalldata(signature string, data []byte) []*DecodedCalldataParam {
	if len(data) < 4 {
		return nil
	}

	args, err := parseSignatureArgs(signature)
	if err != nil {
		return nil
	}

	if len(args) == 0 {
		return []*DecodedCalldataParam{}
	}

	values, err := args.Unpack(data[4:])
	if err != nil {
		return nil
	}

	if len(values) != len(args) {
		return nil
	}

	result := make([]*DecodedCalldataParam, len(args))
	for i, arg := range args {
		result[i] = &DecodedCalldataParam{
			Name:  arg.Name,
			Type:  arg.Type.String(),
			Value: formatABIValue(values[i]),
		}
	}

	return result
}

// DecodePrecompileInput decodes known precompile input data.
func DecodePrecompileInput(precompileIndex uint8, data []byte) []*DecodedCalldataParam {
	switch precompileIndex {
	case 0x01:
		return decodeEcRecoverInput(data)
	case 0x02:
		return decodeRawDataInput("data", data)
	case 0x03:
		return decodeRawDataInput("data", data)
	case 0x04:
		return decodeRawDataInput("data", data)
	case 0x05:
		return decodeModExpInput(data)
	case 0x06:
		return decodeBN256AddInput(data)
	case 0x07:
		return decodeBN256ScalarMulInput(data)
	case 0x08:
		return decodeBN256PairingInput(data)
	case 0x09:
		return decodeBlake2fInput(data)
	case 0x0a:
		return decodeKZGPointEvalInput(data)
	case 0x0b:
		return decodeBLS12G1AddInput(data)
	case 0x0c:
		return decodeBLS12G1MSMInput(data)
	case 0x0d:
		return decodeBLS12G2AddInput(data)
	case 0x0e:
		return decodeBLS12G2MSMInput(data)
	default:
		return nil
	}
}

// DecodeWithdrawalRequestInput decodes EIP-7002 withdrawal request calldata.
// Format: 48 bytes validator pubkey + 8 bytes amount (big-endian).
func DecodeWithdrawalRequestInput(data []byte) []*DecodedCalldataParam {
	if len(data) < 56 {
		return nil
	}

	amount := binary.BigEndian.Uint64(data[48:56])

	return []*DecodedCalldataParam{
		{Name: "validator_pubkey", Type: "bytes48", Value: "0x" + hex.EncodeToString(data[:48])},
		{Name: "amount", Type: "uint64", Value: fmt.Sprintf("%d", amount)},
	}
}

// DecodeConsolidationRequestInput decodes EIP-7251 consolidation request calldata.
// Format: 48 bytes source pubkey + 48 bytes target pubkey.
func DecodeConsolidationRequestInput(data []byte) []*DecodedCalldataParam {
	if len(data) < 96 {
		return nil
	}

	return []*DecodedCalldataParam{
		{Name: "source_pubkey", Type: "bytes48", Value: "0x" + hex.EncodeToString(data[:48])},
		{Name: "target_pubkey", Type: "bytes48", Value: "0x" + hex.EncodeToString(data[48:96])},
	}
}

// parseSignatureArgs parses the parameter types from a function signature
// (e.g., "transfer(address,uint256)") and returns abi.Arguments.
func parseSignatureArgs(signature string) (abi.Arguments, error) {
	parenIdx := strings.Index(signature, "(")
	if parenIdx < 0 || !strings.HasSuffix(signature, ")") {
		return nil, fmt.Errorf("invalid signature format")
	}

	argsStr := signature[parenIdx+1 : len(signature)-1]
	if argsStr == "" {
		return abi.Arguments{}, nil
	}

	typeStrs := splitTopLevel(argsStr, ',')
	args := make(abi.Arguments, 0, len(typeStrs))

	for i, ts := range typeStrs {
		ts = strings.TrimSpace(ts)
		if ts == "" {
			continue
		}

		marshaling := typeStringToMarshaling(ts, fmt.Sprintf("arg%d", i))
		abiType, err := abi.NewType(marshaling.Type, "", marshaling.Components)
		if err != nil {
			return nil, fmt.Errorf("failed to parse type %q: %w", ts, err)
		}

		args = append(args, abi.Argument{
			Name: marshaling.Name,
			Type: abiType,
		})
	}

	return args, nil
}

// splitTopLevel splits a string by a separator, but only at the top level
// (respecting nested parentheses).
func splitTopLevel(s string, sep byte) []string {
	var result []string
	depth := 0
	start := 0

	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case sep:
			if depth == 0 {
				result = append(result, s[start:i])
				start = i + 1
			}
		}
	}

	if start <= len(s) {
		result = append(result, s[start:])
	}

	return result
}

// typeStringToMarshaling converts a type string (possibly with nested tuples)
// into an abi.ArgumentMarshaling suitable for abi.NewType.
func typeStringToMarshaling(typeStr string, name string) abi.ArgumentMarshaling {
	typeStr = strings.TrimSpace(typeStr)

	if !strings.Contains(typeStr, "(") {
		// Simple type (address, uint256, bytes32, uint256[], etc.)
		return abi.ArgumentMarshaling{Name: name, Type: typeStr}
	}

	// Tuple type: find the matching closing paren
	depth := 0
	closeIdx := -1

	for i := 0; i < len(typeStr); i++ {
		if typeStr[i] == '(' {
			depth++
		}
		if typeStr[i] == ')' {
			depth--
			if depth == 0 {
				closeIdx = i
				break
			}
		}
	}

	if closeIdx < 0 {
		// Malformed - treat as simple type
		return abi.ArgumentMarshaling{Name: name, Type: typeStr}
	}

	// Array suffix after the closing paren (e.g., "[]" or "[3]")
	arraySuffix := ""
	if closeIdx < len(typeStr)-1 {
		arraySuffix = typeStr[closeIdx+1:]
	}

	// Parse inner types
	inner := typeStr[1:closeIdx]
	subTypes := splitTopLevel(inner, ',')
	components := make([]abi.ArgumentMarshaling, 0, len(subTypes))

	for i, st := range subTypes {
		st = strings.TrimSpace(st)
		if st == "" {
			continue
		}
		components = append(components, typeStringToMarshaling(st, fmt.Sprintf("field%d", i)))
	}

	return abi.ArgumentMarshaling{
		Name:       name,
		Type:       "tuple" + arraySuffix,
		Components: components,
	}
}

// formatABIValue formats a decoded ABI value to a human-readable string.
func formatABIValue(value interface{}) string {
	if value == nil {
		return "<nil>"
	}

	switch v := value.(type) {
	case common.Address:
		return "0x" + hex.EncodeToString(v[:])
	case *big.Int:
		return v.String()
	case bool:
		if v {
			return "true"
		}
		return "false"
	case string:
		return v
	case []byte:
		return "0x" + hex.EncodeToString(v)
	case [1]byte:
		return "0x" + hex.EncodeToString(v[:])
	case [2]byte:
		return "0x" + hex.EncodeToString(v[:])
	case [3]byte:
		return "0x" + hex.EncodeToString(v[:])
	case [4]byte:
		return "0x" + hex.EncodeToString(v[:])
	case [8]byte:
		return "0x" + hex.EncodeToString(v[:])
	case [16]byte:
		return "0x" + hex.EncodeToString(v[:])
	case [20]byte:
		return "0x" + hex.EncodeToString(v[:])
	case [32]byte:
		return "0x" + hex.EncodeToString(v[:])
	case uint8:
		return fmt.Sprintf("%d", v)
	case uint16:
		return fmt.Sprintf("%d", v)
	case uint32:
		return fmt.Sprintf("%d", v)
	case uint64:
		return fmt.Sprintf("%d", v)
	case int8:
		return fmt.Sprintf("%d", v)
	case int16:
		return fmt.Sprintf("%d", v)
	case int32:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// --- Precompile input decoders ---

func decodeRawDataInput(name string, data []byte) []*DecodedCalldataParam {
	return []*DecodedCalldataParam{
		{Name: name, Type: "bytes", Value: "0x" + hex.EncodeToString(data)},
	}
}

func safeHexSlice(data []byte, start, end int) string {
	if start >= len(data) {
		return "0x" + strings.Repeat("00", end-start)
	}

	padded := make([]byte, end-start)
	n := copy(padded, data[start:min(end, len(data))])
	_ = n

	return "0x" + hex.EncodeToString(padded)
}

func decodeEcRecoverInput(data []byte) []*DecodedCalldataParam {
	if len(data) < 32 {
		return nil
	}

	params := []*DecodedCalldataParam{
		{Name: "hash", Type: "bytes32", Value: safeHexSlice(data, 0, 32)},
	}

	if len(data) >= 64 {
		v := new(big.Int).SetBytes(data[32:64])
		params = append(params, &DecodedCalldataParam{
			Name: "v", Type: "uint256", Value: v.String(),
		})
	}
	if len(data) >= 96 {
		params = append(params, &DecodedCalldataParam{
			Name: "r", Type: "bytes32", Value: safeHexSlice(data, 64, 96),
		})
	}
	if len(data) >= 128 {
		params = append(params, &DecodedCalldataParam{
			Name: "s", Type: "bytes32", Value: safeHexSlice(data, 96, 128),
		})
	}

	return params
}

func decodeModExpInput(data []byte) []*DecodedCalldataParam {
	if len(data) < 96 {
		return nil
	}

	bSize := new(big.Int).SetBytes(data[0:32]).Uint64()
	eSize := new(big.Int).SetBytes(data[32:64]).Uint64()
	mSize := new(big.Int).SetBytes(data[64:96]).Uint64()

	params := []*DecodedCalldataParam{
		{Name: "Bsize", Type: "uint256", Value: fmt.Sprintf("%d", bSize)},
		{Name: "Esize", Type: "uint256", Value: fmt.Sprintf("%d", eSize)},
		{Name: "Msize", Type: "uint256", Value: fmt.Sprintf("%d", mSize)},
	}

	offset := uint64(96)
	if uint64(len(data)) > offset && bSize > 0 {
		end := min(offset+bSize, uint64(len(data)))
		params = append(params, &DecodedCalldataParam{
			Name: "B", Type: fmt.Sprintf("bytes[%d]", bSize),
			Value: "0x" + hex.EncodeToString(data[offset:end]),
		})
		offset += bSize
	}
	if uint64(len(data)) > offset && eSize > 0 {
		end := min(offset+eSize, uint64(len(data)))
		params = append(params, &DecodedCalldataParam{
			Name: "E", Type: fmt.Sprintf("bytes[%d]", eSize),
			Value: "0x" + hex.EncodeToString(data[offset:end]),
		})
		offset += eSize
	}
	if uint64(len(data)) > offset && mSize > 0 {
		end := min(offset+mSize, uint64(len(data)))
		params = append(params, &DecodedCalldataParam{
			Name: "M", Type: fmt.Sprintf("bytes[%d]", mSize),
			Value: "0x" + hex.EncodeToString(data[offset:end]),
		})
	}

	return params
}

func decodeBN256AddInput(data []byte) []*DecodedCalldataParam {
	if len(data) < 64 {
		return nil
	}

	params := []*DecodedCalldataParam{
		{Name: "x1", Type: "uint256", Value: new(big.Int).SetBytes(data[0:32]).String()},
		{Name: "y1", Type: "uint256", Value: new(big.Int).SetBytes(data[32:64]).String()},
	}

	if len(data) >= 96 {
		params = append(params, &DecodedCalldataParam{
			Name: "x2", Type: "uint256", Value: new(big.Int).SetBytes(data[64:96]).String(),
		})
	}
	if len(data) >= 128 {
		params = append(params, &DecodedCalldataParam{
			Name: "y2", Type: "uint256", Value: new(big.Int).SetBytes(data[96:128]).String(),
		})
	}

	return params
}

func decodeBN256ScalarMulInput(data []byte) []*DecodedCalldataParam {
	if len(data) < 64 {
		return nil
	}

	params := []*DecodedCalldataParam{
		{Name: "x", Type: "uint256", Value: new(big.Int).SetBytes(data[0:32]).String()},
		{Name: "y", Type: "uint256", Value: new(big.Int).SetBytes(data[32:64]).String()},
	}

	if len(data) >= 96 {
		params = append(params, &DecodedCalldataParam{
			Name: "scalar", Type: "uint256", Value: new(big.Int).SetBytes(data[64:96]).String(),
		})
	}

	return params
}

func decodeBN256PairingInput(data []byte) []*DecodedCalldataParam {
	const pairSize = 192
	if len(data) < pairSize {
		return nil
	}

	numPairs := len(data) / pairSize
	params := make([]*DecodedCalldataParam, 0, numPairs)

	for i := 0; i < numPairs; i++ {
		offset := i * pairSize
		params = append(params, &DecodedCalldataParam{
			Name:  fmt.Sprintf("pair[%d]", i),
			Type:  "bytes192",
			Value: "0x" + hex.EncodeToString(data[offset:offset+pairSize]),
		})
	}

	return params
}

func decodeBlake2fInput(data []byte) []*DecodedCalldataParam {
	if len(data) < 213 {
		return nil
	}

	rounds := binary.BigEndian.Uint32(data[0:4])
	finalFlag := data[212]

	return []*DecodedCalldataParam{
		{Name: "rounds", Type: "uint32", Value: fmt.Sprintf("%d", rounds)},
		{Name: "h", Type: "bytes64", Value: "0x" + hex.EncodeToString(data[4:68])},
		{Name: "m", Type: "bytes128", Value: "0x" + hex.EncodeToString(data[68:196])},
		{Name: "t", Type: "bytes16", Value: "0x" + hex.EncodeToString(data[196:212])},
		{Name: "f", Type: "bool", Value: fmt.Sprintf("%d", finalFlag)},
	}
}

func decodeKZGPointEvalInput(data []byte) []*DecodedCalldataParam {
	if len(data) < 192 {
		return nil
	}

	return []*DecodedCalldataParam{
		{Name: "versioned_hash", Type: "bytes32", Value: safeHexSlice(data, 0, 32)},
		{Name: "z", Type: "bytes32", Value: safeHexSlice(data, 32, 64)},
		{Name: "y", Type: "bytes32", Value: safeHexSlice(data, 64, 96)},
		{Name: "commitment", Type: "bytes48", Value: safeHexSlice(data, 96, 144)},
		{Name: "proof", Type: "bytes48", Value: safeHexSlice(data, 144, 192)},
	}
}

func decodeBLS12G1AddInput(data []byte) []*DecodedCalldataParam {
	if len(data) < 256 {
		return nil
	}

	return []*DecodedCalldataParam{
		{Name: "x1", Type: "bytes64", Value: safeHexSlice(data, 0, 64)},
		{Name: "y1", Type: "bytes64", Value: safeHexSlice(data, 64, 128)},
		{Name: "x2", Type: "bytes64", Value: safeHexSlice(data, 128, 192)},
		{Name: "y2", Type: "bytes64", Value: safeHexSlice(data, 192, 256)},
	}
}

func decodeBLS12G1MSMInput(data []byte) []*DecodedCalldataParam {
	const entrySize = 160
	if len(data) < entrySize {
		return nil
	}

	numEntries := len(data) / entrySize
	params := make([]*DecodedCalldataParam, 0, numEntries)

	for i := 0; i < numEntries; i++ {
		offset := i * entrySize
		params = append(params, &DecodedCalldataParam{
			Name:  fmt.Sprintf("entry[%d]", i),
			Type:  "bytes160",
			Value: "0x" + hex.EncodeToString(data[offset:offset+entrySize]),
		})
	}

	return params
}

func decodeBLS12G2AddInput(data []byte) []*DecodedCalldataParam {
	if len(data) < 512 {
		return nil
	}

	return []*DecodedCalldataParam{
		{Name: "x1_c0", Type: "bytes64", Value: safeHexSlice(data, 0, 64)},
		{Name: "x1_c1", Type: "bytes64", Value: safeHexSlice(data, 64, 128)},
		{Name: "y1_c0", Type: "bytes64", Value: safeHexSlice(data, 128, 192)},
		{Name: "y1_c1", Type: "bytes64", Value: safeHexSlice(data, 192, 256)},
		{Name: "x2_c0", Type: "bytes64", Value: safeHexSlice(data, 256, 320)},
		{Name: "x2_c1", Type: "bytes64", Value: safeHexSlice(data, 320, 384)},
		{Name: "y2_c0", Type: "bytes64", Value: safeHexSlice(data, 384, 448)},
		{Name: "y2_c1", Type: "bytes64", Value: safeHexSlice(data, 448, 512)},
	}
}

func decodeBLS12G2MSMInput(data []byte) []*DecodedCalldataParam {
	const entrySize = 288
	if len(data) < entrySize {
		return nil
	}

	numEntries := len(data) / entrySize
	params := make([]*DecodedCalldataParam, 0, numEntries)

	for i := 0; i < numEntries; i++ {
		offset := i * entrySize
		params = append(params, &DecodedCalldataParam{
			Name:  fmt.Sprintf("entry[%d]", i),
			Type:  "bytes288",
			Value: "0x" + hex.EncodeToString(data[offset:offset+entrySize]),
		})
	}

	return params
}
