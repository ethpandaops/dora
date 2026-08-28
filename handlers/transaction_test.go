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
	applySignatureRoles(pageData)

	if len(pageData.Signatures) != 3 {
		t.Fatalf("built %d entries, want 3", len(pageData.Signatures))
	}

	sender, payer, witness := pageData.Signatures[0], pageData.Signatures[1], pageData.Signatures[2]

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

// EIP-8141 orders a secp256k1 entry v || r || s, with v first - the opposite of
// go-ethereum's r || s || v - so the split has to follow the spec rather than the habit.
func TestSecp256k1SignatureSplitsIntoVRS(t *testing.T) {
	sig := make([]byte, 65)
	sig[0] = 1                    // v
	sig[1], sig[32] = 0xaa, 0xbb  // r, first and last byte
	sig[33], sig[64] = 0xcc, 0xdd // s, first and last byte

	parts := decodeFrameSignature(txtypes.SigSchemeSecp256k1, sig)
	if len(parts) != 3 {
		t.Fatalf("split into %d parts, want 3", len(parts))
	}

	for i, want := range []struct {
		name        string
		size        int
		first, last byte
	}{
		{"v", 1, 1, 1},
		{"r", 32, 0xaa, 0xbb},
		{"s", 32, 0xcc, 0xdd},
	} {
		got := parts[i]
		if got.Name != want.name || len(got.Value) != want.size {
			t.Errorf("part %d = %q of %d bytes, want %q of %d", i, got.Name, len(got.Value), want.name, want.size)

			continue
		}

		if got.Value[0] != want.first || got.Value[len(got.Value)-1] != want.last {
			t.Errorf("part %q spans the wrong bytes: %x…%x", got.Name, got.Value[0], got.Value[len(got.Value)-1])
		}
	}
}

// A P256 entry carries the public key alongside the signature, which is what the signer
// address is derived from.
func TestP256SignatureSplitsIntoRSAndPublicKey(t *testing.T) {
	parts := decodeFrameSignature(txtypes.SigSchemeP256, make([]byte, 128))

	names := make([]string, 0, len(parts))
	for _, part := range parts {
		names = append(names, part.Name)

		if len(part.Value) != 32 {
			t.Errorf("part %q is %d bytes, want 32", part.Name, len(part.Value))
		}
	}

	if strings.Join(names, ",") != "r,s,qx,qy" {
		t.Errorf("parts = %v, want r,s,qx,qy", names)
	}
}

// Bytes that are not the length the scheme expects are left whole: carving them up would
// name fields that are not there.
func TestSignatureOfTheWrongLengthIsNotSplit(t *testing.T) {
	if parts := decodeFrameSignature(txtypes.SigSchemeSecp256k1, make([]byte, 64)); parts != nil {
		t.Errorf("a 64-byte secp256k1 entry was split into %d parts", len(parts))
	}

	// An arbitrary entry is witness data with no shape the protocol knows.
	if parts := decodeFrameSignature(txtypes.SigSchemeArbitrary, []byte("witness")); parts != nil {
		t.Errorf("an arbitrary entry was split into %d parts", len(parts))
	}
}

// A frame that fails on its own is a failure, not a batch that rolled back: there is no
// success to lose and nothing went down with it. Marking it rolled back made the page
// show it amber, with a tooltip naming the frame itself as the cause.
func TestALoneFailedFrameIsNotRolledBack(t *testing.T) {
	frames := []*models.TransactionPageDataFrame{
		{Index: 0, Status: uint8(txtypes.FrameStatusSuccess)},
		{Index: 1, Status: uint8(txtypes.FrameStatusFailed), StatusText: "Failed"},
		{Index: 2, Status: uint8(txtypes.FrameStatusSuccess)},
	}

	markRolledBackFrames(frames)

	for i, frame := range frames {
		if frame.RolledBack {
			t.Errorf("frame %d was marked rolled back with no batch to roll back", i)
		}
	}
}

// Inside a batch, only the frame that succeeded had anything taken back from it. The one
// that failed and the one that never ran are told by their own status.
func TestOnlyUndoneSuccessesAreMarkedRolledBack(t *testing.T) {
	frames := []*models.TransactionPageDataFrame{
		{Index: 0, Status: uint8(txtypes.FrameStatusSuccess), AtomicBatch: true},
		{Index: 1, Status: uint8(txtypes.FrameStatusFailed), AtomicBatch: true},
		{Index: 2, Status: uint8(txtypes.FrameStatusSkipped)},
	}

	markRolledBackFrames(frames)

	if !frames[0].RolledBack || frames[0].BatchFailedIndex != 1 {
		t.Errorf("the success before the failure should be undone by frame 1, got %v/%d",
			frames[0].RolledBack, frames[0].BatchFailedIndex)
	}

	if frames[1].RolledBack || frames[2].RolledBack {
		t.Error("the failed and skipped frames say what happened to them themselves")
	}
}

