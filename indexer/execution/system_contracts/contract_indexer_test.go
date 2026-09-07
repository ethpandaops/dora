package system_contracts

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethpandaops/spamoor/txtypes"
	"github.com/holiman/uint256"
)

// TestTxRecipient checks the recipient lookup used by the contract-event callbacks.
// Transactions that have no recipient of their own must fall back to the emitting
// contract instead of dereferencing a nil pointer or reporting a target that is not the
// transaction's.
func TestTxRecipient(t *testing.T) {
	contract := common.HexToAddress("0x00000000219ab540356cBB839Cbe05303d7705Fa")
	log := &types.Log{Address: contract}

	// Contract-creation transaction (To is nil): falls back to the emitter.
	createTx := txtypes.NewTx(&txtypes.DynamicFeeTx{Nonce: 0, To: nil, Value: big.NewInt(0)})
	if got := txRecipient(createTx, log); got != contract {
		t.Errorf("creation tx recipient = %x, want %x", got, contract)
	}

	// Normal call transaction: uses its recipient.
	to := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	callTx := txtypes.NewTx(&txtypes.DynamicFeeTx{To: &to})
	if got := txRecipient(callTx, log); got != to {
		t.Errorf("call tx recipient = %x, want %x", got, to)
	}

	// Frame transaction: reports the first SENDER frame's target, which is one of
	// several and not the transaction's own recipient, so the emitter wins.
	frameTarget := common.HexToAddress("0x30592ef78d262bc79f0fe46355e07a51d685e382")
	frameTx := txtypes.NewTx(&txtypes.FrameTx{
		Frames: []*txtypes.Frame{{
			Mode:   txtypes.FrameModeSender,
			Target: &frameTarget,
			Value:  uint256.NewInt(0),
		}},
	})

	if got := frameTx.To(); got == nil || *got != frameTarget {
		t.Fatalf("frame tx To() = %v, want the first SENDER frame target", got)
	}

	if got := txRecipient(frameTx, log); got != contract {
		t.Errorf("frame tx recipient = %x, want %x", got, contract)
	}
}

// TestApplyQueueDequeues checks the activation-aware queue drain: nothing dequeues while the
// activation block is unknown or before it, and only the post-activation part of a block range
// counts.
func TestApplyQueueDequeues(t *testing.T) {
	ci := newContractIndexer(nil, nil, &contractIndexerOptions[struct{}]{dequeueRate: 2})

	tests := []struct {
		name            string
		queueLength     uint64
		fromBlock       uint64
		toBlock         uint64
		activationBlock uint64
		activationKnown bool
		want            uint64
	}{
		{"activation unknown keeps queue", 10, 5, 20, 0, false, 10},
		{"range fully before activation", 10, 5, 7, 8, true, 10},
		{"range straddling activation", 10, 5, 10, 8, true, 4},   // blocks 8-10 dequeue 3*2
		{"range fully after activation", 10, 10, 10, 8, true, 8}, // block 10 dequeues 2
		{"drain clamps at zero", 3, 8, 20, 8, true, 0},
		{"no activation gate dequeues everywhere", 10, 5, 7, 0, true, 4}, // blocks 5-7 dequeue 3*2
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ci.applyQueueDequeues(tt.queueLength, tt.fromBlock, tt.toBlock, tt.activationBlock, tt.activationKnown)
			if got != tt.want {
				t.Errorf("applyQueueDequeues() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCalculateDequeueBlock checks the dequeue block assignment: requests logged before the
// activation block dequeue from the activation block onwards, and get the 0 sentinel while the
// activation block is not known yet.
func TestCalculateDequeueBlock(t *testing.T) {
	ci := newContractIndexer(nil, nil, &contractIndexerOptions[struct{}]{dequeueRate: 2})

	tests := []struct {
		name            string
		logBlock        uint64
		queueLength     uint64
		activationBlock uint64
		activationKnown bool
		want            uint64
	}{
		{"activation unknown yields sentinel", 5, 3, 0, false, 0},
		{"pre-activation log dequeues from activation", 5, 3, 8, true, 9},
		{"post-activation log dequeues from itself", 10, 5, 8, true, 12},
		{"empty queue dequeues in own block", 10, 0, 8, true, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ci.calculateDequeueBlock(tt.logBlock, tt.queueLength, tt.activationBlock, tt.activationKnown)
			if got != tt.want {
				t.Errorf("calculateDequeueBlock() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestComputeRebasedDequeueBlocks checks the one-time dequeue rebase: finalized rows enqueued
// before the activation block are replayed into exact activation-based dequeue blocks, stale
// non-finalized rows are parked behind the finalized backlog, and the remaining queue length is
// reseeded.
func TestComputeRebasedDequeueBlocks(t *testing.T) {
	ci := newContractIndexer(nil, nil, &contractIndexerOptions[struct{}]{dequeueRate: 2})

	t.Run("pre-activation backlog", func(t *testing.T) {
		// 5 finalized requests queued before activation block 100, crawled up to block 90; one
		// stale row from a non-finalized fork
		rows := []*dequeueRebaseRow{
			{blockNumber: 10, blockIndex: 0, forkId: 0, dequeueBlock: 10},
			{blockNumber: 10, blockIndex: 1, forkId: 0, dequeueBlock: 10},
			{blockNumber: 10, blockIndex: 2, forkId: 0, dequeueBlock: 11},
			{blockNumber: 15, blockIndex: 0, forkId: 5, dequeueBlock: 15},
			{blockNumber: 20, blockIndex: 0, forkId: 0, dequeueBlock: 20},
			{blockNumber: 50, blockIndex: 0, forkId: 0, dequeueBlock: 0},
		}

		updates, queueLength := ci.computeRebasedDequeueBlocks(rows, 100, 90)

		if queueLength != 5 {
			t.Errorf("queue length = %v, want 5", queueLength)
		}

		wantDequeues := map[string]uint64{
			"10:0": 100, "10:1": 100, "10:2": 101, "20:0": 101, "50:0": 102, // finalized backlog
			"15:0": 102, // stale fork row parked behind the 5 finalized requests
		}
		if len(updates) != len(wantDequeues) {
			t.Errorf("updates = %v rows, want %v", len(updates), len(wantDequeues))
		}
		for _, row := range updates {
			key := fmt.Sprintf("%v:%v", row.blockNumber, row.blockIndex)
			if want, ok := wantDequeues[key]; !ok || row.dequeueBlock != want {
				t.Errorf("row %v dequeue block = %v, want %v", key, row.dequeueBlock, want)
			}
		}
	})

	t.Run("crawl already past activation", func(t *testing.T) {
		// 3 requests queued before activation block 100, crawled up to block 105: the whole
		// backlog dequeues in blocks 100-101 and the queue is empty again
		rows := []*dequeueRebaseRow{
			{blockNumber: 10, blockIndex: 0, forkId: 0, dequeueBlock: 10},
			{blockNumber: 10, blockIndex: 1, forkId: 0, dequeueBlock: 10},
			{blockNumber: 10, blockIndex: 2, forkId: 0, dequeueBlock: 10},
		}

		updates, queueLength := ci.computeRebasedDequeueBlocks(rows, 100, 105)

		if queueLength != 0 {
			t.Errorf("queue length = %v, want 0", queueLength)
		}
		if len(updates) != 3 {
			t.Fatalf("updates = %v rows, want 3", len(updates))
		}
		for idx, want := range []uint64{100, 100, 101} {
			if updates[idx].dequeueBlock != want {
				t.Errorf("row %v dequeue block = %v, want %v", idx, updates[idx].dequeueBlock, want)
			}
		}
	})
}
