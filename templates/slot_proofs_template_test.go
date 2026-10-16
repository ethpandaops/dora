package templates

import (
	"bytes"
	"testing"
	"text/template"

	"github.com/ethpandaops/dora/types/models"
)

// The proofs tab is loaded lazily from the block root it reads off the page
// model; a bad expression there only shows up when the template executes.
func TestProofsTemplateExecutes(t *testing.T) {
	body, err := Files.ReadFile("slot/proofs.html")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	tmpl, err := template.New("t").Funcs(template.FuncMap(templateFuncs)).Parse(string(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := &models.SlotPageData{
		Slot: 12345,
		Block: &models.SlotPageBlockData{
			BlockRoot:            []byte{0xab, 0xcd},
			ExecutionProofsCount: 2,
		},
	}

	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "block_proofs", data); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		`var blockRoot = "0xabcd"`,
		`fetch('/slot/' + blockRoot + '/proofs')`,
		`document.getElementById('proofs-tab')`,
	} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("rendered output missing %q", want)
		}
	}
}
