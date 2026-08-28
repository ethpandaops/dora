package handlers

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	bdbtypes "github.com/ethpandaops/dora/blockdb/types"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/spamoor/txtypes"
	"github.com/holiman/uint256"
)

var (
	frameTestSender = common.HexToAddress("0x1111111111111111111111111111111111111111")
	frameTestCallee = common.HexToAddress("0x2222222222222222222222222222222222222222")
)

// frameTestTx is a two-frame transaction: a verifier that approves payment, then the
// call the sender meant to make.
func frameTestTx() *txtypes.FrameTx {
	callee := frameTestCallee

	return &txtypes.FrameTx{
		Sender: frameTestSender,
		Frames: []*txtypes.Frame{
			{Mode: txtypes.FrameModeVerify, Flags: txtypes.ApprovePayment, Value: uint256.NewInt(0)},
			{Mode: txtypes.FrameModeSender, Target: &callee, Value: uint256.NewInt(0)},
		},
	}
}

// The transaction declares its frames and the receipt says what they did. A page built
// from the transaction alone has the first half and must not imply it has the second.
func TestFrameResultsAreMissingUntilAReceiptSuppliesThem(t *testing.T) {
	pageData := &models.TransactionPageData{}
	buildFramesFromEnvelope(pageData, frameTestTx())

	if !pageData.FrameResultsMissing {
		t.Fatal("frames built from the transaction alone have no results yet")
	}

	for i, frame := range pageData.Frames {
		if frame.Status != bdbtypes.FrameStatusUnknown {
			t.Errorf("frame %d status = %d, want the unknown sentinel", i, frame.Status)
		}
	}

	applyFrameResults(pageData, []bdbtypes.FrameReceiptEntry{
		{Status: uint8(txtypes.FrameStatusSuccess), ExecGasUsed: 5000},
		{Status: uint8(txtypes.FrameStatusFailed), ExecGasUsed: 21000},
	})

	if pageData.FrameResultsMissing {
		t.Error("results were supplied, so the page must not still say they are missing")
	}

	if pageData.Frames[0].StatusText != "Success" || pageData.Frames[1].StatusText != "Failed" {
		t.Errorf("statuses = %q/%q, want Success/Failed", pageData.Frames[0].StatusText, pageData.Frames[1].StatusText)
	}

	if pageData.Frames[1].ExecGasUsed != 21000 {
		t.Errorf("frame 1 exec gas = %d, want 21000", pageData.Frames[1].ExecGasUsed)
	}
}

// Once the block a transaction came in is no longer retained, its frames cannot be read
// from it. The receipt reports one result per frame, so it still says how many there
// were and what each did - just not what any of them addressed.
func TestFrameResultsRaiseFramesWithoutTheTransaction(t *testing.T) {
	pageData := &models.TransactionPageData{IsFrameTx: true}

	applyFrameResults(pageData, []bdbtypes.FrameReceiptEntry{
		{Status: uint8(txtypes.FrameStatusSuccess)},
		{Status: uint8(txtypes.FrameStatusSkipped)},
		{Status: uint8(txtypes.FrameStatusSuccess), LogCount: 3},
	})

	if pageData.FrameCount != 3 || len(pageData.Frames) != 3 {
		t.Fatalf("raised %d frames (count %d), want 3", len(pageData.Frames), pageData.FrameCount)
	}

	for i, frame := range pageData.Frames {
		if frame.Index != uint32(i) {
			t.Errorf("frame %d carries index %d", i, frame.Index)
		}

		if frame.HasTarget {
			t.Errorf("frame %d claims a target the receipt does not report", i)
		}
	}

	if pageData.Frames[1].StatusText != "Skipped" {
		t.Errorf("frame 1 = %q, want Skipped", pageData.Frames[1].StatusText)
	}

	if pageData.Frames[2].LogCount != 3 {
		t.Errorf("frame 2 log count = %d, want 3", pageData.Frames[2].LogCount)
	}
}

// A client that reports fewer results than the transaction has frames leaves the rest
// without one. Those frames must not keep a neighbour's result or read as failures.
func TestFramesBeyondTheReceiptKeepNoResult(t *testing.T) {
	pageData := &models.TransactionPageData{}
	buildFramesFromEnvelope(pageData, frameTestTx())

	applyFrameResults(pageData, []bdbtypes.FrameReceiptEntry{
		{Status: uint8(txtypes.FrameStatusSuccess)},
	})

	if pageData.Frames[1].Status != bdbtypes.FrameStatusUnknown {
		t.Errorf("unreported frame status = %d, want the unknown sentinel", pageData.Frames[1].Status)
	}

	if pageData.Frames[1].StatusText != "Unknown" {
		t.Errorf("unreported frame reads as %q, want Unknown", pageData.Frames[1].StatusText)
	}
}

