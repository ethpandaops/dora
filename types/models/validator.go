package models

import (
	"time"
)

// ValidatorPageData is a struct to hold info for the validator page
type ValidatorPageData struct {
	Index               uint64    `json:"index"`
	Name                string    `json:"name"`
	PublicKey           []byte    `json:"pubkey"`
	Balance             uint64    `json:"balance"`
	EffectiveBalance    uint64    `json:"eff_balance"`
	State               string    `json:"state"`
	ShowUpcheck         bool      `json:"show_upcheck"`
	UpcheckState        uint8     `json:"upcheck_state"`
	ShowActivation      bool      `json:"show_activation"`
	ActivationTs        time.Time `json:"activation_ts"`
	ActivationEpoch     uint64    `json:"activation_epoch"`
	ShowExit            bool      `json:"show_exit"`
	ExitTs              time.Time `json:"exit_ts"`
	ExitEpoch           uint64    `json:"exit_epoch"`
	ShowWithdrawAddress bool      `json:"show_withdraw_address"`
	WithdrawAddress     []byte    `json:"withdraw_address"`
}
