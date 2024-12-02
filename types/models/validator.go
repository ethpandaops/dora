package models

import (
	"time"
)

// ValidatorPageData is a struct to hold info for the validator page
type ValidatorPageData struct {
	CurrentEpoch        uint64    `json:"current_epoch"`
	Index               uint64    `json:"index"`
	Name                string    `json:"name"`
	PublicKey           []byte    `json:"pubkey"`
	Balance             uint64    `json:"balance"`
	EffectiveBalance    uint64    `json:"eff_balance"`
	State               string    `json:"state"`
	BeaconState         string    `json:"beacon_state"`
	ShowEligible        bool      `json:"show_eligible"`
	EligibleTs          time.Time `json:"eligible_ts"`
	EligibleEpoch       uint64    `json:"eligible_epoch"`
	ShowActivation      bool      `json:"show_activation"`
	ActivationTs        time.Time `json:"activation_ts"`
	ActivationEpoch     uint64    `json:"activation_epoch"`
	IsActive            bool      `json:"is_active"`
	WasActive           bool      `json:"was_active"`
	UpcheckActivity     uint8     `json:"upcheck_act"`
	UpcheckMaximum      uint8     `json:"upcheck_max"`
	ShowExit            bool      `json:"show_exit"`
	ExitTs              time.Time `json:"exit_ts"`
	ExitEpoch           uint64    `json:"exit_epoch"`
	WithdrawCredentials []byte    `json:"withdraw_credentials"`
	ShowWithdrawAddress bool      `json:"show_withdraw_address"`
	WithdrawAddress     []byte    `json:"withdraw_address"`

	TabView         string `json:"tab_view"`
	ElectraIsActive bool   `json:"electra_is_active"`

	RecentBlocks                        []*ValidatorPageDataBlock         `json:"recent_blocks"`
	RecentBlockCount                    uint64                            `json:"recent_block_count"`
	RecentAttestations                  []*ValidatorPageDataAttestation   `json:"recent_attestations"`
	RecentAttestationCount              uint64                            `json:"recent_attestation_count"`
	RecentDeposits                      []*ValidatorPageDataDeposit       `json:"recent_deposits"`
	RecentDepositCount                  uint64                            `json:"recent_deposit_count"`
	AdditionalInitiatedDepositCount     uint64                            `json:"additional_initiated_deposit_count"`
	AdditionalIncludedDepositCount      uint64                            `json:"additional_included_deposit_count"`
	ConsolidationRequests               []*ValidatorPageDataConsolidation `json:"consolidation_requests"`
	ConsolidationRequestCount           uint64                            `json:"consolidation_request_count"`
	AdditionalConsolidationRequestCount uint64                            `json:"additional_consolidation_request_count"`
	WithdrawalRequests                  []*ValidatorPageDataWithdrawal    `json:"withdrawal_requests"`
	WithdrawalRequestCount              uint64                            `json:"withdrawal_request_count"`
	AdditionalWithdrawalRequestCount    uint64                            `json:"additional_withdrawal_request_count"`
}

type ValidatorPageDataBlock struct {
	Epoch        uint64    `json:"epoch"`
	Slot         uint64    `json:"slot"`
	WithEthBlock bool      `json:"with_eth_block"`
	EthBlock     uint64    `json:"eth_block"`
	Ts           time.Time `json:"ts"`
	Status       uint64    `json:"status"`
	BlockRoot    string    `json:"block_root"`
	Graffiti     []byte    `json:"graffiti"`
}

type ValidatorPageDataAttestation struct {
	Epoch          uint64    `json:"epoch"`
	Time           time.Time `json:"time"`
	Status         uint64    `json:"status"`
	Missed         bool      `json:"missed"`
	HasDuty        bool      `json:"has_duty"`
	Scheduled      bool      `json:"scheduled"`
	Slot           uint64    `json:"slot"`
	InclusionSlot  uint64    `json:"inclusion_slot"`
	InclusionRoot  []byte    `json:"inclusion_root"`
	InclusionDelay uint64    `json:"inclusion_delay"`
}

