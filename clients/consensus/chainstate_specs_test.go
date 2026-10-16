package consensus

import (
	"math"
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

func TestComputeMajoritySpecsPrefersScheduledFork(t *testing.T) {
	const farFuture = uint64(math.MaxUint64)

	unscheduled := func(ptcSize uint64) map[string]any {
		return map[string]any{
			"SLOTS_PER_EPOCH":    uint64(32),
			"FULU_FORK_EPOCH":    uint64(50688),
			"GLOAS_FORK_EPOCH":   farFuture,
			"GLOAS_FORK_VERSION": phase0.Version{0xff, 0xff, 0xff, 0xff},
			"PTC_SIZE":           ptcSize,
		}
	}
	scheduled := func(gloasEpoch uint64) map[string]any {
		return map[string]any{
			"SLOTS_PER_EPOCH":    uint64(32),
			"FULU_FORK_EPOCH":    uint64(50688),
			"GLOAS_FORK_EPOCH":   gloasEpoch,
			"GLOAS_FORK_VERSION": phase0.Version{0x80, 0x00, 0x09, 0x10},
			"PTC_SIZE":           uint64(512),
		}
	}

	tests := []struct {
		name          string
		clientSpecs   []map[string]any
		wantGloas     *uint64
		wantVersion   phase0.Version
		wantPtcSize   uint64
		wantSlotsPerE uint64
	}{
		{
			name:          "scheduled minority wins over unscheduled majority",
			clientSpecs:   []map[string]any{unscheduled(64), scheduled(132352), unscheduled(64)},
			wantGloas:     new(uint64(132352)),
			wantVersion:   phase0.Version{0x80, 0x00, 0x09, 0x10},
			wantPtcSize:   512,
			wantSlotsPerE: 32,
		},
		{
			name:          "majority among scheduled clients",
			clientSpecs:   []map[string]any{scheduled(100), scheduled(200), scheduled(200), unscheduled(64)},
			wantGloas:     new(uint64(200)),
			wantVersion:   phase0.Version{0x80, 0x00, 0x09, 0x10},
			wantPtcSize:   512,
			wantSlotsPerE: 32,
		},
		{
			name:          "fork unscheduled on all clients",
			clientSpecs:   []map[string]any{unscheduled(64), unscheduled(64)},
			wantGloas:     new(farFuture),
			wantVersion:   phase0.Version{0xff, 0xff, 0xff, 0xff},
			wantPtcSize:   64,
			wantSlotsPerE: 32,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cs := &ChainState{clientSpecs: make(map[*Client]map[string]any, len(test.clientSpecs))}
			for _, specValues := range test.clientSpecs {
				cs.clientSpecs[&Client{}] = specValues
			}

			specs, err := cs.computeMajoritySpecs()
			if err != nil {
				t.Fatalf("computeMajoritySpecs failed: %v", err)
			}

			if specs.GloasForkEpoch == nil || *specs.GloasForkEpoch != *test.wantGloas {
				t.Errorf("GloasForkEpoch = %v, want %d", specs.GloasForkEpoch, *test.wantGloas)
			}
			if specs.GloasForkVersion != test.wantVersion {
				t.Errorf("GloasForkVersion = %x, want %x", specs.GloasForkVersion, test.wantVersion)
			}
			if specs.PtcSize != test.wantPtcSize {
				t.Errorf("PtcSize = %d, want %d", specs.PtcSize, test.wantPtcSize)
			}
			if specs.SlotsPerEpoch != test.wantSlotsPerE {
				t.Errorf("SlotsPerEpoch = %d, want %d", specs.SlotsPerEpoch, test.wantSlotsPerE)
			}
		})
	}
}
