package statetransition

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/utils"
	"github.com/ethpandaops/go-eth2-client/spec"
	"github.com/ethpandaops/go-eth2-client/spec/all"
	"github.com/ethpandaops/go-eth2-client/spec/altair"
	"github.com/ethpandaops/go-eth2-client/spec/capella"
	"github.com/ethpandaops/go-eth2-client/spec/electra"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/golang/snappy"
	dynssz "github.com/pk910/dynamic-ssz"
)

// The consensus-spec-tests runner. It replays the reference test vectors from
// https://github.com/ethereum/consensus-spec-tests through this package's state
// transition and compares the resulting state against the vector's post state.
//
// Run it with:
//
//	SPEC_TESTS_DIR=<vectors>/tests SPEC_CONFIGS_DIR=<consensus-specs checkout> \
//	    go test ./indexer/beacon/statetransition/ -run TestSpecVectors -v
//
// SPEC_CONFIGS_DIR must hold the presets/ and configs/ of the *same* spec release
// the vectors were generated from — the constants are part of the fixture.
// Optional filters: SPEC_TESTS_PRESET (mainnet|minimal), SPEC_TESTS_FORK,
// SPEC_TESTS_RUNNER (sanity|operations|epoch_processing|finality|random),
// SPEC_TESTS_CASE (substring match on the case name).
//
// Both env vars unset ⇒ the test skips, so the normal `go test ./...` stays fast.

// supportedForks maps a spec-test fork directory to the state version it produces.
// Everything before Fulu is out of scope: applyBlock returns early below Fulu.
var supportedForks = map[string]spec.DataVersion{
	"fulu":  spec.DataVersionFulu,
	"gloas": spec.DataVersionGloas,
	"heze":  spec.DataVersionHeze,
}

// defaultForks are the fork directories run when SPEC_TESTS_FORK is unset. Only
// fulu is on by default: the latest published vectors (v1.6.0-beta.0) predate the
// Gloas spec this package implements (EIP-8282 builder deposits, the removal of
// process_execution_payload, the reworked bid), so their gloas/heze suites report
// divergences against the older spec rather than real bugs.
var defaultForks = []string{"fulu"}

// unsupportedSuites lists the runner/handler combinations this package does not
// implement, with the reason. Anything not listed is expected to pass.
var unsupportedSuites = map[string]string{
	"fork":                   "only the Fulu→Gloas upgrade is implemented, the fork suite covers the preceding upgrades",
	"transition":             "fork transitions other than Fulu→Gloas are not implemented",
	"rewards":                "the rewards suite asserts per-validator delta arrays; rewards are applied inline here",
	"light_client":           "not a state transition suite",
	"fork_choice":            "not a state transition suite",
	"ssz_static":             "not a state transition suite",
	"merkle_proof":           "not a state transition suite",
	"networking":             "not a state transition suite",
	"sync":                   "not a state transition suite",
	"kzg":                    "not a state transition suite",
	"bls":                    "not a state transition suite",
	"genesis":                "genesis initialization is not implemented",
	"random":                 "", // supported, listed for readability
	"finality":               "",
	"sanity":                 "",
	"operations":             "",
	"epoch_processing":       "",
	"operations/deposit":     "",
	"epoch_processing/epoch": "",
}

// specDomainTypes are the signature domain constants. They live in the spec text
// rather than in presets/ or configs/, so the beacon API serves them but the
// files this runner reads do not carry them — and the seeds for proposer,
// committee, sync committee and PTC selection are derived from them.
var specDomainTypes = map[string]any{
	"DOMAIN_BEACON_PROPOSER":                "0x00000000",
	"DOMAIN_BEACON_ATTESTER":                "0x01000000",
	"DOMAIN_RANDAO":                         "0x02000000",
	"DOMAIN_DEPOSIT":                        "0x03000000",
	"DOMAIN_VOLUNTARY_EXIT":                 "0x04000000",
	"DOMAIN_SELECTION_PROOF":                "0x05000000",
	"DOMAIN_AGGREGATE_AND_PROOF":            "0x06000000",
	"DOMAIN_SYNC_COMMITTEE":                 "0x07000000",
	"DOMAIN_SYNC_COMMITTEE_SELECTION_PROOF": "0x08000000",
	"DOMAIN_CONTRIBUTION_AND_PROOF":         "0x09000000",
	"DOMAIN_BLS_TO_EXECUTION_CHANGE":        "0x0A000000",
	"DOMAIN_PTC_ATTESTER":                   "0x0C000000",
	"DOMAIN_BUILDER_DEPOSIT":                "0x0E000000",
	"DOMAIN_BEACON_BUILDER":                 "0x1B000000",
}

