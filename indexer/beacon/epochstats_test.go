package beacon

import (
	"testing"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
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

		// This should panic - we're testing that it does
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("Expected panic when accessing es.dependentState.stateRoot after setting dependentState to nil, but no panic occurred")
			}
		}()

		// This will panic because dependentState is nil
		indexer.logger.Infof(
			"processed epoch %v stats (root: %v / state: %v, validators: %v/%v, %v ms), %v bytes",
			es.epoch,
			es.dependentRoot.String(),
			es.dependentState.stateRoot.String(), // This will panic
			es.values.ActiveValidators,
			0,
			0,
			0,
		)

		// We should never reach here
		t.Errorf("Expected panic, but code continued execution")
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

		// This should not panic because we're using the local copy
		indexer.logger.Infof(
			"processed epoch %v stats (root: %v / state: %v, validators: %v/%v, %v ms), %v bytes",
			es.epoch,
			es.dependentRoot.String(),
			dependentState.stateRoot.String(), // Using local copy - won't panic
			es.values.ActiveValidators,
			0,
			0,
			0,
		)

		// Verify the log message contains the correct stateRoot
		if len(hook.Entries) < 2 {
			t.Errorf("Expected at least 2 log entries, got %d", len(hook.Entries))
		} else {
			logMsg := hook.Entries[len(hook.Entries)-1].Message
			expectedStateRoot := "state: 0x0200000000000000000000000000000000000000000000000000000000000000"
			if logMsg == "" || logMsg != "" && !contains(logMsg, expectedStateRoot) {
				t.Errorf("Expected log message to contain %q, but got %q", expectedStateRoot, logMsg)
			}
		}
	})
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
