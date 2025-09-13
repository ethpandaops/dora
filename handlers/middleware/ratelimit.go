package middleware

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"

	"github.com/ethpandaops/dora/utils"
)

// rateLimitEntry represents a rate limiter for a specific key
type rateLimitEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimitMiddleware handles rate limiting for API requests
type RateLimitMiddleware struct {
	rateLimiters     map[string]*rateLimitEntry
	mutex            sync.RWMutex
	cleanupTimer     *time.Timer
	whitelistedIPs   map[string]bool
	whitelistedCIDRs []*net.IPNet
}

// NewRateLimitMiddleware creates a new rate limiting middleware instance
func NewRateLimitMiddleware() *RateLimitMiddleware {
	middleware := &RateLimitMiddleware{
		rateLimiters:     make(map[string]*rateLimitEntry),
		whitelistedIPs:   make(map[string]bool),
		whitelistedCIDRs: make([]*net.IPNet, 0),
	}

	// Parse whitelisted IPs and CIDRs from single list
	for _, ipOrCidr := range utils.Config.Api.WhitelistedIPs {
		// Try parsing as CIDR first
		if _, ipNet, err := net.ParseCIDR(ipOrCidr); err == nil {
			middleware.whitelistedCIDRs = append(middleware.whitelistedCIDRs, ipNet)
		} else {
			// Try parsing as individual IP (IPv4 or IPv6)
			if parsedIP := net.ParseIP(ipOrCidr); parsedIP != nil {
				middleware.whitelistedIPs[ipOrCidr] = true
			} else {
				logrus.WithField("entry", ipOrCidr).Warn("invalid IP/CIDR in whitelist, ignoring")
			}
		}
	}

	// Start cleanup timer
	middleware.startCleanupTimer()

	return middleware
}

// startCleanupTimer starts a timer to clean up old rate limiters
func (m *RateLimitMiddleware) startCleanupTimer() {
	m.cleanupTimer = time.AfterFunc(5*time.Minute, func() {
		m.cleanupOldLimiters()
		m.startCleanupTimer() // Restart timer
	})
}

// cleanupOldLimiters removes rate limiters that haven't been used recently
func (m *RateLimitMiddleware) cleanupOldLimiters() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	cutoff := time.Now().Add(-10 * time.Minute)
	for key, entry := range m.rateLimiters {
		if entry.lastSeen.Before(cutoff) {
			delete(m.rateLimiters, key)
		}
	}
}

// isWhitelisted checks if an IP address is whitelisted
func (m *RateLimitMiddleware) isWhitelisted(ip string) bool {
	// Check exact IP match
	if m.whitelistedIPs[ip] {
		return true
	}

	// Check CIDR match
	clientIP := net.ParseIP(ip)
	if clientIP == nil {
		return false
	}

	for _, ipNet := range m.whitelistedCIDRs {
		if ipNet.Contains(clientIP) {
			return true
		}
	}

	return false
}

// getRateLimiter gets or creates a rate limiter for a specific key
func (m *RateLimitMiddleware) getRateLimiter(key string, limit uint, burst uint) *rate.Limiter {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if burst == 0 {
		burst = 10
	}

	entry, exists := m.rateLimiters[key]
	if !exists {
		limiter := rate.NewLimiter(rate.Limit(limit)/60, int(burst)) // Convert per-minute to per-second
		entry = &rateLimitEntry{
			limiter:  limiter,
			lastSeen: time.Now(),
		}
		m.rateLimiters[key] = entry
	} else {
		entry.lastSeen = time.Now()
	}

	return entry.limiter
}

// Middleware applies rate limiting to API requests
func (m *RateLimitMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientIP := GetClientIP(r)

		// Determine rate limiting parameters
		var rateLimitKey string
		var rateLimit uint = utils.Config.Api.DefaultRateLimit
		var rateLimitBurst uint = utils.Config.Api.DefaultRateLimitBurst

		// Check for token-specific rate limits
		if tokenInfo := GetTokenInfo(r); tokenInfo != nil {
			// Use token-specific rate limit if set
			if tokenInfo.RateLimit > 0 {
				rateLimit = tokenInfo.RateLimit
				rateLimitBurst = tokenInfo.RateLimit // Use same value for burst
			} else {
				rateLimit = 0 // No rate limit for unlimited tokens
			}

			rateLimitKey = fmt.Sprintf("token:%s", tokenInfo.Name)
		} else {
			// Unauthenticated request - use IP-based rate limiting
			rateLimitKey = fmt.Sprintf("ip:%s", clientIP)
		}

		// Apply rate limiting (skip if disabled, whitelisted, or unlimited token)
		if !utils.Config.Api.DisableDefaultRateLimit && rateLimit > 0 && !m.isWhitelisted(clientIP) {
			limiter := m.getRateLimiter(rateLimitKey, rateLimit, rateLimitBurst)
			if !limiter.AllowN(time.Now(), int(1)) {
				// Add rate limit headers
				w.Header().Set("X-RateLimit-Limit", strconv.FormatUint(uint64(rateLimit), 10))
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))

				logrus.WithFields(logrus.Fields{
					"client_ip":      clientIP,
					"rate_limit_key": rateLimitKey,
					"rate_limit":     rateLimit,
				}).Warn("API rate limit exceeded")

				APIErrorResponse(w, http.StatusTooManyRequests, "ERROR: rate limit exceeded")
				return
			}

			// Add rate limit headers for successful requests
			w.Header().Set("X-RateLimit-Limit", strconv.FormatUint(uint64(rateLimit), 10))
			remaining := limiter.Tokens()
			if remaining < 0 {
				remaining = 0
			}
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatFloat(remaining, 'f', 0, 64))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Minute).Unix(), 10))
		}

		// Continue to next handler
		next.ServeHTTP(w, r)
	})
}
