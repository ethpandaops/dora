package utils

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/bellatrix"
	"github.com/ethpandaops/go-eth2-client/spec/capella"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/holiman/uint256"
	dynssz "github.com/pk910/dynamic-ssz"
)

// testPayload builds a synthetic payload of the given fork plus the reference geth
// block it must encode to. The reference is assembled independently with geth's own
// decoded transaction types and a hand-encoded EIP-7685 request list.
func testPayload(t *testing.T, version spec.DataVersion) (*all.ExecutionPayload, phase0.Root, *all.ExecutionRequests, []byte, *types.Block) {
	t.Helper()
	key, _ := crypto.GenerateKey()
	chainID := big.NewInt(7)
	to := common.HexToAddress("0x1111111111111111111111111111111111111111")
	signer := types.LatestSignerForChainID(chainID)
	txs := []*types.Transaction{
		types.MustSignNewTx(key, signer, &types.LegacyTx{Nonce: 0, GasPrice: big.NewInt(10), Gas: 21000, To: &to, Value: big.NewInt(1), Data: bytes.Repeat([]byte{0xab}, 300)}),
		types.MustSignNewTx(key, signer, &types.DynamicFeeTx{ChainID: chainID, Nonce: 1, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(20), Gas: 50000, To: &to, Data: bytes.Repeat([]byte{0xcd}, 70)}),
	}

	p := &all.ExecutionPayload{
		Version:      version,
		ParentHash:   phase0.Hash32{0x01},
		FeeRecipient: bellatrix.ExecutionAddress{0x02},
		StateRoot:    phase0.Root{0x03},
		ReceiptsRoot: phase0.Root{0x04},
		PrevRandao:   [32]byte{0x05},
		BlockNumber:  1234,
		GasLimit:     60_000_000,
		GasUsed:      71_000,
		Timestamp:    1_700_000_000,
		ExtraData:    []byte("dora"),
	}
	p.LogsBloom[7] = 0x80
	for _, tx := range txs {
		enc, _ := tx.MarshalBinary()
		p.Transactions = append(p.Transactions, enc)
	}
	if version >= spec.DataVersionDeneb {
		p.BaseFeePerGas = uint256.NewInt(7)
	} else {
		p.BaseFeePerGasLE[0] = 7 // little endian
	}

	header := &types.Header{
		ParentHash: common.Hash(p.ParentHash), UncleHash: types.EmptyUncleHash, Coinbase: common.Address(p.FeeRecipient),
		Root: common.Hash(p.StateRoot), TxHash: types.DeriveSha(types.Transactions(txs), trie.NewStackTrie(nil)),
		ReceiptHash: common.Hash(p.ReceiptsRoot), Bloom: types.Bloom(p.LogsBloom), Difficulty: common.Big0,
		Number: big.NewInt(1234), GasLimit: p.GasLimit, GasUsed: p.GasUsed, Time: p.Timestamp, Extra: p.ExtraData,
		MixDigest: common.Hash(p.PrevRandao), BaseFee: big.NewInt(7),
	}
	body := types.Body{Transactions: txs}

	parentRoot := phase0.Root{0x06}
	requests := &all.ExecutionRequests{Version: version}
	var bal []byte
	if version >= spec.DataVersionCapella {
		p.Withdrawals = []*capella.Withdrawal{{Index: 5, ValidatorIndex: 9, Address: bellatrix.ExecutionAddress{0x0a}, Amount: 32_000_000_000}}
		body.Withdrawals = []*types.Withdrawal{{Index: 5, Validator: 9, Address: common.Address{0x0a}, Amount: 32_000_000_000}}
		h := types.DeriveSha(types.Withdrawals(body.Withdrawals), trie.NewStackTrie(nil))
		header.WithdrawalsHash = &h
	}
	if version >= spec.DataVersionDeneb {
		p.BlobGasUsed, p.ExcessBlobGas = 131072, 42
		beaconRoot := common.Hash(parentRoot)
		header.BlobGasUsed, header.ExcessBlobGas, header.ParentBeaconRoot = &p.BlobGasUsed, &p.ExcessBlobGas, &beaconRoot
	}
	if version >= spec.DataVersionElectra {
		requests.Deposits = []*electra.DepositRequest{{Pubkey: phase0.BLSPubKey{0x0b}, WithdrawalCredentials: make([]byte, 32), Amount: 1_000_000_000, Index: 3}}
		// deposit request SSZ: pubkey(48) | withdrawal_credentials(32) | amount(8 LE) | signature(96) | index(8 LE)
		dep := append([]byte{0x00, 0x0b}, make([]byte, 47+32)...)
		dep = binary.LittleEndian.AppendUint64(dep, 1_000_000_000)
		dep = binary.LittleEndian.AppendUint64(append(dep, make([]byte, 96)...), 3)
		el := [][]byte{dep}
		if version >= spec.DataVersionGloas {
			requests.BuilderExits = []*gloas.BuilderExitRequest{{SourceAddress: bellatrix.ExecutionAddress{0x0c}, Pubkey: phase0.BLSPubKey{0x0d}}}
			exit := append(append([]byte{0x04, 0x0c}, make([]byte, 19)...), append([]byte{0x0d}, make([]byte, 47)...)...)
			el = append(el, exit)
		}
		h := types.CalcRequestsHash(el)
		header.RequestsHash = &h
	}
	if version >= spec.DataVersionGloas {
		bal = append([]byte{0xf8, 0x40}, bytes.Repeat([]byte{0x80}, 0x40)...)
		p.BlockAccessList = bal
		p.SlotNumber = 99
		balHash := crypto.Keccak256Hash(bal)
		header.BlockAccessListHash, header.SlotNumber = &balHash, &p.SlotNumber
	}

	block := types.NewBlockWithHeader(header).WithBody(body)
	p.BlockHash = phase0.Hash32(block.Hash())
	return p, parentRoot, requests, bal, block
}

