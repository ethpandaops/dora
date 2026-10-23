package services

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"github.com/ethpandaops/dora/clients/consensus"
	"github.com/ethpandaops/dora/config"
	"github.com/ethpandaops/dora/db"
	"github.com/ethpandaops/dora/dbtypes"
	"github.com/ethpandaops/dora/indexer/beacon"
	"github.com/ethpandaops/dora/utils"
)

var logger_bn = logrus.StandardLogger().WithField("module", "builder_names")

// BuilderNames names builders from a names list using the validator names key syntax.
//
// A builder's identity is its pubkey, while its index is a reusable slot (EIP-8282). All
// pubkey and address based keys are therefore resolved to pubkeys, and only the current
// occupant of an index inherits the name of its pubkey. Index keys name the index itself.
type BuilderNames struct {
	ctx            context.Context
	beaconIndexer  *beacon.Indexer
	chainState     *consensus.ChainState
	updaterRunning bool

	// refreshInterval and namesYaml are copied from the config when the updater starts, so
	// the updater goroutine never reads the global config.
	refreshInterval time.Duration
	namesYaml       string

	// nameIndex holds the published name assignment. It is immutable once published, so
	// lookups need no locking.
	nameIndex atomic.Pointer[builderNameIndex]
}

// builderNameSources holds the parsed keys of a builder names list.
type builderNameSources struct {
	ranges          []validatorNameRange
	byPubkey        map[phase0.BLSPubKey]string
	byWithdrawal    map[common.Address]string
	byDepositOrigin map[common.Address]string
	byDepositTarget map[common.Address]string
}

// builderNameIndex is the immutable, published view of the builder name assignment.
type builderNameIndex struct {
	ranges   []validatorNameRange        // index-based names, sorted & disjoint
	byPubkey map[phase0.BLSPubKey]string // resolved names of all known builder pubkeys
	byIndex  map[uint64]string           // resolved names of the current index occupants
}

func NewBuilderNames(ctx context.Context, beaconIndexer *beacon.Indexer, chainState *consensus.ChainState) *BuilderNames {
	return &BuilderNames{
		ctx:           ctx,
		beaconIndexer: beaconIndexer,
		chainState:    chainState,
	}
}

func (bn *BuilderNames) StartUpdater() {
	if bn.updaterRunning {
		return
	}
	bn.refreshInterval = utils.Config.Frontend.BuilderNamesRefreshInterval
	if bn.refreshInterval <= 0 {
		bn.refreshInterval = 5 * time.Minute
	}
	bn.namesYaml = utils.Config.Frontend.BuilderNamesYaml

	bn.updaterRunning = true
	go bn.runUpdaterLoop()
}

