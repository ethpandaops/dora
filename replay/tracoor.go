package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// zstdMagic identifies a zstd frame. Tracoor stores its artifacts compressed and the
// object store hands them back with Content-Encoding: zstd, which the Go HTTP client
// does not unwrap on its own.
var zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}

// tracoorArtifact is a stored artifact and the slot it belongs to. The slot is what
// lets the replay label the artifact with the right fork.
type tracoorArtifact struct {
	body []byte
	slot uint64
}

// tracoorClient resolves artifacts from a tracoor instance. Tracoor indexes by root
// rather than by slot, so it can only answer for blocks and states whose root is
// already known from a header.
type tracoorClient struct {
	base    string
	network string
	client  *http.Client
	decoder *zstd.Decoder
}

func newTracoorClient(baseURL, network string) (*tracoorClient, error) {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("could not create zstd decoder: %w", err)
	}

	return &tracoorClient{
		base:    strings.TrimSuffix(baseURL, "/"),
		network: network,
		client:  newPooledClient(5 * time.Minute),
		decoder: decoder,
	}, nil
}

func (t *tracoorClient) fetchBeaconBlock(ctx context.Context, blockRoot string) (*tracoorArtifact, error) {
	return t.fetch(ctx, "list-beacon-block", "block_root", blockRoot, "beacon_blocks", "beacon_block")
}

func (t *tracoorClient) fetchBeaconState(ctx context.Context, stateRoot string) (*tracoorArtifact, error) {
	return t.fetch(ctx, "list-beacon-state", "state_root", stateRoot, "beacon_states", "beacon_state")
}

func (t *tracoorClient) fetch(ctx context.Context, endpoint, rootField, root, listField, artifactType string) (*tracoorArtifact, error) {
	id, slot, err := t.lookup(ctx, endpoint, rootField, root, listField)
	if err != nil {
		return nil, err
	}

	body, err := t.download(ctx, artifactType, id)
	if err != nil {
		return nil, err
	}

	return &tracoorArtifact{body: body, slot: slot}, nil
}

// lookup asks tracoor which stored artifacts match a root and returns the newest one.
func (t *tracoorClient) lookup(ctx context.Context, endpoint, rootField, root, listField string) (string, uint64, error) {
	request := map[string]any{
		"network": t.network,
		rootField: root,
		"pagination": map[string]any{
			"limit":    1,
			"offset":   0,
			"order_by": "fetched_at DESC",
		},
	}

	body, err := json.Marshal(request)
	if err != nil {
		return "", 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/v1/api/"+endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}

	req.Header.Set("Content-Type", "application/json")

	rsp, err := t.client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = rsp.Body.Close() }()

	if rsp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(rsp.Body)
		return "", 0, fmt.Errorf("tracoor %v returned %v: %s", endpoint, rsp.StatusCode, truncate(data, 200))
	}

	// the response shape differs per endpoint only in the name of the list field
	parsed := map[string][]struct {
		ID   string `json:"id"`
		Slot string `json:"slot"`
	}{}

	if err := json.NewDecoder(rsp.Body).Decode(&parsed); err != nil {
		return "", 0, fmt.Errorf("error parsing tracoor response: %w", err)
	}

	items := parsed[listField]
	if len(items) == 0 || items[0].ID == "" {
		return "", 0, errNotFound
	}

	slot, err := strconv.ParseUint(items[0].Slot, 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("invalid slot %q in tracoor response: %w", items[0].Slot, err)
	}

	return items[0].ID, slot, nil
}

func (t *tracoorClient) download(ctx context.Context, artifactType, id string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.base+"/download/"+artifactType+"/"+id, http.NoBody)
	if err != nil {
		return nil, err
	}

	rsp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rsp.Body.Close() }()

	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tracoor download of %v/%v returned %v", artifactType, id, rsp.StatusCode)
	}

	body, err := io.ReadAll(rsp.Body)
	if err != nil {
		return nil, err
	}

	return t.decompress(body)
}

// decompress unwraps a zstd frame, leaving anything else untouched.
func (t *tracoorClient) decompress(body []byte) ([]byte, error) {
	if !bytes.HasPrefix(body, zstdMagic) {
		return body, nil
	}

	decoded, err := t.decoder.DecodeAll(body, nil)
	if err != nil {
		return nil, fmt.Errorf("error decompressing tracoor artifact: %w", err)
	}

	return decoded, nil
}
