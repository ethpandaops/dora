package inclusionlists

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethpandaops/go-eth2-client/spec/bellatrix"
	"github.com/ethpandaops/spamoor/txtypes"

	"github.com/ethpandaops/dora/clients/execution"
	"github.com/ethpandaops/dora/utils"
)

const (
	// defaultMulticallAddress is the canonical Multicall3 deployment.
	defaultMulticallAddress = "0xcA11bde05977b3631167028862bE2a173976CA11"

	// multicallProbeInterval is how often a missing Multicall3 is re-probed.
	multicallProbeInterval = 10 * time.Minute

	// stateRequestTimeout bounds one sender state request.
	stateRequestTimeout = 10 * time.Second
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
func (r *Resolver) isMulticallReady(ctx context.Context, client *execution.Client) bool {
	if r.multicallReady {
		return true
	}
	if r.multicallAddr == (common.Address{}) || time.Since(r.multicallProbed) < multicallProbeInterval {
		return false
	}

	ethClient := client.GetRPCClient().GetEthClient()
	if ethClient == nil {
		return false
	}

	code, err := ethClient.CodeAt(ctx, r.multicallAddr, nil)
	if err != nil {
		return false
	}

	r.multicallProbed = time.Now()
	r.multicallReady = len(code) > 0
	if !r.multicallReady {
		r.logger.Debugf("multicall %s not deployed, reading sender balances individually", r.multicallAddr.Hex())
	}

	return r.multicallReady
}

// stateProbe is an account whose nonce at the post-state of a payload is known
// from the payload itself. Not every client answers a state query for a block
// hash at that block: some ignore the hash and answer at their head. Reading
// the probe along with the sender states tells whether the client did.
type stateProbe struct {
	address common.Address
	nonce   uint64
}

// payloadStateProbe derives a state probe from the transactions of a payload:
// the sender of its last transaction that is sequenced by the account nonce,
// whose nonce after the payload is that transaction's nonce plus one. Accounts
// that signed a set-code authorization in the payload are not used, as an
// authorization advances the nonce as well. Returns nil if the payload has no
// suitable transaction.
func payloadStateProbe(transactions []bellatrix.Transaction) *stateProbe {
	decoded := make([]*txtypes.Transaction, len(transactions))
	authorities := make(map[common.Address]bool, 4)
	for idx, rawTx := range transactions {
		tx, err := txtypes.DecodeTx(rawTx)
		if err != nil {
			continue
		}
		decoded[idx] = tx

		for _, authorization := range tx.AuthList() {
			if authority, err := authorization.Authority(); err == nil {
				authorities[authority] = true
			}
		}
	}

	for idx := len(decoded) - 1; idx >= 0; idx-- {
		tx := decoded[idx]
		if tx == nil || tx.Type() == txtypes.FrameTxType || !tx.UsesAccountNonce() {
			continue
		}

		sender, err := tx.From(tx.ChainId())
		if err != nil || authorities[sender] {
			continue
		}

		return &stateProbe{address: sender, nonce: tx.Nonce() + 1}
	}

	return nil
}

// fetchSenderStates loads nonce and balance of the given accounts at the
// post-state of the execution block with the given hash, in a single JSON-RPC
// batch request. The account nonce is not readable from within the EVM, so
// nonces are read with one eth_getTransactionCount call each; the balances are
// read with a single Multicall3 call where it is deployed. If a probe is
// given, the client's answer is rejected unless it reports the probe's nonce.
func (r *Resolver) fetchSenderStates(ctx context.Context, client *execution.Client, blockHash common.Hash, senders []common.Address, probe *stateProbe) (map[common.Address]*senderState, error) {
	ethClient := client.GetRPCClient().GetEthClient()
	if ethClient == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	ctx, cancel := context.WithTimeout(ctx, stateRequestTimeout)
	defer cancel()

	block := rpc.BlockNumberOrHashWithHash(blockHash, false)
	useMulticall := r.isMulticallReady(ctx, client)

	nonces := make([]hexutil.Uint64, len(senders))
	balances := make([]hexutil.Big, len(senders))
	var multicallResult hexutil.Bytes

	batch := make([]rpc.BatchElem, 0, 2*len(senders)+1)
	for i, sender := range senders {
		batch = append(batch, rpc.BatchElem{
			Method: "eth_getTransactionCount",
			Args:   []any{sender, block},
			Result: &nonces[i],
		})
	}

	var probeNonce hexutil.Uint64
	if probe != nil {
		batch = append(batch, rpc.BatchElem{
			Method: "eth_getTransactionCount",
			Args:   []any{probe.address, block},
			Result: &probeNonce,
		})
	}

	if useMulticall {
		calls := make([]multicall3Call, 0, len(senders))
		for _, sender := range senders {
			callData, err := multicall3ABI.Pack("getEthBalance", sender)
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
		for i, sender := range senders {
			batch = append(batch, rpc.BatchElem{
				Method: "eth_getBalance",
				Args:   []any{sender, block},
				Result: &balances[i],
			})
		}
	}

	if err := ethClient.Client().BatchCallContext(ctx, batch); err != nil {
		return nil, fmt.Errorf("sender state batch: %w", err)
	}
	for _, elem := range batch {
		if elem.Error != nil {
			return nil, fmt.Errorf("%s: %w", elem.Method, elem.Error)
		}
	}

	if probe != nil && uint64(probeNonce) != probe.nonce {
		return nil, fmt.Errorf("state is not served at block %s: nonce of %s is %d, expected %d",
			blockHash.Hex(), probe.address.Hex(), uint64(probeNonce), probe.nonce)
	}

	states := make(map[common.Address]*senderState, len(senders))
	for i, sender := range senders {
		states[sender] = &senderState{
			nonce:   uint64(nonces[i]),
			balance: balances[i].ToInt(),
		}
	}

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
		if len(decoded.ReturnData) != len(senders) {
			return nil, fmt.Errorf("multicall returned %d results for %d calls", len(decoded.ReturnData), len(senders))
		}
		for i, sender := range senders {
			if !decoded.ReturnData[i].Success || len(decoded.ReturnData[i].ReturnData) != 32 {
				return nil, fmt.Errorf("multicall balance lookup failed for %s", sender.Hex())
			}
			states[sender].balance = new(big.Int).SetBytes(decoded.ReturnData[i].ReturnData)
		}
	}

	return states, nil
}

// fetchSenderCode loads the code of the given accounts at the post-state of
// the execution block with the given hash and marks the states of those with
// non-delegated code (EIP-3607), in a single JSON-RPC batch request.
func (r *Resolver) fetchSenderCode(ctx context.Context, client *execution.Client, blockHash common.Hash, senders []common.Address, states map[common.Address]*senderState) error {
	ethClient := client.GetRPCClient().GetEthClient()
	if ethClient == nil {
		return fmt.Errorf("client not initialized")
	}

	ctx, cancel := context.WithTimeout(ctx, stateRequestTimeout)
	defer cancel()

	block := rpc.BlockNumberOrHashWithHash(blockHash, false)
	codes := make([]hexutil.Bytes, len(senders))

	batch := make([]rpc.BatchElem, 0, len(senders))
	for i, sender := range senders {
		batch = append(batch, rpc.BatchElem{
			Method: "eth_getCode",
			Args:   []any{sender, block},
			Result: &codes[i],
		})
	}

	if err := ethClient.Client().BatchCallContext(ctx, batch); err != nil {
		return fmt.Errorf("sender code batch: %w", err)
	}
	for _, elem := range batch {
		if elem.Error != nil {
			return fmt.Errorf("%s: %w", elem.Method, elem.Error)
		}
	}

	for i, sender := range senders {
		state := states[sender]
		if state == nil {
			continue
		}

		_, isDelegation := txtypes.ParseDelegation(codes[i])
		state.hasCode = len(codes[i]) > 0 && !isDelegation
		state.codeKnown = true
	}

	return nil
}
