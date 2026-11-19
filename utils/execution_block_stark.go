package utils

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/bellatrix"
	"github.com/ethpandaops/spamoor/txtypes"
)

// StarkCommitment is what an EIP-8288 block header commits to: the recursive STARK
// carried next to the transactions, and the dependencies that proof has to cover.
//
// Neither value travels in the beacon block. Both are derived from the payload - the
// proof hash from the proof, the dependency digest from the frame transactions - and
// enter the header, so the payload's block hash is what confirms them.
type StarkCommitment struct {
	// ProofHash is keccak256 of the proof bytes, the header's stark_proof_hash.
	ProofHash common.Hash
	// DepsHash is the digest of the block's dependencies, the header's block_deps_hash.
	DepsHash common.Hash
	// Declarations lists what each transaction declares, in block order.
	Declarations []*StarkDeclaration
	// Dependencies is the sorted, deduplicated union the proof covers.
	Dependencies []txtypes.FrameDependency
	// TxError is set when a transaction could not be decoded, which leaves the
	// dependency digest incomplete.
	TxError error
}

// StarkDeclaration is one dependency as a transaction declares it.
type StarkDeclaration struct {
	TxIndex uint32
	TxHash  common.Hash
	// Dependency is the resolved triple: it carries the transaction's signature hash
	// where the frame carries zero, which SignsTransaction records.
	Dependency       txtypes.FrameDependency
	SignsTransaction bool
}

// newStarkCommitment derives the header commitments of an EIP-8288 payload.
func newStarkCommitment(payload *all.ExecutionPayload) *StarkCommitment {
	commitment := &StarkCommitment{ProofHash: crypto.Keccak256Hash(payload.RecursiveStarkProof)}

	for i, raw := range payload.Transactions {
		// Only frame transactions declare dependencies, and nothing else needs decoding.
		if len(raw) == 0 || raw[0] != txtypes.FrameTxType {
			continue
		}

		tx, err := txtypes.DecodeTx(raw)
		if err != nil {
			commitment.TxError = fmt.Errorf("cannot decode tx %d: %w", i, err)

			continue
		}

		frameTx, ok := tx.Inner().(*txtypes.FrameTx)
		if !ok || frameTx.DependencyCount() == 0 {
			continue
		}

		deps, err := frameTx.Dependencies()
		if err != nil {
			commitment.TxError = fmt.Errorf("cannot resolve the dependencies of tx %d: %w", i, err)

			continue
		}

		// A dependency over the transaction's own signature hash is declared with a zero
		// data hash, which the resolved list no longer shows.
		var sigHash common.Hash

		for _, frame := range frameTx.Frames {
			for _, dep := range frame.Dependencies() {
				if dep.SignsTransaction() && sigHash == (common.Hash{}) {
					sigHash, _ = frameTx.SigHash()
				}
			}
		}

		for _, dep := range deps {
			commitment.Declarations = append(commitment.Declarations, &StarkDeclaration{
				TxIndex:          uint32(i),
				TxHash:           tx.Hash(),
				Dependency:       dep,
				SignsTransaction: sigHash != (common.Hash{}) && dep.DataHash == sigHash,
			})
			commitment.Dependencies = append(commitment.Dependencies, dep)
		}
	}

	commitment.Dependencies = txtypes.CanonicalDependencies(commitment.Dependencies)
	commitment.DepsHash = txtypes.DependenciesHash(commitment.Dependencies)

	return commitment
}

// headerRLP encodes the block header with the commitments in place. They follow the
// block access list hash and the slot number, which a header that has neither still
// encodes as empty items to keep the position of what comes after them.
func (c *StarkCommitment) headerRLP(header *types.Header) (rlp.RawValue, error) {
	encoded, err := rlp.EncodeToBytes(header)
	if err != nil {
		return nil, err
	}

	var fields []rlp.RawValue
	if err := rlp.DecodeBytes(encoded, &fields); err != nil {
		return nil, err
	}

	if header.BlockAccessListHash == nil {
		fields = append(fields, rlp.EmptyString)
	}

	if header.SlotNumber == nil {
		fields = append(fields, rlp.EmptyString)
	}

	for _, hash := range []common.Hash{c.ProofHash, c.DepsHash} {
		item, err := rlp.EncodeToBytes(hash)
		if err != nil {
			return nil, err
		}

		fields = append(fields, item)
	}

	return rlp.EncodeToBytes(fields)
}

// blockSize is len(rlp(block)) of an EIP-8288 block, whose body ends with the proof.
func (c *StarkCommitment) blockSize(headerRLP rlp.RawValue, txs []bellatrix.Transaction, withdrawals []*types.Withdrawal, proof []byte) (uint64, error) {
	encWithdrawals, err := rlp.EncodeToBytes(withdrawals)
	if err != nil {
		return 0, err
	}

	encProof, err := rlp.EncodeToBytes(proof)
	if err != nil {
		return 0, err
	}

	encTxs, err := encodeRawTxs(txs)
	if err != nil {
		return 0, err
	}

	encTxList, err := rlp.EncodeToBytes(encTxs)
	if err != nil {
		return 0, err
	}

	enc, err := rlp.EncodeToBytes([]rlp.RawValue{headerRLP, encTxList, rlp.EmptyList, encWithdrawals, encProof})

	return uint64(len(enc)), err
}
