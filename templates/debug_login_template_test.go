package templates

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

// The debug login page is rendered to unauthenticated browsers: it must not embed the
// configured key and must hand the cookie name to the script as a JS string.
func TestDebugLoginTemplate(t *testing.T) {
	body, err := Files.ReadFile("debug_login/debug_login.html")
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	tmpl, err := template.New("t").Funcs(template.FuncMap(templateFuncs)).Parse(string(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := struct {
		Rejected   bool
		CookieName string
	}{Rejected: true, CookieName: "dora_debug_key"}

	for _, name := range []string{"page", "js", "css"} {
		var out bytes.Buffer
		if err := tmpl.ExecuteTemplate(&out, name, data); err != nil {
			t.Fatalf("execute %s: %v", name, err)
		}

		switch name {
		case "page":
			if !strings.Contains(out.String(), "The stored debug key was rejected") {
				t.Fatalf("page does not show the rejection notice")
			}
		case "js":
			if !strings.Contains(out.String(), `"dora_debug_key" + "="`) {
				t.Fatalf("js does not embed the cookie name as JS string:\n%s", out.String())
			}
		}
	}
}
