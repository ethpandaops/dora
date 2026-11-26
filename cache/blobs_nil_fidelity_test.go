package cache

import (
	"testing"
	"time"

	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
	"github.com/sirupsen/logrus"
)

func TestBlobsPageKeepsUnavailableCalculatorThroughCache(t *testing.T) {
	previousConfig := utils.Config
	utils.Config = &types.Config{}
	t.Cleanup(func() { utils.Config = previousConfig })

	cache, err := NewTieredCache(100, "", "test", logrus.New())
	if err != nil {
		t.Fatal(err)
	}

	in := &models.BlobsPageData{BlobsLast24h: 12, StorageCalculator: &models.StorageCalculatorData{}}
	if err := cache.Set("blobs", in, time.Hour); err != nil {
		t.Fatalf("cache blobs page: %v", err)
	}

	out := &models.BlobsPageData{}
	if _, err := cache.Get("blobs", out); err != nil {
		t.Fatalf("read blobs page: %v", err)
	}
	if out.BlobsLast24h != 12 || out.StorageCalculator == nil || out.StorageCalculator.TotalColumns != 0 {
		t.Errorf("cached page changed unavailable calculator or blob count: %+v", out)
	}
}
