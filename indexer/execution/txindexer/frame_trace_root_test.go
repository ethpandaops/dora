package txindexer

import (
	"math/big"
	"testing"

	exerpc "github.com/ethpandaops/dora/clients/execution/rpc"
	"github.com/ethpandaops/spamoor/txtypes"
)

func TestCorrelateFrameTraceUnwrapsSyntheticRoot(t *testing.T) {
	ctx := newFrameTestContext()
	callee := ctx.ensureAccount(frameCallee, nil, false)
	verifier := ctx.ensureAccount(expiryVerifier, nil, false)

	frames := []*pendingFrame{
		{index: 0, target: expiryVerifier, toAccount: verifier, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusSuccess},
		{index: 1, target: frameCallee, toAccount: callee, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusSkipped},
		{index: 2, target: frameCallee, toAccount: callee, value: new(big.Int), hasResult: true, status: txtypes.FrameStatusFailed},
	}

	roots := []*exerpc.CallTraceCall{
		{Type: "CALL", From: frameSender, To: txtypes.EntryPoint, Error: "frame failed", Calls: []exerpc.CallTraceCall{
			{Type: "STATICCALL", From: txtypes.EntryPoint, To: expiryVerifier},
			{Type: "CALL", From: frameSender, To: frameCallee, Error: "frame skipped"},
			{Type: "CALL", From: frameSender, To: frameCallee, Error: "execution reverted", Calls: []exerpc.CallTraceCall{
				{Type: "CALL", From: frameCallee, To: frameSender},
			}},
		}},
	}

	if correlateFrameTrace(frames, roots) {
		t.Fatal("the raw synthetic root is expected to fail correlation")
	}

	unwrapped := frameTraceRoots(frames, frameSender, roots)
	if !correlateFrameTrace(frames, unwrapped) {
		t.Fatal("a synthetic root whose calls are the frames must correlate")
	}
	if len(unwrapped) != 2 || unwrapped[1].Error != "execution reverted" || len(unwrapped[1].Calls) != 1 {
		t.Fatalf("expected the two executed frames, got %+v", unwrapped)
	}

	flat := []*exerpc.CallTraceCall{&roots[0].Calls[0], &roots[0].Calls[2]}
	if got := frameTraceRoots(frames, frameSender, flat); len(got) != 2 || !correlateFrameTrace(frames, got) {
		t.Fatal("a list of frame roots must pass through unchanged")
	}

	wrongSender := frameTraceRoots(frames, framePaymaster, roots)
	if correlateFrameTrace(frames, wrongSender) {
		t.Fatal("a root from another account must not be unwrapped")
	}

	short := []*exerpc.CallTraceCall{{Type: "CALL", From: frameSender, To: txtypes.EntryPoint, Calls: roots[0].Calls[:2]}}
	if correlateFrameTrace(frames, frameTraceRoots(frames, frameSender, short)) {
		t.Fatal("a root with fewer calls than frames must not be unwrapped")
	}
}
