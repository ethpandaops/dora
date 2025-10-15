package consensus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/attestantio/go-eth2-client/spec/phase0"
	"gopkg.in/Knetic/govaluate.v3"
	"gopkg.in/yaml.v2"
)

type ForkVersion struct {
	Epoch           uint64
	CurrentVersion  []byte
	PreviousVersion []byte
}

type BlobScheduleEntry struct {
	Epoch            uint64 `yaml:"EPOCH"`
	MaxBlobsPerBlock uint64 `yaml:"MAX_BLOBS_PER_BLOCK"`
}

// https://github.com/ethereum/consensus-specs/blob/dev/configs/mainnet.yaml
type ChainSpecConfig struct {
	PresetBase string `yaml:"PRESET_BASE"`
	ConfigName string `yaml:"CONFIG_NAME" check-if:"false"`

	// Transition
	TerminalTotalDifficulty          uint64 `yaml:"TERMINAL_TOTAL_DIFFICULTY"            check-severity:"warning"`
	TerminalBlockHash                []byte `yaml:"TERMINAL_BLOCK_HASH"                  check-severity:"warning"`
	TerminalBlockHashActivationEpoch uint64 `yaml:"TERMINAL_BLOCK_HASH_ACTIVATION_EPOCH" check-severity:"warning"`

	// Genesis
	MinGenesisActiveValidatorCount uint64         `yaml:"MIN_GENESIS_ACTIVE_VALIDATOR_COUNT"`
	MinGenesisTime                 uint64         `yaml:"MIN_GENESIS_TIME"`
	GenesisForkVersion             phase0.Version `yaml:"GENESIS_FORK_VERSION"`
	GenesisDelay                   uint64         `yaml:"GENESIS_DELAY"`

	// Forking
	AltairForkVersion    phase0.Version `yaml:"ALTAIR_FORK_VERSION"    check-if-fork:"AltairForkEpoch"`
	AltairForkEpoch      *uint64        `yaml:"ALTAIR_FORK_EPOCH"      check-if-fork:"AltairForkEpoch"`
	BellatrixForkVersion phase0.Version `yaml:"BELLATRIX_FORK_VERSION" check-if-fork:"BellatrixForkEpoch"`
	BellatrixForkEpoch   *uint64        `yaml:"BELLATRIX_FORK_EPOCH"   check-if-fork:"BellatrixForkEpoch"`
	CapellaForkVersion   phase0.Version `yaml:"CAPELLA_FORK_VERSION"   check-if-fork:"CapellaForkEpoch"`
	CapellaForkEpoch     *uint64        `yaml:"CAPELLA_FORK_EPOCH"     check-if-fork:"CapellaForkEpoch"`
	DenebForkVersion     phase0.Version `yaml:"DENEB_FORK_VERSION"     check-if-fork:"DenebForkEpoch"`
	DenebForkEpoch       *uint64        `yaml:"DENEB_FORK_EPOCH"       check-if-fork:"DenebForkEpoch"`
	ElectraForkVersion   phase0.Version `yaml:"ELECTRA_FORK_VERSION"   check-if-fork:"ElectraForkEpoch"`
	ElectraForkEpoch     *uint64        `yaml:"ELECTRA_FORK_EPOCH"     check-if-fork:"ElectraForkEpoch"`
	FuluForkVersion      phase0.Version `yaml:"FULU_FORK_VERSION"      check-if-fork:"FuluForkEpoch"`
	FuluForkEpoch        *uint64        `yaml:"FULU_FORK_EPOCH"        check-if-fork:"FuluForkEpoch"`
	GloasForkVersion     phase0.Version `yaml:"GLOAS_FORK_VERSION"   check-if-fork:"GloasForkEpoch"`
	GloasForkEpoch       *uint64        `yaml:"GLOAS_FORK_EPOCH"     check-if-fork:"GloasForkEpoch"`

	// Time parameters
	SecondsPerSlot                  uint64 `yaml:"SECONDS_PER_SLOT"`
	SecondsPerEth1Block             uint64 `yaml:"SECONDS_PER_ETH1_BLOCK"`
	MinValidatorWithdrawbilityDelay uint64 `yaml:"MIN_VALIDATOR_WITHDRAWABILITY_DELAY"`
	ShardCommitteePeriod            uint64 `yaml:"SHARD_COMMITTEE_PERIOD"`
	Eth1FollowDistance              uint64 `yaml:"ETH1_FOLLOW_DISTANCE"`

	// Validator cycle
	InactivityScoreBias             uint64 `yaml:"INACTIVITY_SCORE_BIAS"`
	InactivityScoreRecoveryRate     uint64 `yaml:"INACTIVITY_SCORE_RECOVERY_RATE"`
	EjectionBalance                 uint64 `yaml:"EJECTION_BALANCE"`
	MinPerEpochChurnLimit           uint64 `yaml:"MIN_PER_EPOCH_CHURN_LIMIT"`
	ChurnLimitQuotient              uint64 `yaml:"CHURN_LIMIT_QUOTIENT"`
	MaxPerEpochActivationChurnLimit uint64 `yaml:"MAX_PER_EPOCH_ACTIVATION_CHURN_LIMIT"`

	// fork choice
	ProposerScoreBoost              uint64 `yaml:"PROPOSER_SCORE_BOOST"`
	ReorgHeadWeightThreshold        uint64 `yaml:"REORG_HEAD_WEIGHT_THRESHOLD"`
	ReorgParentWeightThreshold      uint64 `yaml:"REORG_PARENT_WEIGHT_THRESHOLD"`
	ReorgMaxEpochsSinceFinalization uint64 `yaml:"REORG_MAX_EPOCHS_SINCE_FINALIZATION"`

	// Deposit contract
	DepositChainId         uint64 `yaml:"DEPOSIT_CHAIN_ID"`
	DepositNetworkId       uint64 `yaml:"DEPOSIT_NETWORK_ID"`
	DepositContractAddress []byte `yaml:"DEPOSIT_CONTRACT_ADDRESS"`

	// Networking
	MaxPayloadSize                   uint64            `yaml:"MAX_PAYLOAD_SIZE"`
	MaxRequestBlocks                 uint64            `yaml:"MAX_REQUEST_BLOCKS"`
	EpochsPerSubnetSubscription      uint64            `yaml:"EPOCHS_PER_SUBNET_SUBSCRIPTION"`
	MinEpochsForBlockRequests        uint64            `yaml:"MIN_EPOCHS_FOR_BLOCK_REQUESTS"`
	TtfbTimeout                      uint64            `yaml:"TTFB_TIMEOUT"`
	RespTimeout                      uint64            `yaml:"RESP_TIMEOUT"`
	AttestationPropoagationSlotRange uint64            `yaml:"ATTESTATION_PROPAGATION_SLOT_RANGE"`
	MaximumGossipClockDisparity      uint64            `yaml:"MAXIMUM_GOSSIP_CLOCK_DISPARITY"`
	MessageDomainInvalidSnappy       phase0.DomainType `yaml:"MESSAGE_DOMAIN_INVALID_SNAPPY"`
	MessageDomainValidSnappy         phase0.DomainType `yaml:"MESSAGE_DOMAIN_VALID_SNAPPY"`
	SubnetsPerNode                   uint64            `yaml:"SUBNETS_PER_NODE"`
	AttestationSubnetCount           uint64            `yaml:"ATTESTATION_SUBNET_COUNT"`
	AttestationSubnetExtraBits       uint64            `yaml:"ATTESTATION_SUBNET_EXTRA_BITS"`
	AttestationSubnetPrefixBits      uint64            `yaml:"ATTESTATION_SUBNET_PREFIX_BITS"`

	// Deneb
	MaxRequestBlocksDeneb            uint64 `yaml:"MAX_REQUEST_BLOCKS_DENEB"              check-if-fork:"DenebForkEpoch"`
	MinEpochsForBlobSidecarsRequests uint64 `yaml:"MIN_EPOCHS_FOR_BLOB_SIDECARS_REQUESTS" check-if-fork:"DenebForkEpoch"`
	BlobSidecarSubnetCount           uint64 `yaml:"BLOB_SIDECAR_SUBNET_COUNT"             check-if-fork:"DenebForkEpoch"`
	MaxBlobsPerBlock                 uint64 `yaml:"MAX_BLOBS_PER_BLOCK"                   check-if-fork:"DenebForkEpoch"`
	MaxRequestBlobSidecars           uint64 `yaml:"MAX_REQUEST_BLOB_SIDECARS"             check-if-fork:"DenebForkEpoch"`

	// Electra
	MinPerEpochChurnLimitElectra        uint64 `yaml:"MIN_PER_EPOCH_CHURN_LIMIT_ELECTRA"         check-if-fork:"ElectraForkEpoch"`
	MaxPerEpochActivationExitChurnLimit uint64 `yaml:"MAX_PER_EPOCH_ACTIVATION_EXIT_CHURN_LIMIT" check-if-fork:"ElectraForkEpoch"`
	BlobSidecarSubnetCountElectra       uint64 `yaml:"BLOB_SIDECAR_SUBNET_COUNT_ELECTRA"         check-if-fork:"ElectraForkEpoch"`
	MaxBlobsPerBlockElectra             uint64 `yaml:"MAX_BLOBS_PER_BLOCK_ELECTRA"               check-if-fork:"ElectraForkEpoch"`
	MaxRequestBlobSidecarsElectra       uint64 `yaml:"MAX_REQUEST_BLOB_SIDECARS_ELECTRA"         check-if-fork:"ElectraForkEpoch"`

	// Fulu
	MinEpochsForDataColumnSidecars uint64              `yaml:"MIN_EPOCHS_FOR_DATA_COLUMN_SIDECARS_REQUESTS" check-if-fork:"FuluForkEpoch"`
	DataColumnSidecarSubnetCount   *uint64             `yaml:"DATA_COLUMN_SIDECAR_SUBNET_COUNT"             check-if-fork:"FuluForkEpoch"`
	CustodyRequirement             *uint64             `yaml:"CUSTODY_REQUIREMENT"                          check-if-fork:"FuluForkEpoch"`
	BlobSchedule                   []BlobScheduleEntry `yaml:"BLOB_SCHEDULE"                                check-if-fork:"FuluForkEpoch"`
}

