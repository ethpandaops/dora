package handlers

import "testing"

func TestAttesterClientName(t *testing.T) {
	for name, want := range map[string]string{
		"lighthouse-geth-1":             "lighthouse",
		"Silver-geth-2":                 "silver",
		"ethlambda":                     "ethlambda",
		"teku_besu_3":                   "teku",
		"ethpandaops-lighthouse-geth-1": "lighthouse",
		"caplin-erigon-1":               "caplin",
		"nimbusel-nimbus-1":             "nimbus",
		"":                              "unknown",
		"Lido - Operator":               "unknown",
	} {
		if got := attesterClientName(name); got != want {
			t.Errorf("attesterClientName(%q) = %q, want %q", name, got, want)
		}
	}
}
