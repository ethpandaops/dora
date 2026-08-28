package txindexer

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	bdbtypes "github.com/ethpandaops/dora/blockdb/types"
	exerpc "github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/spamoor/txtypes"
	"github.com/sirupsen/logrus"
)

// pendingFrame is one frame of an EIP-8141 frame transaction, paired with the result the
// receipt reports for it.
//
// A frame transaction is an ordered list of calls rather than a single one, so the fields
// el_transactions holds for an ordinary transaction - a recipient, a value, a gas limit,
// a status - exist once per frame here.
type pendingFrame struct {
	index uint16
	mode  uint8
	flags uint8

	// target is the frame's resolved recipient: frames declare no target to address the
	// transaction's sender.
	target    common.Address
	toAccount *pendingAccount

	value    *big.Int
	dataLen  uint32
	methodID []byte

	execGasLimit  uint64
	stateGasLimit uint64

	// hasResult reports whether the receipt carried a result for this frame. A client
	// that reports fewer results than there are frames leaves the rest without one,
	// rather than borrowing another frame's.
	hasResult    bool
	status       uint64
	execGasUsed  uint64
	stateGasUsed uint64
	logCount     uint32

	// rolledBack marks a frame whose atomic batch was undone. Such a frame may still
	// report success, but nothing it did survived.
	rolledBack bool
}

// storedStatus is the frame's result as it is recorded.
//
// A client that reported no result for the frame leaves it neither succeeded nor failed,
// which the sentinel says and a zero status would not - zero is how a failure is spelled.
func (f *pendingFrame) storedStatus() uint8 {
	if !f.hasResult {
		return bdbtypes.FrameStatusUnknown
	}

	return uint8(f.status)
}

// frameReceiptData renders the frames as the receipt content stored in blockdb, or nil
// for a transaction that has none. It is what keeps a frame transaction legible after its
// relational rows have been pruned.
func frameReceiptData(frames []*pendingFrame, payer common.Address) *bdbtypes.FrameReceiptData {
	if len(frames) == 0 {
		return nil
	}

	data := &bdbtypes.FrameReceiptData{
		Payer:  payer,
		Frames: make([]bdbtypes.FrameReceiptEntry, 0, len(frames)),
	}

	for _, frame := range frames {
		data.Frames = append(data.Frames, bdbtypes.FrameReceiptEntry{
			Status:       frame.storedStatus(),
			ExecGasUsed:  frame.execGasUsed,
			StateGasUsed: frame.stateGasUsed,
			LogCount:     frame.logCount,
		})
	}

	return data
}

// isAtomicBatch reports whether the frame is batched with the frames that follow it.
func (f *pendingFrame) isAtomicBatch() bool {
	return f.flags&txtypes.AtomicBatchFlag != 0
}

// executed reports whether the frame ran at all. A skipped frame never did, and a frame
// with no reported result cannot be claimed to have.
func (f *pendingFrame) executed() bool {
	return f.hasResult && f.status != txtypes.FrameStatusSkipped
}

// succeeded reports whether the frame's effects are durable: it ran, it reported success,
// and its atomic batch was not rolled back afterwards.
func (f *pendingFrame) succeeded() bool {
	return f.hasResult && f.status == txtypes.FrameStatusSuccess && !f.rolledBack
}

// resolveFrames builds the per-frame view of a frame transaction, pairing each frame with
// the receipt's result for it and ensuring an account for its target.
func (ctx *txProcessingContext) resolveFrames(
	frameTx *txtypes.FrameTx,
	receipt *txtypes.Receipt,
	fromAccount *pendingAccount,
) []*pendingFrame {
	extra := receipt.FrameExtra()

	// EIP-8141 caps a transaction at MaxFrames, and a block carrying more than that is
	// not valid. The stored frame list is bounded by the same cap, so trusting the count
	// anyway would fail to encode.
	txFrames := frameTx.Frames
	if len(txFrames) > txtypes.MaxFrames {
		ctx.indexer.logger.WithFields(logrus.Fields{
			"frames": len(txFrames),
			"cap":    txtypes.MaxFrames,
		}).Warn("transaction reports more frames than the protocol allows, indexing the first ones only")

		txFrames = txFrames[:txtypes.MaxFrames]
	}

	frames := make([]*pendingFrame, 0, len(txFrames))

	for i, frame := range txFrames {
		target := frame.ResolvedTarget(frameTx.Sender)

		pending := &pendingFrame{
			index:         uint16(i),
			mode:          uint8(frame.Mode),
			flags:         frame.Flags,
			target:        target,
			toAccount:     ctx.ensureAccount(target, fromAccount, false),
			value:         new(big.Int),
			dataLen:       uint32(len(frame.Data)),
			execGasLimit:  frame.Limits.Execution,
			stateGasLimit: frame.Limits.State,
		}

		if frame.Value != nil {
			pending.value = frame.Value.ToBig()
		}

		if len(frame.Data) >= 4 {
			pending.methodID = frame.Data[:4]
		}

		// The receipt reports one result per frame, in frame order.
		if extra != nil && i < len(extra.Frames) {
			result := extra.Frames[i]
			pending.hasResult = true
			pending.status = result.Status
			pending.execGasUsed = result.ExecutionGas
			pending.stateGasUsed = result.StateGas
			pending.logCount = uint32(len(result.Logs))
		}

		frames = append(frames, pending)
	}

	markRolledBackBatches(frames)

	return frames
}

