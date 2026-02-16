package txindexer

import (
	"bytes"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"github.com/ethpandaops/dora/blockdb"
	exerpc "github.com/ethpandaops/dora/clients/execution/rpc"
)

func normalizeHexPrefix(s string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		return s
	}
	return "0x" + s
}

func parseAddressKey(s string) common.Address {
	// common.HexToAddress tolerates non-0x? it expects 0x, so normalize.
	return common.HexToAddress(normalizeHexPrefix(s))
}

func parse32Key(s string) (out [32]byte) {
	h := common.HexToHash(normalizeHexPrefix(s))
	copy(out[:], h[:])
	return out
}

func to32Bytes(b []byte) (out [32]byte) {
	// Left-pad or truncate to 32 bytes (hash-style).
	if len(b) >= 32 {
		copy(out[:], b[len(b)-32:])
		return out
	}
	copy(out[32-len(b):], b)
	return out
}

func balanceBytes(a *exerpc.PrestateAccount) []byte {
	if a == nil || a.Balance == nil {
		return nil
	}
	b := a.Balance.ToInt().Bytes()
	if len(b) == 0 {
		return nil
	}
	return b
}

func nonceVal(a *exerpc.PrestateAccount) uint64 {
	if a == nil || a.Nonce == nil {
		return 0
	}
	return uint64(*a.Nonce)
}

func nonceValPresent(a *exerpc.PrestateAccount) (uint64, bool) {
	if a == nil || a.Nonce == nil {
		return 0, false
	}
	return uint64(*a.Nonce), true
}

func codeBytesPresent(a *exerpc.PrestateAccount) ([]byte, bool) {
	if a == nil {
		return nil, false
	}
	if a.Code == nil {
		return nil, false
	}
	out := make([]byte, len(a.Code))
	copy(out, a.Code)
	return out, true
}

func storageMap(a *exerpc.PrestateAccount) map[string][]byte {
	if a == nil || len(a.Storage) == 0 {
		return nil
	}
	m := make(map[string][]byte, len(a.Storage))
	for k, v := range a.Storage {
		if len(v) == 0 {
			continue
		}
		b := make([]byte, len(v))
		copy(b, v)
		m[k] = b
	}
	return m
}