func (bn *BuilderNames) runUpdaterLoop() {
	defer utils.HandleSubroutinePanic("BuilderNames.runUpdaterLoop", bn.runUpdaterLoop)

	for {
		delay := bn.refreshInterval
		if err := bn.refresh(); err != nil {
			logger_bn.WithError(err).Warn("builder names refresh failed, retrying in 30 sec")
			delay = 30 * time.Second
		}

		select {
		case <-bn.ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func (bn *BuilderNames) getDefaultBuilderNames() string {
	specs := bn.chainState.GetSpecs()
	chainName := ""
	if specs != nil {
		chainName = specs.ConfigName
	}

	// default builder names
	switch chainName {
	case "sepolia":
		return "~internal/sepolia.builders.yaml"
	}

	return ""
}

// refresh reloads the names list and resolves it against the current builder set. The
// previously published names stay live when either step fails.
func (bn *BuilderNames) refresh() error {
	builderNamesYaml := bn.namesYaml
	if builderNamesYaml == "" {
		builderNamesYaml = bn.getDefaultBuilderNames()
	}
	if builderNamesYaml == "" {
		return nil
	}

	names, err := loadBuilderNamesYaml(builderNamesYaml)
	if err != nil {
		return err
	}

	sources := parseBuilderNamesMap(names)

	if bn.beaconIndexer.GetCanonicalHead(nil) == nil {
		return fmt.Errorf("canonical head not available")
	}

	nameIndex := bn.resolveNames(sources)
	bn.nameIndex.Store(nameIndex)

	logger_bn.Debugf("builder names refreshed: %v keys, %v named pubkeys, %v named indexes", len(names), len(nameIndex.byPubkey), len(nameIndex.byIndex))
	return nil
}

func loadBuilderNamesYaml(fileName string) (map[string]string, error) {
	var file io.ReadCloser

	if internalName, isInternal := strings.CutPrefix(fileName, "~internal/"); isInternal {
		f, err := config.BuilderNamesYml.Open(internalName)
		if err != nil {
			return nil, fmt.Errorf("could not find internal builder names file %v: %w", internalName, err)
		}
		file = f
	} else {
		f, err := os.Open(fileName)
		if err != nil {
			return nil, fmt.Errorf("error opening builder names file %v: %w", fileName, err)
		}
		file = f
	}
	defer file.Close()

	names := map[string]string{}
	if err := yaml.NewDecoder(file).Decode(&names); err != nil {
		return nil, fmt.Errorf("error decoding builder names file %v: %w", fileName, err)
	}

	return names, nil
}

func parseBuilderNamesMap(names map[string]string) *builderNameSources {
	sources := &builderNameSources{
		byPubkey:        make(map[phase0.BLSPubKey]string, len(names)),
		byWithdrawal:    make(map[common.Address]string, len(names)),
		byDepositOrigin: make(map[common.Address]string, len(names)),
		byDepositTarget: make(map[common.Address]string, len(names)),
	}
	ranges := make([]validatorNameRange, 0, len(names))

	for keyStr, name := range names {
		key, err := parseNameKey(keyStr)
		if err != nil {
			logger_bn.Warnf("invalid builder name key %v: %v", keyStr, err)
			continue
		}

		switch key.kind {
		case nameKeyRange:
			ranges = append(ranges, validatorNameRange{
				startIndex: key.startIndex,
				endIndex:   key.endIndex,
				name:       name,
			})
		case nameKeyPubkey:
			sources.byPubkey[key.pubkey] = name
		case nameKeyWithdrawal:
			sources.byWithdrawal[key.address] = name
		case nameKeyDepositOrigin:
			sources.byDepositOrigin[key.address] = name
		case nameKeyDepositTarget:
			sources.byDepositTarget[key.address] = name
		}
	}
	sources.ranges = normalizeNameRanges(ranges)

	return sources
}

// resolveNames resolves the pubkey and address based keys to builder pubkeys and maps the
// named pubkeys to the indexes they currently occupy. Later key types override earlier ones,
// matching the validator names precedence: withdrawal < depositor < deposit_target < pubkey.
func (bn *BuilderNames) resolveNames(sources *builderNameSources) *builderNameIndex {
	byPubkey := make(map[phase0.BLSPubKey]string, len(sources.byPubkey))
	allStatus := []dbtypes.BuilderStatus{
		dbtypes.BuilderStatusActiveFilter,
		dbtypes.BuilderStatusExitedFilter,
		dbtypes.BuilderStatusSupersededFilter,
	}

	// resolve names by execution (withdrawal) address
	for address, name := range sources.byWithdrawal {
		builders, _ := GlobalBeaconService.GetFilteredBuilderSet(bn.ctx, &dbtypes.BuilderFilter{
			ExecutionAddress: address[:],
			Status:           allStatus,
		}, false)
		for _, builder := range builders {
			byPubkey[builder.Builder.PublicKey] = name
		}
	}

	// resolve names by deposit tx sender / target
	for address, name := range sources.byDepositOrigin {
		bn.streamDepositPubkeys(&dbtypes.BuilderDepositTxFilter{Address: address[:]}, &dbtypes.DepositTxFilter{Address: address[:]}, func(pubkey phase0.BLSPubKey) {
			byPubkey[pubkey] = name
		})
	}
	for address, name := range sources.byDepositTarget {
		bn.streamDepositPubkeys(&dbtypes.BuilderDepositTxFilter{TargetAddress: address[:]}, &dbtypes.DepositTxFilter{TargetAddress: address[:]}, func(pubkey phase0.BLSPubKey) {
			byPubkey[pubkey] = name
		})
	}

	for pubkey, name := range sources.byPubkey {
		byPubkey[pubkey] = name
	}

	// map the named pubkeys to the indexes they currently occupy
	byIndex := make(map[uint64]string, len(byPubkey))
	if len(byPubkey) > 0 {
		builders, _ := GlobalBeaconService.GetFilteredBuilderSet(bn.ctx, &dbtypes.BuilderFilter{}, false)
		for _, builder := range builders {
			if name, found := byPubkey[builder.Builder.PublicKey]; found {
				byIndex[uint64(builder.Index)] = name
			}
		}
	}

	return &builderNameIndex{
		ranges:   sources.ranges,
		byPubkey: byPubkey,
		byIndex:  byIndex,
	}
}

// streamDepositPubkeys calls cb with the pubkey of every builder deposit matching the
// filters: requests to the builder deposit contract, and builder credential deposits to
// the validator deposit contract, which onboard as builders at the Gloas fork.
func (bn *BuilderNames) streamDepositPubkeys(builderFilter *dbtypes.BuilderDepositTxFilter, depositFilter *dbtypes.DepositTxFilter, cb func(pubkey phase0.BLSPubKey)) {
	pageSize := uint64(5000)

	for offset := uint64(0); ; offset += pageSize {
		depositTxs, depositCount, err := db.GetBuilderDepositTxsFiltered(bn.ctx, offset, uint32(pageSize), builderFilter)
		if err != nil {
			break
		}
		for _, depositTx := range depositTxs {
			cb(phase0.BLSPubKey(depositTx.PublicKey))
		}
		if offset+pageSize >= depositCount {
			break
		}
	}

	canonicalForkIds := GlobalBeaconService.GetCanonicalForkIds()
	for offset := uint64(0); ; offset += pageSize {
		depositTxs, depositCount, err := db.GetDepositTxsFiltered(bn.ctx, offset, uint32(pageSize), canonicalForkIds, depositFilter)
		if err != nil {
			break
		}
		for _, depositTx := range depositTxs {
			if isBuilderCredential(depositTx.WithdrawalCredentials) {
				cb(phase0.BLSPubKey(depositTx.PublicKey))
			}
		}
		if offset+pageSize >= depositCount {
			break
		}
	}
}

// GetBuilderName returns the name of the builder currently occupying the given index.
func (bn *BuilderNames) GetBuilderName(builderIndex uint64) string {
	nameIndex := bn.nameIndex.Load()
	if nameIndex == nil {
		return ""
	}

	rawIdx := builderIndex &^ BuilderIndexFlag
	if name, found := lookupRangeName(nameIndex.ranges, rawIdx); found {
		return name
	}

	return nameIndex.byIndex[rawIdx]
}

// GetBuilderNameByPubkey returns the name of the builder with the given pubkey. Unlike
// GetBuilderName it also names builders whose index has been reused since.
func (bn *BuilderNames) GetBuilderNameByPubkey(pubkey phase0.BLSPubKey) string {
	nameIndex := bn.nameIndex.Load()
	if nameIndex == nil {
		return ""
	}

	return nameIndex.byPubkey[pubkey]
}
