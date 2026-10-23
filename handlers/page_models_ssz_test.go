package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// pageModelsDir is the package holding the page models relative to this package.
const pageModelsDir = "../types/models"

// pageModelSuffixes name the top-level page models of the models package: the
// values handlers hand to the frontend cache. Nested row and section types are
// validated through their parents.
var pageModelSuffixes = []string{"PageData", "Response"}

// cachedPageValues lists every value type handlers hand to the frontend cache:
// the top-level page models of the models package, the handler- and
// service-local response types and the bare slices cached by the submit pages.
// The cache serializes them with SSZ and silently falls back to JSON for types
// that are not SSZ compatible, so every one is checked here.
// TestCachedPageValuesRegistered fails when a page model is added to the models
// package but not to this list.
var cachedPageValues = []any{
	&models.AddressPageData{},
	&models.BlobsPageData{},
	&models.BlocksFilteredPageData{},
	&models.BlocksPageData{},
	&models.BuilderDepositsPageData{},
	&models.BuilderExitsPageData{},
	&models.BuilderPageData{},
	&models.BuildersPageData{},
	&models.ChainForksDiagramData{},
	&models.ChainForksPageData{},
	&models.ClientsCLPageData{},
	&models.ClientsELPageData{},
	&models.ConsolidationsPageData{},
	&models.DepositsPageData{},
	&models.ElConsolidationsPageData{},
	&models.ElWithdrawalsPageData{},
	&models.EpochArrivalResponse{},
	&models.EpochPageData{},
	&models.EpochsPageData{},
	&models.ErrorPageData{},
	&models.ExitsPageData{},
	&models.ForksPageData{},
	&models.IncludedDepositsPageData{},
	&models.IndexPageData{},
	&models.InitiatedDepositsPageData{},
	&models.MevBlocksPageData{},
	&models.QueuedConsolidationsPageData{},
	&models.QueuedDepositsPageData{},
	&models.QueuedWithdrawalsPageData{},
	&models.SlashingsPageData{},
	&models.SlotArrivalResponse{},
	&models.SlotBidSeenResponse{},
	&models.SlotBlockAccessListResponse{},
	&models.SlotDutiesResponse{},
	&models.SlotExecutionProofsResponse{},
	&models.SlotPageData{},
	&models.SlotsFilteredPageData{},
	&models.SlotsPageData{},
	&models.SlotWavesResponse{},
	&models.SubmitBuilderDepositPageData{},
	&models.SubmitBuilderExitPageData{},
	&models.SubmitConsolidationPageData{},
	&models.SubmitDepositPageData{},
	&models.SubmitWithdrawalPageData{},
	&models.TokenPageData{},
	&models.TokensPageData{},
	&models.TracoorBeaconBlockResponse{},
	&models.TracoorBeaconStateResponse{},
	&models.TracoorExecutionBlockTraceResponse{},
	&models.TransactionPageData{},
	&models.TransactionsPageData{},
	&models.TransfersPageData{},
	&models.ValidatorPageData{},
	&models.ValidatorsActivityPageData{},
	&models.ValidatorSlotsPageData{},
	&models.ValidatorsOfflinePageData{},
	&models.ValidatorsPageData{},
	&models.ValidatorsSummaryPageData{},
	&models.ValidatorsWithdrawalDashboardPageData{},
	&models.VoluntaryExitsPageData{},
	&models.WithdrawalsListPageData{},
	&models.WithdrawalsPageData{},
	&AddressBalancesResponse{},
	&searchResolverResult{},
	&searchAheadCached{},
	&services.SlotInclusionListsView{},
	&[]byte{},
	&models.SubmitDepositPageDataDeposits{},
	&[]models.SubmitDepositPageDataValidator{},
	&[]models.SubmitWithdrawalPageDataValidator{},
	&[]models.SubmitConsolidationPageDataValidator{},
	&[]models.SubmitBuilderExitPageDataBuilder{},
}

