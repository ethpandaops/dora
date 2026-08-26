package cache

import (
	"testing"
	"time"

	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

// The waves response leans on nil to mean "no data": a nil section hides its
// panel, a nil FirstSeenMs renders "not seen". Like the arrival response, it
// must stay on the cache's JSON path, where nil survives a round trip.
func TestWavesNilFidelityThroughCache(t *testing.T) {
	utils.Config = &types.Config{}

	tc, err := NewTieredCache(100, "", "test", logrus.New())
	if err != nil {
		t.Fatal(err)
	}

	pct := 87.5
	in := &models.SlotWavesResponse{
		Slot: 9, Settled: true,
		// Attestations deliberately nil: a network without the cbt attestation
		// model must not come back as an empty wave.
		Columns: &models.SlotColumnWave{
			BlobCount: 3,
			Columns: []*models.SlotColumn{
				// probed but never seen
				{Index: 0, AvailabilityPct: &pct, Probes: 4},
				// seen but never probed
				{Index: 1, FirstSeenMs: ptr(uint32(1300))},
			},
		},
	}

	if err := tc.Set("slotwaves:9:x", in, time.Hour); err != nil {
		t.Fatalf("set: %v", err)
	}

	out := &models.SlotWavesResponse{}
	if _, err := tc.Get("slotwaves:9:x", out); err != nil {
		t.Fatalf("get: %v", err)
	}

	if out.Attestations != nil {
		t.Errorf("Attestations should stay nil, got %+v", out.Attestations)
	}

	if out.Columns == nil {
		t.Fatal("Columns section lost")
	}

	unseen, unprobed := out.Columns.Columns[0], out.Columns.Columns[1]

	if unseen.FirstSeenMs != nil {
		t.Errorf("unseen column got FirstSeenMs %d", *unseen.FirstSeenMs)
	}

	if unseen.AvailabilityPct == nil || *unseen.AvailabilityPct != pct {
		t.Errorf("availability lost: %v", unseen.AvailabilityPct)
	}

	if unprobed.AvailabilityPct != nil {
		t.Errorf("unprobed column got AvailabilityPct %v", *unprobed.AvailabilityPct)
	}

	if unprobed.FirstSeenMs == nil || *unprobed.FirstSeenMs != 1300 {
		t.Errorf("first seen lost: %v", unprobed.FirstSeenMs)
	}
}

func ptr[T any](v T) *T {
	return &v
}
