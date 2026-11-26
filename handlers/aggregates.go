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
	observers uint64
	p50Ms     uint32
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

func loadAggregateObservers(ctx context.Context, client *xatu.Client, firstSlot, endSlot phase0.Slot, firstTime, endTime time.Time, settled bool) (map[phase0.Slot]map[string]aggregateObservers, error) {
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

	result := map[phase0.Slot]map[string]aggregateObservers{}

	for rows.Next() {
		var slot, p50 uint32

		var aggregationBits string

		var observers uint64

		if err := rows.Scan(&slot, &aggregationBits, &observers, &p50); err != nil {
			return nil, fmt.Errorf("aggregate observer scan: %w", err)
		}

		if result[phase0.Slot(slot)] == nil {
			result[phase0.Slot(slot)] = map[string]aggregateObservers{}
		}

		result[phase0.Slot(slot)][aggregationBits] = aggregateObservers{observers: observers, p50Ms: p50}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("aggregate observer query: %w", err)
	}

	return result, nil
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
	firstMs   uint32
	seen      uint64
	sizeBytes uint64
}

func buildAggregateRows(slot phase0.Slot, aggregates []*xatuAggregate, observers map[string]aggregateObservers, committees [][]phase0.ValidatorIndex, arrivals map[phase0.ValidatorIndex]uint32) *slotAggregates {
	elected := electedAggregators(slot)
	result := &slotAggregates{}
	seenAggregators := map[uint64]map[phase0.ValidatorIndex]bool{}
	coverage := map[uint64]map[int]bool{}

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

		if elected != nil && committee < uint64(len(elected)) {
			row.Elected = new(bool)
			for _, index := range elected[committee] {
				if index == aggregator {
					*row.Elected = true
				}
			}
		}

		return row
	}

	for _, aggregate := range aggregates {
		row := newRow(aggregate.committee, aggregate.aggregator)
		row.Seen = true
		row.SeenMs = aggregate.seenMs
		row.SizeBytes = aggregate.sizeBytes
		row.Observers = 1

		if seen, ok := observers[aggregate.bits]; ok {
			row.Observers = seen.observers
			row.ObserversP50Ms = seen.p50Ms
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

		members := bitlistMembers(aggregate.bits)

		if aggregate.committee < uint64(len(committees)) {
			for position, index := range committees[aggregate.committee] {
				included := position < len(members) && members[position]
				seenMs, seen := arrivals[index]

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
		}

		result.rows = append(result.rows, row)
	}

	for committee, aggregators := range elected {
		for _, aggregator := range aggregators {
			if !seenAggregators[uint64(committee)][aggregator] {
				result.rows = append(result.rows, newRow(uint64(committee), aggregator))
				result.missing++
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

	if len(aggregates) == 0 && electedAggregators(slot) == nil {
		return nil, nil
	}

	observers, err := loadAggregateObservers(ctx, client, slot, slot+1, firstTime, endTime, settled)
	if err != nil {
		return nil, err
	}

	return buildAggregateRows(slot, aggregates, observers[slot], services.GlobalBeaconService.GetSlotCommittees(ctx, slot), arrivals), nil
}

func loadEpochAggregates(ctx context.Context, epoch phase0.Epoch, firstSlot, endSlot phase0.Slot, settled bool, aggregateDueMs uint32) (*models.EpochAggregates, error) {
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
	clients := map[string]*models.EpochAggregatorClient{}
	clientDelays := map[string][]uint32{}

	for slot := firstSlot; slot < endSlot; slot++ {
		var committees [][]phase0.ValidatorIndex

		if index := int(slot - firstSlot); index < len(epochCommittees) {
			committees = epochCommittees[index]
		}

		built := buildAggregateRows(slot, bySlot[slot], observers[slot], committees, nil)

		summary := &models.EpochAggregateSlot{
			Slot:       uint64(slot),
			Committees: uint64(len(committees)),
			Aggregates: built.seen,
			Missing:    built.missing,
			Members:    built.members,
			Covered:    built.covered,
		}

		if built.seen > 0 {
			summary.FirstMs = &built.firstMs
		}

		result.Slots = append(result.Slots, summary)
		result.Aggregates += built.seen
		result.Missing += built.missing
		result.SizeBytes += built.sizeBytes
		result.Members += built.members
		result.Covered += built.covered

		for _, row := range built.rows {
			group := clients[row.Client]
			if group == nil {
				group = &models.EpochAggregatorClient{Name: row.Client}
				clients[row.Client] = group
			}

			if !row.Seen {
				group.Missing++
				continue
			}

			group.Aggregates++
			group.Attesters += row.Attesters
			group.CommitteeMembers += row.CommitteeSize

			if row.SeenMs >= aggregateDueMs+1000 {
				group.Late++
			}

			clientDelays[row.Client] = append(clientDelays[row.Client], row.SeenMs)
		}
	}

	for name, group := range clients {
		group.P50Ms, group.P90Ms = attesterPercentiles(clientDelays[name])
		result.Clients = append(result.Clients, group)
	}

	sort.Slice(result.Clients, func(a, b int) bool { return result.Clients[a].Name < result.Clients[b].Name })

	return result, nil
}
