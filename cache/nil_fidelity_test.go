package cache

import (
	"testing"
	"time"

	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

func TestNilPointerFidelityThroughCache(t *testing.T) {
	utils.Config = &types.Config{}

	tc, err := NewTieredCache(100, "", "test", logrus.New())
	if err != nil {
		t.Fatal(err)
	}

	// a node that only reported gossip: every other series must stay nil
	p2p := uint32(742)
	in := &models.SlotArrivalResponse{
		Slot: 1, Settled: true,
		Nodes: []*models.SlotArrivalNode{{Name: "n", P2PMs: &p2p}},
	}

	if err := tc.Set("slotarrival:1:x", in, time.Hour); err != nil {
		t.Fatalf("set: %v", err)
	}

	out := &models.SlotArrivalResponse{}
	if _, err := tc.Get("slotarrival:1:x", out); err != nil {
		t.Fatalf("get: %v", err)
	}

	n := out.Nodes[0]
	t.Logf("after round trip: P2PMs=%v APIMs=%v HeadMs=%v NPMs=%v",
		deref(n.P2PMs), deref(n.APIMs), deref(n.HeadMs), deref(n.NPMs))

	for name, ptr := range map[string]*uint32{"APIMs": n.APIMs, "HeadMs": n.HeadMs, "NPMs": n.NPMs} {
		if ptr != nil {
			t.Errorf("%s should be nil after round trip, got pointer to %d", name, *ptr)
		}
	}
}

func deref(p *uint32) string {
	if p == nil {
		return "nil"
	}

	return "&" + itoa(*p)
}

func itoa(v uint32) string {
	if v == 0 {
		return "0"
	}

	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}

	return string(b)
}
