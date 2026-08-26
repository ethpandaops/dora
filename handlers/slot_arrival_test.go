package handlers

import "testing"

func TestParseSentryName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		network  string
		group    string
		operator string
		display  string
	}{
		{
			name:    "ethpandaops strips the network prefix",
			input:   "ethpandaops/mainnet/mainnet-lighthouse-geth-001",
			network: "mainnet",
			group:   "ethpandaops", operator: "ethpandaops", display: "lighthouse-geth-001",
		},
		{
			name:    "ethpandaops strips a utility prefix",
			input:   "ethpandaops/mainnet/utility-mainnet-bootnode-1",
			network: "mainnet",
			group:   "ethpandaops", operator: "ethpandaops", display: "bootnode-1",
		},
		{
			name:    "community node shortens the hashed suffix",
			input:   "pub-contributoor/someoperator/hashed-0123456789abcdef",
			network: "mainnet",
			group:   "community", operator: "someoperator", display: "someoperator #01234567",
		},
		{
			name:    "corp node shortens the hashed suffix",
			input:   "corp-contributoor/bigco/hashed-fedcba9876543210",
			network: "mainnet",
			group:   "corp", operator: "bigco", display: "bigco #fedcba98",
		},
		{
			name:    "short hash is left intact",
			input:   "pub-contributoor/op/hashed-abc",
			network: "mainnet",
			group:   "community", operator: "op", display: "op #abc",
		},
		{
			name:    "unknown classifier falls back to other",
			input:   "something-else/op/node-1",
			network: "mainnet",
			group:   "other", operator: "op", display: "node-1",
		},
		{
			name:    "name without the three-part shape passes through",
			input:   "my-local-xatu",
			network: "mainnet",
			group:   "other", operator: "", display: "my-local-xatu",
		},
		{
			name:    "empty name passes through",
			input:   "",
			network: "mainnet",
			group:   "other", operator: "", display: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, operator, display := parseSentryName(tt.input, tt.network)
			if group != tt.group || operator != tt.operator || display != tt.display {
				t.Errorf("parseSentryName(%q, %q)\n got  group=%q operator=%q display=%q\n want group=%q operator=%q display=%q",
					tt.input, tt.network, group, operator, display, tt.group, tt.operator, tt.display)
			}
		})
	}
}

func TestReduceSidecarName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Xatu Sidecar (lighthouse)", "lighthouse"},
		{"Xatu Sidecar (prysm)", "prysm"},
		{"lighthouse", "lighthouse"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := reduceSidecarName(tt.input); got != tt.want {
			t.Errorf("reduceSidecarName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