// An assertion frame that fails reverts everything after the validation prefix, not just
// its own atomic batch. The frames it reverted still report the success they earned, so
// nothing but the mode says what happened.
func TestFailedAssertionFrameRevertsTheWholeBody(t *testing.T) {
	pageData := &models.TransactionPageData{
		IsFrameTx: true,
		Frames: []*models.TransactionPageDataFrame{
			// The validation prefix: a self-verify frame approves payment and ends it.
			{Index: 0, Mode: uint8(txtypes.FrameModeVerify), Flags: txtypes.ApproveExecutionAndPayment, Status: uint8(txtypes.FrameStatusSuccess)},
			{Index: 1, Mode: uint8(txtypes.FrameModeSender), Status: uint8(txtypes.FrameStatusSuccess)},
			{Index: 2, Mode: uint8(txtypes.FrameModeSender), Status: uint8(txtypes.FrameStatusSuccess)},
			{Index: 3, Mode: uint8(txtypes.FrameModePostTx), Status: uint8(txtypes.FrameStatusFailed)},
		},
	}

	markRolledBackFrames(pageData.Frames)
	applyFrameBodyReverted(pageData)
	summarizeFrames(pageData)

	if !pageData.FrameBodyReverted {
		t.Fatal("a failed assertion frame reverts the body")
	}

	// The prefix commits even when the body reverts - that is what the payer paid for.
	if pageData.Frames[0].RolledBack {
		t.Error("a validation frame's changes are committed regardless of the body")
	}

	for _, i := range []int{1, 2} {
		if !pageData.Frames[i].RolledBack {
			t.Errorf("frame %d reports success but the body was reverted under it", i)
		}
	}

	if pageData.StatusText != "Reverted" {
		t.Errorf("status = %q, want Reverted - this is not a partial completion", pageData.StatusText)
	}
}

// Without an assertion frame the batch rule applies as before, and the body stands.
func TestBatchUnwindIsNotABodyRevert(t *testing.T) {
	pageData := &models.TransactionPageData{
		IsFrameTx: true,
		Frames: []*models.TransactionPageDataFrame{
			{Index: 0, Mode: uint8(txtypes.FrameModeVerify), Flags: txtypes.ApproveExecutionAndPayment, Status: uint8(txtypes.FrameStatusSuccess)},
			{Index: 1, Mode: uint8(txtypes.FrameModeSender), AtomicBatch: true, Status: uint8(txtypes.FrameStatusSuccess)},
			{Index: 2, Mode: uint8(txtypes.FrameModeSender), Status: uint8(txtypes.FrameStatusFailed)},
			{Index: 3, Mode: uint8(txtypes.FrameModeSender), Status: uint8(txtypes.FrameStatusSuccess)},
		},
	}

	markRolledBackFrames(pageData.Frames)
	applyFrameBodyReverted(pageData)
	summarizeFrames(pageData)

	if pageData.FrameBodyReverted {
		t.Error("no assertion frame failed, so the body stands")
	}

	if !pageData.Frames[1].RolledBack || pageData.Frames[1].BatchFailedIndex != 2 {
		t.Errorf("the batched success should be undone by frame 2, got %v/%d",
			pageData.Frames[1].RolledBack, pageData.Frames[1].BatchFailedIndex)
	}

	// Outside the batch, and after it, this one survived.
	if pageData.Frames[3].RolledBack {
		t.Error("a frame outside the failed batch keeps its effects")
	}

	if pageData.StatusText != "Complete" {
		t.Errorf("status = %q, want Complete", pageData.StatusText)
	}
}

// envelopeTx is a frame transaction in a chosen envelope shape, carrying nothing but what
// the shape itself contributes.
func envelopeTx(extensions txtypes.FrameExtensions) *txtypes.FrameTx {
	target := frameTestCallee

	return &txtypes.FrameTx{
		Extensions: extensions,
		ChainID:    uint256.NewInt(1),
		Sender:     frameTestSender,
		NonceSeq:   4,
		Frames: []*txtypes.Frame{{
			Mode:   txtypes.FrameModeSender,
			Target: &target,
			Limits: txtypes.FrameLimits{Execution: 21000},
			Value:  uint256.NewInt(0),
		}},
		Fees: txtypes.FrameFees{
			GasTipCap:  uint256.NewInt(1),
			GasFeeCap:  uint256.NewInt(2),
			BlobFeeCap: uint256.NewInt(0),
		},
	}
}

// A chain can run EIP-8250 and EIP-8272 independently, so which extensions a payload used
// is a property of the transaction. The badge naming them is read off the envelope.
func TestEnvelopeShapeIsNamedFromTheTransaction(t *testing.T) {
	for _, tc := range []struct {
		extensions txtypes.FrameExtensions
		want       string
	}{
		{0, "8141"},
		{txtypes.FrameExtKeyedNonces, "8141+8250"},
		{txtypes.FrameExtRecentRoots, "8141+8272"},
		{txtypes.FrameExtAll, "8141+8250+8272"},
	} {
		pageData := &models.TransactionPageData{}
		applyFrameTxEnvelope(pageData, envelopeTx(tc.extensions))

		if pageData.FrameExtensions != tc.want {
			t.Errorf("extensions = %q, want %q", pageData.FrameExtensions, tc.want)
		}

		if got := pageData.FrameHasKeyedNonces; got != tc.extensions.Has(txtypes.FrameExtKeyedNonces) {
			t.Errorf("%s: keyed nonces = %v", tc.want, got)
		}
	}
}

