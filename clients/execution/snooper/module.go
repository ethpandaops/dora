package snooper

import (
	"sync"

	"github.com/ethpandaops/dora/utils"
)

// ModuleConfig represents the configuration for a snooper module
type ModuleConfig struct {
	Type   string         // Module type (e.g., "response_tracer", "request_snooper")
	Name   string         // Human-readable name
	Config map[string]any // Module-specific configuration
}

// ModuleRegistration represents a registered module
type ModuleRegistration struct {
	Config     ModuleConfig
	ModuleID   uint64
	dispatcher utils.Dispatcher[*WSMessageWithBinary]
	mu         sync.RWMutex
}

// Subscribe creates a subscription for events from this module
func (m *ModuleRegistration) Subscribe(capacity int, blocking bool) *utils.Subscription[*WSMessageWithBinary] {
	return m.dispatcher.Subscribe(capacity, blocking)
}

// FireEvent sends an event to all subscribers of this module
func (m *ModuleRegistration) FireEvent(event *WSMessageWithBinary) {
	m.dispatcher.Fire(event)
}
