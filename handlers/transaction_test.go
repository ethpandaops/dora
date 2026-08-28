package handlers

import (
	"strings"
	"testing"
	"time"

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

// A token transfer is a decoded log, so it belongs to whichever frame emitted that log.
// The transfers are only the subset of logs that decoded as one, so they are keyed on
// the flat event index rather than on their own position.
func TestTokenTransfersAreAttributedToTheirFrames(t *testing.T) {
	pageData := &models.TransactionPageData{
		IsFrameTx:  true,
		EventCount: 4,
		Frames: []*models.TransactionPageDataFrame{
			{Index: 0, Status: uint8(txtypes.FrameStatusSuccess), LogCount: 1},
			{Index: 1, Status: uint8(txtypes.FrameStatusSuccess), LogCount: 2},
			{Index: 2, Status: uint8(txtypes.FrameStatusSuccess), LogCount: 1},
		},
		// Only three of the four logs decoded as transfers.
		TokenTransfers: []*models.TransactionPageDataTokenTransfer{
			{TransferIndex: 0, EventIndex: 0},
			{TransferIndex: 1, EventIndex: 2},
			{TransferIndex: 2, EventIndex: 3},
		},
	}

	attributeTokenTransfersToFrames(pageData)

	want := []uint32{0, 1, 2}
	for i, transfer := range pageData.TokenTransfers {
		if !transfer.HasFrame {
			t.Fatalf("transfer %d was not attributed", i)
		}

		if transfer.FrameIndex != want[i] {
			t.Errorf("transfer %d attributed to frame %d, want %d", i, transfer.FrameIndex, want[i])
		}
	}
}

// One log can decode into several transfers - an ERC1155 batch is a single log - and
// they all belong to the frame that emitted it.
func TestTokenTransfersSharingALogShareItsFrame(t *testing.T) {
	pageData := &models.TransactionPageData{
		IsFrameTx:  true,
		EventCount: 2,
		Frames: []*models.TransactionPageDataFrame{
			{Index: 0, Status: uint8(txtypes.FrameStatusSuccess), LogCount: 1},
			{Index: 1, Status: uint8(txtypes.FrameStatusSuccess), LogCount: 1},
		},
		TokenTransfers: []*models.TransactionPageDataTokenTransfer{
			{TransferIndex: 0, EventIndex: 1},
			{TransferIndex: 1, EventIndex: 1},
		},
	}

	attributeTokenTransfersToFrames(pageData)

	for i, transfer := range pageData.TokenTransfers {
		if transfer.FrameIndex != 1 {
			t.Errorf("transfer %d attributed to frame %d, want 1", i, transfer.FrameIndex)
		}
	}
}

// A frame transaction only reaches the chain once its validation frames succeed, so one
// that is on chain ran and paid. Frames within it failing is not the transaction
// reverting - what the other frames did stands.
func TestFrameTransactionWithAFailedFrameIsCompleteNotReverted(t *testing.T) {
	pageData := &models.TransactionPageData{
		IsFrameTx: true,
		// What the relational row said before the frames were read.
		Status:       false,
		StatusText:   "Failed",
		RevertReason: "unknown",
		Frames: []*models.TransactionPageDataFrame{
			{Index: 0, Status: uint8(txtypes.FrameStatusSuccess)},
			{Index: 1, Status: uint8(txtypes.FrameStatusSuccess), RolledBack: true},
			{Index: 2, Status: uint8(txtypes.FrameStatusFailed), RolledBack: true},
			{Index: 3, Status: uint8(txtypes.FrameStatusSkipped)},
		},
	}

	summarizeFrames(pageData)

	if !pageData.Status {
		t.Error("a frame transaction on chain ran and paid, so it did not revert")
	}

	if pageData.StatusText != "Complete" {
		t.Errorf("status = %q, want Complete", pageData.StatusText)
	}

	if !pageData.FrameIncomplete {
		t.Error("frames failed, so the transaction did not do everything it asked for")
	}

	if pageData.RevertReason != "" {
		t.Errorf("revert reason = %q, want none: the transaction did not revert", pageData.RevertReason)
	}

	for _, want := range []string{"frame #2 failed", "1 succeeded but was undone", "1 never ran"} {
		if !strings.Contains(pageData.FrameStatusDetail, want) {
			t.Errorf("status detail %q does not mention %q", pageData.FrameStatusDetail, want)
		}
	}
}

// With every frame succeeding there is nothing to qualify, and the transaction reads as
// an ordinary success.
func TestFrameTransactionWithNoFailuresIsASuccess(t *testing.T) {
	pageData := &models.TransactionPageData{
		IsFrameTx: true,
		Frames: []*models.TransactionPageDataFrame{
			{Index: 0, Status: uint8(txtypes.FrameStatusSuccess)},
			{Index: 1, Status: uint8(txtypes.FrameStatusSuccess)},
		},
	}

	summarizeFrames(pageData)

	if !pageData.Status || pageData.StatusText != "Success" {
		t.Errorf("status = %v/%q, want true/Success", pageData.Status, pageData.StatusText)
	}

	if pageData.FrameIncomplete {
		t.Error("no frame failed, so nothing is incomplete")
	}
}

// The expiry frame checks the deadline when the transaction executes, so on an included
// transaction the deadline only says how much room it had left.
func TestExpiryIsMeasuredFromInclusion(t *testing.T) {
	included := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	pageData := &models.TransactionPageData{
		HasExpiry:  true,
		BlockTime:  included,
		ExpiryTime: included.Add(30 * time.Minute),
	}

	applyExpiryMargin(pageData)

	if pageData.ExpiryPassed {
		t.Error("the deadline was ahead of the inclusion, so it had not passed")
	}

	if pageData.ExpiryMargin != "30 min." {
		t.Errorf("margin = %q, want 30 min.", pageData.ExpiryMargin)
	}
}

// A deadline already gone when the transaction was included is an anomaly: the frame
// should have rejected it.
func TestExpiryAlreadyPassedAtInclusion(t *testing.T) {
	included := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	pageData := &models.TransactionPageData{
		HasExpiry:  true,
		BlockTime:  included,
		ExpiryTime: included.Add(-2 * time.Minute),
	}

	applyExpiryMargin(pageData)

	if !pageData.ExpiryPassed {
		t.Error("the deadline was behind the inclusion, so it had passed")
	}

	if pageData.ExpiryMargin != "2 min." {
		t.Errorf("margin = %q, want 2 min.", pageData.ExpiryMargin)
	}
}

// Without a block time there is nothing to measure against, and no margin is claimed.
func TestExpiryMarginNeedsAnInclusionTime(t *testing.T) {
	pageData := &models.TransactionPageData{HasExpiry: true, ExpiryTime: time.Now()}

	applyExpiryMargin(pageData)

	if pageData.ExpiryMargin != "" {
		t.Errorf("margin = %q, want none without an inclusion time", pageData.ExpiryMargin)
	}
}

// The signature list is where an account other than the sender agrees to be charged, so
// each entry is named for what it authorises rather than left as opaque bytes.
func TestFrameSignaturesAreNamedForWhatTheyAuthorise(t *testing.T) {
	paymaster := common.HexToAddress("0x3333333333333333333333333333333333333333")

	frameTx := frameTestTx()
	frameTx.Signatures = []*txtypes.FrameSignature{
		txtypes.SenderSignature(),
		txtypes.SignerSignature(paymaster),
		txtypes.ArbitrarySignature([]byte("witness")),
	}

	pageData := &models.TransactionPageData{
		FromAddr:  frameTestSender.Bytes(),
		PayerAddr: paymaster.Bytes(),
	}

	buildFrameSignatures(pageData, frameTx)
	applyFrameSignatureRoles(pageData)

	if len(pageData.FrameSignatures) != 3 {
		t.Fatalf("built %d entries, want 3", len(pageData.FrameSignatures))
	}

	sender, payer, witness := pageData.FrameSignatures[0], pageData.FrameSignatures[1], pageData.FrameSignatures[2]

	// An entry naming no signer authorises for the sender.
	if !sender.SignerIsSender || sender.Role != "the sender, authorising the transaction" {
		t.Errorf("entry 0 = %+v, want the sender's", sender)
	}

	if payer.Role != "the paymaster, agreeing to be charged" {
		t.Errorf("entry 1 role = %q, want the paymaster's", payer.Role)
	}

	// An arbitrary entry is witness data for contract code and has no protocol signer.
	if witness.HasSigner {
		t.Error("an arbitrary entry has no protocol-assigned signer")
	}

	if witness.VerificationGas != 100 || sender.VerificationGas != 2800 {
		t.Errorf("verification gas = %d/%d, want 100/2800", witness.VerificationGas, sender.VerificationGas)
	}
}

// A balance moves for reasons the numbers alone do not show, so the accounts that had a
// part in the transaction are named.
func TestStateChangeRolesNameTheAccountsThatHadAPart(t *testing.T) {
	sender := common.HexToAddress("0x1111111111111111111111111111111111111111")
	paymaster := common.HexToAddress("0x2222222222222222222222222222222222222222")
	feeRecipient := common.HexToAddress("0x3333333333333333333333333333333333333333")
	bystander := common.HexToAddress("0x4444444444444444444444444444444444444444")

	pageData := &models.TransactionPageData{
		FromAddr:         sender.Bytes(),
		PayerAddr:        paymaster.Bytes(),
		FeeRecipientAddr: feeRecipient.Bytes(),
		StateChanges: []*models.TransactionPageDataStateChangeAccount{
			{Address: sender.Bytes()},
			{Address: paymaster.Bytes()},
			{Address: feeRecipient.Bytes()},
			{Address: bystander.Bytes()},
		},
	}

	annotateStateChangeRoles(pageData)

	for i, want := range []struct{ isSender, isPayer, isFee bool }{
		{isSender: true},
		{isPayer: true},
		{isFee: true},
		{},
	} {
		got := pageData.StateChanges[i]
		if got.IsSender != want.isSender || got.IsPayer != want.isPayer || got.IsFeeRecipient != want.isFee {
			t.Errorf("account %d = sender:%v payer:%v fee:%v, want %v/%v/%v",
				i, got.IsSender, got.IsPayer, got.IsFeeRecipient, want.isSender, want.isPayer, want.isFee)
		}
	}
}

// A sender paying its own fee is the ordinary case and is not worth calling a paymaster.
func TestSenderPayingItsOwnFeeIsNotAPaymaster(t *testing.T) {
	sender := common.HexToAddress("0x1111111111111111111111111111111111111111")

	pageData := &models.TransactionPageData{
		FromAddr:      sender.Bytes(),
		PayerAddr:     sender.Bytes(),
		PayerIsSender: true,
		StateChanges: []*models.TransactionPageDataStateChangeAccount{
			{Address: sender.Bytes()},
		},
	}

	annotateStateChangeRoles(pageData)

	if pageData.StateChanges[0].IsPayer {
		t.Error("the sender paying its own fee is not a paymaster")
	}
}
