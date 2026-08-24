package beacon

import (
	"testing"

	"github.com/ethpandaops/dora/dbtypes"
)

func TestPayloadStatusForBlock(t *testing.T) {
	tests := []struct {
		name              string
		hasPayload        bool
		payloadOrphaned   bool
		wantStatus        dbtypes.PayloadStatus
		wantEpochProposed bool
	}{
		{
			name:              "canonical payload",
			hasPayload:        true,
			wantStatus:        dbtypes.PayloadStatusCanonical,
			wantEpochProposed: true,
		},
		{
			name:            "payload missing",
			wantStatus:      dbtypes.PayloadStatusMissing,
			payloadOrphaned: false,
		},
		{
			name:            "late payload skipped by successor",
			hasPayload:      true,
			payloadOrphaned: true,
			wantStatus:      dbtypes.PayloadStatusOrphaned,
		},
		{
			name:            "orphan flag without an envelope remains missing",
			payloadOrphaned: true,
			wantStatus:      dbtypes.PayloadStatusMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := payloadStatusForBlock(tt.hasPayload, tt.payloadOrphaned)
			if status != tt.wantStatus {
				t.Fatalf("payload status = %v, want %v", status, tt.wantStatus)
			}

			if proposed := status == dbtypes.PayloadStatusCanonical; proposed != tt.wantEpochProposed {
				t.Fatalf("counts toward epoch proposals = %v, want %v", proposed, tt.wantEpochProposed)
			}
		})
	}
}
