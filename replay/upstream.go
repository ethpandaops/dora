package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// errNotFound is returned when the upstream has no artifact for a request. It maps to
// a 404 on the proxy, which is a normal answer for empty slots.
var errNotFound = fmt.Errorf("not found")

// upstreamError carries a non-2xx upstream answer so the proxy can hand the same status
// and body back to its own client. Clients read these codes: a 400 for "this block has
// no payload envelope" means something quite different from a gateway failure.
type upstreamError struct {
	status int
	body   []byte
	header http.Header
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("upstream returned %v: %s", e.status, truncate(e.body, 200))
}

// upstream is the read side of the replay: a beacon node that answers by slot or root,
// with an optional tracoor fallback for artifacts the node has already pruned.
type upstream struct {
	logger  logrus.FieldLogger
	base    *url.URL
	client  *http.Client
	tracoor *tracoorClient
	cache   *artifactCache

	// chain is set once the timing specs are known; it labels tracoor artifacts with
	// the fork they belong to.
	chain *chainInfo
}

// artifact is a raw upstream response, kept encoded so it can be forwarded verbatim.
// The headers matter as much as the body: SSZ responses carry their fork in
// Eth-Consensus-Version, without which the explorer cannot decode them.
type artifact struct {
	body   []byte
	header http.Header
}

// newPooledClient returns an HTTP client that keeps connections alive across the many
// small requests a replay makes. The default transport keeps only two idle connections
// per host, which for a TLS upstream means a fresh handshake on nearly every request —
// on a remote devnet that alone dominates the time it takes to step a slot.
func newPooledClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 128
	transport.MaxIdleConnsPerHost = 64
	transport.MaxConnsPerHost = 64
	transport.IdleConnTimeout = 10 * time.Minute

	return &http.Client{Transport: transport, Timeout: timeout}
}

func newUpstream(logger logrus.FieldLogger, cfg *Config) (*upstream, error) {
	base, err := url.Parse(strings.TrimSuffix(cfg.UpstreamURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid upstream url: %w", err)
	}

	up := &upstream{
		logger: logger,
		base:   base,
		client: newPooledClient(10 * time.Minute),
	}

	if cfg.TracoorURL != "" {
		tracoor, err := newTracoorClient(cfg.TracoorURL, cfg.TracoorNetwork)
		if err != nil {
			return nil, err
		}

		up.tracoor = tracoor
	}

	if cfg.CacheDir != "" {
		cache, err := newArtifactCache(logger, cfg.CacheDir)
		if err != nil {
			return nil, err
		}

		up.cache = cache
	}

	return up, nil
}

// get fetches a path from the beacon upstream, serving it from the artifact cache when
// the path is immutable and already recorded.
func (u *upstream) get(ctx context.Context, path string, accept string) (*artifact, error) {
	cacheKey := ""
	if u.cache != nil && isImmutablePath(path) {
		cacheKey = u.cache.key(path, accept)
		if art := u.cache.load(cacheKey); art != nil {
			return art, nil
		}
	}

	art, err := u.fetch(ctx, path, accept)
	if err != nil {
		return nil, err
	}

	if cacheKey != "" {
		u.cache.store(cacheKey, art)
	}

	return art, nil
}

func (u *upstream) fetch(ctx context.Context, path string, accept string) (*artifact, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.base.String()+path, http.NoBody)
	if err != nil {
		return nil, err
	}

	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	rsp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rsp.Body.Close() }()

	body, err := io.ReadAll(rsp.Body)
	if err != nil {
		return nil, err
	}

	if rsp.StatusCode == http.StatusNotFound {
		return nil, errNotFound
	}

	if rsp.StatusCode != http.StatusOK {
		return nil, &upstreamError{
			status: rsp.StatusCode,
			body:   body,
			header: forwardableHeaders(rsp.Header),
		}
	}

	return &artifact{body: body, header: forwardableHeaders(rsp.Header)}, nil
}

// hopByHopHeaders are the response headers that describe this particular connection
// rather than the artifact, so they must not be copied to the proxied response.
var hopByHopHeaders = map[string]bool{
	"Connection":         true,
	"Content-Encoding":   true,
	"Content-Length":     true,
	"Keep-Alive":         true,
	"Proxy-Authenticate": true,
	"Proxy-Connection":   true,
	"Te":                 true,
	"Trailer":            true,
	"Transfer-Encoding":  true,
	"Upgrade":            true,
	"Date":               true,
	"Server":             true,
}

