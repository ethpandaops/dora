package utils

import (
	"bytes"
	"io"
	"math/big"
	"slices"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	dynssz "github.com/pk910/dynamic-ssz"
)

const (
	// MaxRlpBlockSize is the EIP-7934 cap on len(rlp(block)): MAX_BLOCK_SIZE (10 MiB) - SAFETY_MARGIN (2 MiB).
	MaxRlpBlockSize = 8_388_608
	// MaxGossipPayloadSize is the consensus-layer gossip MAX_PAYLOAD_SIZE (10 MiB).
	MaxGossipPayloadSize = 10_485_760
)

// rawTxs is a list of opaque EIP-2718 transaction encodings. It encodes exactly like
// geth's types.Transactions (legacy txs as their RLP list, typed txs as an RLP string
// of the envelope) without having to decode transactions of possibly unknown types.
type rawTxs [][]byte

func (t rawTxs) Len() int                           { return len(t) }
func (t rawTxs) EncodeIndex(i int, w *bytes.Buffer) { w.Write(t[i]) }
func (t rawTxs) EncodeRLP(w io.Writer) error {
	buf := rlp.NewEncoderBuffer(w)
	list := buf.List()
	for _, tx := range t {
		if len(tx) > 0 && tx[0] >= 0xc0 {
			buf.Write(tx) // legacy tx: already an RLP list
		} else {
			buf.WriteBytes(tx) // typed tx: RLP string of type || payload
		}
	}
	buf.ListEnd(list)
	return buf.Flush()
}

// rlpBlock mirrors geth's extblock (core/types/block.go), the encoding measured by
// EIP-7934 and returned by eth_getBlockByNumber "size". The BAL is not part of it.
type rlpBlock struct {
	Header      *types.Header
	Txs         rawTxs
	Uncles      []*types.Header
	Withdrawals []*types.Withdrawal `rlp:"optional"`
}

// ExecutionBlockSize reconstructs the EL block from a beacon execution payload and
// returns len(rlp(block)) plus whether the reconstructed header hash matches the
// payload block hash. parentRoot is the parent beacon block root (Deneb+), requests
// the payload's execution requests (Electra+), and bal the raw RLP block access list
// (Gloas+). When the header cannot be fully reconstructed (e.g. a pruned BAL), the
// size is still exact as long as the header carries all fork fields, but the hash
// check fails.
func ExecutionBlockSize(payload *all.ExecutionPayload, parentRoot phase0.Root, requests *all.ExecutionRequests, bal []byte) (size uint64, hashMatch bool, err error) {
	header, txs, withdrawals := executionBlockFromPayload(payload, parentRoot, requests, bal)
	enc, err := rlp.EncodeToBytes(&rlpBlock{Header: header, Txs: txs, Withdrawals: withdrawals})
	if err != nil {
		return 0, false, err
	}
	return uint64(len(enc)), header.Hash() == common.Hash(payload.BlockHash), nil
}

// executionBlockFromPayload builds the EL header (geth types) and the raw body from a
// beacon execution payload, following engine.ExecutableDataToBlock.
func executionBlockFromPayload(payload *all.ExecutionPayload, parentRoot phase0.Root, requests *all.ExecutionRequests, bal []byte) (header *types.Header, txs rawTxs, withdrawals []*types.Withdrawal) {
	txs = make(rawTxs, len(payload.Transactions))
	for i, tx := range payload.Transactions {
		txs[i] = tx
	}

	baseFee := new(big.Int)
	if payload.BaseFeePerGas != nil {
		baseFee = payload.BaseFeePerGas.ToBig()
	} else {
		le := payload.BaseFeePerGasLE
		slices.Reverse(le[:])
		baseFee.SetBytes(le[:])
	}

	header = &types.Header{
		ParentHash:  common.Hash(payload.ParentHash),
		UncleHash:   types.EmptyUncleHash,
		Coinbase:    common.Address(payload.FeeRecipient),
		Root:        common.Hash(payload.StateRoot),
		TxHash:      types.DeriveSha(txs, trie.NewStackTrie(nil)),
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
		var balHash common.Hash // placeholder when the BAL is unavailable: same size, hash check fails
		if len(bal) > 0 {
			balHash = crypto.Keccak256Hash(bal)
		}
		slot := payload.SlotNumber
		header.BlockAccessListHash, header.SlotNumber = &balHash, &slot
	}
	return header, txs, withdrawals
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
				return // drop the type; the block hash check will then flag the header as unverified
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
