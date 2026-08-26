package templates

import (
	"bytes"
	"testing"
	"text/template"

	"github.com/ethpandaops/dora/types/models"
)

// The arrival tab reads .Block.BlockRoot; a bad expression there only shows up
// when the template executes, so exercise it directly.
func TestArrivalTemplateExecutes(t *testing.T) {
	body, err := Files.ReadFile("slot/arrival.html")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	tmpl, err := template.New("t").Funcs(template.FuncMap(templateFuncs)).Parse(string(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := &models.SlotPageData{
		Slot:  12345,
		Block: &models.SlotPageBlockData{BlockRoot: []byte{0xab, 0xcd}},
	}

	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "block_arrival", data); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	for _, want := range []string{`var arrivalSlot = 12345`, `var arrivalBlockRoot = "0xabcd"`, `'?root=' + encodeURIComponent(arrivalBlockRoot)`, `'/arrival' + params`} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("rendered output missing %q", want)
		}
	}
}

// The cbt panels are gated on XatuCbtEnabled: their markup and fetch must
// only render when a cbt source is configured.
func TestArrivalTemplateGatesCbtPanels(t *testing.T) {
	body, err := Files.ReadFile("slot/arrival.html")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	tmpl, err := template.New("t").Funcs(template.FuncMap(templateFuncs)).Parse(string(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	render := func(cbt bool) string {
		data := &models.SlotPageData{
			Slot:           12345,
			XatuCbtEnabled: cbt,
			Block:          &models.SlotPageBlockData{BlockRoot: []byte{0xab, 0xcd}},
		}

		var out bytes.Buffer
		if err := tmpl.ExecuteTemplate(&out, "block_arrival", data); err != nil {
			t.Fatalf("execute (cbt=%v): %v", cbt, err)
		}

		return out.String()
	}

	with := render(true)
	for _, want := range []string{`id="waves-timeline"`, `id="waves-cols"`} {
		if !bytes.Contains([]byte(with), []byte(want)) {
			t.Errorf("cbt-enabled output missing %q", want)
		}
	}

	without := render(false)
	if bytes.Contains([]byte(without), []byte(`id="waves-timeline"`)) {
		t.Error("cbt panels rendered without a cbt source")
	}
	// the fetch guards on the panel's existence, so the JS may ship either way
}
