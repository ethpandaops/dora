package txindexer

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	bdbtypes "github.com/ethpandaops/dora/blockdb/types"
	exerpc "github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/spamoor/txtypes"
	"github.com/holiman/uint256"
)

var (
	frameSender    = common.HexToAddress("0x6df35438a4dfcdbd25c7a364ab77e3cfdce87fc5")
	framePaymaster = common.HexToAddress("0x1111111111111111111111111111111111111111")
	frameCallee    = common.HexToAddress("0x30592ef78d262bc79f0fe46355e07a51d685e382")
	expiryVerifier = common.HexToAddress("0x0000000000000000000000000000000000008141")
)

// newFrameTestContext builds the minimum processing context the frame helpers need.
// Neither resolveFrames nor aggregateFrames performs I/O: accounts are only recorded for
// batch resolution later.
func newFrameTestContext() *txProcessingContext {
	return &txProcessingContext{
		accounts: make(map[common.Address]*pendingAccount, 8),
		block:    &BlockRef{BlockUID: 1},
	}
}

// frameReceipt builds a receipt carrying the given per-frame results.
func frameReceipt(payer common.Address, results ...*txtypes.FrameReceipt) *txtypes.Receipt {
	return &txtypes.Receipt{
		Type:  txtypes.FrameTxType,
		Extra: &txtypes.FrameReceiptExtra{Payer: payer, Frames: results},
	}
}

// A frame transaction's frames carry their own targets, values and gas budgets, and the
// receipt reports one result per frame in the same order.
func TestResolveFramesPairsReceiptResults(t *testing.T) {
	ctx := newFrameTestContext()

	frameTx := &txtypes.FrameTx{
		Sender: frameSender,
		Frames: []*txtypes.Frame{
			// Expiry check against the deadline predeploy.
			{Mode: txtypes.FrameModeVerify, Target: &expiryVerifier, Value: uint256.NewInt(0)},
			// A frame with no target addresses the transaction's sender.
			{Mode: txtypes.FrameModeVerify, Flags: txtypes.ApproveExecutionAndPayment, Value: uint256.NewInt(0)},
			// The user's own operation, carrying value.
			{Mode: txtypes.FrameModeSender, Target: &frameCallee, Value: uint256.NewInt(7), Data: []byte{0xde, 0xad, 0xbe, 0xef, 0x01}},
		},
	}

	receipt := frameReceipt(framePaymaster,
		&txtypes.FrameReceipt{Status: txtypes.FrameStatusSuccess, ExecutionGas: 51},
		&txtypes.FrameReceipt{Status: txtypes.FrameStatusSuccess},
		&txtypes.FrameReceipt{Status: txtypes.FrameStatusSuccess, ExecutionGas: 21000, StateGas: 5, Logs: []*txtypes.Log{{}, {}}},
	)

	frames := ctx.resolveFrames(frameTx, receipt, nil)

	if len(frames) != 3 {
		t.Fatalf("frames = %d, want 3", len(frames))
	}

	if frames[0].target != expiryVerifier {
		t.Errorf("frame 0 target = %s, want the expiry verifier", frames[0].target.Hex())
	}

	// A frame that declares no target resolves to the sender.
	if frames[1].target != frameSender {
		t.Errorf("frame 1 target = %s, want the sender %s", frames[1].target.Hex(), frameSender.Hex())
	}

	if frames[2].target != frameCallee {
		t.Errorf("frame 2 target = %s, want %s", frames[2].target.Hex(), frameCallee.Hex())
	}

	if got := frames[2].value; got.Cmp(big.NewInt(7)) != 0 {
		t.Errorf("frame 2 value = %s, want 7", got)
	}

	if got := frames[0].execGasUsed; got != 51 {
		t.Errorf("frame 0 execution gas = %d, want 51", got)
	}

	if got := frames[2].stateGasUsed; got != 5 {
		t.Errorf("frame 2 state gas = %d, want 5", got)
	}

	if got := frames[2].logCount; got != 2 {
		t.Errorf("frame 2 log count = %d, want 2", got)
	}

	if got := frames[2].methodID; len(got) != 4 || got[0] != 0xde {
		t.Errorf("frame 2 method id = %x, want the first four calldata bytes", got)
	}

	if got := frames[2].dataLen; got != 5 {
		t.Errorf("frame 2 data length = %d, want 5", got)
	}

	// Every distinct target gets an account tracked for batch resolution.
	for _, addr := range []common.Address{expiryVerifier, frameSender, frameCallee} {
		if _, ok := ctx.accounts[addr]; !ok {
			t.Errorf("no account tracked for frame target %s", addr.Hex())
		}
	}
}