type ChainSpecPreset struct {
	// Presets
	MaxCommitteesPerSlot           uint64 `yaml:"MAX_COMMITTEES_PER_SLOT"`
	TargetCommitteeSize            uint64 `yaml:"TARGET_COMMITTEE_SIZE"`
	MaxValidatorsPerCommittee      uint64 `yaml:"MAX_VALIDATORS_PER_COMMITTEE"`
	ShuffleRoundCount              uint64 `yaml:"SHUFFLE_ROUND_COUNT"`
	HysteresisQuotient             uint64 `yaml:"HYSTERESIS_QUOTIENT"`
	HysteresisDownwardMultiplier   uint64 `yaml:"HYSTERESIS_DOWNWARD_MULTIPLIER"`
	HysteresisUpwardMultiplier     uint64 `yaml:"HYSTERESIS_UPWARD_MULTIPLIER"`
	MinDepositAmount               uint64 `yaml:"MIN_DEPOSIT_AMOUNT"`
	MaxEffectiveBalance            uint64 `yaml:"MAX_EFFECTIVE_BALANCE"`
	EffectiveBalanceIncrement      uint64 `yaml:"EFFECTIVE_BALANCE_INCREMENT"`
	MinAttestationInclusionDelay   uint64 `yaml:"MIN_ATTESTATION_INCLUSION_DELAY"`
	SlotsPerEpoch                  uint64 `yaml:"SLOTS_PER_EPOCH"`
	MinSeedLookahead               uint64 `yaml:"MIN_SEED_LOOKAHEAD"`
	MaxSeedLookahead               uint64 `yaml:"MAX_SEED_LOOKAHEAD"`
	EpochsPerEth1VotingPeriod      uint64 `yaml:"EPOCHS_PER_ETH1_VOTING_PERIOD"`
	SlotsPerHistoricalRoot         uint64 `yaml:"SLOTS_PER_HISTORICAL_ROOT"`
	MinEpochsToInactivityPenalty   uint64 `yaml:"MIN_EPOCHS_TO_INACTIVITY_PENALTY"`
	EpochsPerHistoricalVector      uint64 `yaml:"EPOCHS_PER_HISTORICAL_VECTOR"`
	EpochsPerSlashingVector        uint64 `yaml:"EPOCHS_PER_SLASHINGS_VECTOR"`
	HistoricalRootsLimit           uint64 `yaml:"HISTORICAL_ROOTS_LIMIT"`
	ValidatorRegistryLimit         uint64 `yaml:"VALIDATOR_REGISTRY_LIMIT"`
	BaseRewardFactor               uint64 `yaml:"BASE_REWARD_FACTOR"`
	WhitelistRewardQuotient        uint64 `yaml:"WHISTLEBLOWER_REWARD_QUOTIENT"`
	ProposerRewardQuotient         uint64 `yaml:"PROPOSER_REWARD_QUOTIENT"`
	InactivityPenaltyQuotient      uint64 `yaml:"INACTIVITY_PENALTY_QUOTIENT"`
	MinSlashingPenaltyQuotient     uint64 `yaml:"MIN_SLASHING_PENALTY_QUOTIENT"`
	ProportionalSlashingMultiplier uint64 `yaml:"PROPORTIONAL_SLASHING_MULTIPLIER"`
	MaxProposerSlashings           uint64 `yaml:"MAX_PROPOSER_SLASHINGS"`
	MaxAttesterSlashings           uint64 `yaml:"MAX_ATTESTER_SLASHINGS"`
	MaxAttestations                uint64 `yaml:"MAX_ATTESTATIONS"`
	MaxDeposits                    uint64 `yaml:"MAX_DEPOSITS"`
	MaxVoluntaryExits              uint64 `yaml:"MAX_VOLUNTARY_EXITS"`

	// Altair
	InactivityPenaltyQuotientAltair      uint64 `yaml:"INACTIVITY_PENALTY_QUOTIENT_ALTAIR" check-if-fork:"AltairForkEpoch"`
	MinSlashingPenaltyQuotientAltair     uint64 `yaml:"MIN_SLASHING_PENALTY_QUOTIENT_ALTAIR" check-if-fork:"AltairForkEpoch"`
	ProportionalSlashingMultiplierAltair uint64 `yaml:"PROPORTIONAL_SLASHING_MULTIPLIER_ALTAIR" check-if-fork:"AltairForkEpoch"`
	SyncCommitteeSize                    uint64 `yaml:"SYNC_COMMITTEE_SIZE" check-if-fork:"AltairForkEpoch"`
	EpochsPerSyncCommitteePeriod         uint64 `yaml:"EPOCHS_PER_SYNC_COMMITTEE_PERIOD" check-if-fork:"AltairForkEpoch"`
	MinSyncCommitteeParticipants         uint64 `yaml:"MIN_SYNC_COMMITTEE_PARTICIPANTS" check-if-fork:"AltairForkEpoch"`
	UpdateTimeout                        uint64 `yaml:"UPDATE_TIMEOUT" check-if-fork:"AltairForkEpoch"`

	// Bellatrix
	InactivityPenaltyQuotientBellatrix      uint64 `yaml:"INACTIVITY_PENALTY_QUOTIENT_BELLATRIX" check-if-fork:"BellatrixForkEpoch"`
	MinSlashingPenaltyQuotientBellatrix     uint64 `yaml:"MIN_SLASHING_PENALTY_QUOTIENT_BELLATRIX" check-if-fork:"BellatrixForkEpoch"`
	ProportionalSlashingMultiplierBellatrix uint64 `yaml:"PROPORTIONAL_SLASHING_MULTIPLIER_BELLATRIX" check-if-fork:"BellatrixForkEpoch"`
	MaxBytesPerTransaction                  uint64 `yaml:"MAX_BYTES_PER_TRANSACTION" check-if-fork:"BellatrixForkEpoch"`
	MaxTransactionsPerPayload               uint64 `yaml:"MAX_TRANSACTIONS_PER_PAYLOAD" check-if-fork:"BellatrixForkEpoch"`
	BytesPerLogsBloom                       uint64 `yaml:"BYTES_PER_LOGS_BLOOM" check-if-fork:"BellatrixForkEpoch"`
	MaxExtraDataBytes                       uint64 `yaml:"MAX_EXTRA_DATA_BYTES" check-if-fork:"BellatrixForkEpoch"`

	// Capella
	MaxBlsToExecutionChanges         uint64 `yaml:"MAX_BLS_TO_EXECUTION_CHANGES" check-if-fork:"CapellaForkEpoch"`
	MaxWithdrawalsPerPayload         uint64 `yaml:"MAX_WITHDRAWALS_PER_PAYLOAD" check-if-fork:"CapellaForkEpoch"`
	MaxValidatorsPerWithdrawalsSweep uint64 `yaml:"MAX_VALIDATORS_PER_WITHDRAWALS_SWEEP" check-if-fork:"CapellaForkEpoch"`

	// Deneb
	MaxBlobCommitmentsPerBlock       uint64 `yaml:"MAX_BLOB_COMMITMENTS_PER_BLOCK" check-if-fork:"DenebForkEpoch"`
	KzgCommitmentInclusionProofDepth uint64 `yaml:"KZG_COMMITMENT_INCLUSION_PROOF_DEPTH" check-if-fork:"DenebForkEpoch"`
	FieldElementsPerBlob             uint64 `yaml:"FIELD_ELEMENTS_PER_BLOB" check-if-fork:"DenebForkEpoch"`

	// Electra
	MinActivationBalance                  uint64 `yaml:"MIN_ACTIVATION_BALANCE" check-if-fork:"ElectraForkEpoch"`
	MaxEffectiveBalanceElectra            uint64 `yaml:"MAX_EFFECTIVE_BALANCE_ELECTRA" check-if-fork:"ElectraForkEpoch"`
	MinSlashingPenaltyQuotientElectra     uint64 `yaml:"MIN_SLASHING_PENALTY_QUOTIENT_ELECTRA" check-if-fork:"ElectraForkEpoch"`
	WhistleblowerRewardQuotientElectra    uint64 `yaml:"WHISTLEBLOWER_REWARD_QUOTIENT_ELECTRA" check-if-fork:"ElectraForkEpoch"`
	PendingDepositsLimit                  uint64 `yaml:"PENDING_DEPOSITS_LIMIT" check-if-fork:"ElectraForkEpoch"`
	PendingPartialWithdrawalsLimit        uint64 `yaml:"PENDING_PARTIAL_WITHDRAWALS_LIMIT" check-if-fork:"ElectraForkEpoch"`
	PendingConsolidationsLimit            uint64 `yaml:"PENDING_CONSOLIDATIONS_LIMIT" check-if-fork:"ElectraForkEpoch"`
	MaxAttesterSlashingsElectra           uint64 `yaml:"MAX_ATTESTER_SLASHINGS_ELECTRA" check-if-fork:"ElectraForkEpoch"`
	MaxAttestationsElectra                uint64 `yaml:"MAX_ATTESTATIONS_ELECTRA" check-if-fork:"ElectraForkEpoch"`
	MaxDepositRequestsPerPayload          uint64 `yaml:"MAX_DEPOSIT_REQUESTS_PER_PAYLOAD" check-if-fork:"ElectraForkEpoch"`
	MaxWithdrawalRequestsPerPayload       uint64 `yaml:"MAX_WITHDRAWAL_REQUESTS_PER_PAYLOAD" check-if-fork:"ElectraForkEpoch"`
	MaxConsolidationRequestsPerPayload    uint64 `yaml:"MAX_CONSOLIDATION_REQUESTS_PER_PAYLOAD" check-if-fork:"ElectraForkEpoch"`
	MaxPendingPartialsPerWithdrawalsSweep uint64 `yaml:"MAX_PENDING_PARTIALS_PER_WITHDRAWALS_SWEEP" check-if-fork:"ElectraForkEpoch"`
	MaxPendingDepositsPerEpoch            uint64 `yaml:"MAX_PENDING_DEPOSITS_PER_EPOCH" check-if-fork:"ElectraForkEpoch"`

	// Fulu
	KzgCommitmentsInclusionProofDepth uint64  `yaml:"KZG_COMMITMENTS_INCLUSION_PROOF_DEPTH" check-if-fork:"FuluForkEpoch"`
	FieldElementsPerCell              uint64  `yaml:"FIELD_ELEMENTS_PER_CELL" check-if-fork:"FuluForkEpoch"`
	FieldElementsPerExtBlob           uint64  `yaml:"FIELD_ELEMENTS_PER_EXT_BLOB" check-if-fork:"FuluForkEpoch"`
	CellsPerExtBlob                   uint64  `yaml:"CELLS_PER_EXT_BLOB" check-if-fork:"FuluForkEpoch"`
	NumberOfColumns                   *uint64 `yaml:"NUMBER_OF_COLUMNS" check-if-fork:"FuluForkEpoch"`
}