func forwardableHeaders(src http.Header) http.Header {
	dst := make(http.Header, len(src))

	for key, values := range src {
		if hopByHopHeaders[http.CanonicalHeaderKey(key)] {
			continue
		}

		dst[http.CanonicalHeaderKey(key)] = values
	}

	return dst
}

func (u *upstream) getJSON(ctx context.Context, path string, out any) error {
	art, err := u.get(ctx, path, "application/json")
	if err != nil {
		return err
	}

	if err := json.Unmarshal(art.body, out); err != nil {
		return fmt.Errorf("error parsing %v: %w", path, err)
	}

	return nil
}

// blockHeader is the part of a beacon block header the replay needs to track the head
// and to rewrite `head` aliases into concrete roots.
type blockHeader struct {
	Slot        uint64
	Root        string
	StateRoot   string
	ParentRoot  string
	BlockNumber uint64
}

type headerResponse struct {
	Data struct {
		Root   string `json:"root"`
		Header struct {
			Message struct {
				Slot       string `json:"slot"`
				ParentRoot string `json:"parent_root"`
				StateRoot  string `json:"state_root"`
			} `json:"message"`
		} `json:"header"`
	} `json:"data"`
}

func (r *headerResponse) toHeader() (*blockHeader, error) {
	slot, err := strconv.ParseUint(r.Data.Header.Message.Slot, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid slot in header response: %w", err)
	}

	return &blockHeader{
		Slot:       slot,
		Root:       r.Data.Root,
		StateRoot:  r.Data.Header.Message.StateRoot,
		ParentRoot: r.Data.Header.Message.ParentRoot,
	}, nil
}

// headerBySlot returns the block header at a slot, or nil when the slot is empty.
func (u *upstream) headerBySlot(ctx context.Context, slot uint64) (*blockHeader, error) {
	return u.header(ctx, strconv.FormatUint(slot, 10))
}

// headerByRoot returns the block header for a block root, or nil when it is unknown.
func (u *upstream) headerByRoot(ctx context.Context, root string) (*blockHeader, error) {
	return u.header(ctx, root)
}

func (u *upstream) header(ctx context.Context, blockID string) (*blockHeader, error) {
	rsp := headerResponse{}

	err := u.getJSON(ctx, "/eth/v1/beacon/headers/"+blockID, &rsp)
	if err != nil {
		if err == errNotFound {
			return nil, nil
		}

		return nil, err
	}

	return rsp.toHeader()
}

// finalityCheckpoints is the finality the chain knew at a given block root. Raw keeps
// the upstream response so the replay can serve it back verbatim, including the
// previous justified checkpoint it does not track itself.
type finalityCheckpoints struct {
	JustifiedEpoch uint64
	JustifiedRoot  string
	FinalizedEpoch uint64
	FinalizedRoot  string
	Raw            json.RawMessage
}

func (u *upstream) finality(ctx context.Context, stateID string) (*finalityCheckpoints, error) {
	rsp := struct {
		Data struct {
			CurrentJustified struct {
				Epoch string `json:"epoch"`
				Root  string `json:"root"`
			} `json:"current_justified"`
			Finalized struct {
				Epoch string `json:"epoch"`
				Root  string `json:"root"`
			} `json:"finalized"`
		} `json:"data"`
	}{}

	art, err := u.get(ctx, "/eth/v1/beacon/states/"+stateID+"/finality_checkpoints", "application/json")
	if err != nil {
		return nil, err
	}

	raw := json.RawMessage(art.body)

	if err := json.Unmarshal(raw, &rsp); err != nil {
		return nil, fmt.Errorf("error parsing finality checkpoints: %w", err)
	}

	justifiedEpoch, err := strconv.ParseUint(rsp.Data.CurrentJustified.Epoch, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid justified epoch: %w", err)
	}

	finalizedEpoch, err := strconv.ParseUint(rsp.Data.Finalized.Epoch, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid finalized epoch: %w", err)
	}

	return &finalityCheckpoints{
		JustifiedEpoch: justifiedEpoch,
		JustifiedRoot:  rsp.Data.CurrentJustified.Root,
		FinalizedEpoch: finalizedEpoch,
		FinalizedRoot:  rsp.Data.Finalized.Root,
		Raw:            raw,
	}, nil
}

// payloadBid returns the winning execution payload bid carried by a Gloas block, as
// raw JSON ready to be replayed on the event stream. It returns nil for blocks from
// forks that have no bid.
func (u *upstream) payloadBid(ctx context.Context, root string) (json.RawMessage, error) {
	rsp := struct {
		Data struct {
			Message struct {
				Body struct {
					SignedExecutionPayloadBid json.RawMessage `json:"signed_execution_payload_bid"`
				} `json:"body"`
			} `json:"message"`
		} `json:"data"`
	}{}

	if err := u.getJSON(ctx, "/eth/v2/beacon/blocks/"+root, &rsp); err != nil {
		return nil, err
	}

	bid := rsp.Data.Message.Body.SignedExecutionPayloadBid
	if len(bid) == 0 || string(bid) == "null" {
		return nil, nil
	}

	return bid, nil
}