// A transaction sequenced in a nonce domain of its own does not run against the sender's
// account nonce, so the keys it selects are what the sequence number means.
func TestKeyedNoncesNameTheDomainsTheySequenceIn(t *testing.T) {
	frameTx := envelopeTx(txtypes.FrameExtKeyedNonces)
	frameTx.NonceKeys = []*uint256.Int{uint256.NewInt(7), uint256.NewInt(9)}

	pageData := &models.TransactionPageData{}
	applyFrameTxEnvelope(pageData, frameTx)

	if pageData.NonceIsAccount {
		t.Error("a transaction selecting key 7 is not sequenced against the account nonce")
	}

	if len(pageData.NonceKeys) != 2 || pageData.NonceKeys[0] != "7" || pageData.NonceKeys[1] != "9" {
		t.Errorf("nonce keys = %v, want [7 9]", pageData.NonceKeys)
	}
}

// Key zero is the sender's account nonce by definition, so a transaction selecting only
// that one is sequenced no differently from a transaction predating keyed nonces - and
// the page says nothing about domains for it.
func TestKeyZeroAloneIsTheAccountNonce(t *testing.T) {
	frameTx := envelopeTx(txtypes.FrameExtKeyedNonces)
	frameTx.NonceKeys = []*uint256.Int{uint256.NewInt(0)}

	pageData := &models.TransactionPageData{}
	applyFrameTxEnvelope(pageData, frameTx)

	if !pageData.NonceIsAccount {
		t.Error("key zero alone is the account nonce")
	}

	if len(pageData.NonceKeys) != 0 {
		t.Errorf("nonce keys = %v, want none to be named", pageData.NonceKeys)
	}
}

// A frame can only read a root the transaction declared up front, so the declarations are
// listed whether or not any frame went on to use them.
func TestRecentRootsAreListedAsDeclared(t *testing.T) {
	frameTx := envelopeTx(txtypes.FrameExtRecentRoots)
	frameTx.RecentRoots = []*txtypes.RecentRootReference{
		{SourceID: common.HexToHash("0xaa"), Slot: 1234, Root: common.HexToHash("0xbb")},
		{SourceID: common.HexToHash("0xcc"), Slot: 1235, Root: common.HexToHash("0xdd")},
	}

	pageData := &models.TransactionPageData{}
	applyFrameTxEnvelope(pageData, frameTx)

	if len(pageData.FrameRecentRoots) != 2 {
		t.Fatalf("roots = %d, want 2", len(pageData.FrameRecentRoots))
	}

	first := pageData.FrameRecentRoots[0]
	if first.Index != 0 || first.Slot != 1234 {
		t.Errorf("first root = index %d slot %d, want index 0 slot 1234", first.Index, first.Slot)
	}

	if !strings.EqualFold(common.BytesToHash(first.Root).Hex(), common.HexToHash("0xbb").Hex()) {
		t.Errorf("first root = %x, want ...bb", first.Root)
	}

	if pageData.FrameRecentRoots[1].Index != 1 {
		t.Errorf("second root index = %d, want 1", pageData.FrameRecentRoots[1].Index)
	}
}

// An envelope that declares no roots has no section to show.
func TestNoRecentRootsWithoutDeclarations(t *testing.T) {
	pageData := &models.TransactionPageData{}
	applyFrameTxEnvelope(pageData, envelopeTx(txtypes.FrameExtRecentRoots))

	if pageData.FrameRecentRoots != nil {
		t.Errorf("roots = %v, want none", pageData.FrameRecentRoots)
	}
}

// Storage on the protocol's own accounts is written by the transaction's validation
// rather than by any frame, so those accounts are named for what they are.
func TestProtocolAccountsAreNamedInStateChanges(t *testing.T) {
	pageData := &models.TransactionPageData{
		FromAddr: frameTestSender.Bytes(),
		StateChanges: []*models.TransactionPageDataStateChangeAccount{
			{Address: txtypes.NonceManager.Bytes()},
			{Address: txtypes.RecentRootAddress.Bytes()},
			{Address: frameTestCallee.Bytes()},
		},
	}

	annotateStateChangeRoles(pageData)

	if got := pageData.StateChanges[0].PredeployName; got != "NONCE_MANAGER" {
		t.Errorf("keyed nonce storage = %q, want NONCE_MANAGER", got)
	}

	if got := pageData.StateChanges[1].PredeployName; got != "RECENT_ROOTS" {
		t.Errorf("recent root storage = %q, want RECENT_ROOTS", got)
	}

	if got := pageData.StateChanges[2].PredeployName; got != "" {
		t.Errorf("an ordinary account = %q, want no name", got)
	}
}