// specTestCase is one test vector directory.
type specTestCase struct {
	dir     string
	name    string
	fork    string
	version spec.DataVersion
	specs   *consensus.ChainSpec
	dynSsz  *dynssz.DynSsz
}

// specTestStats counts what the run covered.
type specTestStats struct {
	passed          int
	failed          int
	expectedInvalid int
	unsupported     map[string]int
}

func TestSpecVectors(t *testing.T) {
	testsDir := os.Getenv("SPEC_TESTS_DIR")
	configsDir := os.Getenv("SPEC_CONFIGS_DIR")
	if testsDir == "" || configsDir == "" {
		t.Skip("set SPEC_TESTS_DIR (consensus-spec-tests/tests) and SPEC_CONFIGS_DIR (matching consensus-specs checkout) to run the spec vectors")
	}

	stats := &specTestStats{unsupported: map[string]int{}}

	for _, preset := range listDirs(t, testsDir, os.Getenv("SPEC_TESTS_PRESET")) {
		t.Run(preset, func(t *testing.T) {
			specs, flatSpec := loadSpecConfig(t, configsDir, preset)

			// dynssz resolves the dynamic list limits from the preset, so the specs
			// have to be installed before any type descriptor is built.
			dynssz.SetGlobalSpecs(flatSpec)
			ds := dynssz.NewDynSsz(flatSpec)

			forkFilter := os.Getenv("SPEC_TESTS_FORK")
			for _, fork := range listDirs(t, filepath.Join(testsDir, preset), forkFilter) {
				version, supported := supportedForks[fork]
				if !supported {
					continue
				}
				if forkFilter == "" && !contains(defaultForks, fork) {
					continue
				}

				t.Run(fork, func(t *testing.T) {
					runForkSuites(t, filepath.Join(testsDir, preset, fork), fork, version, specs, ds, stats)
				})
			}
		})
	}

	t.Logf("spec vectors: %d passed, %d failed, %d expected-invalid (skipped)",
		stats.passed, stats.failed, stats.expectedInvalid)
	for suite, count := range stats.unsupported {
		t.Logf("  skipped %d cases in %v: %v", count, suite, unsupportedSuites[suite])
	}
}

// runForkSuites walks <fork>/<runner>/<handler>/<suite>/<case> and dispatches
// every case directory to the runner that handles it.
func runForkSuites(t *testing.T, forkDir, fork string, version spec.DataVersion, specs *consensus.ChainSpec, ds *dynssz.DynSsz, stats *specTestStats) {
	runnerFilter := os.Getenv("SPEC_TESTS_RUNNER")
	caseFilter := os.Getenv("SPEC_TESTS_CASE")

	for _, runner := range listDirs(t, forkDir, runnerFilter) {
		if reason, listed := unsupportedSuites[runner]; listed && reason != "" {
			stats.unsupported[runner] += countCases(filepath.Join(forkDir, runner))
			continue
		}

		for _, handler := range listDirs(t, filepath.Join(forkDir, runner), "") {
			suiteKey := runner + "/" + handler
			if reason, listed := unsupportedSuites[suiteKey]; listed && reason != "" {
				stats.unsupported[suiteKey] += countCases(filepath.Join(forkDir, runner, handler))
				continue
			}

			handlerDir := filepath.Join(forkDir, runner, handler)
			for _, suite := range listDirs(t, handlerDir, "") {
				suiteDir := filepath.Join(handlerDir, suite)
				for _, name := range listDirs(t, suiteDir, "") {
					if caseFilter != "" && !strings.Contains(name, caseFilter) {
						continue
					}

					testCase := &specTestCase{
						dir:     filepath.Join(suiteDir, name),
						name:    name,
						fork:    fork,
						version: version,
						specs:   specs,
						dynSsz:  ds,
					}

					t.Run(runner+"/"+handler+"/"+name, func(t *testing.T) {
						runCase(t, runner, handler, testCase, stats)
					})
				}
			}
		}
	}
}

