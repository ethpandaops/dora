package rpc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"sync"
	"time"
)

// ProbeStatus is the classified result of probing a single beacon-API endpoint.
type ProbeStatus string

const (
	ProbeStatusOK               ProbeStatus = "ok"
	ProbeStatusMissing          ProbeStatus = "missing"
	ProbeStatusAcceptedNoEvents ProbeStatus = "accepted_no_events"
	ProbeStatusUnknown          ProbeStatus = "unknown"
	ProbeStatusError            ProbeStatus = "error"
)

// ProbeKind distinguishes REST endpoints from SSE topics.
type ProbeKind string

const (
	ProbeKindREST ProbeKind = "rest"
	ProbeKindSSE  ProbeKind = "sse"
)

// EndpointInfo describes a single endpoint in the probe catalog.
type EndpointInfo struct {
	Key         string    `json:"key"`
	Number      int       `json:"number"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Description string    `json:"description"`
	SpecPR      string    `json:"spec_pr"`
	Kind        ProbeKind `json:"kind"`
}

// ProbeResult is the per-client outcome of probing a single endpoint.
type ProbeResult struct {
	Status      ProbeStatus `json:"status"`
	StatusCode  int         `json:"status_code,omitempty"`
	LastChecked time.Time   `json:"last_checked"`
	Detail      string      `json:"detail,omitempty"`
}

// EndpointCatalog enumerates the 16 Gloas-era beacon API endpoints and SSE
// topics tracked in the compatibility matrix. The ordering matches the
// ethpandaops/eth-clients matrix.
var EndpointCatalog = []EndpointInfo{
	// GET endpoints
	{Number: 2, Key: "produce_block_v4", Method: "GET", Path: "/eth/v4/validator/blocks/{slot}", Description: "Produce block (v4)", SpecPR: "580", Kind: ProbeKindREST},
	{Number: 4, Key: "get_payload_bid", Method: "GET", Path: "/eth/v1/validator/execution_payload_bid/{slot}/{builder_index}", Description: "Get execution payload bid", SpecPR: "552", Kind: ProbeKindREST},
	{Number: 6, Key: "get_payload_envelope_block", Method: "GET", Path: "/eth/v1/beacon/execution_payload_envelope/{block_id}", Description: "Get execution payload envelope by block id", SpecPR: "552", Kind: ProbeKindREST},
	{Number: 7, Key: "get_payload_envelope_slot", Method: "GET", Path: "/eth/v1/validator/execution_payload_envelope/{slot}", Description: "Get execution payload envelope by slot", SpecPR: "580", Kind: ProbeKindREST},
	{Number: 9, Key: "payload_attestation_data", Method: "GET", Path: "/eth/v1/validator/payload_attestation_data/{slot}", Description: "Payload attestation data", SpecPR: "552", Kind: ProbeKindREST},
	{Number: 11, Key: "get_payload_attestations", Method: "GET", Path: "/eth/v1/beacon/pool/payload_attestations", Description: "Get pooled payload attestations", SpecPR: "552", Kind: ProbeKindREST},

	// POST endpoints
	{Number: 1, Key: "publish_block_v2", Method: "POST", Path: "/eth/v2/beacon/blocks", Description: "Publish Gloas block", SpecPR: "552", Kind: ProbeKindREST},
	{Number: 3, Key: "publish_payload_bid", Method: "POST", Path: "/eth/v1/beacon/execution_payload_bid", Description: "Publish execution payload bid", SpecPR: "552", Kind: ProbeKindREST},
	{Number: 5, Key: "publish_payload_envelope", Method: "POST", Path: "/eth/v1/beacon/execution_payload_envelope", Description: "Publish execution payload envelope (reveal)", SpecPR: "580", Kind: ProbeKindREST},
	{Number: 8, Key: "duties_ptc", Method: "POST", Path: "/eth/v1/validator/duties/ptc/{epoch}", Description: "PTC duties", SpecPR: "552/592", Kind: ProbeKindREST},
	{Number: 10, Key: "post_payload_attestations", Method: "POST", Path: "/eth/v1/beacon/pool/payload_attestations", Description: "Submit payload attestations", SpecPR: "552", Kind: ProbeKindREST},

	// SSE topics
	{Number: 12, Key: "sse_execution_payload_bid", Method: "SSE", Path: "execution_payload_bid", Description: "SSE: execution_payload_bid", SpecPR: "552/587", Kind: ProbeKindSSE},
	{Number: 13, Key: "sse_execution_payload_available", Method: "SSE", Path: "execution_payload_available", Description: "SSE: execution_payload_available", SpecPR: "552", Kind: ProbeKindSSE},
	{Number: 14, Key: "sse_payload_attestation_message", Method: "SSE", Path: "payload_attestation_message", Description: "SSE: payload_attestation_message", SpecPR: "552", Kind: ProbeKindSSE},
	{Number: 15, Key: "sse_execution_payload", Method: "SSE", Path: "execution_payload", Description: "SSE: execution_payload", SpecPR: "588", Kind: ProbeKindSSE},
	{Number: 16, Key: "sse_execution_payload_gossip", Method: "SSE", Path: "execution_payload_gossip", Description: "SSE: execution_payload_gossip", SpecPR: "588", Kind: ProbeKindSSE},
}

// ProbeContext carries the dynamic values needed to build a probe request.
type ProbeContext struct {
	Slot         uint64
	Epoch        uint64
	HeadRoot     string
	BuilderIndex uint64
	// SSEFiredWithin maps the SSE topic name (path) to a flag indicating that
	// the topic has fired within the configured staleness window. Topics not
	// present in the map are treated as "never fired".
	SSEFiredWithin map[string]bool
}

const (
	probeRESTTimeout = 5 * time.Second
	probeSSETimeout  = 2500 * time.Millisecond
)

// ProbeAllEndpoints probes every endpoint in EndpointCatalog and returns the
// classified results keyed by EndpointInfo.Key.
func (bc *BeaconClient) ProbeAllEndpoints(ctx context.Context, probeCtx ProbeContext) map[string]ProbeResult {
	results := make(map[string]ProbeResult, len(EndpointCatalog))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, ep := range EndpointCatalog {
		wg.Add(1)
		go func(ep EndpointInfo) {
			defer wg.Done()
			res := bc.probeEndpoint(ctx, ep, probeCtx)
			mu.Lock()
			results[ep.Key] = res
			mu.Unlock()
		}(ep)
	}

	wg.Wait()
	return results
}

func (bc *BeaconClient) probeEndpoint(ctx context.Context, ep EndpointInfo, probeCtx ProbeContext) ProbeResult {
	now := time.Now()

	switch ep.Kind {
	case ProbeKindSSE:
		return bc.probeSSE(ctx, ep, probeCtx, now)
	case ProbeKindREST:
		return bc.probeREST(ctx, ep, probeCtx, now)
	default:
		return ProbeResult{Status: ProbeStatusUnknown, LastChecked: now, Detail: "unknown probe kind"}
	}
}

func (bc *BeaconClient) probeREST(ctx context.Context, ep EndpointInfo, probeCtx ProbeContext, now time.Time) ProbeResult {
	reqCtx, cancel := context.WithTimeout(ctx, probeRESTTimeout)
	defer cancel()

	url := bc.endpoint + bc.fillPath(ep.Path, probeCtx)

	var (
		req         *nethttp.Request
		err         error
		extraHeader map[string]string
	)

	switch ep.Method {
	case "GET":
		req, err = nethttp.NewRequestWithContext(reqCtx, "GET", url, nethttp.NoBody)
	case "POST":
		body, headers := bc.bodyForPost(ep.Key, probeCtx)
		extraHeader = headers
		req, err = nethttp.NewRequestWithContext(reqCtx, "POST", url, bytes.NewReader(body))
		if req != nil {
			req.Header.Set("Content-Type", "application/json")
		}
	default:
		return ProbeResult{Status: ProbeStatusUnknown, LastChecked: now, Detail: "unsupported method"}
	}

	if err != nil {
		return ProbeResult{Status: ProbeStatusError, LastChecked: now, Detail: err.Error()}
	}

	for k, v := range bc.headers {
		req.Header.Set(k, v)
	}
	for k, v := range extraHeader {
		req.Header.Set(k, v)
	}

	client := &nethttp.Client{Timeout: probeRESTTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return ProbeResult{Status: ProbeStatusError, LastChecked: now, Detail: err.Error()}
	}
	defer resp.Body.Close()

	var detail string
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if len(body) > 0 {
			detail = strings.TrimSpace(string(body))
		}
	}

	return ProbeResult{
		Status:      classifyRESTStatus(resp.StatusCode),
		StatusCode:  resp.StatusCode,
		LastChecked: now,
		Detail:      detail,
	}
}

func (bc *BeaconClient) probeSSE(ctx context.Context, ep EndpointInfo, probeCtx ProbeContext, now time.Time) ProbeResult {
	reqCtx, cancel := context.WithTimeout(ctx, probeSSETimeout)
	defer cancel()

	url := fmt.Sprintf("%s/eth/v1/events?topics=%s", bc.endpoint, ep.Path)
	req, err := nethttp.NewRequestWithContext(reqCtx, "GET", url, nethttp.NoBody)
	if err != nil {
		return ProbeResult{Status: ProbeStatusError, LastChecked: now, Detail: err.Error()}
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range bc.headers {
		req.Header.Set(k, v)
	}

	client := &nethttp.Client{Timeout: probeSSETimeout}
	resp, err := client.Do(req)
	if err != nil {
		// A context-deadline error after a successful connect would mean the
		// stream opened and just had nothing to send within the probe window.
		// That's fine; we'll detect "no events" via SSEFiredWithin instead.
		// Treat plain transport errors as unknown.
		if reqCtx.Err() == context.DeadlineExceeded {
			return bc.classifySSEFiring(ep, probeCtx, 0, now)
		}
		return ProbeResult{Status: ProbeStatusError, LastChecked: now, Detail: err.Error()}
	}
	defer resp.Body.Close()

	// Drain a tiny bit so the server can flush headers cleanly.
	io.CopyN(io.Discard, resp.Body, 64)

	if resp.StatusCode == nethttp.StatusNotFound || resp.StatusCode == nethttp.StatusNotImplemented {
		return ProbeResult{Status: ProbeStatusMissing, StatusCode: resp.StatusCode, LastChecked: now}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ProbeResult{Status: ProbeStatusError, StatusCode: resp.StatusCode, LastChecked: now}
	}

	return bc.classifySSEFiring(ep, probeCtx, resp.StatusCode, now)
}

func (bc *BeaconClient) classifySSEFiring(ep EndpointInfo, probeCtx ProbeContext, statusCode int, now time.Time) ProbeResult {
	if probeCtx.SSEFiredWithin != nil && probeCtx.SSEFiredWithin[ep.Path] {
		return ProbeResult{Status: ProbeStatusOK, StatusCode: statusCode, LastChecked: now}
	}

	// Subscription accepted, but we haven't seen a matching event within the
	// configured window. Report this as a yellow status so the UI can
	// distinguish "topic exists but never fires" from "topic missing".
	return ProbeResult{
		Status:      ProbeStatusAcceptedNoEvents,
		StatusCode:  statusCode,
		LastChecked: now,
		Detail:      "subscription accepted but no events observed within 1 epoch",
	}
}

func (bc *BeaconClient) fillPath(path string, probeCtx ProbeContext) string {
	r := strings.NewReplacer(
		"{slot}", fmt.Sprintf("%d", probeCtx.Slot),
		"{epoch}", fmt.Sprintf("%d", probeCtx.Epoch),
		"{builder_index}", fmt.Sprintf("%d", probeCtx.BuilderIndex),
		"{block_id}", "head",
	)
	return r.Replace(path)
}

// dummyBLSSignature is 96 bytes of 0xff - the wire shape of a BLS signature
// that will trivially fail verification but pass length / hex parsing.
const dummyBLSSignature = "0x" +
	"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" +
	"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" +
	"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

// zeroRoot is a 32-byte all-zero hash (well-formed but obviously fake).
const zeroRoot = "0x0000000000000000000000000000000000000000000000000000000000000000"

// zeroAddr is a 20-byte all-zero Ethereum address.
const zeroAddr = "0x0000000000000000000000000000000000000000"

// bodyForPost returns a probe body shaped like a real signed request for the
// given POST endpoint. Bodies are crafted to pass JSON deserialisation and
// reach the handler's BLS-verification step, where they fail safely (dummy
// signature, zero proposer, malformed body content). This gives a deeper signal
// than `{}` while remaining a no-op against the network.
func (bc *BeaconClient) bodyForPost(key string, probeCtx ProbeContext) ([]byte, map[string]string) {
	slot := probeCtx.Slot
	epoch := probeCtx.Epoch
	_ = epoch

	switch key {
	case "publish_block_v2":
		body := fmt.Sprintf(`{
		"message": {
			"slot": "%d",
			"proposer_index": "0",
			"parent_root": "%s",
			"state_root": "%s",
			"body": {}
		},
		"signature": "%s"
	}`, slot, zeroRoot, zeroRoot, dummyBLSSignature)
		return []byte(body), map[string]string{"Eth-Consensus-Version": "gloas"}

	case "publish_payload_bid":
		body := fmt.Sprintf(`{
		"message": {
			"parent_block_hash": "%s",
			"parent_block_root": "%s",
			"block_hash": "%s",
			"prev_randao": "%s",
			"fee_recipient": "%s",
			"gas_limit": "30000000",
			"builder_index": "0",
			"slot": "%d",
			"value": "0",
			"execution_payment": "0",
			"blob_kzg_commitments": [],
			"execution_requests_root": "%s"
		},
		"signature": "%s"
	}`, zeroRoot, zeroRoot, zeroRoot, zeroRoot, zeroAddr, slot, zeroRoot, dummyBLSSignature)
		return []byte(body), nil

	case "publish_payload_envelope":
		body := fmt.Sprintf(`{
		"message": {
			"payload": null,
			"execution_requests": null,
			"builder_index": "0",
			"beacon_block_root": "%s",
			"parent_beacon_block_root": "%s"
		},
		"signature": "%s"
	}`, zeroRoot, zeroRoot, dummyBLSSignature)
		return []byte(body), nil

	case "duties_ptc":
		// PTC duties endpoint takes a list of validator indices to request
		// duties for. A single index is enough to reach the handler.
		return []byte(`["0"]`), nil

	case "post_payload_attestations":
		body := fmt.Sprintf(`[{
		"validator_index": "0",
		"data": {
			"beacon_block_root": "%s",
			"slot": "%d",
			"payload_present": false,
			"blob_data_available": false
		},
		"signature": "%s"
	}]`, zeroRoot, slot, dummyBLSSignature)
		return []byte(body), nil

	default:
		return []byte(`{}`), nil
	}
}

// RecordSSETopicFired stamps the given topic with the current time. Called by
// the BeaconStream goroutines when an SSE event of that topic arrives.
func (bc *BeaconClient) RecordSSETopicFired(topic string) {
	bc.sseFiredMu.Lock()
	defer bc.sseFiredMu.Unlock()
	if bc.sseFiredAt == nil {
		bc.sseFiredAt = make(map[string]time.Time, 6)
	}
	bc.sseFiredAt[topic] = time.Now()
}

// GetSSEFiredWithin returns a snapshot of which SSE topics have fired within
// the staleness window.
func (bc *BeaconClient) GetSSEFiredWithin(within time.Duration) map[string]bool {
	bc.sseFiredMu.RLock()
	defer bc.sseFiredMu.RUnlock()

	cutoff := time.Now().Add(-within)
	out := make(map[string]bool, len(bc.sseFiredAt))
	for topic, when := range bc.sseFiredAt {
		out[topic] = when.After(cutoff)
	}
	return out
}

func classifyRESTStatus(code int) ProbeStatus {
	switch {
	case code == nethttp.StatusNotFound,
		code == nethttp.StatusNotImplemented:
		return ProbeStatusMissing
	case code >= 200 && code < 300:
		// Direct success means handler exists and accepted our skinny request.
		return ProbeStatusOK
	case code == nethttp.StatusBadRequest,
		code == nethttp.StatusUnprocessableEntity,
		code == nethttp.StatusUnsupportedMediaType,
		code == nethttp.StatusMethodNotAllowed:
		// Handler exists and rejected the probe payload as malformed - this
		// is the expected outcome for POSTs because we send {} or [].
		return ProbeStatusOK
	case code >= 500:
		return ProbeStatusError
	default:
		return ProbeStatusUnknown
	}
}
