package rpc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// lighthouseProofsPayload is a GET /eth/v1/beacon/execution_proofs/{block_id} response
// with proof_type quoted as the beacon API quotes numbers; Lighthouse serves it as a
// bare number (lighthouseBareProofTypePayload).
const lighthouseBareProofTypePayload = `{"data":[{"message":{"proof_data":"0x010203","proof_type":5,"beacon_block_root":"0xf9b85fb511fa7a8561fac828fc390e61a64804e851da4816d7a62cc4648d3e2e"},"validator_index":"7","signature":"0xb399a174693747e9437ba1d94be3ad3e7556a388a6d3e4e7589cea7310735e04296086a957f9a3652d5e14731561151900f4532f72227b60f06449068d610794e8ee273bd00b3179030242096fb95a17cf0277528c7c6c8ddf5a408fca069ad2"}]}`

const lighthouseProofsPayload = `{"data":[{"message":{"proof_data":"0x010203","proof_type":"5","beacon_block_root":"0xf9b85fb511fa7a8561fac828fc390e61a64804e851da4816d7a62cc4648d3e2e"},"validator_index":"7","signature":"0xb399a174693747e9437ba1d94be3ad3e7556a388a6d3e4e7589cea7310735e04296086a957f9a3652d5e14731561151900f4532f72227b60f06449068d610794e8ee273bd00b3179030242096fb95a17cf0277528c7c6c8ddf5a408fca069ad2"}],"execution_optimistic":false,"finalized":true}`

func TestGetExecutionProofsByBlockroot(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		proofs int
		err    error
	}{
		{
			name:   "proofs",
			status: http.StatusOK,
			body:   lighthouseProofsPayload,
			proofs: 1,
		},
		{
			name:   "proofs with bare proof type",
			status: http.StatusOK,
			body:   lighthouseBareProofTypePayload,
			proofs: 1,
		},
		{
			name:   "unknown block",
			status: http.StatusNotFound,
			body:   `{"code":404,"message":"NOT_FOUND: beacon block with root 0x00"}`,
			err:    ErrExecutionProofsNotFound,
		},
		{
			name:   "no proof engine",
			status: http.StatusNotImplemented,
			body:   `{"code":501,"message":"NOT_IMPLEMENTED: proof engine not configured"}`,
			err:    ErrExecutionProofsUnsupported,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/eth/v1/beacon/execution_proofs/0x0102", r.URL.Path)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := &BeaconClient{endpoint: server.URL}
			proofs, err := client.GetExecutionProofsByBlockroot(context.Background(), []byte{1, 2})
			if tt.err != nil {
				require.True(t, errors.Is(err, tt.err), "unexpected error: %v", err)
				return
			}
			require.NoError(t, err)
			require.Len(t, proofs, tt.proofs)

			proof := proofs[0]
			require.Equal(t, uint8(5), proof.ProofType)
			require.Equal(t, []byte{1, 2, 3}, proof.ProofData)
			require.Equal(t, "f9b85fb511fa7a8561fac828fc390e61a64804e851da4816d7a62cc4648d3e2e", proof.BeaconBlockRoot.String()[2:])
			require.Equal(t, uint64(7), proof.ValidatorIndex)
			require.Equal(t, byte(0xb3), proof.Signature[0])
		})
	}
}