// TestCachedPageValuesRegistered fails when the models package declares a
// top-level page model that is missing from cachedPageValues.
func TestCachedPageValuesRegistered(t *testing.T) {
	declared := exportedStructNames(t, pageModelsDir)

	registered := make(map[string]struct{}, len(cachedPageValues))
	for _, value := range cachedPageValues {
		registered[reflect.TypeOf(value).Elem().Name()] = struct{}{}
	}

	missing := make([]string, 0)
	for name := range declared {
		if _, ok := registered[name]; !ok && isPageModelName(name) {
			missing = append(missing, name)
		}
	}

	require.Empty(t, missing, "page models not registered in cachedPageValues: %s", strings.Join(missing, ", "))
}

// TestCachedPageValuesSszCompatible checks that every cached value is stored
// by the frontend cache as SSZ (the cache counts a type as incompatible when it
// has to fall back to JSON), survives a round trip, and declares every pointer
// field reachable from it as an SSZ optional.
func TestCachedPageValuesSszCompatible(t *testing.T) {
	utils.Config = &types.Config{}

	for _, value := range cachedPageValues {
		valueType := reflect.TypeOf(value)

		t.Run(cachedValueName(valueType), func(t *testing.T) {
			tieredCache, err := cache.NewTieredCache(1, "", "test", logrus.New())
			require.NoError(t, err)

			key := "test:" + cachedValueName(valueType)
			require.NoError(t, tieredCache.Set(key, value, time.Hour))

			stats := tieredCache.GetStats()
			require.Equal(t, 0, stats.SSZIncompatTypes, "cached value is not ssz compatible")
			require.Equal(t, 1, stats.SSZCompatTypes)

			decoded := reflect.New(valueType.Elem()).Interface()
			_, err = tieredCache.Get(key, decoded)
			require.NoError(t, err, "round trip")

			// nil pointers carry meaning in page models (absent section, no
			// measurement); without the optional tag a nil pointer comes back
			// from the cache as a pointer to zero
			requireOptionalPointers(t, valueType, cachedValueName(valueType), make(map[reflect.Type]struct{}, 32))
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

func cachedValueName(valueType reflect.Type) string {
	if name := valueType.Elem().Name(); name != "" {
		return name
	}

	return valueType.Elem().String()
}

func isPageModelName(name string) bool {
	for _, suffix := range pageModelSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}

	return false
}

// requireOptionalPointers walks the types reachable from typ and fails on a
// pointer-typed struct field that is not tagged ssz-type:"optional".
func requireOptionalPointers(t *testing.T, typ reflect.Type, path string, visited map[reflect.Type]struct{}) {
	t.Helper()

	switch typ.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Array:
		requireOptionalPointers(t, typ.Elem(), path, visited)
	case reflect.Struct:
		if _, ok := visited[typ]; ok {
			return
		}

		visited[typ] = struct{}{}

		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if !field.IsExported() {
				continue
			}

			fieldPath := path + "." + field.Name
			if field.Type.Kind() == reflect.Ptr {
				require.True(t, hasSszOptionalTag(field.Tag), "%s: pointer field is not tagged ssz-type:\"optional\"", fieldPath)
			}

			requireOptionalPointers(t, field.Type, fieldPath, visited)
		}
	default:
	}
}

func hasSszOptionalTag(tag reflect.StructTag) bool {
	for _, option := range strings.Split(tag.Get("ssz-type"), ",") {
		if strings.TrimSpace(option) == "optional" {
			return true
		}
	}

	return false
}

// exportedStructNames parses the non-test Go files of dir and returns the
// names of all exported top-level struct types.
func exportedStructNames(t *testing.T, dir string) map[string]struct{} {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "read %s", dir)

	fset := token.NewFileSet()
	names := make(map[string]struct{}, 256)

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		require.NoError(t, err, "parse %s", name)

		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}

			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok || !typeSpec.Name.IsExported() {
					continue
				}

				if _, ok := typeSpec.Type.(*ast.StructType); ok {
					names[typeSpec.Name.Name] = struct{}{}
				}
			}
		}
	}

	return names
}
