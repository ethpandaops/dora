package verifiers

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/dora/executionproof"
)

func TestVerifiersRejectGarbage(t *testing.T) {
	ctx := context.Background()
	verifiers, err := New(ctx, t.TempDir())
	require.NoError(t, err)
	defer func() { _ = verifiers.Close(ctx) }()

	require.NotEmpty(t, Generations())

	for _, zkvm := range []executionproof.ZkVM{executionproof.ZkVMSP1, executionproof.ZkVMZisk, executionproof.ZkVMOpenVM} {
		result, err := verifiers.Verify(ctx, zkvm, executionproof.Lookup(map[executionproof.ZkVM]uint8{executionproof.ZkVMSP1: 2, executionproof.ZkVMZisk: 3, executionproof.ZkVMOpenVM: 1}[zkvm]).ProgramVK, []byte{1, 2, 3})
		require.NoError(t, err)
		assert.Equal(t, StatusInvalid, result.Status, "%s: %s", zkvm, result.Reason)
		assert.Equal(t, generations[0], result.Generation)
	}

	result, err := verifiers.Verify(ctx, executionproof.ZkVM("other"), nil, []byte{1})
	require.NoError(t, err)
	assert.Equal(t, StatusUnverifiable, result.Status)
}

// The real proofs are too large for the repository; point
// DORA_TEST_PROOF_FIXTURES at Lighthouse's proof_engine test fixtures to run
// the verifiers on them.
func TestVerifiersFixtures(t *testing.T) {
	dir := os.Getenv("DORA_TEST_PROOF_FIXTURES")
	if dir == "" {
		t.Skip("DORA_TEST_PROOF_FIXTURES not set")
	}

	ctx := context.Background()
	verifiers, err := New(ctx, t.TempDir())
	require.NoError(t, err)
	defer func() { _ = verifiers.Close(ctx) }()

	expected := "68e2250786a622ab175a8149c63d220704e7ed9610a12b7864047510235e7bb70101000000000000000115"
	fixtures := map[uint8]string{
		1: "stateless-validator-ethrex-openvm-v2.1.0-preview.proof",
		2: "stateless-validator-ethrex-sp1-v6.4.0.proof",
		3: "stateless-validator-ethrex-zisk-v1.1.0-alpha.proof",
		4: "stateless-validator-reth-openvm-v2.1.0-preview.proof",
		5: "stateless-validator-reth-sp1-v6.4.0.proof",
		6: "stateless-validator-reth-zisk-v1.1.0-alpha.proof",
	}
	for id, file := range fixtures {
		proof, err := os.ReadFile(filepath.Join(dir, file))
		require.NoError(t, err)
		proofType := executionproof.Lookup(id)

		start := time.Now()
		result, err := verifiers.Verify(ctx, proofType.ZkVM, proofType.ProgramVK, proof)
		require.NoError(t, err)
		t.Logf("type %d (%s): %s by %s in %s", id, proofType.Name(), result.Status, result.Generation, time.Since(start).Round(time.Millisecond))
		require.Equal(t, StatusValid, result.Status, result.Reason)
		assert.Equal(t, expected, hex.EncodeToString(result.PublicValues[:executionproof.PublicInputSize]))

		// the key of the other guest rejects the proof
		other := executionproof.Lookup((id+2)%6 + 1)
		for other.ZkVM != proofType.ZkVM {
			other = executionproof.Lookup(other.ID%7 + 1)
		}
		result, err = verifiers.Verify(ctx, proofType.ZkVM, other.ProgramVK, proof)
		require.NoError(t, err)
		assert.Equal(t, StatusInvalid, result.Status, "type %d with key of type %d", id, other.ID)
	}
}
