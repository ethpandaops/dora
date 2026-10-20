package handlers

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/dora/cache"
	"github.com/ethpandaops/dora/services"
	"github.com/ethpandaops/dora/types"
	"github.com/ethpandaops/dora/types/models"
	"github.com/ethpandaops/dora/utils"
)

// TestCachedPageValuesSszCompatible covers the values handlers pass to the
// frontend cache that are not page model structs of the models package: the
// handler- and service-local response types and the bare slices cached by the
// submit pages. The tiered cache counts a type as SSZ incompatible when it has
// to fall back to JSON, which is what this test asserts against.
func TestCachedPageValuesSszCompatible(t *testing.T) {
	utils.Config = &types.Config{}

	tests := []struct {
		name  string
		value any
	}{
		{name: "address_balances", value: &AddressBalancesResponse{}},
		{name: "search_resolver", value: &searchResolverResult{}},
		{name: "search_ahead", value: &searchAheadCached{}},
		{name: "slot_inclusionlists", value: &services.SlotInclusionListsView{}},
		{name: "block_redirect", value: &[]byte{}},
		{name: "submit_deposit_deposits", value: &models.SubmitDepositPageDataDeposits{}},
		{name: "submit_deposit_validators", value: &[]models.SubmitDepositPageDataValidator{}},
		{name: "submit_withdrawal_validators", value: &[]models.SubmitWithdrawalPageDataValidator{}},
		{name: "submit_consolidation_validators", value: &[]models.SubmitConsolidationPageDataValidator{}},
		{name: "submit_builder_exit_builders", value: &[]models.SubmitBuilderExitPageDataBuilder{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tieredCache, err := cache.NewTieredCache(1, "", "test", logrus.New())
			require.NoError(t, err)

			require.NoError(t, tieredCache.Set(tt.name+":1", tt.value, time.Hour))

			stats := tieredCache.GetStats()
			require.Equal(t, 0, stats.SSZIncompatTypes, "cached value is not ssz compatible")
			require.Equal(t, 1, stats.SSZCompatTypes)
		})
	}
}

// TestSearchAheadCachedRoundTrip checks that a search-ahead result survives
// the frontend cache: a cache hit must serve the same entries as the build.
func TestSearchAheadCachedRoundTrip(t *testing.T) {
	utils.Config = &types.Config{}

	tieredCache, err := cache.NewTieredCache(1, "", "test", logrus.New())
	require.NoError(t, err)

	in := &searchAheadCached{
		Slots: []*models.SearchAheadSlotsResult{{Slot: "42", Orphaned: true}},
	}
	require.NoError(t, tieredCache.Set("search_ahead:slots:42", in, time.Hour))

	out := &searchAheadCached{}
	_, err = tieredCache.Get("search_ahead:slots:42", out)
	require.NoError(t, err)

	slots, ok := out.result("slots").([]*models.SearchAheadSlotsResult)
	require.True(t, ok)
	require.Len(t, slots, 1)
	require.Equal(t, "42", slots[0].Slot)
	require.True(t, slots[0].Orphaned)
	require.Empty(t, out.Epochs)
}
