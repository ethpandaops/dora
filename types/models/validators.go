package models

import (
	"time"
)

// ValidatorsPageData is a struct to hold info for the validators page
type ValidatorsPageData struct {
	Validators     []*ValidatorsPageDataValidator `json:"validators"`
	ValidatorCount uint64                         `json:"validator_count"`
	FirstValidator uint64                         `json:"first_validx"`
	LastValidator  uint64                         `json:"last_validx"`
	StateFilter    string                         `json:"state_filter"`

	IsDefaultPage     bool   `json:"default_page"`
	TotalPages        uint64 `json:"total_pages"`
	PageSize          uint64 `json:"page_size"`
	CurrentPageIndex  uint64 `json:"page_index"`
	CurrentPageValIdx uint64 `json:"page_validx"`
	PrevPageIndex     uint64 `json:"prev_page_index"`
	PrevPageValIdx    uint64 `json:"prev_page_validx"`
	NextPageIndex     uint64 `json:"next_page_index"`
	NextPageValIdx    uint64 `json:"next_page_validx"`
	LastPageValIdx    uint64 `json:"last_page_validx"`
}

type ValidatorsPageDataValidator struct {
	Index               uint64    `json:"index"`
	Name                string    `json:"name"`
	PublicKey           []byte    `json:"pubkey"`
	Balance             uint64    `json:"balance"`
	EffectiveBalance    uint64    `json:"eff_balance"`
	State               string    `json:"state"`
	ShowUpcheck         bool      `json:"show_upcheck"`
	UpcheckActivity     uint8     `json:"upcheck_act"`
	UpcheckMaximum      uint8     `json:"upcheck_max"`
	ShowActivation      bool      `json:"show_activation"`
	ActivationTs        time.Time `json:"activation_ts"`
	ActivationEpoch     uint64    `json:"activation_epoch"`
	ShowExit            bool      `json:"show_exit"`
	ExitTs              time.Time `json:"exit_ts"`
	ExitEpoch           uint64    `json:"exit_epoch"`
	ShowWithdrawAddress bool      `json:"show_withdraw_address"`
	WithdrawAddress     []byte    `json:"withdraw_address"`
}
