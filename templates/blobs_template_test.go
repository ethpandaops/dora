package templates

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/ethpandaops/dora/types/models"
)

func TestBlobsTemplateWithoutPeerDASSpec(t *testing.T) {
	for _, tc := range []struct {
		name       string
		calculator *models.StorageCalculatorData
	}{
		{"uncached nil", nil},
		{"cached zero value", &models.StorageCalculatorData{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderBlobsTemplate(t, &models.BlobsPageData{BlobsLast24h: 12, StorageCalculator: tc.calculator})
			for _, want := range []string{"Storage calculator unavailable", `id="timeBlobs"`, "updateTimeStats('1d')"} {
				if !strings.Contains(got, want) {
					t.Errorf("rendered output missing %q", want)
				}
			}
			for _, unwanted := range []string{`id="ethSlider"`, "calculateCustodyColumns", "updateCalculator(undefined, true)"} {
				if strings.Contains(got, unwanted) {
					t.Errorf("rendered output unexpectedly contains %q", unwanted)
				}
			}
		})
	}
}

func TestBlobsTemplateWithPeerDASSpec(t *testing.T) {
	got := renderBlobsTemplate(t, &models.BlobsPageData{
		StorageCalculator: &models.StorageCalculatorData{
			MaxEth: 4096, DefaultEth: 32, MaxEffectiveBalanceEth: 32,
			ColumnSizeBytes: 2048, TotalColumns: 128, CustodyRequirement: 4,
			ValidatorCustodyRequirement: 8, SlotsPerEpoch: 32, MinEpochsForBlobSidecarsRequests: 4096,
		},
	})
	for _, want := range []string{`id="ethSlider"`, "calculateCustodyColumns", "updateCalculator(undefined, true)", "updateTimeStats('1d')"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q", want)
		}
	}
	if strings.Contains(got, "Storage calculator unavailable") {
		t.Error("rendered output marks available calculator as unavailable")
	}
}

func renderBlobsTemplate(t *testing.T, data *models.BlobsPageData) string {
	t.Helper()
	body, err := Files.ReadFile("blobs/blobs.html")
	if err != nil {
		t.Fatalf("read blobs template: %v", err)
	}

	tmpl, err := template.New("blobs").Funcs(template.FuncMap(templateFuncs)).Parse(string(body))
	if err != nil {
		t.Fatalf("parse blobs template: %v", err)
	}

	var out bytes.Buffer
	for _, name := range []string{"page", "js"} {
		if err := tmpl.ExecuteTemplate(&out, name, data); err != nil {
			t.Fatalf("execute %s: %v", name, err)
		}
	}
	return out.String()
}
