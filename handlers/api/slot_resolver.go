package api

import (
	"context"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/services"
)

// resolveSlotOrHash parses a "slotOrHash" path parameter (slot number or 0x-prefixed
// 32-byte block root) and returns the matching canonical/orphaned slot record from
// the chain service. Returns nil when the value can't be parsed or no block is found,
// after writing an appropriate JSON error response.
func resolveSlotOrHash(ctx context.Context, w http.ResponseWriter, slotOrHash string) *dbtypes.Slot {
	var filter *dbtypes.BlockFilter

	if strings.HasPrefix(slotOrHash, "0x") && len(slotOrHash) == 66 {
		rootBytes, err := hex.DecodeString(strings.TrimPrefix(slotOrHash, "0x"))
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid root format"}`, http.StatusBadRequest)
			return nil
		}
		filter = &dbtypes.BlockFilter{
			BlockRoot:    rootBytes,
			WithOrphaned: 1,
			WithMissing:  0,
		}
	} else {
		slot, err := strconv.ParseUint(slotOrHash, 10, 64)
		if err != nil {
			http.Error(w, `{"status": "ERROR: invalid slot number or root format"}`, http.StatusBadRequest)
			return nil
		}
		filter = &dbtypes.BlockFilter{
			Slot:         &slot,
			WithOrphaned: 1,
			WithMissing:  0,
		}
	}

	assignedSlots := services.GlobalBeaconService.GetDbBlocksByFilter(ctx, filter, 0, 1, 0)
	if len(assignedSlots) == 0 || assignedSlots[0].Block == nil {
		http.Error(w, `{"status": "ERROR: slot not found"}`, http.StatusNotFound)
		return nil
	}

	return assignedSlots[0].Block
}
