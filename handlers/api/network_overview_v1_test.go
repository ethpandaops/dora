package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
)

func TestNetworkOverviewWarnsWhenVoteAggregateContradictsFinality(t *testing.T) {
	overview := buildSafeNetworkOverview(networkOverviewInput{
		CurrentSlot:    34801,
		SlotsPerEpoch:  32,
		FinalizedEpoch: 1085,
		ValidatorStatuses: map[v1.ValidatorState]uint64{
			v1.ValidatorStateActiveOngoing:      12345,
			v1.ValidatorStatePendingQueued:      10,
			v1.ValidatorStateExitedUnslashed:    100,
			v1.ValidatorStateWithdrawalPossible: 45,
		},
		RawAggregate: &networkOverviewRawAggregate{
			GlobalParticipationRate: 58.4,
			VoteParticipation:       58.4,
			AttestationsIndexed:     35,
			IndexedSlots:            32,
			ExpectedSlots:           32,
			Complete:                true,
		},
	})

	if !overview.Finalizing {
		t.Fatalf("expected overview to report finalizing")
	}
	if overview.EpochsSinceFinality != 2 {
		t.Fatalf("expected epochs_since_finality 2, got %d", overview.EpochsSinceFinality)
	}
	if len(overview.DataQualityWarnings) != 1 {
		t.Fatalf("expected one data quality warning, got %d", len(overview.DataQualityWarnings))
	}
	if !strings.Contains(overview.DataQualityWarnings[0], "vote participation aggregate reports 58.4%") {
		t.Fatalf("unexpected warning: %q", overview.DataQualityWarnings[0])
	}
	if overview.Participation == nil {
		t.Fatalf("expected participation provenance")
	}
	if overview.Participation.Rate != nil {
		t.Fatalf("expected contradictory participation rate to be omitted, got %v", *overview.Participation.Rate)
	}
	if overview.Participation.Complete {
		t.Fatalf("expected contradictory participation to be marked incomplete")
	}
	if overview.RawAggregates == nil || overview.RawAggregates.GlobalParticipationRate != 58.4 {
		t.Fatalf("expected raw aggregate to retain the reported vote participation")
	}
}

