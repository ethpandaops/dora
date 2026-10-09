package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/capella"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/holiman/uint256"

	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/utils"
)

// gloasBlockData builds a Gloas slot whose envelope payload hashes correctly and whose
// bid commits to bidHash (nil = the payload's own block hash).
func gloasBlockData(bidHash *phase0.Hash32) *services.CombinedBlockResponse {
	payload := &all.ExecutionPayload{
		Version:       spec.DataVersionGloas,
		ParentHash:    phase0.Hash32{0x01},
		BlockNumber:   42,
		GasLimit:      60_000_000,
		Timestamp:     1_700_000_000,
		BaseFeePerGas: uint256.NewInt(7),
		Withdrawals:   []*capella.Withdrawal{{Index: 1, ValidatorIndex: 2, Amount: 3}},
		SlotNumber:    5,
	}
	bal := []byte{0xc0}
	requests := &all.ExecutionRequests{Version: spec.DataVersionGloas}
	parentRoot := phase0.Root{0x06}
	payload.BlockHash = phase0.Hash32(utils.ExecutionBlockFromPayload(payload, parentRoot, requests, bal).Header.Hash())
	if bidHash == nil {
		bidHash = &payload.BlockHash
	}

	return &services.CombinedBlockResponse{
		Header: &phase0.SignedBeaconBlockHeader{Message: &phase0.BeaconBlockHeader{Slot: 5}},
		Block: &all.SignedBeaconBlock{Version: spec.DataVersionGloas, Message: &all.BeaconBlock{Body: &all.BeaconBlockBody{
			SignedExecutionPayloadBid: &all.SignedExecutionPayloadBid{Message: &all.ExecutionPayloadBid{BlockHash: *bidHash}},
		}}},
		Payload: &all.SignedExecutionPayloadEnvelope{Message: &all.ExecutionPayloadEnvelope{
			Payload: payload, ExecutionRequests: requests, ParentBeaconBlockRoot: parentRoot,
		}},
		BlockAccessList: bal,
	}
}

func TestBlockBodyDownloadsVerifyBlockHash(t *testing.T) {
	blockData := gloasBlockData(nil)
	blockHash := common.Hash(blockData.Payload.Message.Payload.BlockHash)

	w := httptest.NewRecorder()
	if err := handleBlockBodyDownload(w, blockData); err != nil {
		t.Fatalf("json download: %v", err)
	}
	var header types.Header
	if err := json.Unmarshal(w.Body.Bytes(), &header); err != nil || header.Hash() != blockHash {
		t.Fatalf("json download does not decode to the block header (err %v)", err)
	}

	w = httptest.NewRecorder()
	if err := handleBlockBodyRlpDownload(w, blockData); err != nil {
		t.Fatalf("rlp download: %v", err)
	}
	var block types.Block
	if err := rlp.DecodeBytes(w.Body.Bytes(), &block); err != nil || block.Hash() != blockHash {
		t.Fatalf("rlp download does not decode to the block (err %v)", err)
	}

	// A bid committing to another block hash, or no bid at all, must not be served.
	noBid := gloasBlockData(nil)
	noBid.Block.Message.Body.SignedExecutionPayloadBid = nil
	for name, tc := range map[string]struct {
		blockData *services.CombinedBlockResponse
		want      string
	}{
		"bid hash mismatch": {gloasBlockData(&phase0.Hash32{0xee}), "block hash 0xee00000000000000000000000000000000000000000000000000000000000000 in the execution payload bid"},
		"no bid":            {noBid, "no execution payload bid"},
	} {
		for download, handle := range map[string]func(*httptest.ResponseRecorder) error{
			"json": func(w *httptest.ResponseRecorder) error { return handleBlockBodyDownload(w, tc.blockData) },
			"rlp":  func(w *httptest.ResponseRecorder) error { return handleBlockBodyRlpDownload(w, tc.blockData) },
		} {
			w := httptest.NewRecorder()
			if err := handle(w); err == nil || !strings.Contains(err.Error(), tc.want) || w.Body.Len() != 0 {
				t.Errorf("%s %s: got %v (%d bytes written), want an error containing %q", name, download, err, w.Body.Len(), tc.want)
			}
		}
	}
}
