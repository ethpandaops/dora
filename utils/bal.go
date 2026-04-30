package utils

import (
	"github.com/ethereum/go-ethereum/rlp"
)

// EIP-7928 Block Access List (BAL) types for RLP decoding.
// These use variable-length []byte for uint256 fields (balance, storage key/value)
// to match the EIP-7928 spec, which uses standard RLP variable-length encoding.
type (
	BALStorageWrite struct {
		TxIdx      uint16
		ValueAfter []byte
	}
	BALSlotWrites struct {
		Slot     []byte
		Accesses []BALStorageWrite
	}
	BALBalanceChange struct {
		TxIdx   uint16
		Balance []byte
	}
	BALNonceChange struct {
		TxIdx uint16
		Nonce uint64
	}
	BALCodeChange struct {
		TxIndex uint16
		Code    []byte
	}
	BALAccountAccess struct {
		Address        [20]byte
		StorageWrites  []BALSlotWrites
		StorageReads   [][]byte
		BalanceChanges []BALBalanceChange
		NonceChanges   []BALNonceChange
		CodeChanges    []BALCodeChange
	}
)

// DecodeBlockAccessList decodes an EIP-7928 block access list from RLP bytes.
func DecodeBlockAccessList(data []byte) ([]BALAccountAccess, error) {
	var accesses []BALAccountAccess
	if err := rlp.DecodeBytes(data, &accesses); err != nil {
		return nil, err
	}
	return accesses, nil
}
