package rpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// executionProofsPayload is a GET /eth/v1/beacon/execution_proofs/{block_id} response of
// the eth-act Lighthouse optional-proofs branch, which quotes proof_type and
// validator_index.
const executionProofsPayload = `{"data":[{"message":{"proof_data":"0x010203","proof_type":"5","beacon_block_root":"0xf9b85fb511fa7a8561fac828fc390e61a64804e851da4816d7a62cc4648d3e2e"},"validator_index":"7","signature":"0xb399a174693747e9437ba1d94be3ad3e7556a388a6d3e4e7589cea7310735e04296086a957f9a3652d5e14731561151900f4532f72227b60f06449068d610794e8ee273bd00b3179030242096fb95a17cf0277528c7c6c8ddf5a408fca069ad2"}],"execution_optimistic":false,"finalized":true}`

func TestGetExecutionProofsByBlockroot(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		proofs int
	}{
		{
			name:   "proofs",
			status: http.StatusOK,
			body:   executionProofsPayload,
			proofs: 1,
		},
		{
			name:   "unknown block",
			status: http.StatusNotFound,
			body:   `{"code":404,"message":"NOT_FOUND: beacon block with root 0x00"}`,
		},
		{
			name:   "no proof engine",
			status: http.StatusNotImplemented,
			body:   `{"code":501,"message":"NOT_IMPLEMENTED: proof engine not configured"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/eth/v1/beacon/execution_proofs/0x0102" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := &BeaconClient{endpoint: server.URL}
			response, err := client.GetExecutionProofsByBlockroot(context.Background(), []byte{1, 2})
			if err != nil {
				t.Fatalf("fetch failed: %v", err)
			}
			if len(response.Data) != tt.proofs {
				t.Fatalf("unexpected proof count: %d", len(response.Data))
			}
			if tt.proofs == 0 {
				return
			}

			proof := response.Data[0]
			if proof.Message.ProofType != "5" {
				t.Errorf("unexpected proof type: %q", proof.Message.ProofType)
			}
			if proof.Message.ProofData != "0x010203" {
				t.Errorf("unexpected proof data: %q", proof.Message.ProofData)
			}
			if proof.Message.BeaconBlockRoot != "0xf9b85fb511fa7a8561fac828fc390e61a64804e851da4816d7a62cc4648d3e2e" {
				t.Errorf("unexpected beacon block root: %s", proof.Message.BeaconBlockRoot)
			}
			if proof.ValidatorIndex != "7" {
				t.Errorf("unexpected validator index: %q", proof.ValidatorIndex)
			}
			if !response.Finalized {
				t.Error("response not finalized")
			}
		})
	}
}
