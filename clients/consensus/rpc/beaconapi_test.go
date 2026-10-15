package rpc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// executionProofEventQuoted is an execution_proof event with proof_type quoted as the
// beacon API quotes numbers (Prysm); executionProofEventBare carries it as a bare
// number (Lighthouse).
const executionProofEventQuoted = `{"message":{"proof_data":"0x010203","proof_type":"5","beacon_block_root":"0xf9b85fb511fa7a8561fac828fc390e61a64804e851da4816d7a62cc4648d3e2e"},"validator_index":"7","signature":"0xb399a174693747e9437ba1d94be3ad3e7556a388a6d3e4e7589cea7310735e04296086a957f9a3652d5e14731561151900f4532f72227b60f06449068d610794e8ee273bd00b3179030242096fb95a17cf0277528c7c6c8ddf5a408fca069ad2"}`

const executionProofEventBare = `{"message":{"proof_data":"0x010203","proof_type":5,"beacon_block_root":"0xf9b85fb511fa7a8561fac828fc390e61a64804e851da4816d7a62cc4648d3e2e"},"validator_index":"7","signature":"0xb399a174693747e9437ba1d94be3ad3e7556a388a6d3e4e7589cea7310735e04296086a957f9a3652d5e14731561151900f4532f72227b60f06449068d610794e8ee273bd00b3179030242096fb95a17cf0277528c7c6c8ddf5a408fca069ad2"}`

func TestParseExecutionProofEvent(t *testing.T) {
	for name, event := range map[string]string{"quoted proof type": executionProofEventQuoted, "bare proof type": executionProofEventBare} {
		t.Run(name, func(t *testing.T) {
			proof, err := parseExecutionProofEvent([]byte(event))
			require.NoError(t, err)
			require.Equal(t, uint8(5), proof.ProofType)
			require.Equal(t, []byte{1, 2, 3}, proof.ProofData)
			require.Equal(t, uint64(7), proof.ValidatorIndex)
			require.Equal(t, "f9b85fb511fa7a8561fac828fc390e61a64804e851da4816d7a62cc4648d3e2e", proof.BeaconBlockRoot.String()[2:])
			require.Equal(t, byte(0xb3), proof.Signature[0])
			require.Equal(t, byte(0xd2), proof.Signature[95])
		})
	}

	for name, event := range map[string]string{
		"proof type out of range": `{"message":{"proof_data":"0x01","proof_type":"300","beacon_block_root":"0x00"},"validator_index":"7","signature":"0x00"}`,
		"short root":              `{"message":{"proof_data":"0x01","proof_type":"5","beacon_block_root":"0x0102"},"validator_index":"7","signature":"0x00"}`,
		"not json":                `nope`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseExecutionProofEvent([]byte(event))
			require.Error(t, err)
		})
	}
}
