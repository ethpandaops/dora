package types

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// APITokenClaims represents the JWT claims for API authentication
type APITokenClaims struct {
	Name           string   `json:"name"`
	RateLimit      uint     `json:"rate_limit,omitempty"`      // requests per minute, 0 = no limit
	CorsOrigins    []string `json:"cors_origins,omitempty"`    // allowed CORS origins, empty = use global config
	DomainPatterns []string `json:"domain_patterns,omitempty"` // allowed domain patterns, empty = any domain
	jwt.RegisteredClaims
}

// APITokenInfo contains information about an authenticated token
type APITokenInfo struct {
	Name           string
	RateLimit      uint
	CorsOrigins    []string
	DomainPatterns []string
	ExpiresAt      *time.Time
	IssuedAt       time.Time
}

// APIRateLimitInfo contains rate limiting information for a request
type APIRateLimitInfo struct {
	Limit     uint
	Remaining uint
	ResetTime time.Time
}
