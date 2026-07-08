package services

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ENS resolution mechanics: reverse resolution (address -> primary name) with a
// forward-verification step, batched via Multicall3 when available.
//
// Reverse lookup for an address A:
//  1. node   = namehash("<lowerhex(A)>.addr.reverse")
//  2. resolver = ENSRegistry.resolver(node)
//  3. name     = resolver.name(node)
// Forward verification (anti-spoof): the claimed name must resolve back to A:
//  4. fwdResolver = ENSRegistry.resolver(namehash(name))
//  5. addr        = fwdResolver.addr(namehash(name)) ; keep name only if addr == A

// Function selectors.
var (
	selectorResolver = common.Hex2Bytes("0178b8bf") // resolver(bytes32)
	selectorEnsName  = common.Hex2Bytes("691f3431") // name(bytes32)
	selectorEnsAddr  = common.Hex2Bytes("3b3b57de") // addr(bytes32)
)

// multicall3ABI is the minimal ABI needed to batch calls via Multicall3.aggregate3.
var multicall3ABI = mustParseABI(`[{"type":"function","name":"aggregate3","stateMutability":"payable","inputs":[{"name":"calls","type":"tuple[]","components":[{"name":"target","type":"address"},{"name":"allowFailure","type":"bool"},{"name":"callData","type":"bytes"}]}],"outputs":[{"name":"returnData","type":"tuple[]","components":[{"name":"success","type":"bool"},{"name":"returnData","type":"bytes"}]}]}]`)

type multicall3Call struct {
	Target       common.Address
	AllowFailure bool
	CallData     []byte
}

// ensCall is a single low-level eth_call (target contract + calldata).
type ensCall struct {
	target common.Address
	data   []byte
}

// ensCallResult is the outcome of an ensCall.
type ensCallResult struct {
	success bool
	data    []byte
}

func mustParseABI(def string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(def))
	if err != nil {
		panic(fmt.Sprintf("invalid multicall abi: %v", err))
	}
	return parsed
}

// namehash implements the ENS namehash algorithm (EIP-137).
func namehash(name string) [32]byte {
	node := make([]byte, 32)
	if name != "" {
		labels := strings.Split(name, ".")
		for i := len(labels) - 1; i >= 0; i-- {
			labelHash := crypto.Keccak256([]byte(labels[i]))
			node = crypto.Keccak256(node, labelHash)
		}
	}

	var out [32]byte
	copy(out[:], node)
	return out
}

// reverseNode returns the namehash of "<lowerhexaddr>.addr.reverse".
func reverseNode(addr common.Address) [32]byte {
	return namehash(strings.ToLower(addr.Hex()[2:]) + ".addr.reverse")
}

// resolveBatch resolves primary ENS names for the given addresses, trying the usable
// registries in configured order and keeping the first verified name per address.
func (e *EnsResolver) resolveBatch(ctx context.Context, ethClient *ethclient.Client, addrs []common.Address) map[common.Address]string {
	result := make(map[common.Address]string, len(addrs))
	pending := addrs

	for _, registry := range e.registries {
		if len(pending) == 0 {
			break
		}

		resolved := e.resolveWithRegistry(ctx, ethClient, registry, pending)

		remaining := make([]common.Address, 0, len(pending))
		for _, addr := range pending {
			if name, ok := resolved[addr]; ok && name != "" {
				result[addr] = name
			} else {
				remaining = append(remaining, addr)
			}
		}
		pending = remaining
	}

	return result
}

