package consensus

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/ethpandaops/dora/clients/consensus/rpc"
)

// SSEStalenessWindow controls how recently an SSE topic must have fired to be
// classified as "ok" instead of "accepted_no_events". Roughly one epoch on
// mainnet.
const SSEStalenessWindow = 7 * time.Minute

// GetEndpointStatuses returns a snapshot of the most recent per-endpoint probe
// results for this client.
func (client *Client) GetEndpointStatuses() map[string]rpc.ProbeResult {
	client.endpointStatusMu.RLock()
	defer client.endpointStatusMu.RUnlock()

	if client.endpointStatuses == nil {
		return nil
	}
	out := make(map[string]rpc.ProbeResult, len(client.endpointStatuses))
	for k, v := range client.endpointStatuses {
		out[k] = v
	}
	return out
}

// runEndpointProbes drives one round of beacon-API endpoint probes against
// this client and stashes the results for the UI to read.
func (client *Client) runEndpointProbes(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			client.logger.WithField("stack", string(debug.Stack())).
				Errorf("panic in runEndpointProbes: %v", rec)
		}
	}()

	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	chainState := client.pool.chainState
	currentSlot := chainState.CurrentSlot()
	currentEpoch := chainState.CurrentEpoch()

	headSlot, headRoot := client.GetLastHead()
	headRootHex := fmt.Sprintf("0x%x", headRoot[:])
	if headSlot == 0 {
		headRootHex = "head"
	}

	pc := rpc.ProbeContext{
		Slot:           uint64(currentSlot),
		Epoch:          uint64(currentEpoch),
		HeadRoot:       headRootHex,
		BuilderIndex:   0,
		SSEFiredWithin: client.rpcClient.GetSSEFiredWithin(SSEStalenessWindow),
	}

	results := client.rpcClient.ProbeAllEndpoints(probeCtx, pc)

	client.endpointStatusMu.Lock()
	client.endpointStatuses = results
	client.lastEndpointCheckEpoch = currentEpoch
	client.endpointStatusMu.Unlock()

	client.logger.Debugf("endpoint probe complete: %d endpoints, epoch %d", len(results), currentEpoch)
}
