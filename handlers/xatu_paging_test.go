package handlers

import (
	"strings"
	"testing"

	xch "github.com/ethpandaops/xatu/pkg/proto/clickhouse"
)

// The paged readers assume a page token is a row offset and that requesting
// page N yields OFFSET N*pageSize. If that contract changes upstream, paging
// would silently re-read or skip rows.
func TestPageTokenIsRowOffset(t *testing.T) {
	const pageSize = 10000

	for page, wantOffset := range map[int]string{0: "", 1: "10000", 2: "20000"} {
		token := ""
		if page > 0 {
			token = xch.EncodePageToken(uint32(page * pageSize))
		}

		q, err := xch.BuildListBeaconApiEthV1EventsBlockQuery(&xch.ListBeaconApiEthV1EventsBlockRequest{
			MetaNetworkName: &xch.StringFilter{Filter: &xch.StringFilter_Eq{Eq: "mainnet"}},
			Slot:            &xch.UInt32Filter{Filter: &xch.UInt32Filter_Eq{Eq: 1}},
			PageSize:        pageSize,
			PageToken:       token,
		})
		if err != nil {
			t.Fatalf("page %d: build: %v", page, err)
		}

		if !strings.Contains(q.Query, "LIMIT 10000") {
			t.Errorf("page %d: expected LIMIT 10000 in %q", page, q.Query)
		}

		if wantOffset == "" {
			if strings.Contains(q.Query, "OFFSET") {
				t.Errorf("page 0 should not paginate, got %q", q.Query)
			}

			continue
		}

		if !strings.Contains(q.Query, "OFFSET "+wantOffset) {
			t.Errorf("page %d: expected OFFSET %s, got %q", page, wantOffset, q.Query)
		}
	}
}

// A page size above the builders' ceiling must fail loudly, since that is what
// MaxQueryPageSize is pinned to.
func TestMaxPageSizeIsTheCeiling(t *testing.T) {
	base := func(size int32) error {
		_, err := xch.BuildListBeaconApiEthV1EventsBlockQuery(&xch.ListBeaconApiEthV1EventsBlockRequest{
			MetaNetworkName: &xch.StringFilter{Filter: &xch.StringFilter_Eq{Eq: "mainnet"}},
			Slot:            &xch.UInt32Filter{Filter: &xch.UInt32Filter_Eq{Eq: 1}},
			PageSize:        size,
		})

		return err
	}

	if err := base(10000); err != nil {
		t.Fatalf("10000 should be accepted: %v", err)
	}

	if err := base(10001); err == nil {
		t.Fatal("10001 should be rejected, MaxQueryPageSize is no longer the ceiling")
	}
}