// convertStateDiffToStateChanges normalizes the prestateTracer diffMode output
// into the canonical binary format input (accounts + slot diffs).
func convertStateDiffToStateChanges(diff *exerpc.StateDiff) []blockdb.StateChangeAccount {
	if diff == nil {
		return nil
	}

	preRaw := diff.Pre
	postRaw := diff.Post
	if preRaw == nil && postRaw == nil {
		return nil
	}

	// Normalize account maps to common.Address keys to avoid issues with
	// differing string representations (case / 0x prefix).
	pre := make(map[common.Address]exerpc.PrestateAccount, len(preRaw))
	for k, v := range preRaw {
		pre[parseAddressKey(k)] = v
	}
	post := make(map[common.Address]exerpc.PrestateAccount, len(postRaw))
	for k, v := range postRaw {
		post[parseAddressKey(k)] = v
	}

	// Union of touched accounts
	addrSet := make(map[common.Address]struct{}, len(pre)+len(post))
	for k := range pre {
		addrSet[k] = struct{}{}
	}
	for k := range post {
		addrSet[k] = struct{}{}
	}

	addrs := make([]common.Address, 0, len(addrSet))
	for k := range addrSet {
		addrs = append(addrs, k)
	}
	sort.Slice(addrs, func(i, j int) bool {
		return bytes.Compare(addrs[i][:], addrs[j][:]) < 0
	})

	out := make([]blockdb.StateChangeAccount, 0, len(addrs))

	for _, addr := range addrs {
		preAcc, preOk := pre[addr]
		postAcc, postOk := post[addr]

		created := !preOk && postOk
		killed := preOk && !postOk

		var prePtr, postPtr *exerpc.PrestateAccount
		if preOk {
			prePtr = &preAcc
		}
		if postOk {
			postPtr = &postAcc
		}

		preBal := balanceBytes(prePtr)
		postBal := balanceBytes(postPtr)
		preNonce, preNoncePresent := nonceValPresent(prePtr)
		postNonce, postNoncePresent := nonceValPresent(postPtr)
		preCode, preCodePresent := codeBytesPresent(prePtr)
		postCode, postCodePresent := codeBytesPresent(postPtr)
		preStor := storageMap(prePtr)
		postStor := storageMap(postPtr)

		var flags uint8
		if created {
			flags |= blockdb.StateChangeFlagAccountCreated
		}
		if killed {
			flags |= blockdb.StateChangeFlagAccountKilled
		}

		if !bytes.Equal(preBal, postBal) || (preOk != postOk && (len(preBal) > 0 || len(postBal) > 0)) {
			flags |= blockdb.StateChangeFlagBalanceChanged
		}
		// Nonce diffs: only consider changes when nonce was explicitly present in
		// the tracer output for BOTH sides (unless account was created/killed).
		// Many clients omit `nonce` in diffMode when unchanged; treating omitted as 0
		// causes false negative deltas (e.g. "decreased by 1").
		switch {
		case created:
			if postNoncePresent && (postNonce != 0 || preNoncePresent) {
				flags |= blockdb.StateChangeFlagNonceChanged
			}
		case killed:
			if preNoncePresent && preNonce != 0 {
				flags |= blockdb.StateChangeFlagNonceChanged
			}
		default:
			if preNoncePresent && postNoncePresent && preNonce != postNonce {
				// Sanity: nonces must not decrease.
				if postNonce >= preNonce {
					flags |= blockdb.StateChangeFlagNonceChanged
				}
			}
		}
		// Code diffs: only consider changes when code was explicitly present in the
		// tracer output for BOTH sides (unless account was created/killed). Many
		// clients omit `code` in diffMode when unchanged; treating omitted as empty
		// causes false "code removed" entries for every contract call.
		switch {
		case created:
			// If account was created, allow post-only code snapshot as a change.
			if postCodePresent && (len(postCode) > 0 || preCodePresent) {
				flags |= blockdb.StateChangeFlagCodeChanged
			}
		case killed:
			// If account was killed, allow pre-only code snapshot as a change.
			if preCodePresent && len(preCode) > 0 {
				flags |= blockdb.StateChangeFlagCodeChanged
			}
		default:
			if preCodePresent && postCodePresent && !bytes.Equal(preCode, postCode) {
				flags |= blockdb.StateChangeFlagCodeChanged
			}
		}

		// Storage diffs
		slotSet := make(map[string]struct{}, 0)
		for k := range preStor {
			slotSet[k] = struct{}{}
		}
		for k := range postStor {
			slotSet[k] = struct{}{}
		}

		slots := make([]string, 0, len(slotSet))
		for k := range slotSet {
			slots = append(slots, k)
		}
		sort.Slice(slots, func(i, j int) bool {
			si := parse32Key(slots[i])
			sj := parse32Key(slots[j])
			return bytes.Compare(si[:], sj[:]) < 0
		})

		slotDiffs := make([]blockdb.StateChangeSlot, 0, len(slots))
		for _, sk := range slots {
			preV, preHas := preStor[sk]
			postV, postHas := postStor[sk]

			// Normalize to 32-byte words
			preWord := to32Bytes(preV)
			postWord := to32Bytes(postV)

			switch {
			case preHas && postHas:
				if bytes.Equal(preWord[:], postWord[:]) {
					continue
				}
				slotDiffs = append(slotDiffs, blockdb.StateChangeSlot{
					Slot:       parse32Key(sk),
					ChangeType: blockdb.StorageChangeModified,
					PreValue:   preWord,
					PostValue:  postWord,
				})
			case !preHas && postHas:
				slotDiffs = append(slotDiffs, blockdb.StateChangeSlot{
					Slot:       parse32Key(sk),
					ChangeType: blockdb.StorageChangeCreated,
					PostValue:  postWord,
				})
			case preHas && !postHas:
				slotDiffs = append(slotDiffs, blockdb.StateChangeSlot{
					Slot:       parse32Key(sk),
					ChangeType: blockdb.StorageChangeDeleted,
					PreValue:   preWord,
				})
			}
		}

		if len(slotDiffs) > 0 {
			flags |= blockdb.StateChangeFlagStorageChanged
		}

		// Drop accounts with no effective changes.
		if flags == 0 {
			continue
		}

		var acc blockdb.StateChangeAccount
		copy(acc.Address[:], addr[:])
		acc.Flags = flags

		if flags&blockdb.StateChangeFlagBalanceChanged != 0 {
			// If account created/killed, missing side is encoded as len=0 (nil slice).
			acc.PreBalance = preBal
			acc.PostBalance = postBal
		}
		if flags&blockdb.StateChangeFlagNonceChanged != 0 {
			acc.PreNonce = preNonce
			acc.PostNonce = postNonce
		}
		if flags&blockdb.StateChangeFlagCodeChanged != 0 {
			acc.PreCode = preCode
			acc.PostCode = postCode
		}
		if flags&blockdb.StateChangeFlagStorageChanged != 0 {
			acc.Slots = slotDiffs
		}

		out = append(out, acc)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}
