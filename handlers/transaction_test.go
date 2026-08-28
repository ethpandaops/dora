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
