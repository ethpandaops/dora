package middleware

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/ethpandaops/dora/utils"
)

func CorsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && utils.Config.Api.Enabled {
			// Check if origin matches any of the allowed origins
			for _, allowed := range utils.Config.Api.CorsOrigins {
				if matchOrigin(allowed, origin) {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
					break
				}
			}
		}

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func matchOrigin(pattern, origin string) bool {
	if pattern == "*" {
		return true
	}

	// Escape special regex chars except *
	pattern = regexp.QuoteMeta(pattern)
	// Replace * with regex pattern for any characters
	pattern = strings.ReplaceAll(pattern, "\\*", ".*")
	pattern = "^" + pattern + "$"

	matched, err := regexp.MatchString(pattern, origin)
	if err != nil {
		return false
	}
	return matched
}
