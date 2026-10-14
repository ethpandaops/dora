package verifiers

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/dora/executionproof"
)

// Reports what the embedded verifiers cost at runtime: the compilation cache
// on disk, the heap held while verifying and the time per proof once the cache
// is warm. Needs the fixture proofs (DORA_TEST_PROOF_FIXTURES).
func TestVerifiersOverhead(t *testing.T) {
	dir := os.Getenv("DORA_TEST_PROOF_FIXTURES")
	if dir == "" {
		t.Skip("DORA_TEST_PROOF_FIXTURES not set")
	}

	ctx := context.Background()
	cacheDir := t.TempDir()
	verifiers, err := New(ctx, cacheDir)
	require.NoError(t, err)
	defer func() { _ = verifiers.Close(ctx) }()

	proofs := map[uint8]string{
		1: "stateless-validator-ethrex-openvm-v2.1.0-preview.proof",
		2: "stateless-validator-ethrex-sp1-v6.4.0.proof",
		3: "stateless-validator-ethrex-zisk-v1.1.0-alpha.proof",
	}

	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	for id, file := range proofs {
		proof, err := os.ReadFile(filepath.Join(dir, file))
		require.NoError(t, err)
		proofType := executionproof.Lookup(id)

		// cold: compiles every generation it tries, warm: compiled from the disk cache
		cold := time.Now()
		result := verifiers.Verify(ctx, proofType.ZkVM, proofType.ProgramVK, proof)
		coldTime := time.Since(cold)
		require.Equal(t, StatusValid, result.Status, result.Reason)

		var peak runtime.MemStats
		warm := time.Now()
		for range 5 {
			verifiers.Verify(ctx, proofType.ZkVM, proofType.ProgramVK, proof)
			var now runtime.MemStats
			runtime.ReadMemStats(&now)
			if now.HeapInuse > peak.HeapInuse {
				peak = now
			}
		}
		warmTime := time.Since(warm) / 5

		t.Logf("%-16s verified by %s: cold %s, warm %s, heap in use while verifying %d MB (baseline %d MB)",
			proofType.Name(), result.Generation, coldTime.Round(time.Millisecond), warmTime.Round(time.Millisecond),
			peak.HeapInuse/1048576, before.HeapInuse/1048576)
	}

	var after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("heap in use after releasing: %d MB (baseline %d MB)", after.HeapInuse/1048576, before.HeapInuse/1048576)

	var cacheSize int64
	_ = filepath.WalkDir(cacheDir, func(_ string, d fs.DirEntry, _ error) error {
		if info, err := d.Info(); err == nil && !d.IsDir() {
			cacheSize += info.Size()
		}
		return nil
	})
	t.Logf("compilation cache on disk for %d generations: %d MB", len(generations), cacheSize/1048576)
}
