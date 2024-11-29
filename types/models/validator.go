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

	TabView string `json:"tab_view"`

	RecentBlocks           []*ValidatorPageDataBlock       `json:"recent_blocks"`
	RecentBlockCount       uint64                          `json:"recent_block_count"`
	RecentAttestations     []*ValidatorPageDataAttestation `json:"recent_attestations"`
	RecentAttestationCount uint64                          `json:"recent_attestation_count"`
	RecentDeposits         []*ValidatorPageDataDeposit     `json:"recent_deposits"`
	RecentDepositCount     uint64                          `json:"recent_deposit_count"`
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
	SlotRoot  string                             `json:"slot_root"`
	Slot      uint64                             `json:"slot"`
	Amount    uint64                             `json:"amount"`
	Time      time.Time                          `json:"time"`
	Status    uint64                             `json:"status"`
	TxDetails *ValidatorPageDataDepositTxDetails `json:"tx_details"`
}

type ValidatorPageDataDepositTxDetails struct {
	BlockNumber uint64 `json:"block"`
	BlockHash   string `json:"block_hash"`
	BlockTime   uint64 `json:"block_time"`
	TxOrigin    string `json:"tx_origin"`
	TxTarget    string `json:"tx_target"`
	TxHash      string `json:"tx_hash"`
}
