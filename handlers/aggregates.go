package handlers

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/bits"
	"sort"
	"strings"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/clients/xatu"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types/models"
)

type xatuAggregate struct {
	slot       phase0.Slot
	aggregator phase0.ValidatorIndex
	committee  uint64
	bits       string
	seenMs     uint32
	sizeBytes  uint32
}

type aggregateObservers struct {
	members   []bool
	observers uint64
	p50Ms     uint32
}

func membersKey(members []bool) string {
	key := make([]byte, len(members))
	for i, member := range members {
		key[i] = '0'
		if member {
			key[i] = '1'
		}
	}

	return string(key)
}

func indexAggregateObservers(observers []*aggregateObservers) map[string]*aggregateObservers {
	index := make(map[string]*aggregateObservers, len(observers))

	for _, observed := range observers {
		index[membersKey(observed.members)] = observed
	}

	return index
}

func loadAggregates(ctx context.Context, client *xatu.Client, firstSlot, endSlot phase0.Slot, firstTime, endTime time.Time, settled bool, blockRoot string) ([]*xatuAggregate, error) {
	rootFilter := ""
	args := []any{client.Network(), firstTime.Unix(), endTime.Unix(), uint32(firstSlot), uint32(endSlot)}

	if blockRoot != "" {
		rootFilter = " AND beacon_block_root = ?"
		args = append(args, blockRoot)
	}

	rows, err := client.Query(ctx, settled, `SELECT slot, aggregator_index, toUInt64OrZero(toString(committee_index)) AS committee, argMin(aggregation_bits, propagation_slot_start_diff) AS agg_bits, toUInt32(min(propagation_slot_start_diff)) AS first_ms, toUInt32(max(message_size)) AS size
FROM libp2p_gossipsub_aggregate_and_proof
WHERE meta_network_name = ? AND slot_start_date_time >= toDateTime(?) AND slot_start_date_time < toDateTime(?) AND slot >= ? AND slot < ?`+rootFilter+`
GROUP BY slot, aggregator_index, committee`, args...)
	if err != nil {
		if isMissingTableError(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("aggregate query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	aggregates := []*xatuAggregate{}

	for rows.Next() {
		var slot, aggregator, firstMs, size uint32

		var committee uint64

		var aggregationBits string

		if err := rows.Scan(&slot, &aggregator, &committee, &aggregationBits, &firstMs, &size); err != nil {
			return nil, fmt.Errorf("aggregate scan: %w", err)
		}

		aggregates = append(aggregates, &xatuAggregate{
			slot:       phase0.Slot(slot),
			aggregator: phase0.ValidatorIndex(aggregator),
			committee:  committee,
			bits:       aggregationBits,
			seenMs:     firstMs,
			sizeBytes:  size,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("aggregate query: %w", err)
	}

	return aggregates, nil
}

func loadAggregateObservers(ctx context.Context, client *xatu.Client, firstSlot, endSlot phase0.Slot, firstTime, endTime time.Time, settled bool) (map[phase0.Slot]map[string]*aggregateObservers, error) {
	rows, err := client.Query(ctx, settled, `SELECT slot, aggregation_bits, uniqExact(meta_client_name) AS observers, toUInt32(quantile(0.5)(first_ms)) AS p50
FROM (
	SELECT slot, aggregation_bits, meta_client_name, min(propagation_slot_start_diff) AS first_ms
	FROM beacon_api_eth_v1_events_attestation
	WHERE meta_network_name = ? AND slot_start_date_time >= toDateTime(?) AND slot_start_date_time < toDateTime(?) AND slot >= ? AND slot < ? AND attesting_validator_index IS NULL
	GROUP BY slot, aggregation_bits, meta_client_name
)
GROUP BY slot, aggregation_bits`, client.Network(), firstTime.Unix(), endTime.Unix(), uint32(firstSlot), uint32(endSlot))
	if err != nil {
		if isMissingTableError(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("aggregate observer query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	bySlot := map[phase0.Slot][]*aggregateObservers{}

	for rows.Next() {
		var slot, p50 uint32

		var aggregationBits string

		var observers uint64

		if err := rows.Scan(&slot, &aggregationBits, &observers, &p50); err != nil {
			return nil, fmt.Errorf("aggregate observer scan: %w", err)
		}

		bySlot[phase0.Slot(slot)] = append(bySlot[phase0.Slot(slot)], &aggregateObservers{
			members:   bitlistMembers(aggregationBits),
			observers: observers,
			p50Ms:     p50,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("aggregate observer query: %w", err)
	}

	result := make(map[phase0.Slot]map[string]*aggregateObservers, len(bySlot))
	for slot, observed := range bySlot {
		result[slot] = indexAggregateObservers(observed)
	}

	return result, nil
}

func rawBitsMembers(bitsHex string, size int) []bool {
	raw, err := hex.DecodeString(strings.TrimPrefix(bitsHex, "0x"))
	if err != nil {
		return nil
	}

	members := make([]bool, size)
	for i := range members {
		members[i] = i/8 < len(raw) && raw[i/8]&(1<<(i%8)) != 0
	}

	return members
}

func bitlistMembers(bitlistHex string) []bool {
	bitlist, err := hex.DecodeString(strings.TrimPrefix(bitlistHex, "0x"))
	if err != nil {
		return nil
	}

	length := -1

	for i := len(bitlist) - 1; i >= 0; i-- {
		if bitlist[i] != 0 {
			length = i*8 + bits.Len8(bitlist[i]) - 1
			break
		}
	}

	if length < 0 {
		return nil
	}

	members := make([]bool, length)
	for i := range members {
		members[i] = bitlist[i/8]&(1<<(i%8)) != 0
	}

	return members
}

type slotAggregates struct {
	rows      []*models.SlotAggregate
	members   uint64
	covered   uint64
	missing   uint64
	redundant uint64
	firstMs   uint32
	seen      uint64
	sizeBytes uint64
}

func buildAggregateRows(ctx context.Context, slot phase0.Slot, aggregates []*xatuAggregate, observers map[string]*aggregateObservers, committees [][]phase0.ValidatorIndex, arrivals map[phase0.ValidatorIndex]uint32) *slotAggregates {
	elected := electedAggregators(ctx, slot)
	chainState := services.GlobalBeaconService.GetChainState()
	result := &slotAggregates{}
	seenAggregators := map[uint64]map[phase0.ValidatorIndex]bool{}
	coverage := map[uint64]map[int]bool{}
	saturatedMs := map[uint64]uint32{}
	positions := map[uint64]map[phase0.ValidatorIndex]int{}

	for committee, aggregators := range elected {
		positions[uint64(committee)] = map[phase0.ValidatorIndex]int{}
		for position, aggregator := range aggregators {
			positions[uint64(committee)][aggregator] = position
		}
	}

	dueMs := func(committee uint64, aggregator phase0.ValidatorIndex) uint32 {
		return aggregatorDueMs(chainState, slot, positions[committee][aggregator])
	}
	matched := map[*aggregateObservers][]*models.SlotAggregate{}

	newRow := func(committee uint64, aggregator phase0.ValidatorIndex) *models.SlotAggregate {
		name := services.GlobalBeaconService.GetValidatorNameAt(uint64(aggregator), slot)
		row := &models.SlotAggregate{
			CommitteeIndex:  committee,
			AggregatorIndex: uint64(aggregator),
			AggregatorName:  name,
			Client:          attesterClientName(name),
		}

		if committee < uint64(len(committees)) {
			row.CommitteeSize = uint64(len(committees[committee]))
		}

		return row
	}

	for _, aggregate := range aggregates {
		row := newRow(aggregate.committee, aggregate.aggregator)
		row.Seen = true
		row.SeenMs = aggregate.seenMs
		row.DueMs = dueMs(aggregate.committee, aggregate.aggregator)
		row.SizeBytes = aggregate.sizeBytes
		row.Observers = 1
		members := rawBitsMembers(aggregate.bits, int(row.CommitteeSize))

		if observed, ok := observers[membersKey(members)]; ok {
			row.Observers = observed.observers
			row.ObserversP50Ms = observed.p50Ms
			members = observed.members
			matched[observed] = append(matched[observed], row)
		}

		if seenAggregators[aggregate.committee] == nil {
			seenAggregators[aggregate.committee] = map[phase0.ValidatorIndex]bool{}
			coverage[aggregate.committee] = map[int]bool{}
		}

		seenAggregators[aggregate.committee][aggregate.aggregator] = true

		if result.seen == 0 || aggregate.seenMs < result.firstMs {
			result.firstMs = aggregate.seenMs
		}

		result.seen++
		result.sizeBytes += uint64(aggregate.sizeBytes)

		if aggregate.committee < uint64(len(committees)) {
			coversSeen := true

			for position, index := range committees[aggregate.committee] {
				included := position < len(members) && members[position]
				seenMs, seen := arrivals[index]

				if !included && (arrivals == nil || seen) {
					coversSeen = false
				}

				switch {
				case included:
					row.Attesters++
					coverage[aggregate.committee][position] = true

					if !seen {
						row.IncludedUnseen++
					} else if seenMs > row.LastIncludedMs {
						row.LastIncludedMs = seenMs
					}
				case seen && seenMs < aggregate.seenMs:
					row.LeftOut++
				}
			}

			if first, ok := saturatedMs[aggregate.committee]; coversSeen && (!ok || aggregate.seenMs < first) {
				saturatedMs[aggregate.committee] = aggregate.seenMs
			}
		}

		result.rows = append(result.rows, row)
	}

	for _, rows := range matched {
		if len(rows) < 2 {
			continue
		}

		for _, row := range rows {
			row.ObserversShared = true
		}
	}

	for committee, aggregators := range elected {
		if len(aggregates) == 0 {
			break
		}

		for _, aggregator := range aggregators {
			if !seenAggregators[uint64(committee)][aggregator] {
				row := newRow(uint64(committee), aggregator)
				row.DueMs = dueMs(uint64(committee), aggregator)
				coveredMs, covered := saturatedMs[uint64(committee)]
				row.Redundant = covered && (!staggeredAggregation(chainState) || coveredMs <= row.DueMs)
				result.rows = append(result.rows, row)

				if row.Redundant {
					result.redundant++
				} else {
					result.missing++
				}
			}
		}
	}

	for _, committee := range committees {
		result.members += uint64(len(committee))
	}

	for _, positions := range coverage {
		result.covered += uint64(len(positions))
	}

	sort.Slice(result.rows, func(a, b int) bool {
		ra, rb := result.rows[a], result.rows[b]

		if ra.CommitteeIndex != rb.CommitteeIndex {
			return ra.CommitteeIndex < rb.CommitteeIndex
		}

		if ra.Seen != rb.Seen {
			return ra.Seen
		}

		if ra.SeenMs != rb.SeenMs {
			return ra.SeenMs < rb.SeenMs
		}

		return ra.AggregatorIndex < rb.AggregatorIndex
	})

	return result
}

func loadSlotAggregates(ctx context.Context, slot phase0.Slot, settled bool, blockRoot string, arrivals map[phase0.ValidatorIndex]uint32) (*slotAggregates, error) {
	client := xatu.GlobalClient
	if client == nil {
		return nil, nil
	}

	chainState := services.GlobalBeaconService.GetChainState()
	firstTime, endTime := chainState.SlotToTime(slot), chainState.SlotToTime(slot+1)

	aggregates, err := loadAggregates(ctx, client, slot, slot+1, firstTime, endTime, settled, blockRoot)
	if err != nil {
		return nil, err
	}

	if len(aggregates) == 0 {
		return nil, nil
	}

	observers, err := loadAggregateObservers(ctx, client, slot, slot+1, firstTime, endTime, settled)
	if err != nil {
		return nil, err
	}

	return buildAggregateRows(ctx, slot, aggregates, observers[slot], services.GlobalBeaconService.GetSlotCommittees(ctx, slot), arrivals), nil
}

type aggregatorStats struct {
	aggregates, missing, redundant, late, attesters, members uint64
	seenMs                                                   []uint32
	nodes                                                    map[string]*aggregatorStats
}

func (a *aggregatorStats) add(row *models.SlotAggregate) {
	switch {
	case !row.Seen && row.Redundant:
		a.redundant++
	case !row.Seen:
		a.missing++
	default:
		a.aggregates++
		a.attesters += row.Attesters
		a.members += row.CommitteeSize
		a.seenMs = append(a.seenMs, row.SeenMs)

		if row.SeenMs >= row.DueMs+1000 {
			a.late++
		}
	}
}

func (a *aggregatorStats) fill(aggregates, missing, redundant, late, attesters, members *uint64, p50, p90 *uint32) {
	*aggregates, *missing, *redundant, *late, *attesters, *members = a.aggregates, a.missing, a.redundant, a.late, a.attesters, a.members
	*p50, *p90 = attesterPercentiles(a.seenMs)
}

func loadEpochAggregates(ctx context.Context, epoch phase0.Epoch, firstSlot, endSlot phase0.Slot, settled bool) (*models.EpochAggregates, error) {
	client := xatu.GlobalClient
	if client == nil {
		return nil, nil
	}

	chainState := services.GlobalBeaconService.GetChainState()
	firstTime, endTime := chainState.SlotToTime(firstSlot), chainState.SlotToTime(endSlot)

	aggregates, err := loadAggregates(ctx, client, firstSlot, endSlot, firstTime, endTime, settled, "")
	if err != nil {
		return nil, err
	}

	if len(aggregates) == 0 {
		return nil, nil
	}

	observers, err := loadAggregateObservers(ctx, client, firstSlot, endSlot, firstTime, endTime, settled)
	if err != nil {
		return nil, err
	}

	bySlot := map[phase0.Slot][]*xatuAggregate{}
	for _, aggregate := range aggregates {
		bySlot[aggregate.slot] = append(bySlot[aggregate.slot], aggregate)
	}

	epochCommittees := services.GlobalBeaconService.GetEpochCommittees(ctx, epoch)
	result := &models.EpochAggregates{}
	stats := map[string]*aggregatorStats{}

	if currentSlot := chainState.CurrentSlot(); endSlot > currentSlot {
		endSlot = currentSlot
	}

	for slot := firstSlot; slot < endSlot; slot++ {
		var committees [][]phase0.ValidatorIndex

		if index := int(slot - firstSlot); index < len(epochCommittees) {
			committees = epochCommittees[index]
		}

		built := buildAggregateRows(ctx, slot, bySlot[slot], observers[slot], committees, nil)

		summary := &models.EpochAggregateSlot{
			Slot:       uint64(slot),
			Committees: uint64(len(committees)),
			Aggregates: built.seen,
			Missing:    built.missing,
			Redundant:  built.redundant,
			Members:    built.members,
			Covered:    built.covered,
		}

		if built.seen > 0 {
			summary.FirstMs = &built.firstMs
		}

		result.Slots = append(result.Slots, summary)
		result.Aggregates += built.seen
		result.Missing += built.missing
		result.Redundant += built.redundant
		result.SizeBytes += built.sizeBytes
		result.Members += built.members
		result.Covered += built.covered

		for _, row := range built.rows {
			result.Points = append(result.Points, &models.EpochAggregatePoint{
				Slot:          uint64(slot),
				Committee:     row.CommitteeIndex,
				Aggregator:    row.AggregatorIndex,
				Node:          row.AggregatorName,
				Client:        row.Client,
				Seen:          row.Seen,
				Redundant:     row.Redundant,
				Ms:            row.SeenMs,
				Attesters:     row.Attesters,
				CommitteeSize: row.CommitteeSize,
			})

			client := stats[row.Client]
			if client == nil {
				client = &aggregatorStats{nodes: map[string]*aggregatorStats{}}
				stats[row.Client] = client
			}

			client.add(row)

			if row.AggregatorName != "" {
				node := client.nodes[row.AggregatorName]
				if node == nil {
					node = &aggregatorStats{}
					client.nodes[row.AggregatorName] = node
				}

				node.add(row)
			}
		}
	}

	for name, client := range stats {
		group := &models.EpochAggregatorClient{Name: name}
		client.fill(&group.Aggregates, &group.Missing, &group.Redundant, &group.Late, &group.Attesters, &group.CommitteeMembers, &group.P50Ms, &group.P90Ms)

		for nodeName, node := range client.nodes {
			entry := &models.EpochAggregatorNode{Name: nodeName}
			node.fill(&entry.Aggregates, &entry.Missing, &entry.Redundant, &entry.Late, &entry.Attesters, &entry.CommitteeMembers, &entry.P50Ms, &entry.P90Ms)
			group.Nodes = append(group.Nodes, entry)
		}

		sort.Slice(group.Nodes, func(a, b int) bool { return group.Nodes[a].Name < group.Nodes[b].Name })
		result.Clients = append(result.Clients, group)
	}

	sort.Slice(result.Clients, func(a, b int) bool { return result.Clients[a].Name < result.Clients[b].Name })

	return result, nil
}

func setAggregateGeo(rows []*models.SlotAggregate, geo map[string]*nodeGeo) {
	for _, row := range rows {
		if entry := geo[row.AggregatorName]; entry != nil {
			row.Continent = entry.continent
			row.Country = entry.country
			row.CountryCode = entry.countryCode
		}
	}
}

func setAggregatorNodeGeo(clients []*models.EpochAggregatorClient, geo map[string]*nodeGeo) {
	for _, group := range clients {
		for _, node := range group.Nodes {
			if entry := geo[node.Name]; entry != nil {
				node.Continent = entry.continent
				node.Country = entry.country
				node.CountryCode = entry.countryCode
			}
		}
	}
}