type ValidatorPageDataDeposit struct {
	IsIncluded      bool                               `json:"is_included"`
	HasIndex        bool                               `json:"has_index"`
	Index           uint64                             `json:"index"`
	SlotRoot        []byte                             `json:"slot_root"`
	Slot            uint64                             `json:"slot"`
	Time            time.Time                          `json:"time"`
	Amount          uint64                             `json:"amount"`
	WithdrawalCreds []byte                             `json:"withdrawal_creds"`
	Status          uint64                             `json:"status"`
	TxStatus        uint64                             `json:"tx_status"`
	TxDetails       *ValidatorPageDataDepositTxDetails `json:"tx_details"`
	TxHash          []byte                             `json:"tx_hash"`
}

type ValidatorPageDataDepositTxDetails struct {
	BlockNumber uint64 `json:"block"`
	BlockHash   string `json:"block_hash"`
	BlockTime   uint64 `json:"block_time"`
	TxOrigin    string `json:"tx_origin"`
	TxTarget    string `json:"tx_target"`
	TxHash      string `json:"tx_hash"`
}

type ValidatorPageDataConsolidation struct {
	IsIncluded           bool      `json:"is_included"`
	SlotNumber           uint64    `json:"slot"`
	SlotRoot             []byte    `json:"slot_root"`
	Time                 time.Time `json:"time"`
	Status               uint64    `json:"status"`
	TxStatus             uint64    `json:"tx_status"`
	SourceAddr           []byte    `json:"src_addr"`
	SourceValidatorValid bool      `json:"src_vvalid"`
	SourceValidatorIndex uint64    `json:"src_vindex"`
	SourceValidatorName  string    `json:"src_vname"`
	SourcePublicKey      []byte    `json:"src_pubkey"`
	TargetValidatorValid bool      `json:"tgt_vvalid"`
	TargetValidatorIndex uint64    `json:"tgt_vindex"`
	TargetValidatorName  string    `json:"tgt_vname"`
	TargetPublicKey      []byte    `json:"tgt_pubkey"`
	LinkedTransaction    bool      `json:"linked_tx"`
	TransactionHash      []byte    `json:"tx_hash"`

	TransactionDetails *ValidatorPageDataConsolidationTxDetails `json:"tx_details"`
}

type ValidatorPageDataConsolidationTxDetails struct {
	BlockNumber uint64 `json:"block"`
	BlockHash   string `json:"block_hash"`
	BlockTime   uint64 `json:"block_time"`
	TxOrigin    string `json:"tx_origin"`
	TxTarget    string `json:"tx_target"`
	TxHash      string `json:"tx_hash"`
}

type ValidatorPageDataWithdrawal struct {
	IsIncluded        bool      `json:"is_included"`
	SlotNumber        uint64    `json:"slot"`
	SlotRoot          []byte    `json:"slot_root"`
	Time              time.Time `json:"time"`
	Status            uint64    `json:"status"`
	TxStatus          uint64    `json:"tx_status"`
	SourceAddr        []byte    `json:"source_addr"`
	Amount            uint64    `json:"amount"`
	ValidatorValid    bool      `json:"vvalid"`
	ValidatorIndex    uint64    `json:"vindex"`
	ValidatorName     string    `json:"vname"`
	PublicKey         []byte    `json:"pubkey"`
	LinkedTransaction bool      `json:"linked_tx"`
	TransactionHash   []byte    `json:"tx_hash"`

	TransactionDetails *ValidatorPageDataWithdrawalTxDetails `json:"tx_details"`
}

type ValidatorPageDataWithdrawalTxDetails struct {
	BlockNumber uint64 `json:"block"`
	BlockHash   string `json:"block_hash"`
	BlockTime   uint64 `json:"block_time"`
	TxOrigin    string `json:"tx_origin"`
	TxTarget    string `json:"tx_target"`
	TxHash      string `json:"tx_hash"`
}
