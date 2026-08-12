package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
)

// parseHexRoot parses a 0x-prefixed (or bare) 32-byte hex root.
func parseHexRoot(s string) (phase0.Root, error) {
	var root phase0.Root
	s = strings.TrimPrefix(s, "0x")
	if len(s) != 64 {
		return root, fmt.Errorf("invalid root length")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return root, err
	}
	copy(root[:], b)
	return root, nil
}

// parseDutiesFork resolves the optional fork selector shared by the duties and
// committees endpoints. It reads the query parameters:
//   - dependent_root: used directly as the committee-shuffling dependent root.
//   - block_root: the dependent root is resolved for the given epoch on the fork
//     that block sits on (via ResolveDependentRoot).
//
// It returns (depRoot, hasFork, ok). hasFork is false when neither parameter is
// present (the caller should fall back to canonical resolution). ok is false
// when a parameter was malformed and an error response has already been written.
func parseDutiesFork(ctx context.Context, w http.ResponseWriter, r *http.Request, epoch phase0.Epoch) (phase0.Root, bool, bool) {
	q := r.URL.Query()

	if drStr := q.Get("dependent_root"); drStr != "" {
		root, err := parseHexRoot(drStr)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid dependent_root parameter"}`, http.StatusBadRequest)
			return phase0.Root{}, false, false
		}
		return root, true, true
	}

	if brStr := q.Get("block_root"); brStr != "" {
		blockRoot, err := parseHexRoot(brStr)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid block_root parameter"}`, http.StatusBadRequest)
			return phase0.Root{}, false, false
		}
		// A broken chain yields the zero root, which the ForRoot services treat
		// as canonical - acceptable graceful degradation.
		depRoot, _ := services.GlobalBeaconService.ResolveDependentRoot(ctx, epoch, blockRoot)
		return depRoot, true, true
	}

	return phase0.Root{}, false, true
}
