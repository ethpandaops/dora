package utils

import (
	"bytes"
	"fmt"
	"math/big"
	"slices"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/bellatrix"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	dynssz "github.com/pk910/dynamic-ssz"
)

const (
	// MaxRlpBlockSize is the EIP-7934 cap on len(rlp(block)): MAX_BLOCK_SIZE (10 MiB) - SAFETY_MARGIN (2 MiB).
	MaxRlpBlockSize = 8_388_608
	// MaxGossipPayloadSize is the default consensus-layer gossip MAX_PAYLOAD_SIZE (10 MiB).
	MaxGossipPayloadSize = 10_485_760
)

// ExecutionBlock is the EL block reconstructed from a beacon execution payload.
type ExecutionBlock struct {
	Header    *types.Header // always set
	Block     *types.Block  // nil if go-ethereum cannot decode a transaction (see TxError)
	TxError   error
	HashMatch bool // Header.Hash() equals the payload block hash
}

// rawTxs feeds the opaque EIP-2718 tx encodings to DeriveSha, so the transactions
// root does not depend on go-ethereum knowing every tx type.
type rawTxs []bellatrix.Transaction

func (t rawTxs) Len() int                           { return len(t) }
func (t rawTxs) EncodeIndex(i int, w *bytes.Buffer) { w.Write(t[i]) }

// ExecutionBlockFromPayload reconstructs the EL block (go-ethereum types) from a beacon
// execution payload, following engine.ExecutableDataToBlock. parentRoot is the parent
// beacon block root (Deneb+), requests the payload's execution requests (Electra+) and
// bal the raw RLP block access list (Gloas+). Without the BAL the header carries a zero
// BAL hash: the RLP size stays exact, but HashMatch is false.
func ExecutionBlockFromPayload(payload *all.ExecutionPayload, parentRoot phase0.Root, requests *all.ExecutionRequests, bal []byte) *ExecutionBlock {
	baseFee := new(big.Int)
	if payload.BaseFeePerGas != nil {
		baseFee = payload.BaseFeePerGas.ToBig()
	} else {
		le := payload.BaseFeePerGasLE
		slices.Reverse(le[:])
		baseFee.SetBytes(le[:])
	}

	header := &types.Header{
		ParentHash:  common.Hash(payload.ParentHash),
		UncleHash:   types.EmptyUncleHash,
		Coinbase:    common.Address(payload.FeeRecipient),
		Root:        common.Hash(payload.StateRoot),
		TxHash:      types.DeriveSha(rawTxs(payload.Transactions), trie.NewStackTrie(nil)),
		ReceiptHash: common.Hash(payload.ReceiptsRoot),
		Bloom:       types.Bloom(payload.LogsBloom),
		Difficulty:  new(big.Int),
		Number:      new(big.Int).SetUint64(payload.BlockNumber),
		GasLimit:    payload.GasLimit,
		GasUsed:     payload.GasUsed,
		Time:        payload.Timestamp,
		Extra:       payload.ExtraData,
		MixDigest:   common.Hash(payload.PrevRandao),
		BaseFee:     baseFee,
	}

	var withdrawals []*types.Withdrawal
	if payload.Version >= spec.DataVersionCapella {
		withdrawals = make([]*types.Withdrawal, len(payload.Withdrawals))
		for i, w := range payload.Withdrawals {
			withdrawals[i] = &types.Withdrawal{
				Index:     uint64(w.Index),
				Validator: uint64(w.ValidatorIndex),
				Address:   common.Address(w.Address),
				Amount:    uint64(w.Amount),
			}
		}
		h := types.DeriveSha(types.Withdrawals(withdrawals), trie.NewStackTrie(nil))
		header.WithdrawalsHash = &h
	}
	if payload.Version >= spec.DataVersionDeneb {
		blobGasUsed, excessBlobGas := payload.BlobGasUsed, payload.ExcessBlobGas
		beaconRoot := common.Hash(parentRoot)
		header.BlobGasUsed, header.ExcessBlobGas, header.ParentBeaconRoot = &blobGasUsed, &excessBlobGas, &beaconRoot
	}
	if payload.Version >= spec.DataVersionElectra {
		h := types.CalcRequestsHash(ExecutionRequestsList(requests))
		header.RequestsHash = &h
	}
	if payload.Version >= spec.DataVersionGloas {
		var balHash common.Hash // placeholder when the BAL is unavailable
		if len(bal) > 0 {
			balHash = crypto.Keccak256Hash(bal)
		}
		slot := payload.SlotNumber
		header.BlockAccessListHash, header.SlotNumber = &balHash, &slot
	}

	result := &ExecutionBlock{Header: header, HashMatch: header.Hash() == common.Hash(payload.BlockHash)}
	txs := make([]*types.Transaction, len(payload.Transactions))
	for i, raw := range payload.Transactions {
		txs[i] = new(types.Transaction)
		if err := txs[i].UnmarshalBinary(raw); err != nil {
			result.TxError = fmt.Errorf("go-ethereum cannot decode tx %d: %w", i, err)
			return result
		}
	}
	result.Block = types.NewBlockWithHeader(header).WithBody(types.Body{Transactions: txs, Withdrawals: withdrawals})
	return result
}

// ExecutionRequestsList encodes execution requests as the EIP-7685 list
// (request_type || ssz(requests)) per get_execution_requests_list, skipping empty types.
func ExecutionRequestsList(requests *all.ExecutionRequests) [][]byte {
	list := [][]byte{}
	if requests == nil {
		return list
	}
	type sszItem interface{ MarshalSSZ() ([]byte, error) }
	add := func(reqType byte, n int, item func(i int) sszItem) {
		if n == 0 {
			return
		}
		buf := []byte{reqType}
		for i := 0; i < n; i++ {
			enc, err := item(i).MarshalSSZ()
			if err != nil {
				return // drop the type; the block hash check then flags the header as unverified
			}
			buf = append(buf, enc...)
		}
		list = append(list, buf)
	}
	add(0x00, len(requests.Deposits), func(i int) sszItem { return requests.Deposits[i] })
	add(0x01, len(requests.Withdrawals), func(i int) sszItem { return requests.Withdrawals[i] })
	add(0x02, len(requests.Consolidations), func(i int) sszItem { return requests.Consolidations[i] })
	add(0x03, len(requests.BuilderDeposits), func(i int) sszItem { return requests.BuilderDeposits[i] })
	add(0x04, len(requests.BuilderExits), func(i int) sszItem { return requests.BuilderExits[i] })
	return list
}

// EnvelopeSSZSize returns the SSZ size of the signed execution payload envelope as
// gossiped. If the stored envelope had its BAL pruned, the separately preserved bal is
// substituted so the size reflects the original envelope.
func EnvelopeSSZSize(ds *dynssz.DynSsz, env *all.SignedExecutionPayloadEnvelope, bal []byte) (uint64, error) {
	if env.Message != nil && env.Message.Payload != nil && len(env.Message.Payload.BlockAccessList) == 0 && len(bal) > 0 {
		msg, payload := *env.Message, *env.Message.Payload
		payload.BlockAccessList = bal
		msg.Payload = &payload
		envCopy := *env
		envCopy.Message = &msg
		env = &envCopy
	}
	size, err := ds.SizeSSZ(env)
	return uint64(size), err
}
