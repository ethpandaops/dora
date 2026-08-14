package models

import "strings"

type UrlParam struct {
	Key   string
	Value string
}

type KeyValue struct {
	Key   string `json:"k"`
	Value string `json:"v"`
}

// EnsNameEntry is one resolved ENS name for an address on a specific network.
type EnsNameEntry struct {
	Name    string `json:"name"`
	Network string `json:"network"`         // display name of the network the name was resolved on
	Local   bool   `json:"local,omitempty"` // resolved on the chain this explorer indexes
}

// EnsNameMapping maps a lowercase 0x-hex execution address to all its resolved ENS
// names (primary/display name first: local network, then remotes in config order).
// A slice of these is embedded in page models (SSZ-cacheable, unlike a map) and
// rendered client-side to swap displayed addresses for their name.
type EnsNameMapping struct {
	Address string         `json:"a"`
	Names   []EnsNameEntry `json:"n"`
}

// EnsNameData is embedded in page models that display execution addresses. It carries
// the resolved ENS names and exposes them to the layout without reflection.
type EnsNameData struct {
	EnsNames []EnsNameMapping `json:"ens_names,omitempty"`
}

// SetEnsNames stores resolved names (address -> per-network names) in cacheable slice form.
func (d *EnsNameData) SetEnsNames(names map[string][]EnsNameEntry) {
	d.EnsNames = EnsNamesFromMap(names)
}

// EnsNamesForJS returns the carried names as an address->entries map
// (map[string][]EnsNameEntry, typed `any` to satisfy the provider interface in utils,
// which must not import this package) for embedding in a page's ens-names
// <script type="application/json"> block. It is returned as a Go value (not
// pre-marshaled) so html/template JSON-encodes it exactly once in the script context —
// pre-marshaling to a string would get double-encoded there.
func (d *EnsNameData) EnsNamesForJS() any {
	if d == nil {
		return map[string][]EnsNameEntry{}
	}
	names := make(map[string][]EnsNameEntry, len(d.EnsNames))
	for _, mapping := range d.EnsNames {
		names[strings.ToLower(mapping.Address)] = mapping.Names
	}
	return names
}

// EnsNamesFromMap converts a resolver result map into the cacheable slice form.
func EnsNamesFromMap(names map[string][]EnsNameEntry) []EnsNameMapping {
	if len(names) == 0 {
		return nil
	}
	out := make([]EnsNameMapping, 0, len(names))
	for addr, entries := range names {
		if len(entries) == 0 {
			continue
		}
		out = append(out, EnsNameMapping{Address: strings.ToLower(addr), Names: entries})
	}
	return out
}