type ChainSpecDomainTypes struct {
	DomainBeaconProposer              phase0.DomainType `yaml:"DOMAIN_BEACON_PROPOSER"`
	DomainBeaconAttester              phase0.DomainType `yaml:"DOMAIN_BEACON_ATTESTER"`
	DomainRandao                      phase0.DomainType `yaml:"DOMAIN_RANDAO"`
	DomainDeposit                     phase0.DomainType `yaml:"DOMAIN_DEPOSIT"`
	DomainVoluntaryExit               phase0.DomainType `yaml:"DOMAIN_VOLUNTARY_EXIT"`
	DomainSelectionProof              phase0.DomainType `yaml:"DOMAIN_SELECTION_PROOF"`
	DomainAggregateAndProof           phase0.DomainType `yaml:"DOMAIN_AGGREGATE_AND_PROOF"`
	DomainSyncCommittee               phase0.DomainType `yaml:"DOMAIN_SYNC_COMMITTEE"`
	DomainSyncCommitteeSelectionProof phase0.DomainType `yaml:"DOMAIN_SYNC_COMMITTEE_SELECTION_PROOF"`
	DomainContributionAndProof        phase0.DomainType `yaml:"DOMAIN_CONTRIBUTION_AND_PROOF"`
	DomainBlsToExecutionChange        phase0.DomainType `yaml:"DOMAIN_BLS_TO_EXECUTION_CHANGE"`
}

