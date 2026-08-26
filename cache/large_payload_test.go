package cache

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// A mainnet-scale slot arrival payload runs to hundreds of kilobytes, well past
// the 100KB MaxEntrySize. That setting only sizes the initial allocation; the
// real ceiling is HardMaxCacheSize/Shards, so these must still round trip. If
// they ever stop, every request rebuilds and the page cache silently stops
// absorbing load.
func TestLargeArrivalPayloadRoundTrips(t *testing.T) {
	utils.Config = &types.Config{}

	tc, err := NewTieredCache(100, "", "test", logrus.New())
	if err != nil {
		t.Fatalf("cache init: %v", err)
	}

	for _, nodes := range []int{500, 1000, 2000} {
		resp := &models.SlotArrivalResponse{Slot: 15060285, Settled: true}
		for i := 0; i < nodes; i++ {
			resp.Nodes = append(resp.Nodes, &models.SlotArrivalNode{
				Name:           fmt.Sprintf("pub-contributoor/operator-%04d/hashed-abcdef01", i),
				FullName:       fmt.Sprintf("pub-contributoor/operator-%04d/hashed-abcdef0123456789", i),
				Group:          "community",
				Implementation: "lighthouse",
				Continent:      "EU",
				Country:        "Germany",
				CountryCode:    "de",
				MinMs:          uint32(700 + i%800),
			})
		}

		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		key := fmt.Sprintf("slotarrival:15060285:%d", nodes)
		if err := tc.Set(key, resp, time.Hour); err != nil {
			t.Errorf("%d nodes (%d KB payload): Set failed: %v", nodes, len(raw)/1024, err)

			continue
		}

		out := &models.SlotArrivalResponse{}
		if _, err := tc.Get(key, out); err != nil {
			t.Errorf("%d nodes (%d KB): Get failed: %v", nodes, len(raw)/1024, err)

			continue
		}

		if len(out.Nodes) != nodes {
			t.Errorf("%d nodes: round-tripped %d", nodes, len(out.Nodes))

			continue
		}

		t.Logf("%4d nodes, %4d KB payload: cached and retrieved OK", nodes, len(raw)/1024)
	}
}
