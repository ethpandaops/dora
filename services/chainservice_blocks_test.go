package services

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/dora/utils"
)

// TestRaceBlockLoad covers which source wins the node-vs-block-db race for the
// configured delays and what the caller learns about the block db's answer.
func TestRaceBlockLoad(t *testing.T) {
	nodeBlock := &CombinedBlockResponse{Root: phase0.Root{1}}
	dbBlock := &CombinedBlockResponse{Root: phase0.Root{2}}
	dbErr := errors.New("db unavailable")

	// answer returns a source that replies with res after d, or as soon as the
	// race cancels it. calls counts how often the source was asked.
	answer := func(res *CombinedBlockResponse, d time.Duration, calls *atomic.Int32) func(context.Context) (*CombinedBlockResponse, bool) {
		return func(ctx context.Context) (*CombinedBlockResponse, bool) {
			calls.Add(1)
			select {
			case <-time.After(d):
				return res, false
			case <-ctx.Done():
				return nil, true
			}
		}
	}

	delayOf := func(d time.Duration) *time.Duration { return &d }

	tests := []struct {
		name        string
		delay       *time.Duration
		nodeRes     *CombinedBlockResponse
		nodeDelay   time.Duration
		blockDb     bool
		dbRes       *CombinedBlockResponse
		dbErr       error
		dbDelay     time.Duration
		wantRes     *CombinedBlockResponse
		wantDbDone  bool
		wantErr     error
		wantDbCalls int
	}{
		{
			name:        "sequential when delay is unset",
			delay:       nil,
			nodeRes:     nil,
			blockDb:     true,
			dbRes:       dbBlock,
			wantRes:     nil,
			wantDbDone:  false,
			wantDbCalls: 0,
		},
		{
			name:        "sequential when no block db loader",
			delay:       delayOf(0),
			nodeRes:     nodeBlock,
			blockDb:     false,
			wantRes:     nodeBlock,
			wantDbDone:  false,
			wantDbCalls: 0,
		},
		{
			name:        "nodes win before the delay expires",
			delay:       delayOf(200 * time.Millisecond),
			nodeRes:     nodeBlock,
			nodeDelay:   10 * time.Millisecond,
			blockDb:     true,
			dbRes:       dbBlock,
			wantRes:     nodeBlock,
			wantDbDone:  false,
			wantDbCalls: 0,
		},
		{
			name:        "block db wins when nodes are slow",
			delay:       delayOf(0),
			nodeRes:     nodeBlock,
			nodeDelay:   2 * time.Second,
			blockDb:     true,
			dbRes:       dbBlock,
			dbDelay:     10 * time.Millisecond,
			wantRes:     dbBlock,
			wantDbDone:  true,
			wantDbCalls: 1,
		},
		{
			name:        "block db is asked at once when nodes give up before the delay",
			delay:       delayOf(2 * time.Second),
			nodeRes:     nil,
			nodeDelay:   10 * time.Millisecond,
			blockDb:     true,
			dbRes:       dbBlock,
			dbDelay:     10 * time.Millisecond,
			wantRes:     dbBlock,
			wantDbDone:  true,
			wantDbCalls: 1,
		},
		{
			name:        "nodes win while block db is still loading",
			delay:       delayOf(0),
			nodeRes:     nodeBlock,
			nodeDelay:   10 * time.Millisecond,
			blockDb:     true,
			dbRes:       dbBlock,
			dbDelay:     2 * time.Second,
			wantRes:     nodeBlock,
			wantDbDone:  false,
			wantDbCalls: 1,
		},
		{
			name:        "neither source has the block",
			delay:       delayOf(0),
			nodeRes:     nil,
			blockDb:     true,
			dbRes:       nil,
			dbErr:       dbErr,
			wantRes:     nil,
			wantDbDone:  true,
			wantErr:     dbErr,
			wantDbCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			utils.Config.BlockDb.ParallelLoadDelay = tt.delay

			var nodeCalls, dbCalls atomic.Int32
			nodeSource := answer(tt.nodeRes, tt.nodeDelay, &nodeCalls)
			dbSource := answer(tt.dbRes, tt.dbDelay, &dbCalls)

			loadFromClients := func(ctx context.Context) clientBlockLoad {
				res, _ := nodeSource(ctx)
				return clientBlockLoad{result: res}
			}
			var loadFromBlockDb func(ctx context.Context) (*CombinedBlockResponse, error)
			if tt.blockDb {
				loadFromBlockDb = func(ctx context.Context) (*CombinedBlockResponse, error) {
					res, cancelled := dbSource(ctx)
					if cancelled {
						return nil, ctx.Err()
					}
					return res, tt.dbErr
				}
			}

			start := time.Now()
			res, _, dbDone, err := raceBlockLoad(context.Background(), phase0.Root{}, loadFromClients, loadFromBlockDb)
			require.Less(t, time.Since(start), time.Second, "race must not wait for the losing source")

			require.Equal(t, tt.wantRes, res)
			require.Equal(t, tt.wantDbDone, dbDone)
			require.Equal(t, tt.wantErr, err)
			require.Equal(t, int32(1), nodeCalls.Load())
			require.Equal(t, int32(tt.wantDbCalls), dbCalls.Load())
		})
	}
}
