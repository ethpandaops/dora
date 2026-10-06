package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/jmoiron/sqlx"
	dynssz "github.com/pk910/dynamic-ssz"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// Exercise the HTTP handler and the real cache/database service against a fixed
// dataset. Cached blocks are restored through the normal unfinalized DB path.
func TestAPISlotsV1Pagination(t *testing.T) {
	setupSlotsPaginationService(t)
	cases := []struct {
		name, query string
		count       int
	}{
		{"cache only", "min_slot=96&max_slot=127", 33},
		{"database only", "min_slot=64&max_slot=95", 33},
		{"mixed cache and database", "min_slot=80&max_slot=111", 34},
		{"filtered mixed", "min_slot=80&max_slot=111&proposer=1", 17},
		{"missing slots", "min_slot=80&max_slot=111&with_missing=2", 7},
		{"exclude missing", "min_slot=80&max_slot=111&with_missing=0", 27},
		{"shared slot", "min_slot=94&max_slot=98&with_missing=0", 6},
		{"orphaned only", "min_slot=80&max_slot=111&with_orphaned=2", 2},
		{"exclude orphaned", "min_slot=80&max_slot=111&with_orphaned=0", 32},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getPage := func(page, limit int) *APISlotsData {
				rec := httptest.NewRecorder()
				APISlotsV1(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/slots?%s&page=%d&limit=%d", tc.query, page, limit), nil))
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				var response APISlotsResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
				require.Equal(t, "OK", response.Status)
				require.NotNil(t, response.Data)
				return response.Data
			}
			// A single complete response supplies an independent reference traversal.
			reference := getPage(0, 1000)
			require.Nil(t, reference.NextPage)
			require.Len(t, reference.Slots, tc.count)
			seen := make(map[string]bool)
			for _, slot := range reference.Slots {
				id := fmt.Sprintf("%d/%s", slot.Slot, slot.BlockRoot)
				require.False(t, seen[id], "duplicate identity in reference: %s", id)
				if tc.name == "orphaned only" {
					require.Equal(t, "Orphaned", slot.Status)
				}
				seen[id] = true
			}
			identities := func(slots []*APISlotListItem) []string {
				ids := make([]string, 0, len(slots))
				for _, s := range slots {
					ids = append(ids, fmt.Sprintf("%d/%s", s.Slot, s.BlockRoot))
				}
				return ids
			}
			for _, limit := range []int{1, 2, 3, 5, len(reference.Slots), len(reference.Slots) + 1} {
				t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
					var collected []*APISlotListItem
					for page := 0; page <= len(reference.Slots); page++ {
						result := getPage(page, limit)
						require.LessOrEqual(t, len(result.Slots), limit)
						collected = append(collected, result.Slots...)
						if result.NextPage == nil {
							break
						}
						require.Equal(t, uint64(page+1), *result.NextPage)
						require.Len(t, result.Slots, limit)
					}
					require.Equal(t, identities(reference.Slots), identities(collected))
					// Pages beyond the end must not repeat the tail of either source.
					require.Empty(t, getPage(len(reference.Slots)+1, limit).Slots)
				})
			}
		})
	}
}

