package replay

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// blockIDPaths are the beacon API paths whose first variable segment identifies a
// block, and stateIDPaths the ones whose first variable segment identifies a state.
// Everything else is proxied unchanged.
var (
	blockIDPaths = []string{
		"/eth/v1/beacon/headers/",
		"/eth/v1/beacon/blocks/",
		"/eth/v2/beacon/blocks/",
		"/eth/v1/beacon/blinded_blocks/",
		"/eth/v2/beacon/blinded_blocks/",
		"/eth/v1/beacon/blob_sidecars/",
		"/eth/v1/beacon/blobs/",
		"/eth/v1/beacon/execution_payload_envelopes/",
		"/eth/v1/beacon/execution_proofs/",
		"/eth/v1/beacon/inclusion_lists/",
	}

	stateIDPaths = []string{
		"/eth/v1/beacon/states/",
		"/eth/v2/beacon/states/",
		"/eth/v1/debug/beacon/states/",
		"/eth/v2/debug/beacon/states/",
	}
)

// clHandler is the fake beacon node. Everything is proxied to the real upstream except
// where the virtual head matters: the sync status is synthesized, the event stream is
// generated locally, `head` aliases resolve to the head at the virtual slot, and
// anything beyond the virtual slot is reported as missing.
func (r *Replay) clHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/eth/v1/node/syncing", r.serveSyncing)
	mux.HandleFunc("/eth/v1/events", r.events.serveHTTP)
	mux.HandleFunc("/eth/v1/beacon/states/head/finality_checkpoints", r.serveHeadFinality)
	mux.HandleFunc("/", r.serveProxied)

	return mux
}

func (r *Replay) serveSyncing(w http.ResponseWriter, _ *http.Request) {
	r.mutex.RLock()
	headSlot := uint64(0)
	if r.head != nil {
		headSlot = r.head.Slot
	}
	r.mutex.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"head_slot":     strconv.FormatUint(headSlot, 10),
			"sync_distance": "0",
			"is_syncing":    false,
			"is_optimistic": false,
			"el_offline":    false,
		},
	})
}

// serveHeadFinality answers from the finality the replay tracks rather than proxying.
// Reading finality upstream forces the node to load the head state, which for a
// replayed (historical) head is expensive at best and pruned away at worst.
func (r *Replay) serveHeadFinality(w http.ResponseWriter, _ *http.Request) {
	r.mutex.RLock()
	finality := r.finality
	r.mutex.RUnlock()

	if finality == nil || len(finality.Raw) == 0 {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(finality.Raw); err != nil {
		r.logger.WithError(err).Debug("error writing head finality response")
	}
}

func (r *Replay) serveProxied(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeAPIError(w, http.StatusNotImplemented, "the replay proxy only serves read requests")
		return
	}

	path, err := r.rewritePath(req.Context(), req.URL.Path)
	if err != nil {
		if err == errNotFound {
			writeAPIError(w, http.StatusNotFound, "not found")
			return
		}

		writeAPIError(w, http.StatusBadGateway, err.Error())

		return
	}

	if query := req.URL.RawQuery; query != "" {
		if err := r.checkQueryCutoff(req); err != nil {
			writeAPIError(w, http.StatusNotFound, "not found")
			return
		}

		path += "?" + query
	}

	// a full beacon state blocks the explorer for as long as it takes to arrive; the
	// driver waits for these rather than replaying slots the explorer cannot see yet
	if isStatePath(path) {
		defer r.states.begin()()
	}

	r.upstream.serve(w, req, path)
}

// rewritePath resolves the block/state identifier in a beacon API path against the
// virtual head, so the explorer can never see past the slot the replay has reached.
func (r *Replay) rewritePath(ctx context.Context, path string) (string, error) {
	for _, prefix := range blockIDPaths {
		if strings.HasPrefix(path, prefix) {
			return r.rewriteID(ctx, path, prefix, false)
		}
	}

	for _, prefix := range stateIDPaths {
		if strings.HasPrefix(path, prefix) {
			return r.rewriteID(ctx, path, prefix, true)
		}
	}

	return path, nil
}

func (r *Replay) rewriteID(ctx context.Context, path, prefix string, isState bool) (string, error) {
	rest := strings.TrimPrefix(path, prefix)

	id, suffix, _ := strings.Cut(rest, "/")
	if suffix != "" {
		suffix = "/" + suffix
	}

	resolved, err := r.resolveID(ctx, id, isState)
	if err != nil {
		return "", err
	}

	return prefix + resolved + suffix, nil
}

// resolveID maps a beacon API identifier to something the upstream can answer with the
// same meaning it had at the virtual head slot.
func (r *Replay) resolveID(ctx context.Context, id string, isState bool) (string, error) {
	switch id {
	case "head":
		header := r.currentHead()
		if header == nil {
			return "", errNotFound
		}

		if isState {
			return header.StateRoot, nil
		}

		return header.Root, nil

	case "finalized", "justified":
		return r.resolveCheckpointID(ctx, id, isState)

	case "genesis":
		return id, nil
	}

	if strings.HasPrefix(id, "0x") {
		// roots are only ever learned from artifacts the replay already released
		return id, nil
	}

	slot, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		// an identifier the replay does not understand is passed through unchanged
		return id, nil
	}

	r.mutex.RLock()
	virtualSlot := r.virtualSlot
	r.mutex.RUnlock()

	if slot > virtualSlot {
		return "", errNotFound
	}

	return id, nil
}

// resolveCheckpointID turns the `finalized` and `justified` aliases into the concrete
// roots the chain had at the virtual head, rather than the ones the upstream has today.
func (r *Replay) resolveCheckpointID(ctx context.Context, id string, isState bool) (string, error) {
	r.mutex.RLock()
	finality := r.finality
	r.mutex.RUnlock()

	if finality == nil {
		return "", errNotFound
	}

	root := finality.FinalizedRoot
	if id == "justified" {
		root = finality.JustifiedRoot
	}

	if !isState {
		return root, nil
	}

	header, err := r.upstream.headerByRoot(ctx, root)
	if err != nil || header == nil {
		return "", errNotFound
	}

	return header.StateRoot, nil
}

// checkQueryCutoff rejects queries that select data beyond the virtual head slot.
func (r *Replay) checkQueryCutoff(req *http.Request) error {
	slotParam := req.URL.Query().Get("slot")
	if slotParam == "" {
		return nil
	}

	slot, err := strconv.ParseUint(slotParam, 10, 64)
	if err != nil {
		return nil
	}

	r.mutex.RLock()
	virtualSlot := r.virtualSlot
	r.mutex.RUnlock()

	if slot > virtualSlot {
		return errNotFound
	}

	return nil
}

func (r *Replay) currentHead() *blockHeader {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	return r.head
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		return
	}
}

// writeAPIError answers in the shape the beacon API uses for errors, so clients report
// something meaningful instead of a decode failure.
func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"code":    status,
		"message": message,
	})
}

// parseRoot decodes a 0x-prefixed root for use in an event payload. A malformed root
// yields the zero root, which is what the upstream would have to have served for this
// to happen at all.
func parseRoot(value string) phase0.Root {
	root := phase0.Root{}

	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "0x"))
	if err != nil || len(decoded) != len(root) {
		return root
	}

	copy(root[:], decoded)

	return root
}
