package replay

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func testReplay(virtualSlot uint64) *Replay {
	logger := logrus.New()
	logger.SetLevel(logrus.ErrorLevel)

	return &Replay{
		logger:  logger,
		events:  newEventHub(logger),
		control: newLossyEventHub(logger),
		states:  newStateLoads(),
		chain: &chainInfo{
			slotsPerEpoch: 32,
			slotDuration:  12 * time.Second,
		},
		upstream:    &upstream{logger: logger},
		virtualSlot: virtualSlot,
		head: &blockHeader{
			Slot:      virtualSlot - 1,
			Root:      "0x1111111111111111111111111111111111111111111111111111111111111111",
			StateRoot: "0x2222222222222222222222222222222222222222222222222222222222222222",
		},
		finality: &finalityCheckpoints{
			JustifiedEpoch: 10,
			JustifiedRoot:  "0x3333333333333333333333333333333333333333333333333333333333333333",
			FinalizedEpoch: 9,
			FinalizedRoot:  "0x4444444444444444444444444444444444444444444444444444444444444444",
		},
	}
}

func TestRewritePath(t *testing.T) {
	replay := testReplay(100)
	ctx := context.Background()

	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{
			name: "head header resolves to the head block root",
			path: "/eth/v1/beacon/headers/head",
			want: "/eth/v1/beacon/headers/" + replay.head.Root,
		},
		{
			name: "head finality resolves to the head state root",
			path: "/eth/v1/beacon/states/head/finality_checkpoints",
			want: "/eth/v1/beacon/states/" + replay.head.StateRoot + "/finality_checkpoints",
		},
		{
			name: "finalized block id resolves to the tracked checkpoint",
			path: "/eth/v2/beacon/blocks/finalized",
			want: "/eth/v2/beacon/blocks/" + replay.finality.FinalizedRoot,
		},
		{
			name: "roots are passed through",
			path: "/eth/v2/debug/beacon/states/0x9999999999999999999999999999999999999999999999999999999999999999",
			want: "/eth/v2/debug/beacon/states/0x9999999999999999999999999999999999999999999999999999999999999999",
		},
		{
			name: "genesis is passed through",
			path: "/eth/v2/debug/beacon/states/genesis",
			want: "/eth/v2/debug/beacon/states/genesis",
		},
		{
			name: "a slot at the virtual head is served",
			path: "/eth/v1/beacon/headers/100",
			want: "/eth/v1/beacon/headers/100",
		},
		{
			name:    "a slot beyond the virtual head is hidden",
			path:    "/eth/v1/beacon/headers/101",
			wantErr: true,
		},
		{
			name: "unrelated paths are untouched",
			path: "/eth/v1/config/spec",
			want: "/eth/v1/config/spec",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := replay.rewritePath(ctx, test.path)

			if test.wantErr {
				require.ErrorIs(t, err, errNotFound)
				return
			}

			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestRewritePathWithoutHead(t *testing.T) {
	replay := testReplay(100)
	replay.head = nil

	_, err := replay.rewritePath(context.Background(), "/eth/v1/beacon/headers/head")
	require.ErrorIs(t, err, errNotFound)
}

func TestParseRoot(t *testing.T) {
	root := parseRoot("0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	require.Equal(t, byte(0x01), root[0])
	require.Equal(t, byte(0x20), root[31])

	// a malformed root degrades to the zero root rather than panicking
	require.Equal(t, parseRoot("nonsense"), parseRoot("0x00"))
}
