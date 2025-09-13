package middleware

import (
	"context"
	"net/http"
	"sync"
)

type callCostKey string

const (
	contextKeyCallCost callCostKey = "call_cost"
)

var (
	endpointCosts = make(map[string]int)
	costMutex     sync.RWMutex
)

// SetEndpointCost sets the call cost for a specific endpoint path
func SetEndpointCost(path string, cost int) {
	costMutex.Lock()
	defer costMutex.Unlock()
	endpointCosts[path] = cost
}

// CallCostMiddleware sets call costs based on endpoint mapping
func CallCostMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get call cost from mapping
		costMutex.RLock()
		cost, exists := endpointCosts[r.URL.Path]
		costMutex.RUnlock()
		
		if !exists {
			cost = 1 // Default cost
		}
		
		// Store cost in request context for rate limiting middleware
		ctx := context.WithValue(r.Context(), contextKeyCallCost, cost)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetCallCost extracts the call cost from request context, defaults to 1
func GetCallCost(r *http.Request) int {
	if cost, ok := r.Context().Value(contextKeyCallCost).(int); ok {
		return cost
	}
	return 1 // Default cost for endpoints without explicit cost
}