type ChainSpec struct {
	ChainSpecConfig      `yaml:",inline"`
	ChainSpecPreset      `yaml:",inline"`
	ChainSpecDomainTypes `yaml:",inline"`
}

var byteType = reflect.TypeOf(byte(0))
var specExpressionCache = map[string]*govaluate.EvaluableExpression{}
var specExpressionCacheMutex sync.Mutex

func (chain *ChainSpec) ParseAdditive(values map[string]interface{}) error {
	valuesYaml, err := yaml.Marshal(values)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(valuesYaml, &chain.ChainSpecConfig)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(valuesYaml, &chain.ChainSpecPreset)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(valuesYaml, &chain.ChainSpecDomainTypes)
	if err != nil {
		return err
	}

	return nil
}

type SpecMismatch struct {
	Name     string
	Severity string
}

func (chain *ChainSpec) CheckMismatch(chain2 *ChainSpec) ([]SpecMismatch, error) {
	mismatches := []SpecMismatch{}

	genericSpecValues := map[string]any{}
	specData, err := json.Marshal(chain)
	if err != nil {
		return nil, fmt.Errorf("error marshalling chain spec: %v", err)
	}
	err = json.Unmarshal(specData, &genericSpecValues)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling chain spec: %v", err)
	}

	compareSpecs := func(chain any, chain2 any) error {
		chainT := reflect.ValueOf(chain).Elem()
		chain2T := reflect.ValueOf(chain2).Elem()

		for i := 0; i < chainT.NumField(); i++ {
			fieldT := chainT.Type().Field(i)

			// Check both types of conditions
			checkIfExpression := fieldT.Tag.Get("check-if")
			checkIfFork := fieldT.Tag.Get("check-if-fork")
			checkSeverity := fieldT.Tag.Get("check-severity")

			if checkSeverity == "" {
				checkSeverity = "error"
			}

			if checkIfFork != "" {
				checkIfExpression = fmt.Sprintf("(%s ?? 18446744073709551615) < 18446744073709551615", checkIfFork)
			}

			if checkIfExpression != "" {
				ok, err := specCheckIf(checkIfExpression, genericSpecValues)
				if err != nil {
					return fmt.Errorf("error checking if expression: %v", err)
				}
				if !ok {
					continue
				}
			}

			fieldV := chainT.Field(i)
			field2V := chain2T.Field(i)

			if fieldV.Type().Kind() == reflect.Ptr {
				if !fieldV.IsNil() {
					fieldV = fieldV.Elem()
				}
				if !field2V.IsNil() {
					field2V = field2V.Elem()
				}
			}

			if fieldV.Type().Kind() == reflect.Slice && fieldV.Type().Elem() == byteType {
				// compare byte slices
				bytesA := fieldV.Interface().([]byte)
				bytesB := field2V.Interface().([]byte)

				if !bytes.Equal(bytesA, bytesB) {
					mismatches = append(mismatches, SpecMismatch{
						Name:     chainT.Type().Field(i).Name,
						Severity: checkSeverity,
					})
				}
			} else if fieldV.Type().Kind() == reflect.Slice && fieldV.Type().Elem() == reflect.TypeOf(BlobScheduleEntry{}) {
				// compare blob schedule entries
				blobScheduleA := fieldV.Interface().([]BlobScheduleEntry)
				blobScheduleB := field2V.Interface().([]BlobScheduleEntry)

				// sort both by epoch
				sort.Slice(blobScheduleA, func(i, j int) bool {
					return blobScheduleA[i].Epoch < blobScheduleA[j].Epoch
				})
				sort.Slice(blobScheduleB, func(i, j int) bool {
					return blobScheduleB[i].Epoch < blobScheduleB[j].Epoch
				})

				// compare each entry
				for i := range blobScheduleA {
					if len(blobScheduleB) > i && blobScheduleA[i] != blobScheduleB[i] {
						mismatches = append(mismatches, SpecMismatch{
							Name:     fmt.Sprintf("%s[%d]", fieldT.Name, i),
							Severity: checkSeverity,
						})
						break
					}
				}
			} else if fieldV.Interface() != field2V.Interface() {
				if chainT.Field(i).Interface() == reflect.Zero(chainT.Field(i).Type()).Interface() {
					// 0 value on chain side are allowed
					continue
				}
				mismatches = append(mismatches, SpecMismatch{
					Name:     chainT.Type().Field(i).Name,
					Severity: checkSeverity,
				})
			}
		}

		return nil
	}

	err = compareSpecs(&chain.ChainSpecConfig, &chain2.ChainSpecConfig)
	if err != nil {
		return nil, err
	}

	err = compareSpecs(&chain.ChainSpecPreset, &chain2.ChainSpecPreset)
	if err != nil {
		return nil, err
	}

	err = compareSpecs(&chain.ChainSpecDomainTypes, &chain2.ChainSpecDomainTypes)
	if err != nil {
		return nil, err
	}

	return mismatches, nil
}

func specCheckIf(expressionStr string, genericSpecValues map[string]any) (bool, error) {
	specExpressionCacheMutex.Lock()
	expression, ok := specExpressionCache[expressionStr]
	if !ok {
		var err error
		expression, err = govaluate.NewEvaluableExpression(expressionStr)
		if err != nil {
			specExpressionCacheMutex.Unlock()
			return false, fmt.Errorf("error parsing dynamic spec expression: %v", err)
		}

		specExpressionCache[expressionStr] = expression
	}
	specExpressionCacheMutex.Unlock()

	result, err := expression.Evaluate(genericSpecValues)
	if err != nil {
		return false, fmt.Errorf("error evaluating dynamic spec expression: %v", err)
	}

	value, ok := result.(bool)
	if ok {
		return value, nil
	}

	return false, nil
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
