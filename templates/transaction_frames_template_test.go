package templates

import (
	"bytes"
	"testing"
	"text/template"
	"time"

	"github.com/ethpandaops/dora/types/models"
)

// frameTemplate parses the frames table on its own so a bad expression in it shows up
// here rather than on a rendered transaction page.
func frameTemplate(t *testing.T) *template.Template {
	t.Helper()

	body, err := Files.ReadFile("transaction/frames.html")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	tmpl, err := template.New("t").Funcs(template.FuncMap(templateFuncs)).Parse(string(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	return tmpl
}

func renderFrames(t *testing.T, data *models.TransactionPageData) string {
	t.Helper()

	var out bytes.Buffer
	if err := frameTemplate(t).ExecuteTemplate(&out, "frames", data); err != nil {
		t.Fatalf("execute: %v", err)
	}

	return out.String()
}

// The four per-frame statuses have to be told apart: a skipped frame is not a failure,
// and a frame whose atomic batch rolled back is not a success even though it reports one.
func TestFramesTemplateDistinguishesStatuses(t *testing.T) {
	data := &models.TransactionPageData{
		IsFrameTx:  true,
		FrameCount: 4,
		Frames: []*models.TransactionPageDataFrame{
			{Index: 0, Species: "Expiry check", Status: 1, StatusText: "Success", HasTarget: true, TargetAddr: []byte{0x81, 0x41}, BatchIndex: 0, BatchSize: 1},
			{Index: 1, Species: "User operation", Status: 0, StatusText: "Failed", HasTarget: true, TargetAddr: []byte{0x30, 0x59}, BatchIndex: 1, BatchSize: 1},
			{Index: 2, Species: "User operation", Status: 2, StatusText: "Skipped", HasTarget: true, TargetAddr: []byte{0x30, 0x59}, BatchIndex: 2, BatchSize: 1},
			{Index: 3, Species: "User operation", Status: 255, StatusText: "Unknown", HasTarget: true, TargetAddr: []byte{0x30, 0x59}, BatchIndex: 3, BatchSize: 1},
		},
	}

	got := renderFrames(t, data)

	for _, want := range []string{"Success", "Failed", "Skipped", "Unknown", "text-bg-success", "text-bg-danger", "text-bg-secondary"} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("rendered output missing %q", want)
		}
	}
}

// A frame that ran inside a batch that later rolled back reports success, but nothing it
// did survived. It must not render as a plain success.
func TestFramesTemplateMarksRolledBackFrames(t *testing.T) {
	data := &models.TransactionPageData{
		IsFrameTx:  true,
		FrameCount: 2,
		Frames: []*models.TransactionPageDataFrame{
			{Index: 0, Status: 1, StatusText: "Rolled back", RolledBack: true, AtomicBatch: true, BatchIndex: 0, BatchSize: 2, HasTarget: true, TargetAddr: []byte{0x01}},
			{Index: 1, Status: 0, StatusText: "Failed", RolledBack: true, BatchIndex: 0, BatchSize: 2, HasTarget: true, TargetAddr: []byte{0x02}},
		},
	}

	got := renderFrames(t, data)

	if !bytes.Contains([]byte(got), []byte("Rolled back")) {
		t.Error("a rolled-back frame must say so")
	}

	if !bytes.Contains([]byte(got), []byte("text-bg-warning")) {
		t.Error("a rolled-back frame must not be styled as a plain success")
	}

	// The batch is marked once, on its first frame.
	if n := bytes.Count([]byte(got), []byte("fa-link")); n != 1 {
		t.Errorf("atomic batch marked %d times, want once", n)
	}
}

// The expiry deadline is one of the more useful things to show, and it comes from the
// envelope rather than any row, so it is absent whenever the block has been pruned.
func TestFramesTemplateShowsExpiryOnlyWhenKnown(t *testing.T) {
	frames := []*models.TransactionPageDataFrame{
		{Index: 0, Species: "Expiry check", Status: 1, StatusText: "Success", HasTarget: true, TargetAddr: []byte{0x81, 0x41}, BatchSize: 1},
	}

	withExpiry := renderFrames(t, &models.TransactionPageData{
		IsFrameTx: true, FrameCount: 1, Frames: frames,
		HasExpiry: true, ExpiryTime: time.Now().Add(10 * time.Minute),
	})
	if !bytes.Contains([]byte(withExpiry), []byte("expires")) {
		t.Error("a known deadline must be shown")
	}

	withoutExpiry := renderFrames(t, &models.TransactionPageData{
		IsFrameTx: true, FrameCount: 1, Frames: frames,
	})
	if bytes.Contains([]byte(withoutExpiry), []byte("expires")) {
		t.Error("no deadline must be claimed when none is known")
	}
}

// A frame that declares no target addresses the sender, which is worth saying so the
// address does not look like an arbitrary recipient.
func TestFramesTemplateMarksSelfAddressedFrames(t *testing.T) {
	got := renderFrames(t, &models.TransactionPageData{
		IsFrameTx:  true,
		FrameCount: 1,
		Frames: []*models.TransactionPageDataFrame{
			{Index: 0, Species: "Self verify", Status: 1, StatusText: "Success", HasTarget: true, TargetIsSender: true, TargetAddr: []byte{0x6d, 0xf3}, BatchSize: 1},
		},
	})

	if !bytes.Contains([]byte(got), []byte("(sender)")) {
		t.Error("a self-addressed frame must be marked as such")
	}
}

// The frames table is included from the details tab, so both files have to parse
// together: a reference to a template that is not registered only fails at parse time of
// the whole set, not of either file alone.
func TestTransactionTemplateSetParses(t *testing.T) {
	files := []string{
		"transaction/transaction.html",
		"transaction/events.html",
		"transaction/statechanges.html",
		"transaction/transfers.html",
		"transaction/internaltxs.html",
		"transaction/authorizations.html",
		"transaction/blobs.html",
		"transaction/frames.html",
	}

	tmpl := template.New("t").Funcs(template.FuncMap(templateFuncs))

	for _, name := range files {
		body, err := Files.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}

		if _, err := tmpl.Parse(string(body)); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
	}

	if tmpl.Lookup("frames") == nil {
		t.Error(`the details tab includes {{ template "frames" }}, which is not defined`)
	}
}
