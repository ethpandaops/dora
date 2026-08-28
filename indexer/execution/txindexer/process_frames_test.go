package txindexer

import (
	"io"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	bdbtypes "github.com/ethpandaops/dora/blockdb/types"
	exerpc "github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/spamoor/txtypes"
	"github.com/holiman/uint256"
	"github.com/sirupsen/logrus"
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
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	return &txProcessingContext{
		accounts: make(map[common.Address]*pendingAccount, 8),
		block:    &BlockRef{BlockUID: 1},
		indexer:  &TxIndexer{logger: logger},
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

// The batch rules are txtypes'; what is checked here is that a frame is called undone
// only when a success of its own was taken back.
func TestMarkUndoneFrames(t *testing.T) {
	const batched = txtypes.AtomicBatchFlag

	tests := []struct {
		name   string
		flags  []uint8
		status []uint64
		want   []bool
	}{
		{
			// A frame that fails on its own is a failure, not a batch that rolled back:
			// it has no success to lose and nothing else went down with it.
			name:   "no batches, independent failure",
			flags:  []uint8{0, 0, 0},
			status: []uint64{txtypes.FrameStatusSuccess, txtypes.FrameStatusFailed, txtypes.FrameStatusSuccess},
			want:   []bool{false, false, false},
		},
		{
			// Only the success before the failure had anything taken back from it. The
			// frame that failed and the one that never ran are told by their own status.
			name:   "batch rolls back the successes before the failure",
			flags:  []uint8{batched, batched, 0},
			status: []uint64{txtypes.FrameStatusSuccess, txtypes.FrameStatusFailed, txtypes.FrameStatusSkipped},
			want:   []bool{true, false, false},
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
			want:   []bool{false, false, true, false},
		},
		{
			name:   "a trailing batch flag does not run past the end",
			flags:  []uint8{batched, batched},
			status: []uint64{txtypes.FrameStatusSuccess, txtypes.FrameStatusFailed},
			want:   []bool{true, false},
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

			frameTx := &txtypes.FrameTx{Frames: make([]*txtypes.Frame, len(frames))}
			extra := &txtypes.FrameReceiptExtra{Frames: make([]*txtypes.FrameReceipt, len(frames))}

			for i := range frames {
				frameTx.Frames[i] = &txtypes.Frame{Flags: tt.flags[i], Value: uint256.NewInt(0)}
				extra.Frames[i] = &txtypes.FrameReceipt{Status: tt.status[i]}
			}

			markUndoneFrames(frames, extra, frameTx)

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
}

// A client that does decompose the transaction reports one root per executed frame,
// addressing that frame's target. Only then is the trace the transaction's frames.
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

// A frame transaction's row keeps no recipient of its own, and callers must be able to
// tell that apart from a contract creation, which is the other reason a row has none.
func TestIsMultiTargetIdentifiesFrameTransactions(t *testing.T) {
	if !dbtypes.IsMultiTarget(txtypes.FrameTxType) {
		t.Error("a frame transaction addresses more than one recipient")
	}

	// The create flag shares the tx_type byte and must not be read as a type.
	if !dbtypes.IsMultiTarget(txtypes.FrameTxType | dbtypes.ElTxFlagCreate) {
		t.Error("the create flag must not hide the transaction type")
	}

	for _, txType := range []uint8{
		txtypes.LegacyTxType, txtypes.AccessListTxType, txtypes.DynamicFeeTxType,
		txtypes.BlobTxType, txtypes.SetCodeTxType,
	} {
		if dbtypes.IsMultiTarget(txType) {
			t.Errorf("type %d addresses a single recipient", txType)
		}

		if dbtypes.IsMultiTarget(txType | dbtypes.ElTxFlagCreate) {
			t.Errorf("type %d with the create flag addresses a single recipient", txType)
		}
	}
}

// A transaction reporting more frames than EIP-8141 allows cannot be in a valid block.
// The stored frame list is bounded by the same cap, so keeping all of them anyway would
// fail to encode and the block would fail on every retry.
func TestResolveFramesCapsFrameCount(t *testing.T) {
	ctx := newFrameTestContext()

	frames := make([]*txtypes.Frame, 0, txtypes.MaxFrames+8)
	for i := 0; i < txtypes.MaxFrames+8; i++ {
		target := frameCallee
		frames = append(frames, &txtypes.Frame{
			Mode:   txtypes.FrameModeSender,
			Target: &target,
			Value:  uint256.NewInt(0),
		})
	}

	frameTx := &txtypes.FrameTx{Sender: frameSender, Frames: frames}

	resolved := ctx.resolveFrames(frameTx, &txtypes.Receipt{Type: txtypes.FrameTxType}, nil)

	if len(resolved) != txtypes.MaxFrames {
		t.Fatalf("resolved %d frames, want the cap of %d", len(resolved), txtypes.MaxFrames)
	}

	if last := resolved[len(resolved)-1].index; last != txtypes.MaxFrames-1 {
		t.Errorf("last frame index = %d, want %d", last, txtypes.MaxFrames-1)
	}
}

// The receipt is the only place a frame's result is kept, and a page is rebuilt from it
// for as long as the transaction is resolvable at all. It therefore has to carry every
// frame's result, including the absence of one.
func TestFrameReceiptDataRecordsEveryResult(t *testing.T) {
	payer := framePaymaster

	frames := []*pendingFrame{
		{index: 0, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusSuccess, execGasUsed: 21000, stateGasUsed: 40, logCount: 2},
		{index: 1, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusFailed},
		{index: 2, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusSkipped},
		// The client reported no result for this one.
		{index: 3, value: new(big.Int)},
	}

	receiptData := frameReceiptData(frames, payer)
	if receiptData == nil || len(receiptData.Frames) != len(frames) {
		t.Fatal("blockdb frame data was not built")
	}

	if common.Address(receiptData.Payer) != payer {
		t.Errorf("payer = %x, want %x", receiptData.Payer, payer)
	}

	for i, frame := range frames {
		if got := receiptData.Frames[i].Status; got != frame.storedStatus() {
			t.Errorf("frame %d stored as %d, want %d", i, got, frame.storedStatus())
		}
	}

	if got := receiptData.Frames[0]; got.ExecGasUsed != 21000 || got.StateGasUsed != 40 || got.LogCount != 2 {
		t.Errorf("frame 0 recorded %d/%d gas and %d logs, want 21000/40 and 2", got.ExecGasUsed, got.StateGasUsed, got.LogCount)
	}

	// A frame with no reported result must say so, rather than claim the failure that a
	// zero status spells.
	if got := receiptData.Frames[3].Status; got != bdbtypes.FrameStatusUnknown {
		t.Errorf("unreported frame stored as %d, want the unknown sentinel %d", got, bdbtypes.FrameStatusUnknown)
	}
}

// A frame transaction has no frames of its own to record when it has none at all.
func TestFrameReceiptDataIsNilWithoutFrames(t *testing.T) {
	if frameReceiptData(nil, framePaymaster) != nil {
		t.Error("a transaction with no frames has no frame receipt content")
	}
}

func TestMergeInternalAggregates(t *testing.T) {
	ctx := newFrameTestContext()
	sender := ctx.ensureAccount(frameSender, nil, false)
	callee := ctx.ensureAccount(frameCallee, nil, false)
	other := ctx.ensureAccount(framePaymaster, nil, false)

	dst := map[*pendingAccount]*pendingInternalAggregate{
		sender: {account: sender, outCount: 1, valueOut: 2, gasUsed: 10, callTypeMask: 1 << bdbtypes.CallTypeCall},
		callee: {account: callee, inCount: 1, valueIn: 2, gasUsed: 10},
	}

	src := map[*pendingAccount]*pendingInternalAggregate{
		sender: {account: sender, outCount: 2, valueOut: 3, gasUsed: 5, callTypeMask: 1 << bdbtypes.CallTypeFrame},
		other:  {account: other, inCount: 1, gasUsed: 7},
	}

	merged := mergeInternalAggregates(dst, src)

	if len(merged) != 3 {
		t.Fatalf("merged accounts = %d, want 3", len(merged))
	}

	got := merged[sender]
	if got.outCount != 3 || got.valueOut != 5 || got.gasUsed != 15 {
		t.Errorf("sender merged to out=%d value=%v gas=%d, want 3/5/15", got.outCount, got.valueOut, got.gasUsed)
	}

	// Both sources' call types survive, so a frame stays distinguishable from a sub-call.
	wantMask := uint16(1<<bdbtypes.CallTypeCall | 1<<bdbtypes.CallTypeFrame)
	if got.callTypeMask != wantMask {
		t.Errorf("sender call type mask = %b, want %b", got.callTypeMask, wantMask)
	}

	if merged[other].gasUsed != 7 {
		t.Error("an account only the second set touched was dropped")
	}

	if merged[callee].inCount != 1 {
		t.Error("an account only the first set touched was disturbed")
	}
}

// An empty destination must not lose the source, which is the shape a frame transaction
// takes when no trace was collected for it.
func TestMergeInternalAggregatesFromEmpty(t *testing.T) {
	ctx := newFrameTestContext()
	sender := ctx.ensureAccount(frameSender, nil, false)

	src := map[*pendingAccount]*pendingInternalAggregate{
		sender: {account: sender, outCount: 1},
	}

	if merged := mergeInternalAggregates(nil, src); len(merged) != 1 {
		t.Fatalf("merged accounts = %d, want 1", len(merged))
	}
}

// A decomposed frame transaction's trace has one root per frame, and DEFAULT and VERIFY
// frames are entered by the ENTRY_POINT predeploy rather than by the sender. Aggregating
// those roots would record ENTRY_POINT as an account that took part, collecting every
// frame transaction on the chain onto one address - and would count each frame a second
// time, since the receipt already describes them.
func TestProcessCallTraceLeavesFrameRootsToTheReceipt(t *testing.T) {
	ctx := newFrameTestContext()
	entryPoint := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	inner := common.HexToAddress("0x1111111111111111111111111111111111111111")

	roots := []*exerpc.CallTraceCall{
		// A VERIFY frame, entered by the predeploy.
		{Type: "CALL", From: entryPoint, To: frameSender, Gas: 5000, GasUsed: 51},
		// A SENDER frame, which does make a call of its own.
		{Type: "CALL", From: frameSender, To: frameCallee, Gas: 30000, GasUsed: 21000, Calls: []exerpc.CallTraceCall{
			{Type: "CALL", From: frameCallee, To: inner, Gas: 1000, GasUsed: 700},
		}},
	}

	frames, aggregates := ctx.processCallTrace(roots, nil)

	// Every call is still stored for display, roots included.
	if len(frames) != 3 {
		t.Fatalf("stored call frames = %d, want 3", len(frames))
	}

	for account := range aggregates {
		if common.BytesToAddress(account.account.Address) == entryPoint {
			t.Error("ENTRY_POINT must not be recorded as a participant in the transaction")
		}
	}

	// Only the call made from within a frame is aggregated.
	if len(aggregates) != 2 {
		t.Fatalf("aggregated accounts = %d, want 2 (the sub-call's caller and callee)", len(aggregates))
	}

	for account, agg := range aggregates {
		addr := common.BytesToAddress(account.account.Address)
		switch addr {
		case frameCallee:
			if agg.outCount != 1 || agg.inCount != 0 {
				t.Errorf("frame target in/out = %d/%d, want 0/1 - its own frame is the receipt's to count", agg.inCount, agg.outCount)
			}
		case inner:
			if agg.inCount != 1 || agg.gasUsed != 700 {
				t.Errorf("sub-call callee in=%d gas=%d, want 1/700", agg.inCount, agg.gasUsed)
			}
		default:
			t.Errorf("unexpected account aggregated: %s", addr.Hex())
		}
	}
}

// A frame skipped by an earlier failure called nobody. Registering its target would put
// an account in the index whose first sighting is a call that never happened, funded by
// this block from a transfer that did not occur.
func TestResolveFramesRegistersOnlyTheTargetsThatWereCalled(t *testing.T) {
	ctx := newFrameTestContext()

	called := frameCallee
	uncalled := framePaymaster

	frameTx := &txtypes.FrameTx{
		Sender: frameSender,
		Frames: []*txtypes.Frame{
			{Mode: txtypes.FrameModeSender, Target: &called, Value: uint256.NewInt(0)},
			{Mode: txtypes.FrameModeSender, Target: &uncalled, Value: uint256.NewInt(0)},
		},
	}

	receipt := frameReceipt(common.Address{},
		&txtypes.FrameReceipt{Status: txtypes.FrameStatusSuccess},
		&txtypes.FrameReceipt{Status: txtypes.FrameStatusSkipped},
	)

	frames := ctx.resolveFrames(frameTx, receipt, nil)

	if frames[0].toAccount == nil {
		t.Error("a frame that ran needs its target, for the aggregates and any transfer")
	}

	if frames[1].toAccount != nil {
		t.Error("a frame that never ran must not register its target")
	}

	if _, tracked := ctx.accounts[uncalled]; tracked {
		t.Error("the skipped frame's target was entered into the block's accounts")
	}

	if _, tracked := ctx.accounts[called]; !tracked {
		t.Error("the executed frame's target should be tracked")
	}
}
