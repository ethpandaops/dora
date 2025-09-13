package middleware

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// APIErrorResponse represents a standardized API error response
func APIErrorResponse(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	response := map[string]string{
		"status": message,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logrus.WithError(err).Error("failed to encode API error response")
	}
}

// GetClientIP extracts the real client IP from the request
func GetClientIP(r *http.Request) string {
	proxyCount := utils.Config.RateLimit.ProxyCount

	var ip string

	if proxyCount > 0 {
		forwardIps := strings.Split(r.Header.Get("X-Forwarded-For"), ", ")
		forwardIdx := len(forwardIps) - int(proxyCount)
		if forwardIdx >= 0 {
			ip = forwardIps[forwardIdx]
		}
	}
	if ip != "" {
		return ip
	}

	// Fall back to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
