package handlers

import (
	"fmt"
	"strings"
	"testing"

	cbtch "github.com/ethpandaops/xatu-cbt/pkg/proto/clickhouse"
)

// The cbt cluster runs with force_primary_key, and which of slot or
// slot_start_date_time leads a table's primary key differs between tables and
// replicas. Every cbt query therefore has to filter on both; a builder that
// silently dropped one would only fail in production.
func TestCbtQueriesFilterBothPrimaryKeys(t *testing.T) {
	slotFilter := &cbtch.UInt32Filter{Filter: &cbtch.UInt32Filter_Eq{Eq: 123}}
	timeFilter := &cbtch.UInt32Filter{Filter: &cbtch.UInt32Filter_Eq{Eq: 456}}

	queries := map[string]func() (cbtch.SQLQuery, error){
		"attestation wave": func() (cbtch.SQLQuery, error) {
			return cbtch.BuildListFctAttestationFirstSeenChunked50MsQuery(&cbtch.ListFctAttestationFirstSeenChunked50MsRequest{
				Slot: slotFilter, SlotStartDateTime: timeFilter,
			})
		},
		"column first seen": func() (cbtch.SQLQuery, error) {
			return cbtch.BuildListFctBlockDataColumnSidecarFirstSeenQuery(&cbtch.ListFctBlockDataColumnSidecarFirstSeenRequest{
				Slot: slotFilter, SlotStartDateTime: timeFilter,
			})
		},
		"column availability": func() (cbtch.SQLQuery, error) {
			return cbtch.BuildListFctDataColumnAvailabilityBySlotQuery(&cbtch.ListFctDataColumnAvailabilityBySlotRequest{
				Slot: slotFilter, SlotStartDateTime: timeFilter,
			})
		},
	}

	for name, build := range queries {
		q, err := build()
		if err != nil {
			t.Fatalf("%s: build: %v", name, err)
		}

		for _, column := range []string{"slot =", "slot_start_date_time ="} {
			if !strings.Contains(q.Query, column) {
				t.Errorf("%s: missing %q filter in %q", name, column, q.Query)
			}
		}
	}
}

// The paged reader contract from the raw tables holds for the cbt builders
// too: a page token is a row offset, and the ceiling matches MaxQueryPageSize.
func TestCbtPageTokenIsRowOffset(t *testing.T) {
	const pageSize = 10000

	for page, wantOffset := range map[int]string{0: "", 1: "10000", 2: "20000"} {
		q, err := cbtch.BuildListFctAttestationFirstSeenChunked50MsQuery(&cbtch.ListFctAttestationFirstSeenChunked50MsRequest{
			Slot:      &cbtch.UInt32Filter{Filter: &cbtch.UInt32Filter_Eq{Eq: 1}},
			PageSize:  pageSize,
			PageToken: cbtPageToken(uint32(page * pageSize)),
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

func TestCbtMaxPageSizeIsTheCeiling(t *testing.T) {
	build := func(size int32) error {
		_, err := cbtch.BuildListFctAttestationFirstSeenChunked50MsQuery(&cbtch.ListFctAttestationFirstSeenChunked50MsRequest{
			Slot:     &cbtch.UInt32Filter{Filter: &cbtch.UInt32Filter_Eq{Eq: 1}},
			PageSize: size,
		})

		return err
	}

	if err := build(10000); err != nil {
		t.Fatalf("page size 10000 must be accepted: %v", err)
	}

	if err := build(10001); err == nil {
		t.Fatal("page size 10001 must be rejected")
	}
}

// A slot under attack can carry votes for many roots; the response merges the
// tail so the payload stays bounded, and the viewed root keeps its own entry
// even when it is not the most voted one.
func TestAssembleWaveRootsMergesTheTail(t *testing.T) {
	roots := map[string]*waveRoot{}

	for i := 0; i < maxWaveRoots+3; i++ {
		root := fmt.Sprintf("0x%064d", i)
		roots[root] = &waveRoot{
			root:    root,
			count:   100 - i,
			buckets: map[uint32]int{4000: 100 - i},
		}
	}

	total := 0
	for _, root := range roots {
		total += root.count
	}

	viewedRoot := fmt.Sprintf("0x%064d", 1)
	wave := assembleWaveRoots(roots, total, viewedRoot)

	if len(wave.Roots) != maxWaveRoots+1 {
		t.Fatalf("expected %d roots after merge, got %d", maxWaveRoots+1, len(wave.Roots))
	}

	last := wave.Roots[len(wave.Roots)-1]
	if last.Root != "" {
		t.Errorf("merged tail must have an empty root, got %q", last.Root)
	}

	if last.Count != (100-maxWaveRoots)+(100-maxWaveRoots-1)+(100-maxWaveRoots-2) {
		t.Errorf("merged tail count wrong: %d", last.Count)
	}

	if !wave.Roots[1].Viewed || wave.Roots[1].Root != viewedRoot {
		t.Errorf("viewed root not marked: %+v", wave.Roots[1])
	}

	sum := 0
	for _, root := range wave.Roots {
		sum += root.Count
	}

	if sum != total {
		t.Errorf("merge lost votes: %d != %d", sum, total)
	}
}