func TestExecutionBlockFromPayload(t *testing.T) {
	for _, version := range []spec.DataVersion{spec.DataVersionBellatrix, spec.DataVersionCapella, spec.DataVersionDeneb, spec.DataVersionElectra, spec.DataVersionFulu, spec.DataVersionGloas} {
		p, parentRoot, requests, bal, ref := testPayload(t, version)
		eb := ExecutionBlockFromPayload(p, parentRoot, requests, bal)
		if eb.Block == nil {
			t.Fatalf("%v: no block: %v", version, eb.TxError)
		}
		if !eb.HashMatch || eb.Block.Hash() != ref.Hash() {
			t.Errorf("%v: reconstructed header hash does not match block hash", version)
		}
		got, _ := rlp.EncodeToBytes(eb.Block)
		want, _ := rlp.EncodeToBytes(ref)
		if !bytes.Equal(got, want) || eb.Block.Size() != ref.Size() {
			t.Errorf("%v: rlp mismatch: size %d, want %d", version, eb.Block.Size(), ref.Size())
		}
		if raw, err := rawBlockSize(eb.Header, p.Transactions, ref.Withdrawals()); err != nil || raw != ref.Size() || eb.Size != ref.Size() {
			t.Errorf("%v: raw-tx size %d / Size %d, want %d (err %v)", version, raw, eb.Size, ref.Size(), err)
		}

		if version >= spec.DataVersionGloas {
			// A pruned BAL keeps the size exact but cannot verify the header.
			pruned := ExecutionBlockFromPayload(p, parentRoot, requests, nil)
			if pruned.HashMatch || pruned.Block.Size() != ref.Size() {
				t.Errorf("pruned BAL: size %d (want %d), hashMatch %v (want false)", pruned.Block.Size(), ref.Size(), pruned.HashMatch)
			}
			// The BAL is not part of the EIP-7934 block size.
			bigger := append(bal, bytes.Repeat([]byte{0x80}, 1000)...)
			if size := ExecutionBlockFromPayload(p, parentRoot, requests, bigger).Block.Size(); size != ref.Size() {
				t.Errorf("BAL leaked into block size: %d != %d", size, ref.Size())
			}
		}
	}
}

