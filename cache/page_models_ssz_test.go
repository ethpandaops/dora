package cache

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ethpandaops/dora/types/models"
)

// pageModelsDir is the package holding the page models relative to this package.
const pageModelsDir = "../types/models"

// pageModelSuffixes name the top-level page models of the models package: the
// values handlers hand to the frontend cache. Nested row and section types are
// validated through their parents.
var pageModelSuffixes = []string{"PageData", "Response"}

// pageModels lists the top-level page models. The frontend cache serializes
// them with SSZ and silently falls back to JSON for types that are not SSZ
// compatible, so every model is checked here. TestPageModelsRegistered fails
// when a model is added to the package but not to this list.
var pageModels = []any{
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
}

// TestPageModelsRegistered fails when the models package declares a top-level
// page model that is missing from pageModels, or when pageModels holds a type
// that is no longer declared as a struct in the package.
func TestPageModelsRegistered(t *testing.T) {
	declared := exportedStructNames(t, pageModelsDir)

	registered := make(map[string]struct{}, len(pageModels))
	for _, model := range pageModels {
		registered[reflect.TypeOf(model).Elem().Name()] = struct{}{}
	}

	missing := make([]string, 0)
	for name := range declared {
		if _, ok := registered[name]; !ok && isPageModelName(name) {
			missing = append(missing, name)
		}
	}

	stale := make([]string, 0)
	for name := range registered {
		if _, ok := declared[name]; !ok {
			stale = append(stale, name)
		}
	}

	require.Empty(t, missing, "page models not registered in pageModels: %s", strings.Join(missing, ", "))
	require.Empty(t, stale, "registered page models not declared in %s: %s", pageModelsDir, strings.Join(stale, ", "))
}

// TestPageModelsSszCompatible checks that every page model passes the SSZ type
// validation the frontend cache performs, survives an SSZ round trip and
// declares every pointer field reachable from it as an SSZ optional.
func TestPageModelsSszCompatible(t *testing.T) {
	for _, model := range pageModels {
		modelType := reflect.TypeOf(model)

		t.Run(modelType.Elem().Name(), func(t *testing.T) {
			require.NoError(t, modelDynSsz.ValidateType(modelType), "page model is not ssz compatible")

			encoded, err := modelDynSsz.MarshalSSZ(model)
			require.NoError(t, err, "marshal zero value")

			decoded := reflect.New(modelType.Elem()).Interface()
			require.NoError(t, modelDynSsz.UnmarshalSSZ(decoded, encoded), "unmarshal zero value")

			// nil pointers carry meaning in page models (absent section, no
			// measurement); without the optional tag a nil pointer comes back
			// from the cache as a pointer to zero
			requireOptionalPointers(t, modelType, modelType.Elem().Name(), make(map[reflect.Type]struct{}, 32))
		})
	}
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
