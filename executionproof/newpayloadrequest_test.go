package executionproof

import (
	"encoding/hex"
	"testing"

	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/deneb"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	dynssz "github.com/pk910/dynamic-ssz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gloasRequestVector is the guest encoding of a Gloas request, decoded with
// the fork's own types before it is handed to the fork-agnostic request.
type gloasRequestVector struct {
	ExecutionPayload      *gloas.ExecutionPayload
	VersionedHashes       []deneb.VersionedHash `ssz-type:"progressive-list"`
	ParentBeaconBlockRoot phase0.Root
	ExecutionRequests     *gloas.ExecutionRequests
}

func TestNewPayloadRequestRoot(t *testing.T) {
	ds := dynssz.NewDynSsz(nil)

	for _, vector := range requestVectors {
		t.Run(vector.name, func(t *testing.T) {
			data, err := hex.DecodeString(vector.ssz)
			require.NoError(t, err)

			decoded := &gloasRequestVector{}
			require.NoError(t, ds.UnmarshalSSZ(decoded, data))

			payload := &all.ExecutionPayload{}
			require.NoError(t, payload.FromView(decoded.ExecutionPayload))
			requests := &all.ExecutionRequests{}
			require.NoError(t, requests.FromView(decoded.ExecutionRequests))
			request := &NewPayloadRequest{
				ExecutionPayload:      payload,
				VersionedHashes:       decoded.VersionedHashes,
				ParentBeaconBlockRoot: decoded.ParentBeaconBlockRoot,
				ExecutionRequests:     requests,
			}

			encoded, err := ds.MarshalSSZ(request)
			require.NoError(t, err)
			assert.Equal(t, data, encoded, "serialization matches the guest encoding")

			root, err := request.Root(ds)
			require.NoError(t, err)
			assert.Equal(t, vector.root, hex.EncodeToString(root[:]))
		})
	}
}

func TestVersionedHash(t *testing.T) {
	commitment := deneb.KZGCommitment{0xc0}
	hash := VersionedHash(commitment)
	assert.Equal(t, byte(0x01), hash[0])
	assert.NotEqual(t, phase0.Root(hash), phase0.Root{})
}
