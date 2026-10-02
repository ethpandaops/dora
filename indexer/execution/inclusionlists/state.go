package inclusionlists

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/spamoor/txtypes"

	"github.com/ethpandaops/dora/utils"
)

const (
	// defaultMulticallAddress is the canonical Multicall3 deployment.
	defaultMulticallAddress = "0xcA11bde05977b3631167028862bE2a173976CA11"

	// multicallProbeInterval is how often a missing Multicall3 is re-probed.
	multicallProbeInterval = 10 * time.Minute

	// stateRequestTimeout bounds one sender state request.
	stateRequestTimeout = 10 * time.Second

	// maxStateProbes is the number of accounts probed per state request.
	maxStateProbes = 16
)

// multicall3ABI is the minimal ABI needed to read balances via
// Multicall3.aggregate3 and Multicall3.getEthBalance.
var multicall3ABI = mustParseABI(`[
	{"type":"function","name":"aggregate3","stateMutability":"payable","inputs":[{"name":"calls","type":"tuple[]","components":[{"name":"target","type":"address"},{"name":"allowFailure","type":"bool"},{"name":"callData","type":"bytes"}]}],"outputs":[{"name":"returnData","type":"tuple[]","components":[{"name":"success","type":"bool"},{"name":"returnData","type":"bytes"}]}]},
	{"type":"function","name":"getEthBalance","stateMutability":"view","inputs":[{"name":"addr","type":"address"}],"outputs":[{"name":"balance","type":"uint256"}]}
]`)

type multicall3Call struct {
	Target       common.Address
	AllowFailure bool
	CallData     []byte
}

func mustParseABI(def string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(def))
	if err != nil {
		panic(fmt.Sprintf("invalid multicall abi: %v", err))
	}
	return parsed
}

// multicallAddress returns the configured Multicall3 address, or the zero
// address if multicall is disabled.
func multicallAddress() common.Address {
	raw := utils.Config.EnsResolver.MulticallAddress
	if raw == "" {
		raw = defaultMulticallAddress
	}
	if !common.IsHexAddress(raw) {
		return common.Address{}
	}
	return common.HexToAddress(raw)
}

// isMulticallReady reports whether Multicall3 is deployed on the network. A
// deployment is probed once; a missing one is re-probed periodically.
func (r *Resolver) isMulticallReady(ctx context.Context, ethClient *ethclient.Client) bool {
	if r.multicallReady {
		return true
	}
	if r.multicallAddr == (common.Address{}) || time.Since(r.multicallProbed) < multicallProbeInterval {
		return false
	}

	r.multicallProbed = time.Now()

	code, err := ethClient.CodeAt(ctx, r.multicallAddr, nil)
	if err != nil {
		return false
	}

	r.multicallReady = len(code) > 0
	if !r.multicallReady {
		r.logger.Debugf("multicall %s not deployed, reading sender balances individually", r.multicallAddr.Hex())
	}

	return r.multicallReady
}

// errStateNotAtBlock marks a client that answered a state request for a block
// hash with the state of another block.
var errStateNotAtBlock = errors.New("state is not served at the requested block")

// stateProbe is an account whose state after a payload is known from the
// payload itself. Not every client answers a state query for a block hash at
// that block: some answer at their head, or at the canonical block of the same
// height, which after a reorg is a sibling of the requested one. Reading the
// probes with the same calls as the sender states tells whether the client
// answered at the block.
type stateProbe struct {
	address common.Address
	// nonce is the account nonce after the payload, if hasNonce is set.
	nonce    uint64
	hasNonce bool
	// balance is the account balance after the payload, or nil if unknown.
	balance *big.Int
}

