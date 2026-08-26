package cache

import (
	"testing"
	"time"

	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

func TestEpochPageNilArrivalThroughCache(t *testing.T) {
	utils.Config = &types.Config{}

	tc, err := NewTieredCache(100, "", "test", logrus.New())
	if err != nil {
		t.Fatal(err)
	}

	// no arrival data for this slot: ArrivalNodes is the presence signal
	in := &models.EpochPageData{
		Epoch: 7,
		Slots: []*models.EpochPageDataSlot{
			{Slot: 224},
			{Slot: 225, ArrivalNodes: 12, ArrivalMinMs: 742, ArrivalP90Ms: 1180},
		},
	}

	if err := tc.Set("epoch:7", in, time.Hour); err != nil {
		t.Fatalf("set: %v", err)
	}

	out := &models.EpochPageData{}
	if _, err := tc.Get("epoch:7", out); err != nil {
		t.Fatalf("get: %v", err)
	}

	absent, present := out.Slots[0], out.Slots[1]
	t.Logf("absent slot: nodes=%d min=%d p90=%d", absent.ArrivalNodes, absent.ArrivalMinMs, absent.ArrivalP90Ms)
	t.Logf("present slot: nodes=%d min=%d p90=%d", present.ArrivalNodes, present.ArrivalMinMs, present.ArrivalP90Ms)

	if absent.ArrivalNodes != 0 {
		t.Errorf("slot with no data came back with %d nodes", absent.ArrivalNodes)
	}

	if present.ArrivalNodes != 12 || present.ArrivalMinMs != 742 || present.ArrivalP90Ms != 1180 {
		t.Errorf("real arrival data did not survive: nodes=%d min=%d p90=%d",
			present.ArrivalNodes, present.ArrivalMinMs, present.ArrivalP90Ms)
	}
}
