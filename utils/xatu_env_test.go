package utils

import (
	"os"
	"testing"

	"github.com/ethpandaops/dora/types"
)

// envconfig matches either the parent-prefixed key or the bare tag value, so a
// tag must carry its own namespace. Without it, generic names like ENABLED or
// DATABASE would bind from the surrounding environment.
func TestXatuEnvNamesAreNamespaced(t *testing.T) {
	bare := map[string]func(*types.Config) bool{
		"ENABLED":        func(c *types.Config) bool { return c.Xatu.Enabled },
		"CLICKHOUSE_DSN": func(c *types.Config) bool { return c.Xatu.Raw.ClickhouseDsn != "" },
		"DATABASE":       func(c *types.Config) bool { return c.Xatu.Raw.Database != "" },
	}

	for name, bound := range bare {
		t.Setenv(name, "true")

		cfg := &types.Config{}
		if err := readConfigEnv(cfg); err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if bound(cfg) {
			t.Errorf("bare %s must not bind a xatu field", name)
		}
	}

	for _, tc := range []struct {
		env   string
		check func(*types.Config) bool
	}{
		{"XATU_ENABLED", func(c *types.Config) bool { return c.Xatu.Enabled }},
		{"XATU_RAW_CLICKHOUSE_DSN", func(c *types.Config) bool { return c.Xatu.Raw.ClickhouseDsn == "v" }},
		{"XATU_RAW_DATABASE", func(c *types.Config) bool { return c.Xatu.Raw.Database == "v" }},
		{"XATU_NETWORK_NAME", func(c *types.Config) bool { return c.Xatu.NetworkName == "v" }},
	} {
		value := "v"
		if tc.env == "XATU_ENABLED" {
			value = "true"
		}

		t.Setenv(tc.env, value)

		cfg := &types.Config{}
		if err := readConfigEnv(cfg); err != nil {
			t.Fatalf("%s: %v", tc.env, err)
		}

		if !tc.check(cfg) {
			t.Errorf("%s did not bind", tc.env)
		}

		os.Unsetenv(tc.env)
	}
}