// runCase runs a single test vector. Cases without a post state expect the block
// or operation to be rejected, which this package leaves to the beacon node — it
// only ever replays blocks that a node already accepted — so they are skipped.
func runCase(t *testing.T, runner, handler string, testCase *specTestCase, stats *specTestStats) {
	if !testCase.hasFile("post.ssz_snappy") {
		stats.expectedInvalid++
		t.Skip("expected-invalid case: the state transition does not implement the validity checks")
	}

	var err error
	switch runner {
	case "sanity", "finality", "random":
		switch handler {
		case "blocks", "finality", "random":
			err = testCase.runBlocks(t)
		case "slots":
			err = testCase.runSlots(t)
		default:
			t.Skipf("unknown %v handler %v", runner, handler)
		}
	case "operations":
		err = testCase.runOperation(t, handler)
	case "epoch_processing":
		err = testCase.runEpochProcessing(t, handler)
	default:
		t.Skipf("unknown runner %v", runner)
	}

	if err != nil {
		stats.failed++
		t.Fatalf("%v", err)
	}

	stats.passed++
}

// runBlocks applies the case's blocks and compares against the post state.
func (tc *specTestCase) runBlocks(t *testing.T) error {
	state := tc.loadState(t, "pre")
	blocksCount := tc.metaInt(t, "blocks_count")

	st := NewStateTransition(tc.specs, tc.dynSsz)
	for i := 0; i < blocksCount; i++ {
		block := &all.SignedBeaconBlock{Version: tc.version}
		tc.readSSZ(t, fmt.Sprintf("blocks_%d", i), block)

		if err := st.ApplyBlock(state, block); err != nil {
			return fmt.Errorf("apply block %d (slot %d): %w", i, block.Message.Slot, err)
		}
	}

	return tc.compareState(t, state)
}

// runSlots advances the state by the case's slot count.
func (tc *specTestCase) runSlots(t *testing.T) error {
	state := tc.loadState(t, "pre")

	// slots.yaml holds the bare count, followed by the yaml end-of-document marker.
	slotsValue, _, _ := strings.Cut(tc.readFile(t, "slots.yaml"), "\n")
	slots, err := strconv.ParseUint(strings.TrimSpace(slotsValue), 10, 64)
	if err != nil {
		return fmt.Errorf("failed to read slots.yaml: %w", err)
	}

	st := NewStateTransition(tc.specs, tc.dynSsz)
	if err := st.processSlots(state, state.Slot+phase0.Slot(slots), nil); err != nil {
		return fmt.Errorf("process_slots: %w", err)
	}

	return tc.compareState(t, state)
}

