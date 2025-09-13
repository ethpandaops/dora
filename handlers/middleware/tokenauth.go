package middleware

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"

	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
)

type contextKey string

const (
	contextKeyTokenInfo contextKey = "token_info"
)

// TokenAuthMiddleware handles JWT token authentication for API requests
type TokenAuthMiddleware struct{}

// NewTokenAuthMiddleware creates a new token authentication middleware instance
func NewTokenAuthMiddleware() *TokenAuthMiddleware {
	return &TokenAuthMiddleware{}
}

// authenticateToken validates a JWT token and returns token information
func (m *TokenAuthMiddleware) authenticateToken(tokenString string) (*types.APITokenInfo, error) {
	if utils.Config.Api.AuthSecret == "" {
		return nil, fmt.Errorf("authentication secret not configured")
	}

	token, err := jwt.ParseWithClaims(tokenString, &types.APITokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(utils.Config.Api.AuthSecret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("invalid token: %v", err)
	}

	if claims, ok := token.Claims.(*types.APITokenClaims); ok && token.Valid {
		tokenInfo := &types.APITokenInfo{
			Name:           claims.Name,
			RateLimit:      claims.RateLimit,
			CorsOrigins:    claims.CorsOrigins,
			DomainPatterns: claims.DomainPatterns,
			IssuedAt:       claims.IssuedAt.Time,
		}

		if claims.ExpiresAt != nil {
			tokenInfo.ExpiresAt = &claims.ExpiresAt.Time
		}

		return tokenInfo, nil
	}

	return nil, fmt.Errorf("invalid token claims")
}

// validateDomainPatterns checks if the request domain matches any of the allowed patterns
func validateDomainPatterns(requestDomain string, patterns []string) bool {
	// If no patterns are specified, allow any domain
	if len(patterns) == 0 {
		return true
	}

	// Check each pattern
	for _, pattern := range patterns {
		// Use filepath.Match for wildcard pattern matching
		if matched, _ := filepath.Match(pattern, requestDomain); matched {
			return true
		}
		// Also check exact match (in case pattern has no wildcards)
		if pattern == requestDomain {
			return true
		}
	}

	return false
}

// Middleware processes JWT authentication and adds token info to request context
func (m *TokenAuthMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var tokenInfo *types.APITokenInfo

		// Check for Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			// Extract token from "Bearer <token>"
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
				tokenString := parts[1]
				var err error
				tokenInfo, err = m.authenticateToken(tokenString)
				if err != nil {
					clientIP := GetClientIP(r)
					logrus.WithError(err).WithField("client_ip", clientIP).Warn("API authentication failed")
					APIErrorResponse(w, http.StatusUnauthorized, "ERROR: invalid authentication token")
					return
				}

				// Validate domain patterns if specified in token
				requestDomain := r.Host
				if !validateDomainPatterns(requestDomain, tokenInfo.DomainPatterns) {
					clientIP := GetClientIP(r)
					logrus.WithFields(logrus.Fields{
						"client_ip":        clientIP,
						"token_name":       tokenInfo.Name,
						"request_domain":   requestDomain,
						"allowed_patterns": tokenInfo.DomainPatterns,
					}).Warn("API request rejected: domain not allowed for token")
					APIErrorResponse(w, http.StatusForbidden, "ERROR: token not valid for this domain")
					return
				}

				clientIP := GetClientIP(r)
				logrus.WithFields(logrus.Fields{
					"client_ip":  clientIP,
					"token_name": tokenInfo.Name,
				}).Debug("API request with valid token")
			} else {
				APIErrorResponse(w, http.StatusUnauthorized, "ERROR: invalid authorization header format")
				return
			}
		}

		// Check if authentication is required
		if utils.Config.Api.RequireAuth && tokenInfo == nil {
			// Skip authentication requirement for OPTIONS requests (CORS preflight)
			if r.Method == "OPTIONS" {
				next.ServeHTTP(w, r)
				return
			}

			clientIP := GetClientIP(r)
			logrus.WithField("client_ip", clientIP).Warn("API request rejected: authentication required")
			APIErrorResponse(w, http.StatusUnauthorized, "ERROR: authentication required")
			return
		}

		// Add token info to request context if authenticated
		if tokenInfo != nil {
			ctx := context.WithValue(r.Context(), contextKeyTokenInfo, tokenInfo)
			r = r.WithContext(ctx)
		}

		// Continue to next handler
		next.ServeHTTP(w, r)
	})
}

// GetTokenInfo extracts token information from request context
func GetTokenInfo(r *http.Request) *types.APITokenInfo {
	if tokenInfo, ok := r.Context().Value(contextKeyTokenInfo).(*types.APITokenInfo); ok {
		return tokenInfo
	}
	return nil
}
