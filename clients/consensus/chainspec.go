package consensus

import (
	"reflect"
	"time"

	"github.com/attestantio/go-eth2-client/spec/phase0"
)

type ForkVersion struct {
	Epoch           uint64
	CurrentVersion  []byte
	PreviousVersion []byte
}

// https://github.com/ethereum/consensus-specs/blob/dev/configs/mainnet.yaml
type ChainSpec struct {
	PresetBase                   string            `yaml:"PRESET_BASE"`
	ConfigName                   string            `yaml:"CONFIG_NAME"`
	MinGenesisTime               time.Time         `yaml:"MIN_GENESIS_TIME"`
	GenesisForkVersion           phase0.Version    `yaml:"GENESIS_FORK_VERSION"`
	AltairForkVersion            phase0.Version    `yaml:"ALTAIR_FORK_VERSION"`
	AltairForkEpoch              uint64            `yaml:"ALTAIR_FORK_EPOCH"`
	BellatrixForkVersion         phase0.Version    `yaml:"BELLATRIX_FORK_VERSION"`
	BellatrixForkEpoch           uint64            `yaml:"BELLATRIX_FORK_EPOCH"`
	CappellaForkVersion          phase0.Version    `yaml:"CAPELLA_FORK_VERSION"`
	CappellaForkEpoch            uint64            `yaml:"CAPELLA_FORK_EPOCH"`
	DenebForkVersion             phase0.Version    `yaml:"DENEB_FORK_VERSION"`
	DenebForkEpoch               uint64            `yaml:"DENEB_FORK_EPOCH"`
	ElectraForkVersion           phase0.Version    `yaml:"ELECTRA_FORK_VERSION"`
	ElectraForkEpoch             uint64            `yaml:"ELECTRA_FORK_EPOCH"`
	SecondsPerSlot               time.Duration     `yaml:"SECONDS_PER_SLOT"`
	SlotsPerEpoch                uint64            `yaml:"SLOTS_PER_EPOCH"`
	EpochsPerHistoricalVector    uint64            `yaml:"EPOCHS_PER_HISTORICAL_VECTOR"`
	EpochsPerSlashingVector      uint64            `yaml:"EPOCHS_PER_SLASHINGS_VECTOR"`
	EpochsPerSyncCommitteePeriod uint64            `yaml:"EPOCHS_PER_SYNC_COMMITTEE_PERIOD"`
	MinSeedLookahead             uint64            `yaml:"MIN_SEED_LOOKAHEAD"`
	ShuffleRoundCount            uint64            `yaml:"SHUFFLE_ROUND_COUNT"`
	MaxEffectiveBalance          uint64            `yaml:"MAX_EFFECTIVE_BALANCE"`
	MaxEffectiveBalanceElectra   uint64            `yaml:"MAX_EFFECTIVE_BALANCE_ELECTRA"`
	TargetCommitteeSize          uint64            `yaml:"TARGET_COMMITTEE_SIZE"`
	MaxCommitteesPerSlot         uint64            `yaml:"MAX_COMMITTEES_PER_SLOT"`
	DomainBeaconProposer         phase0.DomainType `yaml:"DOMAIN_BEACON_PROPOSER"`
	DomainBeaconAttester         phase0.DomainType `yaml:"DOMAIN_BEACON_ATTESTER"`
	DomainSyncCommittee          phase0.DomainType `yaml:"DOMAIN_SYNC_COMMITTEE"`

	// additional dora specific specs
	WhiskForkEpoch *uint64
}

func (chain *ChainSpec) CheckMismatch(chain2 *ChainSpec) []string {
	mismatches := []string{}

	chainT := reflect.ValueOf(chain).Elem()
	chain2T := reflect.ValueOf(chain2).Elem()

	for i := 0; i < chainT.NumField(); i++ {
		if chainT.Field(i).Interface() != chain2T.Field(i).Interface() {
			// 0 value on chain side are allowed
			if chainT.Field(i).Interface() == reflect.Zero(chainT.Field(i).Type()).Interface() {
				continue
			}

			mismatches = append(mismatches, chainT.Type().Field(i).Name)
		}
	}

	return mismatches
}

func (chain *ChainSpec) Clone() *ChainSpec {
	res := &ChainSpec{}
	chainT := reflect.ValueOf(chain).Elem()
	chain2T := reflect.ValueOf(res).Elem()

	for i := 0; i < chainT.NumField(); i++ {
		value := chainT.Field(i)
		chain2T.Field(i).Set(value)
	}

	return res
}