// payloadStateProbes derives state probes from the block access list of a
// payload, which records the nonce and balance of every account the payload
// changed. Several accounts are probed, because a sibling payload on the same
// parent can leave a single account in the very same state. Accounts with both
// a nonce and a balance change come first, as they verify both lookups.
// Returns nil if the payload changed no nonce and no balance, or carries no
// decodable access list.
func payloadStateProbes(payload *all.ExecutionPayload) []*stateProbe {
	if len(payload.BlockAccessList) == 0 {
		return nil
	}

	accesses, err := utils.DecodeBlockAccessList(payload.BlockAccessList)
	if err != nil {
		return nil
	}

	probes := make([]*stateProbe, 0, len(accesses))
	for idx := range accesses {
		access := &accesses[idx]
		probe := &stateProbe{address: access.Address}

		// The entry with the highest index holds the value after the payload.
		var nonceIdx, balanceIdx uint16
		for _, change := range access.NonceChanges {
			if !probe.hasNonce || change.TxIdx >= nonceIdx {
				probe.nonce = change.Nonce
				nonceIdx = change.TxIdx
			}
			probe.hasNonce = true
		}
		for _, change := range access.BalanceChanges {
			if probe.balance == nil || change.TxIdx >= balanceIdx {
				probe.balance = new(big.Int).SetBytes(change.Balance)
				balanceIdx = change.TxIdx
			}
		}

		if probe.hasNonce || probe.balance != nil {
			probes = append(probes, probe)
		}
	}

	sort.SliceStable(probes, func(i, j int) bool {
		return probes[i].rank() < probes[j].rank()
	})
	if len(probes) > maxStateProbes {
		probes = probes[:maxStateProbes]
	}

	return probes
}

// rank orders probes by how much they verify: nonce and balance first, then
// nonce only, then balance only.
func (probe *stateProbe) rank() int {
	switch {
	case probe.hasNonce && probe.balance != nil:
		return 0
	case probe.hasNonce:
		return 1
	default:
		return 2
	}
}

// blockHead is the part of a block header the head check needs.
type blockHead struct {
	Hash common.Hash `json:"hash"`
}