// A client that reports fewer results than there are frames leaves the remaining frames
// without one. They must not be read as having failed, which is what status 0 would say.
func TestResolveFramesToleratesShortReceipt(t *testing.T) {
	ctx := newFrameTestContext()

	frameTx := &txtypes.FrameTx{
		Sender: frameSender,
		Frames: []*txtypes.Frame{
			{Mode: txtypes.FrameModeSender, Target: &frameCallee, Value: uint256.NewInt(0)},
			{Mode: txtypes.FrameModeSender, Target: &frameCallee, Value: uint256.NewInt(0)},
		},
	}

	receipt := frameReceipt(common.Address{},
		&txtypes.FrameReceipt{Status: txtypes.FrameStatusSuccess},
	)

	frames := ctx.resolveFrames(frameTx, receipt, nil)

	if !frames[0].hasResult || !frames[0].succeeded() {
		t.Error("frame 0 should carry the reported success")
	}

	if frames[1].hasResult {
		t.Error("frame 1 must not claim a result the receipt did not report")
	}

	if frames[1].executed() || frames[1].succeeded() {
		t.Error("a frame with no reported result is neither executed nor successful")
	}
}

func TestMarkRolledBackBatches(t *testing.T) {
	const batched = txtypes.AtomicBatchFlag

	tests := []struct {
		name   string
		flags  []uint8
		status []uint64
		want   []bool
	}{
		{
			name:   "no batches, independent failure",
			flags:  []uint8{0, 0, 0},
			status: []uint64{txtypes.FrameStatusSuccess, txtypes.FrameStatusFailed, txtypes.FrameStatusSuccess},
			want:   []bool{false, true, false},
		},
		{
			name:   "batch rolls back the successes before the failure",
			flags:  []uint8{batched, batched, 0},
			status: []uint64{txtypes.FrameStatusSuccess, txtypes.FrameStatusFailed, txtypes.FrameStatusSkipped},
			want:   []bool{true, true, true},
		},
		{
			name:   "batch that fully succeeds survives",
			flags:  []uint8{batched, batched, 0},
			status: []uint64{txtypes.FrameStatusSuccess, txtypes.FrameStatusSuccess, txtypes.FrameStatusSuccess},
			want:   []bool{false, false, false},
		},
		{
			name:   "only the failing batch rolls back",
			flags:  []uint8{batched, 0, batched, 0},
			status: []uint64{txtypes.FrameStatusSuccess, txtypes.FrameStatusSuccess, txtypes.FrameStatusSuccess, txtypes.FrameStatusFailed},
			want:   []bool{false, false, true, true},
		},
		{
			name:   "a trailing batch flag does not run past the end",
			flags:  []uint8{batched, batched},
			status: []uint64{txtypes.FrameStatusSuccess, txtypes.FrameStatusFailed},
			want:   []bool{true, true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frames := make([]*pendingFrame, len(tt.flags))
			for i := range tt.flags {
				frames[i] = &pendingFrame{
					index:     uint16(i),
					flags:     tt.flags[i],
					hasResult: true,
					status:    tt.status[i],
					value:     new(big.Int),
				}
			}

			markRolledBackBatches(frames)

			for i, want := range tt.want {
				if frames[i].rolledBack != want {
					t.Errorf("frame %d rolledBack = %v, want %v", i, frames[i].rolledBack, want)
				}
			}
		})
	}
}