// runOperation applies a single block operation.
func (tc *specTestCase) runOperation(t *testing.T, handler string) error {
	state := tc.loadState(t, "pre")
	st := NewStateTransition(tc.specs, tc.dynSsz)

	s, err := st.newAccessor(state)
	if err != nil {
		return fmt.Errorf("failed to create accessor: %w", err)
	}

	switch handler {
	case "attestation":
		attestation := &all.Attestation{Version: tc.version}
		tc.readSSZ(t, "attestation", attestation)
		processAttestation(s, attestation, s.caches.committeeCache, 0)

	case "attester_slashing":
		slashing := &all.AttesterSlashing{Version: tc.version}
		tc.readSSZ(t, "attester_slashing", slashing)
		processAttesterSlashing(s, slashing)

	case "proposer_slashing":
		slashing := &phase0.ProposerSlashing{}
		tc.readSSZ(t, "proposer_slashing", slashing)
		processProposerSlashing(s, slashing)

	case "voluntary_exit":
		exit := &phase0.SignedVoluntaryExit{}
		tc.readSSZ(t, "voluntary_exit", exit)
		processVoluntaryExit(s, exit)

	case "bls_to_execution_change":
		change := &capella.SignedBLSToExecutionChange{}
		tc.readSSZ(t, "address_change", change)
		processBLSToExecutionChange(s, change)

	case "deposit_request":
		request := &electra.DepositRequest{}
		tc.readSSZ(t, "deposit_request", request)
		processDepositRequest(s, request)

	case "withdrawal_request":
		request := &electra.WithdrawalRequest{}
		tc.readSSZ(t, "withdrawal_request", request)
		processWithdrawalRequest(s, request)

	case "consolidation_request":
		request := &electra.ConsolidationRequest{}
		tc.readSSZ(t, "consolidation_request", request)
		processConsolidationRequest(s, request)

	case "sync_aggregate":
		aggregate := &altair.SyncAggregate{}
		tc.readSSZ(t, "sync_aggregate", aggregate)
		processSyncAggregate(s, tc.wrapBody(&all.BeaconBlockBody{Version: tc.version, SyncAggregate: aggregate}))

	case "withdrawals":
		// The payload is the expected output, which this package derives from the
		// state instead of reading it from the block.
		processWithdrawals(s)

	case "block_header":
		block := &all.BeaconBlock{Version: tc.version}
		tc.readSSZ(t, "block", block)
		bodyRoot, rootErr := block.Body.HashTreeRoot()
		if rootErr != nil {
			return fmt.Errorf("failed to compute body root: %w", rootErr)
		}
		processBlockHeader(s, block.Slot, block.ProposerIndex, block.ParentRoot, bodyRoot)

	case "execution_payload":
		if !tc.metaBool(t, "execution_valid", true) {
			t.Skip("case expects the execution layer to reject the payload")
		}
		body := &all.BeaconBlockBody{Version: tc.version}
		tc.readSSZ(t, "body", body)
		processFuluExecutionPayload(s, tc.wrapBody(body))

	default:
		t.Skipf("no mapping for operation handler %v", handler)
	}

	return tc.compareState(t, state)
}

// epochHandlers maps an epoch_processing handler to the function it exercises.
var epochHandlers = map[string]func(s *stateAccessor) error{
	"justification_and_finalization": processJustificationAndFinalization,
	"inactivity_updates":             processInactivityUpdates,
	"rewards_and_penalties":          processRewardsAndPenalties,
	"registry_updates":               processRegistryUpdates,
	"slashings":                      processSlashings,
	"pending_deposits":               processPendingDeposits,
	"eth1_data_reset":                func(s *stateAccessor) error { processEth1DataReset(s); return nil },
	"pending_consolidations":         func(s *stateAccessor) error { processPendingConsolidations(s); return nil },
	"effective_balance_updates":      func(s *stateAccessor) error { processEffectiveBalanceUpdates(s); return nil },
	"slashings_reset":                func(s *stateAccessor) error { processSlashingsReset(s); return nil },
	"randao_mixes_reset":             func(s *stateAccessor) error { processRandaoMixesReset(s); return nil },
	"historical_summaries_update":    func(s *stateAccessor) error { processHistoricalSummariesUpdate(s); return nil },
	"participation_flag_updates":     func(s *stateAccessor) error { processParticipationFlagUpdates(s); return nil },
	"sync_committee_updates":         func(s *stateAccessor) error { processSyncCommitteeUpdates(s); return nil },
	"proposer_lookahead":             func(s *stateAccessor) error { processProposerLookahead(s); return nil },
	"builder_pending_payments":       func(s *stateAccessor) error { processBuilderPendingPayments(s); return nil },
	"ptc_window":                     func(s *stateAccessor) error { processPtcWindow(s); return nil },
}

