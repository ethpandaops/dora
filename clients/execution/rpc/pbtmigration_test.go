package rpc

import (
	"encoding/json"
	"testing"
)

// Responses as returned by the pinned clients on glamsterdam-pbt-1.
func TestPbtMigrationProgressDecodesClientShapes(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		phase      string
		binPhase   string
		cursor     uint64
		shadowRoot string
		mode       string
		stopped    *bool
	}{
		{name: "geth importing", raw: `{"Phase":"importing","Binary":null,"Merkle":null}`, phase: "importing"},
		{name: "besu idle", raw: `{"Phase":"running","Binary":{"Phase":"idle","Cursor":0,"CursorHash":"","ShadowRoot":"","Error":""},"Merkle":null}`,
			phase: "running", binPhase: "idle"},
		{name: "nethermind lowercase", raw: `{"phase":"importing","binary":null,"merkle":null}`, phase: "importing"},
		{name: "hex cursor", raw: `{"Phase":"running","Binary":{"Phase":"following","Cursor":"0x175e8c8","ShadowRoot":"0xd4597e"}}`,
			phase: "running", binPhase: "following", cursor: 24504520, shadowRoot: "0xd4597e"},
		{name: "erigon dual", raw: `{"mode":"hex+bin","shadowStopped":false}`, mode: "hex+bin", stopped: new(bool)},
	}

	for _, c := range cases {
		res := &PbtMigrationProgress{}
		if err := json.Unmarshal([]byte(c.raw), res); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if res.Phase != c.phase || res.Mode != c.mode {
			t.Errorf("%s: phase/mode = %q/%q", c.name, res.Phase, res.Mode)
		}
		if (c.stopped == nil) != (res.ShadowStopped == nil) || (c.stopped != nil && *c.stopped != *res.ShadowStopped) {
			t.Errorf("%s: shadowStopped = %v", c.name, res.ShadowStopped)
		}
		if c.binPhase == "" {
			if res.Binary != nil {
				t.Errorf("%s: unexpected binary %+v", c.name, res.Binary)
			}
			continue
		}
		if res.Binary == nil || res.Binary.Phase != c.binPhase || uint64(res.Binary.Cursor) != c.cursor || res.Binary.ShadowRoot != c.shadowRoot {
			t.Errorf("%s: binary = %+v", c.name, res.Binary)
		}
	}
}