// resolveWithRegistry runs the full reverse+verify flow against a single registry.
func (e *EnsResolver) resolveWithRegistry(ctx context.Context, ethClient *ethclient.Client, registry common.Address, addrs []common.Address) map[common.Address]string {
	out := make(map[common.Address]string)

	// stage 1: registry.resolver(reverseNode) -> reverse resolver address
	revNodes := make([][32]byte, len(addrs))
	calls := make([]ensCall, len(addrs))
	for i, addr := range addrs {
		revNodes[i] = reverseNode(addr)
		calls[i] = ensCall{target: registry, data: appendNode(selectorResolver, revNodes[i])}
	}

	res, err := e.callBatch(ctx, ethClient, calls)
	if err != nil {
		e.logger.Warnf("ens stage1 (resolver) failed: %v", err)
		return out
	}

	type ensWork struct {
		addr        common.Address
		revNode     [32]byte
		revResolver common.Address
		name        string
		fwdNode     [32]byte
		fwdResolver common.Address
	}

	works := make([]*ensWork, 0, len(addrs))
	for i, addr := range addrs {
		if !res[i].success {
			continue
		}
		revResolver := decodeAddress(res[i].data)
		if revResolver == (common.Address{}) {
			continue
		}
		works = append(works, &ensWork{addr: addr, revNode: revNodes[i], revResolver: revResolver})
	}
	if len(works) == 0 {
		return out
	}

	// stage 2: reverseResolver.name(reverseNode) -> candidate name
	calls = make([]ensCall, len(works))
	for i, w := range works {
		calls[i] = ensCall{target: w.revResolver, data: appendNode(selectorEnsName, w.revNode)}
	}

	res, err = e.callBatch(ctx, ethClient, calls)
	if err != nil {
		e.logger.Warnf("ens stage2 (name) failed: %v", err)
		return out
	}

	named := make([]*ensWork, 0, len(works))
	for i, w := range works {
		if !res[i].success {
			continue
		}
		name := decodeENSString(res[i].data)
		if name == "" {
			continue
		}
		w.name = name
		w.fwdNode = namehash(strings.ToLower(name))
		named = append(named, w)
	}
	if len(named) == 0 {
		return out
	}

	// stage 3: registry.resolver(fwdNode) -> forward resolver address
	calls = make([]ensCall, len(named))
	for i, w := range named {
		calls[i] = ensCall{target: registry, data: appendNode(selectorResolver, w.fwdNode)}
	}

	res, err = e.callBatch(ctx, ethClient, calls)
	if err != nil {
		e.logger.Warnf("ens stage3 (fwd resolver) failed: %v", err)
		return out
	}

	verify := make([]*ensWork, 0, len(named))
	for i, w := range named {
		if !res[i].success {
			continue
		}
		fwdResolver := decodeAddress(res[i].data)
		if fwdResolver == (common.Address{}) {
			continue
		}
		w.fwdResolver = fwdResolver
		verify = append(verify, w)
	}
	if len(verify) == 0 {
		return out
	}

	// stage 4: forwardResolver.addr(fwdNode) -> must equal the original address
	calls = make([]ensCall, len(verify))
	for i, w := range verify {
		calls[i] = ensCall{target: w.fwdResolver, data: appendNode(selectorEnsAddr, w.fwdNode)}
	}

	res, err = e.callBatch(ctx, ethClient, calls)
	if err != nil {
		e.logger.Warnf("ens stage4 (addr) failed: %v", err)
		return out
	}

	for i, w := range verify {
		if !res[i].success {
			continue
		}
		if decodeAddress(res[i].data) == w.addr {
			out[w.addr] = w.name
		}
	}

	return out
}

// callBatch executes a set of eth_calls, using Multicall3 when available and falling
// back to individual calls otherwise.
func (e *EnsResolver) callBatch(ctx context.Context, ethClient *ethclient.Client, calls []ensCall) ([]ensCallResult, error) {
	if len(calls) == 0 {
		return nil, nil
	}

	if e.multicallReady {
		return e.callBatchMulticall(ctx, ethClient, calls)
	}

	results := make([]ensCallResult, len(calls))
	for i := range calls {
		target := calls[i].target
		data, err := ethClient.CallContract(ctx, ethereum.CallMsg{To: &target, Data: calls[i].data}, nil)
		if err != nil {
			continue
		}
		results[i] = ensCallResult{success: len(data) > 0, data: data}
	}
	return results, nil
}

// callBatchMulticall batches all calls into a single Multicall3.aggregate3 eth_call.
func (e *EnsResolver) callBatchMulticall(ctx context.Context, ethClient *ethclient.Client, calls []ensCall) ([]ensCallResult, error) {
	mcCalls := make([]multicall3Call, len(calls))
	for i, c := range calls {
		mcCalls[i] = multicall3Call{Target: c.target, AllowFailure: true, CallData: c.data}
	}

	input, err := multicall3ABI.Pack("aggregate3", mcCalls)
	if err != nil {
		return nil, fmt.Errorf("pack aggregate3: %w", err)
	}

	target := e.multicallAddress
	output, err := ethClient.CallContract(ctx, ethereum.CallMsg{To: &target, Data: input}, nil)
	if err != nil {
		return nil, fmt.Errorf("multicall eth_call: %w", err)
	}

	var decoded struct {
		ReturnData []struct {
			Success    bool
			ReturnData []byte
		}
	}
	if err := multicall3ABI.UnpackIntoInterface(&decoded, "aggregate3", output); err != nil {
		return nil, fmt.Errorf("unpack aggregate3: %w", err)
	}
	if len(decoded.ReturnData) != len(calls) {
		return nil, fmt.Errorf("multicall returned %d results for %d calls", len(decoded.ReturnData), len(calls))
	}

	results := make([]ensCallResult, len(calls))
	for i, r := range decoded.ReturnData {
		results[i] = ensCallResult{success: r.Success && len(r.ReturnData) > 0, data: r.ReturnData}
	}
	return results, nil
}

// appendNode returns selector ++ node (the standard 32-byte-arg calldata).
func appendNode(selector []byte, node [32]byte) []byte {
	data := make([]byte, 0, len(selector)+32)
	data = append(data, selector...)
	data = append(data, node[:]...)
	return data
}

// decodeAddress reads a 20-byte address from a 32-byte-aligned return word.
func decodeAddress(data []byte) common.Address {
	if len(data) < 32 {
		return common.Address{}
	}
	return common.BytesToAddress(data[12:32])
}

// decodeENSString decodes a single ABI-encoded string return value.
func decodeENSString(data []byte) string {
	if len(data) < 64 {
		return ""
	}
	offset := new(big.Int).SetBytes(data[:32]).Uint64()
	if offset+32 > uint64(len(data)) {
		return ""
	}
	length := new(big.Int).SetBytes(data[offset : offset+32]).Uint64()
	if length == 0 || offset+32+length > uint64(len(data)) {
		return ""
	}
	return strings.TrimRight(string(data[offset+32:offset+32+length]), "\x00")
}