// runEpochProcessing runs a single epoch transition step.
func (tc *specTestCase) runEpochProcessing(t *testing.T, handler string) error {
	process, known := epochHandlers[handler]
	if !known {
		t.Skipf("no mapping for epoch_processing handler %v", handler)
	}

	state := tc.loadState(t, "pre")
	st := NewStateTransition(tc.specs, tc.dynSsz)

	s, err := st.newAccessor(state)
	if err != nil {
		return fmt.Errorf("failed to create accessor: %w", err)
	}

	if err := process(s); err != nil {
		return fmt.Errorf("%v: %w", handler, err)
	}

	return tc.compareState(t, state)
}

// compareState checks the produced state against the case's post state and, on a
// mismatch, points at the first field that differs.
func (tc *specTestCase) compareState(t *testing.T, got *all.BeaconState) error {
	want := tc.loadState(t, "post")

	gotRoot, err := tc.dynSsz.HashTreeRoot(got)
	if err != nil {
		return fmt.Errorf("failed to hash produced state: %w", err)
	}
	wantRoot, err := tc.dynSsz.HashTreeRoot(want)
	if err != nil {
		return fmt.Errorf("failed to hash post state: %w", err)
	}
	if gotRoot == wantRoot {
		return nil
	}

	return fmt.Errorf("state mismatch:\n  got  0x%x\n  want 0x%x\n%v",
		gotRoot, wantRoot, describeStateDiff(got, want))
}

// describeStateDiff lists the high-level state fields that differ, to point at the
// processing step responsible for a mismatch.
func describeStateDiff(got, want *all.BeaconState) string {
	var diffs []string

	report := func(field string, gotValue, wantValue any) {
		if fmt.Sprint(gotValue) != fmt.Sprint(wantValue) {
			diffs = append(diffs, fmt.Sprintf("  %v: got %v, want %v", field, gotValue, wantValue))
		}
	}

	report("slot", got.Slot, want.Slot)
	report("len(validators)", len(got.Validators), len(want.Validators))
	report("len(balances)", len(got.Balances), len(want.Balances))
	report("eth1_deposit_index", got.ETH1DepositIndex, want.ETH1DepositIndex)
	report("deposit_requests_start_index", got.DepositRequestsStartIndex, want.DepositRequestsStartIndex)
	report("deposit_balance_to_consume", got.DepositBalanceToConsume, want.DepositBalanceToConsume)
	report("exit_balance_to_consume", got.ExitBalanceToConsume, want.ExitBalanceToConsume)
	report("earliest_exit_epoch", got.EarliestExitEpoch, want.EarliestExitEpoch)
	report("consolidation_balance_to_consume", got.ConsolidationBalanceToConsume, want.ConsolidationBalanceToConsume)
	report("earliest_consolidation_epoch", got.EarliestConsolidationEpoch, want.EarliestConsolidationEpoch)
	report("len(pending_deposits)", len(got.PendingDeposits), len(want.PendingDeposits))
	report("len(pending_partial_withdrawals)", len(got.PendingPartialWithdrawals), len(want.PendingPartialWithdrawals))
	report("len(pending_consolidations)", len(got.PendingConsolidations), len(want.PendingConsolidations))
	report("next_withdrawal_index", got.NextWithdrawalIndex, want.NextWithdrawalIndex)
	report("next_withdrawal_validator_index", got.NextWithdrawalValidatorIndex, want.NextWithdrawalValidatorIndex)
	report("finalized_checkpoint.epoch", checkpointEpoch(got.FinalizedCheckpoint), checkpointEpoch(want.FinalizedCheckpoint))
	report("current_justified_checkpoint.epoch", checkpointEpoch(got.CurrentJustifiedCheckpoint), checkpointEpoch(want.CurrentJustifiedCheckpoint))
	report("len(builders)", len(got.Builders), len(want.Builders))
	report("current_sync_committee", syncCommitteeDigest(got.CurrentSyncCommittee), syncCommitteeDigest(want.CurrentSyncCommittee))
	report("next_sync_committee", syncCommitteeDigest(got.NextSyncCommittee), syncCommitteeDigest(want.NextSyncCommittee))
	report("len(builder_pending_withdrawals)", len(got.BuilderPendingWithdrawals), len(want.BuilderPendingWithdrawals))

	for i := range got.Validators {
		if i >= len(want.Validators) {
			break
		}
		gotValidator := fmt.Sprintf("%+v", *got.Validators[i])
		wantValidator := fmt.Sprintf("%+v", *want.Validators[i])
		if gotValidator != wantValidator {
			diffs = append(diffs, fmt.Sprintf("  validators[%d]:\n    got  %v\n    want %v", i, gotValidator, wantValidator))
		}
		if len(diffs) > 12 {
			diffs = append(diffs, "  ...")
			break
		}
	}

	for i := range got.Balances {
		if i >= len(want.Balances) {
			break
		}
		if got.Balances[i] != want.Balances[i] {
			diffs = append(diffs, fmt.Sprintf("  balances[%d]: got %v, want %v (diff %v)",
				i, got.Balances[i], want.Balances[i], int64(got.Balances[i])-int64(want.Balances[i])))
		}
		if len(diffs) > 20 {
			diffs = append(diffs, "  ...")
			break
		}
	}

	if len(diffs) == 0 {
		return "  (no difference in the compared fields)"
	}

	return strings.Join(diffs, "\n")
}