// A frame transaction's per-account rows come from its frames: the sender is the caller
// of each, and every target it addressed is reachable from the transaction.
func TestAggregateFramesAttributesFramesToTheirTargets(t *testing.T) {
	ctx := newFrameTestContext()
	sender := ctx.ensureAccount(frameSender, nil, false)
	callee := ctx.ensureAccount(frameCallee, nil, false)

	frames := []*pendingFrame{
		{index: 0, target: frameCallee, toAccount: callee, value: big.NewInt(5), hasResult: true, status: txtypes.FrameStatusSuccess, execGasUsed: 100, stateGasUsed: 20},
		{index: 1, target: frameCallee, toAccount: callee, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusSuccess, execGasUsed: 50},
	}

	aggregates := ctx.aggregateFrames(frames, sender)

	senderAgg := aggregates[sender]
	if senderAgg == nil {
		t.Fatal("sender has no aggregate")
	}

	if senderAgg.outCount != 2 {
		t.Errorf("sender out count = %d, want 2", senderAgg.outCount)
	}

	if senderAgg.gasUsed != 170 {
		t.Errorf("sender gas = %d, want 170", senderAgg.gasUsed)
	}

	calleeAgg := aggregates[callee]
	if calleeAgg == nil {
		t.Fatal("callee has no aggregate")
	}

	if calleeAgg.inCount != 2 {
		t.Errorf("callee in count = %d, want 2", calleeAgg.inCount)
	}

	if calleeAgg.callTypeMask != 1<<bdbtypes.CallTypeFrame {
		t.Errorf("callee call type mask = %b, want the frame bit", calleeAgg.callTypeMask)
	}

	if calleeAgg.valueIn != weiToFloat(big.NewInt(5), 18) {
		t.Errorf("callee value in = %v, want the value of the one frame that carried it", calleeAgg.valueIn)
	}
}

// A frame that never ran touched nothing, and a frame whose batch rolled back spent gas
// but moved no value.
func TestAggregateFramesIgnoresUnexecutedAndRolledBackEffects(t *testing.T) {
	ctx := newFrameTestContext()
	sender := ctx.ensureAccount(frameSender, nil, false)
	callee := ctx.ensureAccount(frameCallee, nil, false)

	frames := []*pendingFrame{
		// Executed, reported success, but its batch was undone afterwards.
		{index: 0, target: frameCallee, toAccount: callee, value: big.NewInt(9), hasResult: true, status: txtypes.FrameStatusSuccess, execGasUsed: 70, rolledBack: true},
		// Never executed.
		{index: 1, target: frameCallee, toAccount: callee, value: big.NewInt(3), hasResult: true, status: txtypes.FrameStatusSkipped},
	}

	aggregates := ctx.aggregateFrames(frames, sender)

	calleeAgg := aggregates[callee]
	if calleeAgg == nil {
		t.Fatal("callee has no aggregate")
	}

	if calleeAgg.inCount != 1 {
		t.Errorf("callee in count = %d, want 1 - the skipped frame never ran", calleeAgg.inCount)
	}

	if calleeAgg.valueIn != 0 {
		t.Errorf("callee value in = %v, want 0 - neither frame durably moved value", calleeAgg.valueIn)
	}

	if calleeAgg.gasUsed != 70 {
		t.Errorf("callee gas = %d, want 70 - a rolled-back frame still spent its gas", calleeAgg.gasUsed)
	}
}

// A frame addressing the transaction's own sender makes it both caller and callee.
func TestAggregateFramesCountsSelfAddressedFrameBothWays(t *testing.T) {
	ctx := newFrameTestContext()
	sender := ctx.ensureAccount(frameSender, nil, false)

	frames := []*pendingFrame{
		{index: 0, target: frameSender, toAccount: sender, value: big.NewInt(4), hasResult: true, status: txtypes.FrameStatusSuccess, execGasUsed: 10},
	}

	aggregates := ctx.aggregateFrames(frames, sender)

	if len(aggregates) != 1 {
		t.Fatalf("aggregates = %d, want 1", len(aggregates))
	}

	agg := aggregates[sender]
	if agg.inCount != 1 || agg.outCount != 1 {
		t.Errorf("in/out = %d/%d, want 1/1", agg.inCount, agg.outCount)
	}

	if agg.valueIn != agg.valueOut {
		t.Errorf("value in %v != value out %v", agg.valueIn, agg.valueOut)
	}
}