func TestExecutionBlockUnknownTxType(t *testing.T) {
	p, parentRoot, requests, bal, ref := testPayload(t, spec.DataVersionGloas)
	p.Transactions = append(p.Transactions, bellatrix.Transaction{0x7e, 0xc0}) // tx type go-ethereum does not know
	eb := ExecutionBlockFromPayload(p, parentRoot, requests, bal)
	if eb.Block != nil || eb.TxError == nil {
		t.Fatalf("expected a tx decode error, got block %v err %v", eb.Block != nil, eb.TxError)
	}
	if eb.Header == nil || eb.Header.TxHash == ref.TxHash() {
		t.Fatal("header must still be built, with the unknown tx in its transactions root")
	}
	// Size still comes from the raw tx bytes: one extra RLP string 0x82 0x7e 0xc0.
	if eb.Size != ref.Size()+3 {
		t.Errorf("unknown tx type: size %d, want %d", eb.Size, ref.Size()+3)
	}
}

func TestExecutionRequestsListEmpty(t *testing.T) {
	// No requests -> requests_hash = sha256(""), as for an empty Prague block.
	if h := types.CalcRequestsHash(ExecutionRequestsList(&all.ExecutionRequests{})); h != common.Hash(sha256.Sum256(nil)) {
		t.Fatalf("unexpected empty requests hash %x", h)
	}
}

func TestEnvelopeSSZSize(t *testing.T) {
	p, parentRoot, requests, bal, _ := testPayload(t, spec.DataVersionGloas)
	env := &all.SignedExecutionPayloadEnvelope{
		Version: spec.DataVersionGloas,
		Message: &all.ExecutionPayloadEnvelope{Version: spec.DataVersionGloas, Payload: p, ExecutionRequests: requests, ParentBeaconBlockRoot: parentRoot},
	}
	ds := dynssz.GetGlobalDynSsz()
	enc, err := ds.MarshalSSZ(env)
	if err != nil {
		t.Fatal(err)
	}
	size, err := EnvelopeSSZSize(ds, env, bal)
	if err != nil || size != uint64(len(enc)) {
		t.Fatalf("size %d (err %v), marshalled %d", size, err, len(enc))
	}

	// Stored envelope with a pruned BAL: the preserved BAL is substituted back.
	p.BlockAccessList = nil
	if pruned, _ := EnvelopeSSZSize(ds, env, bal); pruned != size {
		t.Fatalf("pruned envelope size %d, want %d", pruned, size)
	}
	if p.BlockAccessList != nil {
		t.Fatal("EnvelopeSSZSize mutated the envelope")
	}
}

func TestBeaconBlockSSZSize(t *testing.T) {
	// The slot page sizes the beacon block via DynSsz.SizeSSZ; it must equal the
	// encoded length, for blocks with an inline payload (Fulu) and without (Gloas).
	ds := dynssz.GetGlobalDynSsz()
	fuluPayload, _, fuluRequests, _, _ := testPayload(t, spec.DataVersionFulu)
	for _, block := range []*all.SignedBeaconBlock{
		{Version: spec.DataVersionFulu, Message: &all.BeaconBlock{Version: spec.DataVersionFulu, Body: &all.BeaconBlockBody{
			Version: spec.DataVersionFulu, Graffiti: [32]byte{0x01}, ExecutionPayload: fuluPayload, ExecutionRequests: fuluRequests,
		}}},
		{Version: spec.DataVersionGloas, Message: &all.BeaconBlock{Version: spec.DataVersionGloas, Body: &all.BeaconBlockBody{
			Version: spec.DataVersionGloas, Graffiti: [32]byte{0x02},
		}}},
	} {
		enc, err := ds.MarshalSSZ(block)
		if err != nil {
			t.Fatalf("%v: %v", block.Version, err)
		}
		if size, err := ds.SizeSSZ(block); err != nil || size != len(enc) {
			t.Fatalf("%v: size %d (err %v), marshalled %d", block.Version, size, err, len(enc))
		}
	}
}
