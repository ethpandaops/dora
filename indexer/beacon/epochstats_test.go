package beacon

import (
	"testing"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
)

// TestEpochStats_PrunedDependentState tests that storing a local copy of dependentState
// at the beginning of a function protects against it being set to nil during execution.
func TestEpochStats_PrunedDependentState(t *testing.T) {
	logger, hook := test.NewNullLogger()
	logger.SetLevel(logrus.InfoLevel)

	indexer := &Indexer{
		logger: logrus.NewEntry(logger),
	}

	t.Run("DependentStateNil_ShouldPanic", func(t *testing.T) {
		es := &EpochStats{
			epoch:         phase0.Epoch(123),
			dependentRoot: phase0.Root{0x01},
			dependentState: &epochState{
				stateRoot: phase0.Root{0x02},
			},
			values: &EpochStatsValues{
				ActiveValidators: 100,
			},
		}

		// Access prior to pruning is fine.
		indexer.logger.Infof(
			"processed epoch %v stats (root: %v / state: %v, validators: %v/%v, %v ms), %v bytes",
			es.epoch,
			es.dependentRoot.String(),
			es.dependentState.stateRoot.String(),
			es.values.ActiveValidators,
			0,
			0,
			0,
		)

		// Set dependentState to nil to simulate the epoch cache pruner.
		es.dependentState = nil

		// This should panic - assert it does.
		assert.Panics(t, func() {
			indexer.logger.Infof(
				"processed epoch %v stats (root: %v / state: %v, validators: %v/%v, %v ms), %v bytes",
				es.epoch,
				es.dependentRoot.String(),
				es.dependentState.stateRoot.String(),
				es.values.ActiveValidators,
				0,
				0,
				0,
			)
		}, "Should panic when accessing es.dependentState.stateRoot after setting dependentState to nil")
	})

	t.Run("DependentStateNotNil_ShouldNotPanic", func(t *testing.T) {
		es := &EpochStats{
			epoch:         phase0.Epoch(123),
			dependentRoot: phase0.Root{0x01},
			dependentState: &epochState{
				stateRoot: phase0.Root{0x02},
			},
			values: &EpochStatsValues{
				ActiveValidators: 100,
			},
		}

		dependentState := es.dependentState

		// Access prior to pruning is fine.
		indexer.logger.Infof(
			"processed epoch %v stats (root: %v / state: %v, validators: %v/%v, %v ms), %v bytes",
			es.epoch,
			es.dependentRoot.String(),
			dependentState.stateRoot.String(),
			es.values.ActiveValidators,
			0,
			0,
			0,
		)

		// Set es.dependentState to nil to simulate the epoch cache pruner.
		es.dependentState = nil

		assert.NotPanics(t, func() {
			indexer.logger.Infof(
				"processed epoch %v stats (root: %v / state: %v, validators: %v/%v, %v ms), %v bytes",
				es.epoch,
				es.dependentRoot.String(),
				dependentState.stateRoot.String(),
				es.values.ActiveValidators,
				0,
				0,
				0,
			)
		}, "Should not panic when using local copy of dependentState")

		assert.Contains(
			t,
			hook.LastEntry().Message,
			"state: 0x0200000000000000000000000000000000000000000000000000000000000000",
			"Log message should contain the correct stateRoot",
		)
	})
}
