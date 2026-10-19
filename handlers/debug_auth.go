package handlers

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"

	"github.com/ethpandaops/dora/templates"
	"github.com/ethpandaops/dora/utils"
)

// debugKeyCookie carries the pprof key for the /debug/pprof pages in a browser.
// The login page sets it (scoped to /debug/pprof, SameSite=Strict) and reloads;
// see templates/debug_login/debug_login.html.
const debugKeyCookie = "dora_debug_key"

// DebugAuth serves the wrapped pprof handler only to requests that carry the
// configured pprof key, either as a bearer header (tools: curl, go tool pprof)
// or as the debugKeyCookie (browser). A browser without a valid key gets the
// login page, which stores the entered key as cookie and reloads; everything
// else gets a 401.
func DebugAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		hasCookie := false
		if key == "" {
			if cookie, err := r.Cookie(debugKeyCookie); err == nil {
				// the login page stores the key percent-encoded (cookie-safe)
				key, err = url.PathUnescape(cookie.Value)
				if err != nil {
					key = cookie.Value
				}
				hasCookie = true
			}
		}

		if checkDebugKey(key) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Cache-Control", "no-store")

		if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") {
			debugLoginPage(w, r, hasCookie)
			return
		}

		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "debug key required", http.StatusUnauthorized)
	})
}

// checkDebugKey compares the supplied key to the configured pprof key in
// constant time. An empty configured key never matches.
func checkDebugKey(key string) bool {
	expected := utils.Config.Frontend.PprofKey
	if expected == "" || key == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(key), []byte(expected)) == 1
}

// DebugLoginPageData holds the data for the debug login page.
type DebugLoginPageData struct {
	// Rejected is set when the request carried a key cookie that did not match.
	Rejected bool
	// CookieName is the cookie the page stores the entered key in.
	CookieName string
}

// debugLoginPage renders the key prompt for the pprof pages with a 401 status.
func debugLoginPage(w http.ResponseWriter, r *http.Request, rejected bool) {
	templateFiles := append(layoutTemplateFiles, "debug_login/debug_login.html")
	pageTemplate := templates.GetTemplate(templateFiles...)

	data := InitPageData(w, r, "blockchain", r.URL.Path, "Debug Login", templateFiles)
	data.Data = &DebugLoginPageData{
		Rejected:   rejected,
		CookieName: debugKeyCookie,
	}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusUnauthorized)
	if handleTemplateError(w, r, "debug_auth.go", "DebugLogin", "", pageTemplate.ExecuteTemplate(w, "layout", data)) != nil {
		return
	}
}
