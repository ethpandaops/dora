package services

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/utils"
)

// newStaleTestResolver builds a resolver with a local and a remote network and the
// default refresh intervals, without starting the worker.
func newStaleTestResolver(t *testing.T) *EnsResolver {
	t.Helper()

	utils.Config = &types.Config{}
	utils.Config.EnsResolver.RefreshPositive = 24 * time.Hour
	utils.Config.EnsResolver.RefreshNegative = 6 * time.Hour

	return &EnsResolver{
		networks: []*ensNetwork{
			{key: "", name: "local", local: true},
			{key: "remote", name: "remote"},
		},
	}
}

func TestEnsResolverIsStale(t *testing.T) {
	now := time.Now().Unix()
	addr := []byte{0x01}

	tests := []struct {
		name          string
		rows          []*dbtypes.EnsName
		remoteFailing bool
		want          bool
	}{
		{
			name: "fresh results on all networks are not stale",
			rows: []*dbtypes.EnsName{
				{Address: addr, Network: "", Name: "a.eth", ResolvedTime: now},
				{Address: addr, Network: "remote", Name: "b.eth", ResolvedTime: now},
			},
			want: false,
		},
		{
			name: "missing result on an available network is stale",
			rows: []*dbtypes.EnsName{
				{Address: addr, Network: "", Name: "a.eth", ResolvedTime: now},
			},
			want: true,
		},
		{
			name: "outdated positive result is stale",
			rows: []*dbtypes.EnsName{
				{Address: addr, Network: "", Name: "a.eth", ResolvedTime: now - int64((25 * time.Hour).Seconds())},
				{Address: addr, Network: "remote", Name: "b.eth", ResolvedTime: now},
			},
			want: true,
		},
		{
			name: "negative result refreshes on the shorter interval",
			rows: []*dbtypes.EnsName{
				{Address: addr, Network: "", Name: "", ResolvedTime: now - int64((7 * time.Hour).Seconds())},
				{Address: addr, Network: "remote", Name: "b.eth", ResolvedTime: now},
			},
			want: true,
		},
		{
			// regression: a network stuck in error backoff must not keep every viewed
			// address in an endless re-resolve loop
			name: "missing result on a failing network is not stale",
			rows: []*dbtypes.EnsName{
				{Address: addr, Network: "", Name: "a.eth", ResolvedTime: now},
			},
			remoteFailing: true,
			want:          false,
		},
		{
			name: "outdated result on a failing network is not stale",
			rows: []*dbtypes.EnsName{
				{Address: addr, Network: "", Name: "a.eth", ResolvedTime: now},
				{Address: addr, Network: "remote", Name: "b.eth", ResolvedTime: now - int64((72 * time.Hour).Seconds())},
			},
			remoteFailing: true,
			want:          false,
		},
		{
			name: "outdated local result is stale even while the remote is failing",
			rows: []*dbtypes.EnsName{
				{Address: addr, Network: "", Name: "a.eth", ResolvedTime: now - int64((25 * time.Hour).Seconds())},
			},
			remoteFailing: true,
			want:          true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := newStaleTestResolver(t)
			if test.remoteFailing {
				resolver.networks[1].markFailed(now)
			}

			entry := resolver.cacheEntryFromRows(test.rows)
			require.Equal(t, test.want, resolver.isStale(entry, now))
		})
	}
}

// TestEnsResolverStaleAfterRecovery verifies that addresses which settled while a
// network was failing are re-resolved once that network answers again.
func TestEnsResolverStaleAfterRecovery(t *testing.T) {
	now := time.Now().Unix()
	addr := []byte{0x01}

	resolver := newStaleTestResolver(t)
	remote := resolver.networks[1]
	remote.markFailed(now)

	entry := resolver.cacheEntryFromRows([]*dbtypes.EnsName{
		{Address: addr, Network: "", Name: "a.eth", ResolvedTime: now},
	})
	require.False(t, resolver.isStale(entry, now), "address must settle while the remote is down")

	remote.markAvailable()
	require.True(t, resolver.isStale(entry, now), "address must be re-resolved once the remote recovers")
}

// TestEnsNetworkBackoff verifies the error backoff escalates per consecutive failure,
// stays capped and resets on success.
func TestEnsNetworkBackoff(t *testing.T) {
	now := time.Now().Unix()
	network := &ensNetwork{key: "remote", name: "remote"}

	require.True(t, network.isAvailable(now), "a network without failures is available")

	require.Equal(t, ensNetworkBackoff, network.markFailed(now))
	require.False(t, network.isAvailable(now))
	require.True(t, network.isAvailable(now+int64(ensNetworkBackoff.Seconds())), "available again after the backoff")

	require.Equal(t, 2*ensNetworkBackoff, network.markFailed(now), "backoff doubles per failure")

	for i := 0; i < 20; i++ {
		require.LessOrEqual(t, network.markFailed(now), ensNetworkBackoffMax, "backoff stays capped")
	}

	network.markAvailable()
	require.True(t, network.isAvailable(now))
	require.Equal(t, ensNetworkBackoff, network.markFailed(now), "backoff restarts after a success")
}
