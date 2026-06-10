package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
)

func TestNetworkOverviewWarnsWhenVoteAggregateContradictsFinality(t *testing.T) {
	participation, warning := buildNetworkOverviewParticipation(&networkOverviewRawAggregate{
		GlobalParticipationRate: 58.4,
		VoteParticipation:       58.4,
		AttestationsIndexed:     35,
		IndexedSlots:            32,
		ExpectedSlots:           32,
		Complete:                true,
	}, true, 2)

	if warning == "" {
		t.Fatalf("expected a data quality warning")
	}
	if !strings.Contains(warning, "vote participation aggregate reports 58.4%") {
		t.Fatalf("unexpected warning: %q", warning)
	}
	if participation.Rate != nil {
		t.Fatalf("expected contradictory participation rate to be omitted, got %v", *participation.Rate)
	}
	if participation.Complete {
		t.Fatalf("expected contradictory participation to be marked incomplete")
	}
}

func TestNetworkOverviewCompleteAggregateExposesParticipationRate(t *testing.T) {
	participation, warning := buildNetworkOverviewParticipation(&networkOverviewRawAggregate{
		GlobalParticipationRate: 98.7,
		VoteParticipation:       99.1,
		AttestationsIndexed:     35,
		IndexedSlots:            32,
		ExpectedSlots:           32,
		Complete:                true,
	}, true, 2)

	if warning != "" {
		t.Fatalf("unexpected warning: %q", warning)
	}
	if !participation.Complete {
		t.Fatalf("expected complete participation source")
	}
	if participation.Rate == nil || *participation.Rate != 98.7 {
		t.Fatalf("expected participation rate 98.7, got %v", participation.Rate)
	}
}

func TestNetworkOverviewIncompleteIndexOmitsTopLevelParticipationRate(t *testing.T) {
	participation, warning := buildNetworkOverviewParticipation(&networkOverviewRawAggregate{
		GlobalParticipationRate: 83.2,
		VoteParticipation:       83.2,
		AttestationsIndexed:     35,
		IndexedSlots:            5,
		ExpectedSlots:           32,
		Complete:                false,
	}, true, 3)

	if warning != "" {
		t.Fatalf("unexpected data quality warning: %q", warning)
	}
	if participation.Rate != nil {
		t.Fatalf("expected incomplete participation rate to be null")
	}
	if participation.Complete {
		t.Fatalf("expected incomplete participation source")
	}
	if participation.Warning != networkOverviewParticipationWarnText {
		t.Fatalf("unexpected participation warning: %q", participation.Warning)
	}

	responseBytes, err := json.Marshal(APINetworkOverviewResponse{
		Status: "OK",
		Data:   &APINetworkOverviewData{Participation: participation},
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

func TestNetworkOverviewResponseContainsClientSnapshotFields(t *testing.T) {
	activeCount, pendingCount, exitedCount, totalCount := summarizeValidatorStatuses(map[v1.ValidatorState]uint64{
		v1.ValidatorStateActiveOngoing:   12345,
		v1.ValidatorStatePendingQueued:   10,
		v1.ValidatorStateExitedUnslashed: 145,
	})

	if activeCount != 12345 || pendingCount != 10 || exitedCount != 145 || totalCount != 12500 {
		t.Fatalf("unexpected validator status summary: %d/%d/%d/%d", activeCount, pendingCount, exitedCount, totalCount)
	}

	responseBytes, err := json.Marshal(APINetworkOverviewResponse{
		Status: "OK",
		Data: &APINetworkOverviewData{
			NetworkInfo:           &APINetworkInfo{},
			CurrentState:          &APICurrentState{},
			Checkpoints:           &APICheckpoints{},
			ValidatorStats:        &APIValidatorStats{},
			Forks:                 []*APINetworkForkInfo{},
			IsSynced:              true,
			CurrentSlot:           34801,
			CurrentEpoch:          1087,
			FinalizedEpoch:        1085,
			EpochsSinceFinality:   2,
			Finalizing:            true,
			ActiveValidatorCount:  activeCount,
			TotalValidatorCount:   totalCount,
			PendingValidatorCount: pendingCount,
			ExitedValidatorCount:  exitedCount,
			DataQualityWarnings:   []string{},
			Metadata:              APINetworkOverviewMetadata{SlotsPerEpoch: 32},
		},
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