// A frame transaction's logs are the per-frame lists concatenated in frame order, so the
// per-frame counts say which frame emitted each one.
func TestEventsAreAttributedToTheFrameThatEmittedThem(t *testing.T) {
	pageData := &models.TransactionPageData{
		IsFrameTx: true,
		Frames: []*models.TransactionPageDataFrame{
			{Index: 0, Status: uint8(txtypes.FrameStatusSuccess), LogCount: 1},
			{Index: 1, Status: uint8(txtypes.FrameStatusSuccess), LogCount: 0},
			{Index: 2, Status: uint8(txtypes.FrameStatusSuccess), LogCount: 2},
		},
		Events: []*models.TransactionPageDataEvent{
			{EventIndex: 0}, {EventIndex: 1}, {EventIndex: 2},
		},
	}

	attributeEventsToFrames(pageData)

	want := []uint32{0, 2, 2}
	for i, event := range pageData.Events {
		if !event.HasFrame {
			t.Fatalf("event %d was not attributed", i)
		}

		if event.FrameIndex != want[i] {
			t.Errorf("event %d attributed to frame %d, want %d", i, event.FrameIndex, want[i])
		}
	}
}

// A client that reports a partial set of per-frame counts would shift every log after the
// gap onto the wrong frame. No attribution beats a wrong one.
func TestEventsAreNotAttributedWhenTheCountsDisagree(t *testing.T) {
	pageData := &models.TransactionPageData{
		IsFrameTx: true,
		Frames: []*models.TransactionPageDataFrame{
			{Index: 0, Status: uint8(txtypes.FrameStatusSuccess), LogCount: 1},
		},
		Events: []*models.TransactionPageDataEvent{{EventIndex: 0}, {EventIndex: 1}},
	}

	attributeEventsToFrames(pageData)

	for i, event := range pageData.Events {
		if event.HasFrame {
			t.Errorf("event %d was attributed from counts that do not add up", i)
		}
	}
}

// Without a receipt there are no per-frame counts, so nothing can be attributed.
func TestEventsAreNotAttributedWithoutResults(t *testing.T) {
	pageData := &models.TransactionPageData{
		IsFrameTx:           true,
		FrameResultsMissing: true,
		Frames:              []*models.TransactionPageDataFrame{{Index: 0, LogCount: 1}},
		Events:              []*models.TransactionPageDataEvent{{EventIndex: 0}},
	}

	attributeEventsToFrames(pageData)

	if pageData.Events[0].HasFrame {
		t.Error("an event was attributed although no receipt reported the frames' logs")
	}
}

// A client that decomposes the transaction traces one root per executed frame, so each
// root and everything below it belongs to that frame. A skipped frame made no call and
// takes no root.
func TestInternalTxsAreAttributedToTheirFrames(t *testing.T) {
	pageData := &models.TransactionPageData{
		IsFrameTx: true,
		Frames: []*models.TransactionPageDataFrame{
			{Index: 0, Status: uint8(txtypes.FrameStatusSuccess)},
			{Index: 1, Status: uint8(txtypes.FrameStatusSkipped)},
			{Index: 2, Status: uint8(txtypes.FrameStatusSuccess)},
		},
		InternalTxs: []*models.TransactionPageDataInternalTx{
			{CallIndex: 0, Depth: 0},
			{CallIndex: 1, Depth: 1},
			{CallIndex: 2, Depth: 2},
			{CallIndex: 3, Depth: 0},
		},
	}

	attributeInternalTxsToFrames(pageData)

	want := []uint32{0, 0, 0, 2}
	for i, itx := range pageData.InternalTxs {
		if !itx.HasFrame {
			t.Fatalf("call %d was not attributed", i)
		}

		if itx.FrameIndex != want[i] {
			t.Errorf("call %d attributed to frame %d, want %d", i, itx.FrameIndex, want[i])
		}
	}
}

// A trace whose roots are not the transaction's executed frames says nothing about them,
// and guessing a mapping onto it would name the wrong frame for every call.
func TestInternalTxsAreNotAttributedWhenTheTraceIsNotADecomposition(t *testing.T) {
	pageData := &models.TransactionPageData{
		IsFrameTx: true,
		Frames: []*models.TransactionPageDataFrame{
			{Index: 0, Status: uint8(txtypes.FrameStatusSuccess)},
			{Index: 1, Status: uint8(txtypes.FrameStatusSuccess)},
		},
		// One self-addressed placeholder for the whole transaction, as ethrex reports.
		InternalTxs: []*models.TransactionPageDataInternalTx{{CallIndex: 0, Depth: 0}},
	}

	attributeInternalTxsToFrames(pageData)

	if pageData.InternalTxs[0].HasFrame {
		t.Error("a placeholder root must not be read as a frame's calls")
	}
}
