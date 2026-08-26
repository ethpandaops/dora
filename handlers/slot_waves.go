package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/clients/xatu"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
	cbtch "github.com/ethpandaops/xatu-cbt/pkg/proto/clickhouse"
)

// maxWaveRoots caps how many voted block roots the attestation wave reports
// individually. A contested slot rarely splits across more than two or three
// roots; anything beyond the cap is merged into one unnamed entry so a chain
// spam incident cannot inflate the payload.
const maxWaveRoots = 4

// SlotWaves returns the xatu-cbt backed slot panels as JSON for the lazy
// propagation tab on the slot page: the attestation first-seen wave and the
// data column propagation strip.
func SlotWaves(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if xatu.GlobalCbtClient == nil {
		http.Error(w, "Xatu cbt is not configured", http.StatusNotFound)
		return
	}

	vars := mux.Vars(r)
	slot, err := strconv.ParseUint(vars["slotOrHash"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid slot", http.StatusBadRequest)
		return
	}

	// Scope to the block being viewed, so a slot with competing blocks reports
	// each block's own wave rather than merging them. Requests without a
	// usable root fall back to the whole slot.
	blockRoot := normalizeBlockRoot(r.URL.Query().Get("root"))

	cacheKey := fmt.Sprintf("slotwaves:%d:%s", slot, blockRoot)
	pageRes, pageErr := services.GlobalFrontendCache.ProcessCachedPage(cacheKey, true, &models.SlotWavesResponse{}, func(pageCall *services.FrontendCacheProcessingPage) any {
		data, cacheTimeout, buildErr := buildSlotWavesData(pageCall.CallCtx, phase0.Slot(slot), blockRoot)
		if buildErr != nil {
			logrus.WithError(buildErr).Error("error loading slot waves data from xatu-cbt")
			pageCall.CacheTimeout = -1

			return &models.SlotWavesResponse{Slot: slot}
		}

		pageCall.CacheTimeout = cacheTimeout

		return data
	})
	if pageErr != nil {
		logrus.WithError(pageErr).Error("error building slot waves data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	result, ok := pageRes.(*models.SlotWavesResponse)
	if !ok {
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	if err := json.NewEncoder(w).Encode(result); err != nil {
		logrus.WithError(err).Error("error encoding slot waves data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}
}

// buildSlotWavesData queries xatu-cbt for the slot's attestation wave and
// data column measurements. It returns the response and the cache timeout,
// following the same ladder as the arrival endpoint: one slot while the cbt
// transformations may still rewrite the slot's rows, then short, then long.
func buildSlotWavesData(ctx context.Context, slot phase0.Slot, blockRoot string) (*models.SlotWavesResponse, time.Duration, error) {
	client := xatu.GlobalCbtClient
	chainState := services.GlobalBeaconService.GetChainState()
	slotTime := chainState.SlotToTime(slot)

	settled := time.Now().After(slotTime.Add(client.SettleDelay()))

	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	attestations, err := loadAttestationWave(queryCtx, client, slot, slotTime, settled, blockRoot)
	if err != nil {
		return nil, -1, err
	}

	if attestations != nil {
		attestations.DeadlineMs = attestationDeadlineMs(chainState, slot)
		attestations.ExpectedCount = expectedAttesters(queryCtx, chainState, slot)

		if xatu.GlobalClient != nil {
			p50, plErr := queryPayloadP50(queryCtx, xatu.GlobalClient, slot, slotTime, settled, blockRoot)
			if plErr != nil {
				// the payload marker is garnish on the attestation chart, so
				// its failure must not take the whole panel down
				logrus.WithError(plErr).Warn("error loading payload p50 from xatu")
			} else {
				attestations.PayloadP50Ms = p50
			}
		}
	}

	columns, err := loadColumnWave(queryCtx, client, slot, slotTime, settled, blockRoot)
	if err != nil {
		return nil, -1, err
	}

	var ptc *models.SlotPtcWave

	if xatu.GlobalClient != nil {
		ptc, err = loadPtcWave(queryCtx, xatu.GlobalClient, slot, slotTime, settled, blockRoot)
		if err != nil {
			// like the payload marker, the PTC row must not take the other
			// panels down with it
			logrus.WithError(err).Warn("error loading ptc wave from xatu")

			ptc = nil
		}

		if ptc != nil {
			// PAYLOAD_ATTESTATION_DUE_BPS: votes are due within 75% of the slot
			ptc.DeadlineMs = lateThreshold(chainState) * 75 / 100
		}
	}

	response := &models.SlotWavesResponse{
		Slot:         uint64(slot),
		Settled:      settled,
		SlotMs:       lateThreshold(chainState),
		Attestations: attestations,
		Ptc:          ptc,
		Columns:      columns,
	}

	cacheTimeout := time.Duration(-1)

	switch {
	case !settled:
		cacheTimeout = unsettledCacheTimeout(chainState)
	case time.Since(slotTime) < 30*time.Minute:
		cacheTimeout = 30 * time.Second
	default:
		cacheTimeout = time.Hour
	}

	return response, cacheTimeout, nil
}

// attestationDeadlineMs is the point where validators attest without a block
// in hand: a third of the slot, moved to a quarter by gloas
// (ATTESTATION_DUE_BPS_GLOAS = 2500).
func attestationDeadlineMs(chainState *consensus.ChainState, slot phase0.Slot) uint32 {
	slotMs := lateThreshold(chainState)
	specs := chainState.GetSpecs()

	if specs.GloasForkEpoch != nil && chainState.EpochOfSlot(slot) >= phase0.Epoch(*specs.GloasForkEpoch) {
		return slotMs * 25 / 100
	}

	return slotMs / 3
}

// expectedAttesters is how many validators were due to attest in the slot:
// the epoch's active validator count spread over its slots. Recent epochs
// come from the in-memory epoch stats; older ones fall back to the epochs
// table, which serves history but only fills when the synchronizer runs.
func expectedAttesters(ctx context.Context, chainState *consensus.ChainState, slot phase0.Slot) int {
	epoch := chainState.EpochOfSlot(slot)

	slotsPerEpoch := chainState.GetSpecs().SlotsPerEpoch
	if slotsPerEpoch == 0 {
		return 0
	}

	beaconIndexer := services.GlobalBeaconService.GetBeaconIndexer()
	if epochStats := beaconIndexer.GetEpochStats(epoch, nil); epochStats != nil {
		if values := epochStats.GetOrLoadValues(ctx, beaconIndexer, true, false); values != nil && values.ActiveValidators > 0 {
			return int(values.ActiveValidators / slotsPerEpoch) //nolint:gosec // bounded by the validator set
		}
	}

	rows := db.GetEpochs(ctx, uint64(epoch), 1)
	if len(rows) == 0 || rows[0].Epoch != uint64(epoch) {
		return 0
	}

	return int(rows[0].ValidatorCount / slotsPerEpoch) //nolint:gosec // bounded by the validator set
}

// payloadP50Query reduces the gloas payload gossip series to its median; see
// payloadGossipQuery for why this is plain SQL.
const payloadP50Query = `SELECT count() AS observations, toUInt32(quantileExact(0.5)(propagation_slot_start_diff)) AS p50
FROM beacon_api_eth_v1_events_execution_payload_gossip
WHERE meta_network_name = ? AND slot_start_date_time = toDateTime(?) AND slot = ?%s`

// queryPayloadP50 returns the median payload arrival for the slot, or zero on
// networks without the gloas payload series.
func queryPayloadP50(ctx context.Context, client *xatu.Client, slot phase0.Slot, slotTime time.Time, settled bool, blockRoot string) (uint32, error) {
	filter := ""
	args := []any{client.Network(), slotTime.Unix(), uint32(slot)}

	if blockRoot != "" {
		filter = " AND block_root = ?"

		args = append(args, blockRoot)
	}

	rows, err := client.Query(ctx, settled, fmt.Sprintf(payloadP50Query, filter), args...)
	if err != nil {
		if isMissingTableError(err) {
			return 0, nil
		}

		return 0, fmt.Errorf("payload p50 query: %w", err)
	}
	defer rows.Close()

	var observations uint64

	var p50 uint32

	for rows.Next() {
		if err := rows.Scan(&observations, &p50); err != nil {
			return 0, fmt.Errorf("payload p50 scan: %w", err)
		}
	}

	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("payload p50 query: %w", err)
	}

	if observations == 0 {
		return 0, nil
	}

	return p50, nil
}

// ptcWaveQuery reduces the gloas payload attestation events to 50ms chunks of
// unique PTC votes, split by the vote's payload_present verdict. Each vote is
// deduplicated to its first sighting across all observing nodes. Plain SQL
// for the same reason as payloadGossipQuery.
const ptcWaveQuery = `SELECT chunk, payload_present, count() AS votes
FROM (
    SELECT validator_index, payload_present,
           toUInt32(intDiv(min(propagation_slot_start_diff), 50) * 50) AS chunk
    FROM beacon_api_eth_v1_events_payload_attestation
    WHERE meta_network_name = ? AND slot_start_date_time = toDateTime(?) AND slot = ?%s
    GROUP BY validator_index, payload_present
)
GROUP BY chunk, payload_present
ORDER BY chunk`

// loadPtcWave loads the payload timeliness committee's voting wave, or nil on
// networks without the gloas schema.
func loadPtcWave(ctx context.Context, client *xatu.Client, slot phase0.Slot, slotTime time.Time, settled bool, blockRoot string) (*models.SlotPtcWave, error) {
	filter := ""
	args := []any{client.Network(), slotTime.Unix(), uint32(slot)}

	if blockRoot != "" {
		filter = " AND beacon_block_root = ?"

		args = append(args, blockRoot)
	}

	rows, err := client.Query(ctx, settled, fmt.Sprintf(ptcWaveQuery, filter), args...)
	if err != nil {
		if isMissingTableError(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("ptc wave query: %w", err)
	}
	defer rows.Close()

	buckets := map[uint32]*models.SlotPtcBucket{}
	wave := &models.SlotPtcWave{}

	for rows.Next() {
		var chunk uint32

		var present bool

		var votes uint64

		if err := rows.Scan(&chunk, &present, &votes); err != nil {
			return nil, fmt.Errorf("ptc wave scan: %w", err)
		}

		bucket := buckets[chunk]
		if bucket == nil {
			bucket = &models.SlotPtcBucket{Ms: chunk}
			buckets[chunk] = bucket
			wave.Buckets = append(wave.Buckets, bucket)
		}

		count := int(votes) //nolint:gosec // bounded by the committee size
		wave.TotalCount += count

		if present {
			bucket.Present += count
			wave.PresentCount += count
		} else {
			bucket.Missing += count
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ptc wave query: %w", err)
	}

	if wave.TotalCount == 0 {
		return nil, nil
	}

	sort.Slice(wave.Buckets, func(a, b int) bool {
		return wave.Buckets[a].Ms < wave.Buckets[b].Ms
	})

	return wave, nil
}

// waveRoot accumulates one voted block root's buckets before they are sorted
// into the response.
type waveRoot struct {
	root    string
	count   int
	buckets map[uint32]int
}

// loadAttestationWave loads the 50ms attestation first-seen chunks for the
// slot, grouped by voted block root. The cbt tables hold one network per
// database, so no network filter is needed. It returns nil when the table has
// no rows for the slot.
func loadAttestationWave(ctx context.Context, client *xatu.Client, slot phase0.Slot, slotTime time.Time, settled bool, blockRoot string) (*models.SlotAttestationWave, error) {
	// Both slot and slot_start_date_time are filtered: the cbt cluster rejects
	// queries that cannot use a table's primary key, and which of the two
	// leads the key varies between tables.
	req := &cbtch.ListFctAttestationFirstSeenChunked50MsRequest{
		Slot: &cbtch.UInt32Filter{Filter: &cbtch.UInt32Filter_Eq{Eq: uint32(slot)}},
		SlotStartDateTime: &cbtch.UInt32Filter{Filter: &cbtch.UInt32Filter_Eq{
			Eq: uint32(slotTime.Unix()),
		}},
		OrderBy:  "chunk_slot_start_diff",
		PageSize: xatu.MaxQueryPageSize(),
	}

	roots := map[string]*waveRoot{}
	total := 0

	err := client.QueryPaged(ctx, settled, func(pageOffset uint32) (string, []any, error) {
		req.PageToken = cbtPageToken(pageOffset)

		query, err := cbtch.BuildListFctAttestationFirstSeenChunked50MsQuery(req)

		return query.Query, query.Args, err
	}, func(rows driver.Rows) error {
		var row cbtch.FctAttestationFirstSeenChunked50MsRow
		if err := rows.ScanStruct(&row); err != nil {
			return fmt.Errorf("attestation wave scan: %w", err)
		}

		root := roots[row.BlockRoot]
		if root == nil {
			root = &waveRoot{root: row.BlockRoot, buckets: map[uint32]int{}}
			roots[row.BlockRoot] = root
		}

		count := int(row.AttestationCount)
		root.count += count
		root.buckets[row.ChunkSlotStartDiff] += count
		total += count

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("attestation wave query: %w", err)
	}

	if total == 0 {
		return nil, nil
	}

	return assembleWaveRoots(roots, total, blockRoot), nil
}

// assembleWaveRoots orders the accumulated roots by vote count, merges the
// tail beyond maxWaveRoots into one unnamed entry and marks the viewed root.
func assembleWaveRoots(roots map[string]*waveRoot, total int, blockRoot string) *models.SlotAttestationWave {
	ordered := make([]*waveRoot, 0, len(roots))
	for _, root := range roots {
		ordered = append(ordered, root)
	}

	sort.Slice(ordered, func(a, b int) bool {
		return ordered[a].count > ordered[b].count
	})

	if len(ordered) > maxWaveRoots {
		rest := &waveRoot{buckets: map[uint32]int{}}

		for _, root := range ordered[maxWaveRoots:] {
			rest.count += root.count

			for ms, count := range root.buckets {
				rest.buckets[ms] += count
			}
		}

		ordered = append(ordered[:maxWaveRoots], rest)
	}

	wave := &models.SlotAttestationWave{TotalCount: total}

	for _, root := range ordered {
		entry := &models.SlotAttestationRoot{
			Root:    root.root,
			Viewed:  blockRoot != "" && root.root == blockRoot,
			Count:   root.count,
			Buckets: make([]*models.SlotAttestationBucket, 0, len(root.buckets)),
		}

		for ms, count := range root.buckets {
			entry.Buckets = append(entry.Buckets, &models.SlotAttestationBucket{Ms: ms, Count: count})
		}

		sort.Slice(entry.Buckets, func(a, b int) bool {
			return entry.Buckets[a].Ms < entry.Buckets[b].Ms
		})

		wave.Roots = append(wave.Roots, entry)
	}

	return wave
}

// loadColumnWave loads every node's first sighting of each data column and
// the availability probes for the slot, and reduces them into one entry per
// column index with min/p50/p90 timings. It returns nil when neither table
// has rows, and also for a blobless slot, where there are no columns to
// spread.
func loadColumnWave(ctx context.Context, client *xatu.Client, slot phase0.Slot, slotTime time.Time, settled bool, blockRoot string) (*models.SlotColumnWave, error) {
	columns := map[uint32]*models.SlotColumn{}
	sightings := map[uint32][]uint32{}

	column := func(index uint32) *models.SlotColumn {
		entry := columns[index]
		if entry == nil {
			entry = &models.SlotColumn{Index: index}
			columns[index] = entry
		}

		return entry
	}

	seenReq := &cbtch.ListFctBlockDataColumnSidecarFirstSeenByNodeRequest{
		Slot: &cbtch.UInt32Filter{Filter: &cbtch.UInt32Filter_Eq{Eq: uint32(slot)}},
		SlotStartDateTime: &cbtch.UInt32Filter{Filter: &cbtch.UInt32Filter_Eq{
			Eq: uint32(slotTime.Unix()),
		}},
		OrderBy:  "column_index",
		PageSize: xatu.MaxQueryPageSize(),
	}

	if blockRoot != "" {
		seenReq.BlockRoot = &cbtch.StringFilter{Filter: &cbtch.StringFilter_Eq{Eq: blockRoot}}
	}

	blobCount := 0

	err := client.QueryPaged(ctx, settled, func(pageOffset uint32) (string, []any, error) {
		seenReq.PageToken = cbtPageToken(pageOffset)

		query, err := cbtch.BuildListFctBlockDataColumnSidecarFirstSeenByNodeQuery(seenReq)

		return query.Query, query.Args, err
	}, func(rows driver.Rows) error {
		var row cbtch.FctBlockDataColumnSidecarFirstSeenByNodeRow
		if err := rows.ScanStruct(&row); err != nil {
			return fmt.Errorf("column sightings scan: %w", err)
		}

		entry := column(row.ColumnIndex)
		sightings[row.ColumnIndex] = append(sightings[row.ColumnIndex], row.SeenSlotStartDiff)

		if entry.FirstSeenMs == nil || row.SeenSlotStartDiff < *entry.FirstSeenMs {
			ms := row.SeenSlotStartDiff
			entry.FirstSeenMs = &ms

			_, _, display := parseSentryName(row.MetaClientName, client.Network())
			entry.FirstSeenBy = display
			entry.CountryCode = row.MetaClientGeoCountryCode

			entry.Implementation = row.MetaConsensusImplementation
			if entry.Implementation == "" {
				entry.Implementation = reduceSidecarName(row.MetaClientImplementation)
			}
		}

		if int(row.RowCount) > blobCount {
			blobCount = int(row.RowCount)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("column sightings query: %w", err)
	}

	// Availability is probed per slot and column, not per block root, so it
	// stays unfiltered even when the page views one specific block.
	availReq := &cbtch.ListFctDataColumnAvailabilityBySlotRequest{
		Slot: &cbtch.UInt32Filter{Filter: &cbtch.UInt32Filter_Eq{Eq: uint32(slot)}},
		SlotStartDateTime: &cbtch.UInt32Filter{Filter: &cbtch.UInt32Filter_Eq{
			Eq: uint32(slotTime.Unix()),
		}},
		OrderBy:  "column_index",
		PageSize: xatu.MaxQueryPageSize(),
	}

	err = client.QueryPaged(ctx, settled, func(pageOffset uint32) (string, []any, error) {
		availReq.PageToken = cbtPageToken(pageOffset)

		query, err := cbtch.BuildListFctDataColumnAvailabilityBySlotQuery(availReq)

		return query.Query, query.Args, err
	}, func(rows driver.Rows) error {
		var row cbtch.FctDataColumnAvailabilityBySlotRow
		if err := rows.ScanStruct(&row); err != nil {
			return fmt.Errorf("column availability scan: %w", err)
		}

		entry := column(uint32(row.ColumnIndex)) //nolint:gosec // column indexes are 0-127

		pct := row.AvailabilityPct
		entry.AvailabilityPct = &pct
		entry.Probes = int(row.ProbeCount)
		entry.P50ResponseMs = row.P50ResponseTimeMs

		if int(row.BlobCount) > blobCount {
			blobCount = int(row.BlobCount)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("column availability query: %w", err)
	}

	if len(columns) == 0 || blobCount == 0 {
		return nil, nil
	}

	wave := &models.SlotColumnWave{
		BlobCount:       blobCount,
		MinAvailability: 100,
		Columns:         make([]*models.SlotColumn, 0, len(columns)),
	}

	availabilitySum := float64(0)
	allSightings := []uint32{}

	for _, entry := range columns {
		wave.Columns = append(wave.Columns, entry)

		if times := sightings[entry.Index]; len(times) > 0 {
			sort.Slice(times, func(a, b int) bool { return times[a] < times[b] })
			entry.P50Ms = times[len(times)/2]
			entry.P90Ms = times[len(times)*9/10]
			entry.Observations = len(times)

			wave.Observations += len(times)
			allSightings = append(allSightings, times...)
		}

		if entry.FirstSeenMs != nil {
			wave.SeenColumns++

			if wave.FirstMs == 0 || *entry.FirstSeenMs < wave.FirstMs {
				wave.FirstMs = *entry.FirstSeenMs
			}

			if *entry.FirstSeenMs > wave.LastMs {
				wave.LastMs = *entry.FirstSeenMs
			}
		}

		if entry.AvailabilityPct != nil {
			wave.ProbedColumns++
			wave.TotalProbes += entry.Probes
			availabilitySum += *entry.AvailabilityPct

			if *entry.AvailabilityPct < wave.MinAvailability {
				wave.MinAvailability = *entry.AvailabilityPct
			}

			// p50 covers successful probes only, so a fully failed column
			// reports zero and cannot be the slowest.
			if entry.P50ResponseMs > wave.WorstP50Ms {
				wave.WorstP50Ms = entry.P50ResponseMs
			}
		}
	}

	if len(allSightings) > 0 {
		sort.Slice(allSightings, func(a, b int) bool { return allSightings[a] < allSightings[b] })
		wave.MedianMs = allSightings[len(allSightings)/2]
	}

	if wave.ProbedColumns > 0 {
		wave.AvgAvailability = availabilitySum / float64(wave.ProbedColumns)
	} else {
		wave.MinAvailability = 0
	}

	sort.Slice(wave.Columns, func(a, b int) bool {
		return wave.Columns[a].Index < wave.Columns[b].Index
	})

	return wave, nil
}