// markRolledBackBatches flags the frames whose effects were undone.
//
// An atomic batch is a maximal run of frames in which every frame but the last carries
// the batch flag. When one frame of a batch fails the whole batch rolls back: the frames
// after it are reported as skipped, but the ones before it keep the success status and
// execution gas they earned, with only their logs discarded and their state gas zeroed.
// Their state changes are gone all the same, so a success inside a rolled-back batch must
// not be read as having moved anything.
func markRolledBackBatches(frames []*pendingFrame) {
	batchStart := 0

	for i, frame := range frames {
		if frame.isAtomicBatch() && i+1 < len(frames) {
			continue
		}

		batch := frames[batchStart : i+1]
		batchStart = i + 1

		failed := false

		for _, member := range batch {
			if member.hasResult && member.status == txtypes.FrameStatusFailed {
				failed = true

				break
			}
		}

		if !failed {
			continue
		}

		for _, member := range batch {
			member.rolledBack = true
		}
	}
}

// aggregateFrames builds the per-account internal-transaction aggregates of a frame
// transaction from its frames.
//
// The frames come from the receipt, so this is what a frame transaction's account
// activity is built from whether or not tracing runs. A call trace contributes only the
// calls made from within the frames; its own roots are the frames again and are left out
// of it, so nothing is counted twice.
//
// Every frame is attributed to the transaction's sender as caller. DEFAULT and VERIFY
// frames are entered by the ENTRY_POINT predeploy rather than by the sender, but that
// address is a protocol placeholder that never holds code, and recording it as a
// participant would collect every frame transaction on the chain onto one account.
func (ctx *txProcessingContext) aggregateFrames(
	frames []*pendingFrame,
	senderAccount *pendingAccount,
) map[*pendingAccount]*pendingInternalAggregate {
	aggregates := make(map[*pendingAccount]*pendingInternalAggregate, len(frames)+1)

	getAgg := func(account *pendingAccount) *pendingInternalAggregate {
		agg, ok := aggregates[account]
		if !ok {
			agg = &pendingInternalAggregate{account: account}
			aggregates[account] = agg
		}

		return agg
	}

	for _, frame := range frames {
		// A frame that never ran touched nothing.
		if !frame.executed() {
			continue
		}

		gasUsed := frame.execGasUsed + frame.stateGasUsed

		// A rolled-back frame spent its gas but moved no value.
		value := 0.0
		if frame.succeeded() && frame.value.Sign() > 0 {
			value = weiToFloat(frame.value, 18)
		}

		if frame.toAccount == senderAccount {
			// The frame addresses the sender, which is both caller and callee.
			agg := getAgg(senderAccount)
			agg.inCount++
			agg.outCount++
			agg.callTypeMask |= 1 << bdbtypes.CallTypeFrame
			agg.valueIn += value
			agg.valueOut += value
			agg.gasUsed += gasUsed

			continue
		}

		fromAgg := getAgg(senderAccount)
		fromAgg.outCount++
		fromAgg.valueOut += value
		fromAgg.gasUsed += gasUsed

		toAgg := getAgg(frame.toAccount)
		toAgg.inCount++
		toAgg.callTypeMask |= 1 << bdbtypes.CallTypeFrame
		toAgg.valueIn += value
		toAgg.gasUsed += gasUsed
	}

	return aggregates
}

// correlateFrameTrace reports whether a frame transaction's call-trace roots are its
// frames.
//
// EIP-8141 makes a transaction a list of calls rather than one, so a client that traces
// such a transaction has one top-level call per frame that executed. Nothing specifies
// this: the callTracer is not part of execution-apis and EIP-8141 says nothing about
// debug tracing. The mapping is therefore only claimed once it verifies - one root per
// executed frame, each addressing that frame's resolved target - and refused otherwise.
//
// ethrex, the first client to ship the type, reports one self-addressed childless
// placeholder for the whole transaction instead. It carries no frame information and does
// not verify, so a caller falls back to the frames the receipt already describes.
func correlateFrameTrace(frames []*pendingFrame, roots []*exerpc.CallTraceCall) bool {
	executed := make([]*pendingFrame, 0, len(frames))

	for _, frame := range frames {
		// A frame that never ran makes no call, so the trace holds nothing for it.
		if frame.executed() {
			executed = append(executed, frame)
		}
	}

	if len(roots) == 0 || len(roots) != len(executed) {
		return false
	}

	for i, root := range roots {
		if root.To != executed[i].target {
			return false
		}
	}

	return true
}

// mergeInternalAggregates folds src into dst and returns the combined set. Accounts are
// keyed by the pending entry a block shares for one address, so the same account resolves
// to the same key in both.
func mergeInternalAggregates(dst, src map[*pendingAccount]*pendingInternalAggregate) map[*pendingAccount]*pendingInternalAggregate {
	if len(dst) == 0 {
		return src
	}

	for account, agg := range src {
		existing, ok := dst[account]
		if !ok {
			dst[account] = agg

			continue
		}

		existing.inCount += agg.inCount
		existing.outCount += agg.outCount
		existing.callTypeMask |= agg.callTypeMask
		existing.valueIn += agg.valueIn
		existing.valueOut += agg.valueOut
		existing.gasUsed += agg.gasUsed
	}

	return dst
}