func isStatePath(path string) bool {
	return strings.Contains(path, "/debug/beacon/states/")
}

// serve answers a proxied GET for a client of the fake node. Beacon states are read
// from tracoor first when it is configured: it keeps every state, while beacon nodes
// prune all but the most recent ones, and serving a 17 MB state per epoch out of the
// archive would hammer the devnet.
func (u *upstream) serve(w http.ResponseWriter, r *http.Request, path string) {
	accept := r.Header.Get("Accept")

	// states are read from tracoor first, blocks are not: the node still has every
	// block of the replayed range and answers in one round trip, while a tracoor read
	// costs a lookup plus a download
	if isStatePath(path) {
		if art := u.tryTracoor(r.Context(), path, accept); art != nil {
			writeArtifact(w, art, http.StatusOK)
			return
		}
	}

	art, err := u.get(r.Context(), path, accept)

	switch {
	case err == errNotFound:
		if art = u.tryTracoor(r.Context(), path, "application/octet-stream"); art == nil {
			writeAPIError(w, http.StatusNotFound, "not found")
			return
		}

	case err != nil:
		var upErr *upstreamError
		if errors.As(err, &upErr) {
			// hand the upstream's own answer back unchanged; the client knows what a
			// 400 or a 503 from a beacon node means
			u.logger.Debugf("upstream %v answered %v", path, upErr.status)
			writeArtifact(w, &artifact{body: upErr.body, header: upErr.header}, upErr.status)

			return
		}

		u.logger.WithError(err).Warnf("upstream request failed: %v", path)
		writeAPIError(w, http.StatusBadGateway, err.Error())

		return
	}

	writeArtifact(w, art, http.StatusOK)
}

// tryTracoor resolves a root-addressed block or state from tracoor, returning nil when
// tracoor is not configured, cannot serve this path, or the client cannot take SSZ.
func (u *upstream) tryTracoor(ctx context.Context, path, accept string) *artifact {
	if u.tracoor == nil || !acceptsSSZ(accept) {
		return nil
	}

	segments := strings.Split(strings.Trim(path, "/"), "/")
	root := segments[len(segments)-1]

	if !strings.HasPrefix(root, "0x") {
		return nil
	}

	cacheKey := ""
	if u.cache != nil {
		cacheKey = u.cache.key(path, "tracoor")
		if art := u.cache.load(cacheKey); art != nil {
			return art
		}
	}

	var (
		fetched *tracoorArtifact
		err     error
	)

	switch {
	case strings.Contains(path, "/debug/beacon/states/"):
		fetched, err = u.tracoor.fetchBeaconState(ctx, root)
	case strings.Contains(path, "/beacon/blocks/"):
		fetched, err = u.tracoor.fetchBeaconBlock(ctx, root)
	default:
		return nil
	}

	if err != nil {
		if err != errNotFound {
			u.logger.WithError(err).Debugf("tracoor lookup failed for %v", path)
		}

		return nil
	}

	art := &artifact{
		body: fetched.body,
		header: http.Header{
			"Content-Type":          []string{"application/octet-stream"},
			"Eth-Consensus-Version": []string{u.forkAt(fetched.slot)},
		},
	}

	if cacheKey != "" {
		u.cache.store(cacheKey, art)
	}

	u.logger.Debugf("served %v from tracoor (slot %v, %v bytes)", path, fetched.slot, len(art.body))

	return art
}

func (u *upstream) forkAt(slot uint64) string {
	if u.chain == nil {
		return ""
	}

	return u.chain.forkAt(slot)
}

// acceptsSSZ reports whether a client will take an SSZ-encoded response. Tracoor only
// stores SSZ, so a JSON-only client has to be served from the beacon node.
func acceptsSSZ(accept string) bool {
	return accept == "" || strings.Contains(accept, "application/octet-stream") || strings.Contains(accept, "*/*")
}

func writeArtifact(w http.ResponseWriter, art *artifact, status int) {
	for key, values := range art.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/octet-stream")
	}

	w.WriteHeader(status)

	if _, err := w.Write(art.body); err != nil {
		return
	}
}

func truncate(data []byte, max int) string {
	if len(data) <= max {
		return string(data)
	}

	return string(data[:max]) + "..."
}
