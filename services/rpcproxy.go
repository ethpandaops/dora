package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// RPCRequest represents a JSON-RPC 2.0 request
type RPCRequest struct {
	ID      interface{} `json:"id"`
	Jsonrpc string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// RPCResponse represents a JSON-RPC 2.0 response
type RPCResponse struct {
	ID      interface{} `json:"id"`
	Jsonrpc string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// RateLimiter implements a token bucket rate limiter per IP
type RateLimiter struct {
	buckets         map[string]*TokenBucket
	mutex           sync.RWMutex
	rate            time.Duration // Time between refills
	burst           int           // Maximum tokens
	cleanupInterval time.Duration // Cleanup interval
}

// TokenBucket represents a token bucket for rate limiting
type TokenBucket struct {
	tokens     int
	lastRefill time.Time
}

// RPCProxyConfig holds the configuration for the RPC proxy
type RPCProxyConfig struct {
	// Upstream execution client endpoint
	UpstreamURL string

	// Rate limiting
	RequestsPerMinute int // Requests per minute per IP
	BurstLimit        int // Maximum burst requests

	// Method filtering
	AllowedMethods []string // Whitelist of allowed methods

	// Request timeout
	Timeout time.Duration

	// Logging
	LogRequests bool
}

// RPCProxy implements a filtered JSON-RPC proxy with rate limiting
type RPCProxy struct {
	config      *RPCProxyConfig
	rateLimiter *RateLimiter
	httpClient  *http.Client
	logger      *logrus.Entry
}

// NewRPCProxy creates a new RPC proxy instance
func NewRPCProxy(config *RPCProxyConfig) *RPCProxy {
	// Default configuration
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.RequestsPerMinute == 0 {
		config.RequestsPerMinute = 30 // 30 requests per minute by default
	}
	if config.BurstLimit == 0 {
		config.BurstLimit = 10 // 10 burst requests by default
	}
	if len(config.AllowedMethods) == 0 {
		// Default safe methods for validator operations
		config.AllowedMethods = []string{
			"eth_blockNumber",
			"eth_getStorageAt",
			"eth_getLogs",
			"eth_call",
			"eth_estimateGas",
			"eth_gasPrice",
			"eth_maxPriorityFeePerGas",
			"eth_feeHistory",
			"eth_getBalance",
			"eth_getTransactionCount",
			"eth_getCode",
			"eth_chainId",
			"net_version",
		}
	}

	rateLimiter := &RateLimiter{
		buckets:         make(map[string]*TokenBucket),
		rate:            time.Minute / time.Duration(config.RequestsPerMinute),
		burst:           config.BurstLimit,
		cleanupInterval: 10 * time.Minute, // Clean up old buckets every 10 minutes
	}

	// Start cleanup routine
	go rateLimiter.cleanupOldBuckets()

	return &RPCProxy{
		config:      config,
		rateLimiter: rateLimiter,
		httpClient: &http.Client{
			Timeout: config.Timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		logger: logrus.WithField("service", "rpcproxy"),
	}
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(requestsPerMinute, burstLimit int) *RateLimiter {
	rl := &RateLimiter{
		buckets:         make(map[string]*TokenBucket),
		rate:            time.Minute / time.Duration(requestsPerMinute),
		burst:           burstLimit,
		cleanupInterval: 10 * time.Minute,
	}
	go rl.cleanupOldBuckets()
	return rl
}

// Allow checks if a request from the given IP is allowed
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mutex.Lock()
	defer rl.mutex.Unlock()

	bucket, exists := rl.buckets[ip]
	if !exists {
		bucket = &TokenBucket{
			tokens:     rl.burst - 1, // Use one token immediately
			lastRefill: time.Now(),
		}
		rl.buckets[ip] = bucket
		return true
	}

	// Refill tokens based on elapsed time
	now := time.Now()
	elapsed := now.Sub(bucket.lastRefill)
	tokensToAdd := int(elapsed / rl.rate)

	if tokensToAdd > 0 {
		bucket.tokens += tokensToAdd
		if bucket.tokens > rl.burst {
			bucket.tokens = rl.burst
		}
		bucket.lastRefill = now
	}

	// Check if we have tokens available
	if bucket.tokens > 0 {
		bucket.tokens--
		return true
	}

	return false
}

// cleanupOldBuckets removes old buckets periodically
func (rl *RateLimiter) cleanupOldBuckets() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		rl.mutex.Lock()
		now := time.Now()
		for ip, bucket := range rl.buckets {
			// Remove buckets that haven't been used in the last cleanup interval
			if now.Sub(bucket.lastRefill) > rl.cleanupInterval {
				delete(rl.buckets, ip)
			}
		}
		rl.mutex.Unlock()
	}
}

// isMethodAllowed checks if the given method is in the allowlist
func (rp *RPCProxy) isMethodAllowed(method string) bool {
	for _, allowedMethod := range rp.config.AllowedMethods {
		if allowedMethod == method {
			return true
		}
	}
	return false
}

// validateRequest validates the JSON-RPC request
func (rp *RPCProxy) validateRequest(req *RPCRequest) error {
	// Check if method is allowed
	if !rp.isMethodAllowed(req.Method) {
		return fmt.Errorf("method '%s' is not allowed", req.Method)
	}

	// Additional parameter validation based on method
	switch req.Method {
	case "eth_getStorageAt":
		params, ok := req.Params.([]interface{})
		if !ok || len(params) < 2 {
			return fmt.Errorf("eth_getStorageAt requires at least 2 parameters")
		}

		// Validate address format (basic check)
		address, ok := params[0].(string)
		if !ok || !strings.HasPrefix(address, "0x") || len(address) != 42 {
			return fmt.Errorf("invalid address format")
		}

	case "eth_getLogs":
		params, ok := req.Params.([]interface{})
		if !ok || len(params) != 1 {
			return fmt.Errorf("eth_getLogs requires exactly 1 parameter")
		}

		// Validate filter object
		filter, ok := params[0].(map[string]interface{})
		if !ok {
			return fmt.Errorf("eth_getLogs filter must be an object")
		}

		// Check for reasonable block range to prevent DoS
		if fromBlock, exists := filter["fromBlock"]; exists {
			if toBlock, exists := filter["toBlock"]; exists {
				if from, err := parseBlockNumber(fromBlock); err == nil {
					if to, err := parseBlockNumber(toBlock); err == nil {
						if to-from > 1000 {
							return fmt.Errorf("block range too large (max 1000 blocks)")
						}
					}
				}
			}
		}

	case "eth_call":
		params, ok := req.Params.([]interface{})
		if !ok || len(params) < 1 {
			return fmt.Errorf("eth_call requires at least 1 parameter")
		}
	}

	return nil
}

// parseBlockNumber parses a block number from various formats
func parseBlockNumber(block interface{}) (int64, error) {
	switch v := block.(type) {
	case string:
		if strings.HasPrefix(v, "0x") {
			return strconv.ParseInt(v[2:], 16, 64)
		}
		switch v {
		case "latest", "pending", "earliest":
			return 0, nil // Special values are allowed
		default:
			return strconv.ParseInt(v, 10, 64)
		}
	case float64:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("invalid block number format")
	}
}

// getClientIP extracts the client IP from the request
func (rp *RPCProxy) getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for reverse proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain
		if ips := strings.Split(xff, ","); len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to remote address
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ServeHTTP implements the HTTP handler for the RPC proxy
func (rp *RPCProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only allow POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get client IP and check rate limit
	clientIP := rp.getClientIP(r)
	if !rp.rateLimiter.Allow(clientIP) {
		rp.logger.WithField("client_ip", clientIP).Warn("Rate limit exceeded")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(RPCResponse{
			Jsonrpc: "2.0",
			Error: &RPCError{
				Code:    -32000,
				Message: "Rate limit exceeded",
			},
		})
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		rp.logger.WithError(err).Error("Failed to read request body")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Parse JSON-RPC request
	var rpcReq RPCRequest
	if err := json.Unmarshal(body, &rpcReq); err != nil {
		rp.logger.WithError(err).WithField("client_ip", clientIP).Error("Failed to parse JSON-RPC request")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(RPCResponse{
			Jsonrpc: "2.0",
			Error: &RPCError{
				Code:    -32700,
				Message: "Parse error",
			},
		})
		return
	}

	// Validate request
	if err := rp.validateRequest(&rpcReq); err != nil {
		rp.logger.WithError(err).WithFields(logrus.Fields{
			"client_ip": clientIP,
			"method":    rpcReq.Method,
		}).Warn("Request validation failed")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(RPCResponse{
			ID:      rpcReq.ID,
			Jsonrpc: "2.0",
			Error: &RPCError{
				Code:    -32602,
				Message: "Invalid params: " + err.Error(),
			},
		})
		return
	}

	// Log request if configured
	if rp.config.LogRequests {
		rp.logger.WithFields(logrus.Fields{
			"client_ip": clientIP,
			"method":    rpcReq.Method,
			"id":        rpcReq.ID,
		}).Info("Proxying RPC request")
	}

	// Forward request to upstream
	start := time.Now()
	upstreamReq, err := http.NewRequestWithContext(r.Context(), "POST", rp.config.UpstreamURL, bytes.NewBuffer(body))
	if err != nil {
		rp.logger.WithError(err).Error("Failed to create upstream request")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Copy headers
	upstreamReq.Header.Set("Content-Type", "application/json")

	// Execute upstream request
	resp, err := rp.httpClient.Do(upstreamReq)
	if err != nil {
		rp.logger.WithError(err).WithField("method", rpcReq.Method).Error("Upstream request failed")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(RPCResponse{
			ID:      rpcReq.ID,
			Jsonrpc: "2.0",
			Error: &RPCError{
				Code:    -32603,
				Message: "Internal error: upstream unavailable",
			},
		})
		return
	}
	defer resp.Body.Close()

	// Read upstream response
	upstreamBody, err := io.ReadAll(resp.Body)
	if err != nil {
		rp.logger.WithError(err).Error("Failed to read upstream response")
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}

	// Log response time
	duration := time.Since(start)
	if rp.config.LogRequests {
		rp.logger.WithFields(logrus.Fields{
			"client_ip":       clientIP,
			"method":          rpcReq.Method,
			"duration_ms":     duration.Milliseconds(),
			"upstream_status": resp.StatusCode,
		}).Info("RPC request completed")
	}

	// Forward response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(upstreamBody)
}
