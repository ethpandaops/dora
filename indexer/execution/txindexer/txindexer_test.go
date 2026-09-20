package txindexer

import (
	"io"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// Block processing decodes contract-controlled call data and event logs on a
// worker goroutine that nothing else recovers. A panic there must surface as a
// block error so the pipeline can move on, instead of taking the process down
// and re-crashing on the same block after every restart.
func TestBlockPanicBecomesError(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)

	// A nil indexer context makes processElBlock panic on its first
	// context.WithTimeout call, which stands in for any decoding panic.
	indexer := &TxIndexer{logger: logrus.NewEntry(logger)}

	stats, err := indexer.processElBlockGuarded(&BlockRef{Slot: 1, BlockUID: 2})
	if err == nil {
		t.Fatal("expected a recovered panic to be reported as an error")
	}

	if !strings.Contains(err.Error(), "panic while processing el block") {
		t.Fatalf("error does not identify the panic: %v", err)
	}

	if stats != nil {
		t.Fatalf("expected nil stats after a recovered panic, got %+v", stats)
	}
}