func TestNetworkOverviewIncompleteIndexOmitsTopLevelParticipationRate(t *testing.T) {
	overview := buildSafeNetworkOverview(networkOverviewInput{
		CurrentSlot:       34801,
		SlotsPerEpoch:     32,
		FinalizedEpoch:    1084,
		ValidatorStatuses: map[v1.ValidatorState]uint64{},
		RawAggregate: &networkOverviewRawAggregate{
			GlobalParticipationRate: 83.2,
			VoteParticipation:       83.2,
			AttestationsIndexed:     35,
			IndexedSlots:            5,
			ExpectedSlots:           32,
			Complete:                false,
		},
	})

	responseBytes, err := json.Marshal(APINetworkOverviewResponse{
		Status: "OK",
		Data:   overview,
	})
	if err != nil {
		t.Fatalf("marshal overview response: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("unmarshal overview response: %v", err)
	}
	data := response["data"].(map[string]any)
	if _, ok := data["participation_rate"]; ok {
		t.Fatalf("overview must not expose a top-level participation_rate")
	}
	if overview.Participation == nil {
		t.Fatalf("expected participation provenance")
	}
	if overview.Participation.Rate != nil {
		t.Fatalf("expected incomplete participation rate to be null")
	}
	if overview.Participation.Complete {
		t.Fatalf("expected incomplete participation source")
	}
	if overview.Participation.Warning != networkOverviewParticipationWarnText {
		t.Fatalf("unexpected participation warning: %q", overview.Participation.Warning)
	}
}

func TestNetworkOverviewCurrentSlotUsesActualHeadSlot(t *testing.T) {
	overview := buildSafeNetworkOverview(networkOverviewInput{
		CurrentSlot:    34801,
		SlotsPerEpoch:  32,
		FinalizedEpoch: 1085,
	})

	if overview.CurrentSlot != 34801 {
		t.Fatalf("expected current_slot to remain actual head slot, got %d", overview.CurrentSlot)
	}
	if overview.CurrentEpoch != 1087 {
		t.Fatalf("expected current_epoch derived from head slot, got %d", overview.CurrentEpoch)
	}
	if overview.CurrentSlot == overview.CurrentEpoch*overview.Metadata.SlotsPerEpoch {
		t.Fatalf("current_slot was collapsed to epoch start slot")
	}
}

func TestNetworkOverviewErrorEnvelopeIsNotSuccessful(t *testing.T) {
	w := httptest.NewRecorder()
	sendServerErrorResponse(w, "/api/v1/network/overview", "failed to build network overview")

	if w.Code < http.StatusBadRequest {
		t.Fatalf("expected non-2xx HTTP status, got %d", w.Code)
	}

	var response ApiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if response.Status == "OK" {
		t.Fatalf("non-OK response must not use OK status")
	}
	if response.Data != nil {
		t.Fatalf("error response must not include successful data")
	}
}

func TestNetworkOverviewHandlerUnavailableStateReturnsErrorEnvelope(t *testing.T) {
	oldFrontendCache := services.GlobalFrontendCache
	oldBeaconService := services.GlobalBeaconService
	services.GlobalFrontendCache = nil
	services.GlobalBeaconService = nil
	defer func() {
		services.GlobalFrontendCache = oldFrontendCache
		services.GlobalBeaconService = oldBeaconService
	}()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/network/overview", nil)
	APINetworkOverviewV1(w, req)

	if w.Code < http.StatusBadRequest {
		t.Fatalf("expected non-2xx HTTP status, got %d", w.Code)
	}

	var response ApiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if response.Status == "OK" {
		t.Fatalf("unavailable overview must not use OK status")
	}
	if response.Data != nil {
		t.Fatalf("unavailable overview must not include successful data")
	}
}

func TestNetworkOverviewResponseContainsClientSnapshotFields(t *testing.T) {
	overview := buildSafeNetworkOverview(networkOverviewInput{
		CurrentSlot:    34801,
		SlotsPerEpoch:  32,
		FinalizedEpoch: 1085,
		ValidatorStatuses: map[v1.ValidatorState]uint64{
			v1.ValidatorStateActiveOngoing:   12345,
			v1.ValidatorStatePendingQueued:   10,
			v1.ValidatorStateExitedUnslashed: 145,
		},
	})
	overview.NetworkInfo = &APINetworkInfo{}
	overview.CurrentState = &APICurrentState{}
	overview.Checkpoints = &APICheckpoints{}
	overview.ValidatorStats = &APIValidatorStats{}
	overview.Forks = []*APINetworkForkInfo{}
	overview.IsSynced = true

	responseBytes, err := json.Marshal(APINetworkOverviewResponse{
		Status: "OK",
		Data:   overview,
	})
	if err != nil {
		t.Fatalf("marshal overview response: %v", err)
	}

	var response struct {
		Status string         `json:"status"`
		Data   map[string]any `json:"data"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("unmarshal overview response: %v", err)
	}
	if response.Status != "OK" {
		t.Fatalf("expected OK response, got %q", response.Status)
	}

	requiredFields := []string{
		"current_slot",
		"current_epoch",
		"finalized_epoch",
		"epochs_since_finality",
		"finalizing",
		"active_validator_count",
		"total_validator_count",
		"pending_validator_count",
		"exited_validator_count",
		"data_quality_warnings",
		"metadata",
		"network_info",
		"current_state",
		"checkpoints",
		"validator_stats",
		"forks",
		"is_synced",
	}
	for _, field := range requiredFields {
		if _, ok := response.Data[field]; !ok {
			t.Fatalf("overview missing required field %q", field)
		}
	}

	metadata, ok := response.Data["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("overview metadata has unexpected shape")
	}
	if metadata["slots_per_epoch"] != float64(32) {
		t.Fatalf("expected metadata.slots_per_epoch 32, got %v", metadata["slots_per_epoch"])
	}
}

func TestBuildNetworkOverviewRawAggregateMarksIncompleteIndex(t *testing.T) {
	raw := buildNetworkOverviewRawAggregate(&dbtypes.Epoch{
		Eligible:         100,
		VotedTarget:      83,
		VotedTotal:       85,
		AttestationCount: 35,
		BlockCount:       5,
	}, 32)

	if raw == nil {
		t.Fatalf("expected raw aggregate")
	}
	if raw.Complete {
		t.Fatalf("expected raw aggregate to be incomplete")
	}
	if raw.IndexedSlots != 5 || raw.ExpectedSlots != 32 {
		t.Fatalf("unexpected indexed/expected slots: %d/%d", raw.IndexedSlots, raw.ExpectedSlots)
	}
}