// syncCommitteeDigest renders a sync committee compactly enough to compare.
func syncCommitteeDigest(committee *altair.SyncCommittee) string {
	if committee == nil {
		return "<nil>"
	}

	first := "-"
	if len(committee.Pubkeys) > 0 {
		first = fmt.Sprintf("%#x", committee.Pubkeys[0][:4])
	}

	return fmt.Sprintf("%d pubkeys, first %v, aggregate %#x", len(committee.Pubkeys), first, committee.AggregatePubkey[:4])
}

func checkpointEpoch(checkpoint *phase0.Checkpoint) phase0.Epoch {
	if checkpoint == nil {
		return 0
	}
	return checkpoint.Epoch
}

// wrapBody puts a block body into the block envelope the process functions take.
func (tc *specTestCase) wrapBody(body *all.BeaconBlockBody) *all.SignedBeaconBlock {
	return &all.SignedBeaconBlock{
		Version: tc.version,
		Message: &all.BeaconBlock{Version: tc.version, Body: body},
	}
}

func (tc *specTestCase) hasFile(name string) bool {
	_, err := os.Stat(filepath.Join(tc.dir, name))
	return err == nil
}

func (tc *specTestCase) readFile(t *testing.T, name string) string {
	data, err := os.ReadFile(filepath.Join(tc.dir, name))
	if err != nil {
		t.Fatalf("failed to read %v: %v", name, err)
	}
	return string(data)
}

// readSSZ decodes a snappy-compressed SSZ fixture into obj.
func (tc *specTestCase) readSSZ(t *testing.T, name string, obj any) {
	compressed, err := os.ReadFile(filepath.Join(tc.dir, name+".ssz_snappy"))
	if err != nil {
		t.Fatalf("failed to read %v: %v", name, err)
	}

	data, err := snappy.Decode(nil, compressed)
	if err != nil {
		t.Fatalf("failed to decompress %v: %v", name, err)
	}

	if err := tc.dynSsz.UnmarshalSSZ(obj, data); err != nil {
		t.Fatalf("failed to unmarshal %v: %v", name, err)
	}
}

func (tc *specTestCase) loadState(t *testing.T, name string) *all.BeaconState {
	state := &all.BeaconState{Version: tc.version}
	tc.readSSZ(t, name, state)
	return state
}