// fetchSenderStates loads nonce, balance and code of the given accounts at the
// post-state of the execution block with the given hash, in a single JSON-RPC
// batch request. The account nonce is not readable from within the EVM, so
// nonces are read with one eth_getTransactionCount call each; the balances are
// read with a single Multicall3 call where it is deployed.
//
// The answer is only accepted if the client provably served it at that block:
// it must report the known state of all probes, or, if the payload offers no
// probe, have the block as its head before and after the lookups. Otherwise
// errStateNotAtBlock is returned.
func (r *Resolver) fetchSenderStates(ctx context.Context, ethClient *ethclient.Client, blockHash common.Hash, senders []common.Address, probes []*stateProbe) (map[common.Address]*senderState, error) {
	ctx, cancel := context.WithTimeout(ctx, stateRequestTimeout)
	defer cancel()

	block := rpc.BlockNumberOrHashWithHash(blockHash, false)
	useMulticall := r.isMulticallReady(ctx, ethClient)

	// The probes are looked up like further accounts.
	accounts := append(make([]common.Address, 0, len(senders)+len(probes)), senders...)
	for _, probe := range probes {
		accounts = append(accounts, probe.address)
	}

	nonces := make([]hexutil.Uint64, len(accounts))
	balances := make([]hexutil.Big, len(accounts))
	codes := make([]hexutil.Bytes, len(senders))
	var multicallResult hexutil.Bytes
	var headBefore, headAfter *blockHead

	batch := make([]rpc.BatchElem, 0, 3*len(accounts)+2)
	if len(probes) == 0 {
		batch = append(batch, rpc.BatchElem{
			Method: "eth_getBlockByNumber",
			Args:   []any{"latest", false},
			Result: &headBefore,
		})
	}

	for i, account := range accounts {
		batch = append(batch, rpc.BatchElem{
			Method: "eth_getTransactionCount",
			Args:   []any{account, block},
			Result: &nonces[i],
		})
	}

	if useMulticall {
		calls := make([]multicall3Call, 0, len(accounts))
		for _, account := range accounts {
			callData, err := multicall3ABI.Pack("getEthBalance", account)
			if err != nil {
				return nil, fmt.Errorf("pack getEthBalance: %w", err)
			}
			calls = append(calls, multicall3Call{Target: r.multicallAddr, AllowFailure: true, CallData: callData})
		}

		input, err := multicall3ABI.Pack("aggregate3", calls)
		if err != nil {
			return nil, fmt.Errorf("pack aggregate3: %w", err)
		}

		batch = append(batch, rpc.BatchElem{
			Method: "eth_call",
			Args: []any{
				map[string]any{"to": r.multicallAddr, "data": hexutil.Bytes(input)},
				block,
			},
			Result: &multicallResult,
		})
	} else {
		for i, account := range accounts {
			batch = append(batch, rpc.BatchElem{
				Method: "eth_getBalance",
				Args:   []any{account, block},
				Result: &balances[i],
			})
		}
	}

	for i, sender := range senders {
		batch = append(batch, rpc.BatchElem{
			Method: "eth_getCode",
			Args:   []any{sender, block},
			Result: &codes[i],
		})
	}

	if len(probes) == 0 {
		batch = append(batch, rpc.BatchElem{
			Method: "eth_getBlockByNumber",
			Args:   []any{"latest", false},
			Result: &headAfter,
		})
	}

	if err := ethClient.Client().BatchCallContext(ctx, batch); err != nil {
		return nil, fmt.Errorf("sender state batch: %w", err)
	}
	for _, elem := range batch {
		if elem.Error != nil {
			return nil, fmt.Errorf("%s: %w", elem.Method, elem.Error)
		}
	}

	accountBalances := make([]*big.Int, len(accounts))
	if useMulticall {
		var decoded struct {
			ReturnData []struct {
				Success    bool
				ReturnData []byte
			}
		}
		if err := multicall3ABI.UnpackIntoInterface(&decoded, "aggregate3", multicallResult); err != nil {
			return nil, fmt.Errorf("unpack aggregate3: %w", err)
		}
		if len(decoded.ReturnData) != len(accounts) {
			return nil, fmt.Errorf("multicall returned %d results for %d calls", len(decoded.ReturnData), len(accounts))
		}
		for i, account := range accounts {
			if !decoded.ReturnData[i].Success || len(decoded.ReturnData[i].ReturnData) != 32 {
				return nil, fmt.Errorf("multicall balance lookup failed for %s", account.Hex())
			}
			accountBalances[i] = new(big.Int).SetBytes(decoded.ReturnData[i].ReturnData)
		}
	} else {
		for i := range accounts {
			accountBalances[i] = balances[i].ToInt()
		}
	}

	for i, probe := range probes {
		probeIdx := len(senders) + i
		if probe.hasNonce && uint64(nonces[probeIdx]) != probe.nonce {
			return nil, fmt.Errorf("%w %s: nonce of %s is %d, expected %d",
				errStateNotAtBlock, blockHash.Hex(), probe.address.Hex(), uint64(nonces[probeIdx]), probe.nonce)
		}
		if probe.balance != nil && accountBalances[probeIdx].Cmp(probe.balance) != 0 {
			return nil, fmt.Errorf("%w %s: balance of %s is %v, expected %v",
				errStateNotAtBlock, blockHash.Hex(), probe.address.Hex(), accountBalances[probeIdx], probe.balance)
		}
	}

	if len(probes) == 0 && (headBefore == nil || headAfter == nil || headBefore.Hash != blockHash || headAfter.Hash != blockHash) {
		// Without a probe the answer can only be trusted from a client whose
		// head is the block: it is then at that block whether or not the
		// client honours the block hash of a state request.
		return nil, fmt.Errorf("%w %s: no state probe and the block is not the client head", errStateNotAtBlock, blockHash.Hex())
	}

	states := make(map[common.Address]*senderState, len(senders))
	for i, sender := range senders {
		// A sender with code cannot send transactions (EIP-3607), unless the
		// code is a delegation.
		_, isDelegation := txtypes.ParseDelegation(codes[i])

		states[sender] = &senderState{
			nonce:   uint64(nonces[i]),
			balance: accountBalances[i],
			hasCode: len(codes[i]) > 0 && !isDelegation,
		}
	}

	return states, nil
}
