package types

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTrimPrunedPayload(t *testing.T) {
	tests := []struct {
		name        string
		size        int
		wantVisible int
		wantPruned  bool
	}{
		{name: "empty", size: 0, wantVisible: 0, wantPruned: false},
		{name: "below limit", size: 128, wantVisible: 128, wantPruned: false},
		{name: "at limit", size: TracePayloadLimit, wantVisible: TracePayloadLimit, wantPruned: false},
		{
			name:        "one past limit marks truncation",
			size:        TracePayloadLimit + 1,
			wantVisible: TracePayloadLimit,
			wantPruned:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			visible, pruned := TrimPrunedPayload(bytes.Repeat([]byte{0xab}, test.size))
			assert.Len(t, visible, test.wantVisible)
			assert.Equal(t, test.wantPruned, pruned)
		})
	}
}
