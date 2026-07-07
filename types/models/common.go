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

// EnsNameMapping maps a lowercase 0x-hex execution address to its primary ENS name.
// A slice of these is embedded in page models (SSZ-cacheable, unlike a map) and
// rendered client-side to swap displayed addresses for their name.
type EnsNameMapping struct {
	Address string `json:"a"`
	Name    string `json:"n"`
}

// EnsNameData is embedded in page models that display execution addresses. It carries
// the resolved primary ENS names and exposes them to the layout without reflection.
type EnsNameData struct {
	EnsNames []EnsNameMapping `json:"ens_names,omitempty"`
}

// SetEnsNames stores resolved names (address -> name) in cacheable slice form.
func (d *EnsNameData) SetEnsNames(names map[string]string) {
	d.EnsNames = EnsNamesFromMap(names)
}

// EnsNamesForJS returns the carried names as an address->name map for embedding in a
// page's ens-names <script type="application/json"> block. It is returned as a Go value
// (not pre-marshaled) so html/template JSON-encodes it exactly once in the script
// context — pre-marshaling to a string would get double-encoded there.
func (d *EnsNameData) EnsNamesForJS() map[string]string {
	if d == nil {
		return map[string]string{}
	}
	names := make(map[string]string, len(d.EnsNames))
	for _, mapping := range d.EnsNames {
		names[strings.ToLower(mapping.Address)] = mapping.Name
	}
	return names
}

// EnsNamesFromMap converts a resolver result map into the cacheable slice form.
func EnsNamesFromMap(names map[string]string) []EnsNameMapping {
	if len(names) == 0 {
		return nil
	}
	out := make([]EnsNameMapping, 0, len(names))
	for addr, name := range names {
		out = append(out, EnsNameMapping{Address: strings.ToLower(addr), Name: name})
	}
	return out
}