// ethrex traces a frame transaction as a single self-addressed childless call, whatever
// its frames are. Observed verbatim on a devnet running eip8141-v2-lenient: a four-frame
// transaction from 0x6bcb34 produced one root with from == to == the sender and no calls.
//
// Nothing in it identifies a frame, so it must not be accepted as a decomposition.
func TestCorrelateFrameTraceRejectsPlaceholderRoot(t *testing.T) {
	ctx := newFrameTestContext()
	callee := ctx.ensureAccount(frameCallee, nil, false)

	frames := []*pendingFrame{
		{index: 0, target: frameSender, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusSuccess},
		{index: 1, target: frameCallee, toAccount: callee, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusSuccess},
	}

	roots := []*exerpc.CallTraceCall{
		{Type: "CALL", From: frameSender, To: frameSender},
	}

	if correlateFrameTrace(frames, roots) {
		t.Error("a single self-addressed root must not be read as a decomposition of two frames")
	}

	for _, frame := range frames {
		if frame.traceCount != 0 {
			t.Errorf("frame %d claimed %d trace frames from a placeholder", frame.index, frame.traceCount)
		}
	}
}

// A client that does decompose the transaction reports one root per executed frame,
// addressing that frame's target. Then the trace partitions cleanly by frame.
func TestCorrelateFrameTraceMapsRootsToFrames(t *testing.T) {
	ctx := newFrameTestContext()
	callee := ctx.ensureAccount(frameCallee, nil, false)
	verifier := ctx.ensureAccount(expiryVerifier, nil, false)

	frames := []*pendingFrame{
		{index: 0, target: expiryVerifier, toAccount: verifier, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusSuccess},
		// Skipped frames make no call, so the trace holds nothing for them.
		{index: 1, target: frameCallee, toAccount: callee, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusSkipped},
		{index: 2, target: frameCallee, toAccount: callee, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusSuccess},
	}

	roots := []*exerpc.CallTraceCall{
		{Type: "CALL", From: frameSender, To: expiryVerifier},
		{Type: "CALL", From: frameSender, To: frameCallee, Calls: []exerpc.CallTraceCall{
			{Type: "STATICCALL", From: frameCallee, To: frameSender},
			{Type: "CALL", From: frameCallee, To: frameSender, Calls: []exerpc.CallTraceCall{
				{Type: "CALL", From: frameSender, To: frameCallee},
			}},
		}},
	}

	if !correlateFrameTrace(frames, roots) {
		t.Fatal("one root per executed frame, addressing its target, must correlate")
	}

	if frames[0].traceCount != 1 {
		t.Errorf("frame 0 trace count = %d, want 1", frames[0].traceCount)
	}

	if frames[1].traceCount != 0 {
		t.Errorf("skipped frame 1 trace count = %d, want 0", frames[1].traceCount)
	}

	// The root, its two children and the grandchild below them.
	if frames[2].traceCount != 4 {
		t.Errorf("frame 2 trace count = %d, want 4", frames[2].traceCount)
	}
}

// A root that addresses something other than the frame it lines up with is not that
// frame's call, and the whole mapping is refused rather than guessed at.
func TestCorrelateFrameTraceRejectsMismatchedTargets(t *testing.T) {
	frames := []*pendingFrame{
		{index: 0, target: frameCallee, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusSuccess},
	}

	roots := []*exerpc.CallTraceCall{
		{Type: "CALL", From: frameSender, To: framePaymaster},
	}

	if correlateFrameTrace(frames, roots) {
		t.Error("a root addressing a different account must not be mapped onto the frame")
	}
}