// metaInt reads a numeric key from meta.yaml, which is a flat one-line mapping.
func (tc *specTestCase) metaInt(t *testing.T, key string) int {
	if !tc.hasFile("meta.yaml") {
		return 0
	}

	for k, v := range parseFlatYaml(tc.readFile(t, "meta.yaml")) {
		if k != key {
			continue
		}
		value, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("failed to parse meta.yaml %v: %v", key, err)
		}
		return value
	}

	return 0
}

func (tc *specTestCase) metaBool(t *testing.T, key string, fallback bool) bool {
	if !tc.hasFile("meta.yaml") {
		return fallback
	}

	for k, v := range parseFlatYaml(tc.readFile(t, "meta.yaml")) {
		if k == key {
			return v == "true"
		}
	}

	return fallback
}

// loadSpecConfig builds the chain spec for a preset from the spec repository's
// preset and config files. Every value is handed over as a string so it goes
// through the same parser as the /eth/v1/config/spec response, which is what the
// chain spec is written against.
func loadSpecConfig(t *testing.T, configsDir, preset string) (*consensus.ChainSpec, map[string]any) {
	values := map[string]any{}
	for key, value := range specDomainTypes {
		values[key] = value
	}

	presetFiles, err := filepath.Glob(filepath.Join(configsDir, "presets", preset, "*.yaml"))
	if err != nil || len(presetFiles) == 0 {
		t.Fatalf("no preset files for %v in %v", preset, configsDir)
	}

	for _, file := range append(presetFiles, filepath.Join(configsDir, "configs", preset+".yaml")) {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("failed to read %v: %v", file, err)
		}
		for key, value := range parseFlatYaml(string(data)) {
			values[key] = value
		}
	}

	parsed := utils.ParseSpecMap(values)
	specs := &consensus.ChainSpec{}
	if err := specs.ParseAdditive(parsed); err != nil {
		t.Fatalf("failed to parse spec for preset %v: %v", preset, err)
	}
	if specs.SlotsPerEpoch == 0 {
		t.Fatalf("incomplete spec for preset %v (SlotsPerEpoch=0)", preset)
	}

	return specs, parsed
}

// parseFlatYaml reads the `KEY: value` lines of a flat yaml document. The preset,
// config and meta files are all flat, and reading them as raw strings keeps hex
// values like 0x01000000 from being folded into integers.
func parseFlatYaml(content string) map[string]string {
	values := map[string]string{}

	for _, line := range strings.Split(content, "\n") {
		// Keys always sit at column 0; indented lines belong to a nested value.
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}

		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") || line == "---" || line == "..." {
			continue
		}

		// meta.yaml uses an inline mapping: {blocks_count: 2, bls_setting: 1}
		isInline := strings.HasPrefix(line, "{")
		line = strings.TrimPrefix(line, "{")
		line = strings.TrimSuffix(line, "}")

		pairs := []string{line}
		if isInline {
			pairs = strings.Split(line, ",")
		}

		for _, pair := range pairs {
			key, value, found := strings.Cut(pair, ":")
			if !found {
				continue
			}

			key = strings.TrimSpace(key)
			if key == "" || strings.ContainsAny(key, " \t") {
				continue
			}

			if comment := strings.Index(value, "#"); comment >= 0 {
				value = value[:comment]
			}
			value = strings.TrimSpace(value)
			value = strings.Trim(value, `'"`)
			// Lists and mappings (BLOB_SCHEDULE) have no scalar form the spec parser reads.
			if value == "" || strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{") {
				continue
			}

			values[key] = value
		}
	}

	return values
}

// listDirs returns the sorted subdirectories of dir, optionally filtered to one name.
func listDirs(t *testing.T, dir, filter string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to list %v: %v", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if filter != "" && entry.Name() != filter {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	return names
}

// countCases counts the test vector directories below dir.
func countCases(dir string) int {
	count := 0
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && entry.Name() == "pre.ssz_snappy" {
			count++
		}
		return nil
	})

	return count
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}

	return false
}
