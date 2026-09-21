package utils

import (
	"strings"
	"testing"
	"text/template"
)

func TestFormatProposerWithBuildSourceTemplateExec(t *testing.T) {
	tmpl := template.Must(template.New("t").Funcs(GetTemplateFuncs()).Parse(
		`{{ formatProposerWithBuildSource 1 100 "V" true 42 "BuilderX" 3 10 }}` +
			`|{{ formatProposerWithBuildSource 1 100 "V" true 42 "BuilderX" 0 10 }}` +
			`|{{ formatProposerWithBuildSource 1 100 "V" true 42 "BuilderX" 0 0 }}` +
			`|{{ formatProposerWithBuildSource 1 100 "V" false 0 "" 0 0 }}`))
	var sb strings.Builder
	if err := tmpl.Execute(&sb, nil); err != nil {
		t.Fatalf("exec: %v", err)
	}
	out := sb.String()
	for _, want := range []string{
		"in-protocol bid (3/10 gossip)",
		"build-source-oop",
		"out-of-protocol bid (no gossip observation)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %v", want, out)
		}
	}
	// unknown-data case must NOT get the oop class
	last := strings.Split(out, "|")[2]
	if strings.Contains(last, "build-source-oop") {
		t.Fatalf("unexpected oop class for unknown bid data: %v", last)
	}
}
