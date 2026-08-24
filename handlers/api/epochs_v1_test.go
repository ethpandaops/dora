package api

import "testing"

func TestEpochSlotRange(t *testing.T) {
	tests := []struct {
		name          string
		epoch         uint64
		slotsPerEpoch uint64
		wantFirst     uint64
		wantLast      uint64
	}{
		{
			name:          "genesis epoch",
			epoch:         0,
			slotsPerEpoch: 32,
			wantFirst:     0,
			wantLast:      31,
		},
		{
			name:          "devnet regression epoch",
			epoch:         2454,
			slotsPerEpoch: 32,
			wantFirst:     78528,
			wantLast:      78559,
		},
		{
			name:          "zero slots is safe",
			epoch:         2454,
			slotsPerEpoch: 0,
			wantFirst:     0,
			wantLast:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, last := epochSlotRange(tt.epoch, tt.slotsPerEpoch)
			if first != tt.wantFirst || last != tt.wantLast {
				t.Fatalf("epoch slot range = [%d, %d], want [%d, %d]", first, last, tt.wantFirst, tt.wantLast)
			}

			if tt.slotsPerEpoch > 0 && last-first+1 != tt.slotsPerEpoch {
				t.Fatalf("epoch range contains %d slots, want %d", last-first+1, tt.slotsPerEpoch)
			}
		})
	}
}
