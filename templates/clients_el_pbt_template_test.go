package templates

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/ethpandaops/dora/types/models"
)

// The PBT migration column only renders when executionapi.pbtMigrationProgress is
// enabled; a bad expression there only shows up when the template executes.
func TestClientsELPbtMigrationTemplateExecutes(t *testing.T) {
	body, err := Files.ReadFile("clients/clients_el.html")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	tmpl, err := template.New("t").Funcs(template.FuncMap(templateFuncs)).Parse(string(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	clients := []*models.ClientsELPageDataClient{
		{Name: "geth-import", Status: "online", HeadSlot: 24504500, PbtMigration: &models.ClientELPageDataPbtMigration{
			Available: true, LastUpdate: time.Now(), Phase: "running", HasBinary: true, BinaryPhase: "following",
			BinaryCursor: 24504400, BinaryLag: 100, BinaryShadowRoot: "0xd4597ecebdacec3c8633fe0fac261b4667f3bcbb3fcc29bed25cd24298b14eab",
		}},
		{Name: "besu-idle", Status: "online", PbtMigration: &models.ClientELPageDataPbtMigration{
			Available: true, LastUpdate: time.Now(), Phase: "running", HasBinary: true, BinaryPhase: "idle",
		}},
		{Name: "geth-failed", Status: "online", PbtMigration: &models.ClientELPageDataPbtMigration{
			Available: true, LastUpdate: time.Now(), Phase: "importFailed", HasBinary: true, BinaryError: "root mismatch",
		}},
		{Name: "erigon-dual", Status: "online", PbtMigration: &models.ClientELPageDataPbtMigration{
			Available: true, LastUpdate: time.Now(), Mode: "hex+bin", HasShadowStopped: true, ShadowStopped: false,
		}},
		{Name: "no-method", Status: "online", PbtMigration: &models.ClientELPageDataPbtMigration{
			PollError: "the method debug_migrationProgress does not exist/is not available",
		}},
	}

	for _, show := range []bool{true, false} {
		data := &models.ClientsELPageData{Clients: clients, ClientCount: uint64(len(clients)), ShowPbtMigration: show}

		var out bytes.Buffer
		if err := tmpl.ExecuteTemplate(&out, "page", data); err != nil {
			t.Fatalf("execute (show=%v): %v", show, err)
		}
		got := out.String()

		if !show {
			if strings.Contains(got, "PBT migration") {
				t.Fatalf("PBT column rendered while disabled")
			}
			continue
		}
		for _, want := range []string{
			"PBT migration",
			`colspan="9"`,
			`24<span class="thousands-separator"></span>504<span class="thousands-separator"></span>400`,
			"(-100)",
			"0xd4597ecebdacec3c8633fe0fac261b4667f3bcbb3fcc29bed25cd24298b14eab",
			"importFailed",
			"root mismatch",
			"hex&#43;bin", // html/template escapes '+'

			"n/a",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("rendered page misses %q", want)
			}
		}
	}
}
