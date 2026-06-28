package types

import (
	"testing"
)

func TestConfigGetProxyCount(t *testing.T) {
	tests := []struct {
		name       string
		serverVal  *uint
		rateVal    uint
		expected   uint
	}{
		{
			name:      "only server.proxyCount is defined",
			serverVal: uintPtr(3),
			rateVal:   0,
			expected:  3,
		},
		{
			name:      "only rateLimit.proxyCount is defined",
			serverVal: nil,
			rateVal:   5,
			expected:  5,
		},
		{
			name:      "both are defined, server.proxyCount should prioritize",
			serverVal: uintPtr(2),
			rateVal:   4,
			expected:  2,
		},
		{
			name:      "neither is defined (server.proxyCount nil, rateLimit.proxyCount 0)",
			serverVal: nil,
			rateVal:   0,
			expected:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{}
			cfg.Server.ProxyCount = tt.serverVal
			cfg.RateLimit.ProxyCount = tt.rateVal

			got := cfg.GetProxyCount()
			if got != tt.expected {
				t.Errorf("GetProxyCount() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

func uintPtr(u uint) *uint {
	return &u
}