func setupSlotsPaginationService(t *testing.T) {
	t.Helper()
	previousConfig, previousService := utils.Config, services.GlobalBeaconService
	cfg := &types.Config{}
	cfg.Indexer.DisableSynchronizer = true
	cfg.Indexer.DisableBlockDB = true
	cfg.KillSwitch.DisableSSZRequests = true
	cfg.KillSwitch.DisableSSZEncoding = true
	cfg.Frontend.ValidatorNamesRefreshInterval = time.Hour
	cfg.Frontend.ValidatorNamesResolveInterval = time.Hour
	cfg.Frontend.BuildoorRefreshInterval = time.Hour
	utils.Config = cfg
	services.GlobalBeaconService = nil
	ctx, cancel := context.WithCancel(context.Background())
	db.MustInitDB(&types.DatabaseConfig{Engine: "sqlite", Sqlite: &types.SqliteDatabaseConfig{File: filepath.Join(t.TempDir(), "slots.sqlite")}})
	require.NoError(t, db.ApplyEmbeddedDbSchema(-2))
	t.Cleanup(func() {
		cancel()
		services.GlobalBeaconService.StopService()
		db.MustCloseDB()
		utils.Config = previousConfig
		services.GlobalBeaconService = previousService
	})

	// Long slots keep the wall clock inside slot 127 throughout the test.
	genesis := time.Now().Add(-127*time.Hour - time.Minute).Unix()
	root := func(slot uint64, variant byte) phase0.Root { return phase0.Root{byte(slot), variant} }
	require.NoError(t, db.RunDBTransaction(func(tx *sqlx.Tx) error {
		for slot := uint64(64); slot <= 127; slot++ {
			if slot%5 == 0 {
				if slot < 96 {
					if err := db.InsertMissingSlot(ctx, tx, &dbtypes.MissedSlot{Slot: slot, Proposer: 1}); err != nil {
						return err
					}
				}
				continue
			}
			for _, variant := range []byte{1, 2} {
				if variant == 2 && slot != 94 && slot != 98 {
					continue
				}
				blockRoot := root(slot, variant)
				if slot < 96 {
					status := dbtypes.Canonical
					if variant == 2 {
						status = dbtypes.Orphaned
					}
					if err := db.InsertSlot(ctx, tx, &dbtypes.Slot{Slot: slot, Proposer: slot % 2, Root: blockRoot[:], StateRoot: blockRoot[:], Status: status, PayloadStatus: dbtypes.PayloadStatusMissing}); err != nil {
						return err
					}
				} else {
					header := &phase0.SignedBeaconBlockHeader{Message: &phase0.BeaconBlockHeader{Slot: phase0.Slot(slot), ProposerIndex: phase0.ValidatorIndex(slot % 2)}}
					headerSSZ, err := header.MarshalSSZ()
					if err != nil {
						return err
					}
					block := &all.SignedBeaconBlock{Version: spec.DataVersionPhase0, Message: &all.BeaconBlock{Slot: phase0.Slot(slot), ProposerIndex: phase0.ValidatorIndex(slot % 2), Body: &all.BeaconBlockBody{ETH1Data: &phase0.ETH1Data{BlockHash: make([]byte, 32)}}}}
					blockVer, blockSSZ, err := beacon.MarshalSignedBeaconBlockSSZ(dynssz.NewDynSsz(nil), block, false, true)
					if err != nil {
						return err
					}
					if err := db.InsertUnfinalizedBlock(ctx, tx, &dbtypes.UnfinalizedBlock{Root: blockRoot[:], Slot: slot, HeaderVer: 1, HeaderSSZ: headerSSZ, BlockVer: blockVer, BlockSSZ: blockSSZ, ForkId: uint64(variant)}); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}))
	ready := make(chan struct{})
	var readyOnce sync.Once
	zeroRoot := "0x" + strings.Repeat("00", 32)
	checkpointRoot := "0x01" + strings.Repeat("00", 31)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var data any
		switch r.URL.Path {
		case "/eth/v1/node/version":
			data = map[string]any{"version": "Lighthouse/test"}
		case "/eth/v1/node/identity":
			data = map[string]any{"peer_id": "test", "enr": "", "p2p_addresses": []string{}, "discovery_addresses": []string{}, "metadata": map[string]any{"seq_number": "0", "attnets": "0x0000000000000000", "syncnets": "0x00"}}
		case "/eth/v1/node/peers":
			data = []any{}
		case "/eth/v1/beacon/genesis":
			data = map[string]any{"genesis_time": fmt.Sprint(genesis), "genesis_validators_root": zeroRoot, "genesis_fork_version": "0x00000000"}
		case "/eth/v1/config/spec":
			data = map[string]any{"PRESET_BASE": "mainnet", "CONFIG_NAME": "pagination-test", "SLOTS_PER_EPOCH": "32", "SECONDS_PER_SLOT": "3600", "SLOT_DURATION_MS": "3600000", "GENESIS_FORK_VERSION": "0x00000000", "DEPOSIT_CHAIN_ID": "1", "DEPOSIT_NETWORK_ID": "1", "DEPOSIT_CONTRACT_ADDRESS": "0x" + strings.Repeat("00", 20)}
		case "/eth/v1/beacon/headers/head":
			readyOnce.Do(func() { close(ready) })
			// Keep client indexing idle; the fixed cache dataset was restored from DB.
			data = map[string]any{"root": zeroRoot, "canonical": true, "header": &phase0.SignedBeaconBlockHeader{Message: &phase0.BeaconBlockHeader{}}}
		case "/eth/v1/node/syncing":
			data = map[string]any{"head_slot": "127", "sync_distance": "0", "is_syncing": true, "is_optimistic": false}
		case "/eth/v1/beacon/states/head/finality_checkpoints":
			cp := map[string]any{"epoch": "3", "root": checkpointRoot}
			data = map[string]any{"previous_justified": cp, "current_justified": cp, "finalized": cp}
		default:
			http.Error(w, "no fixture for "+r.URL.Path, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(server.Close)
	cfg.BeaconApi.Endpoints = []types.EndpointConfig{{Url: server.URL, Name: "fixture", SkipValidators: true}}
	logger := logrus.New()
	logger.AddHook(&slotsPaginationReadyHook{ready: ready})
	services.InitChainService(ctx, logger)
	require.NoError(t, services.GlobalBeaconService.StartService())
	finalized, _ := services.GlobalBeaconService.GetBeaconIndexer().GetBlockCacheState()
	require.Equal(t, phase0.Epoch(3), finalized)
	require.NotNil(t, services.GlobalBeaconService.GetBeaconIndexer().GetBlockByRoot(root(98, 2)))
}

// StartService polls currently unlocked chain-state getters while the client
// initializes. Synchronize the fixture at the last point before that poll so
// this pagination test does not exercise unrelated startup races. Requesting
// the head happens after the client has published genesis, specs and finality.
type slotsPaginationReadyHook struct{ ready <-chan struct{} }

func (h *slotsPaginationReadyHook) Levels() []logrus.Level { return []logrus.Level{logrus.InfoLevel} }
func (h *slotsPaginationReadyHook) Fire(e *logrus.Entry) error {
	if e.Message == "Blockdb disabled" {
		select {
		case <-h.ready:
		case <-time.After(10 * time.Second):
			return fmt.Errorf("fixture client did not initialize")
		}
	}
	return nil
}
