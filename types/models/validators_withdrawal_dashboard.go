package models

type ValidatorsWithdrawalDashboardPageData struct {
	Query             string `json:"query"`
	NormalizedAddress []byte `json:"normalized_address"`
	Credentials       []byte `json:"credentials"`
	HasQuery          bool   `json:"has_query"`
	Error             string `json:"error"`

	ValidatorCount           uint64  `json:"validator_count"`
	DisplayedValidatorCount  uint64  `json:"displayed_validator_count"`
	ActiveCount              uint64  `json:"active_count"`
	OnlineCount              uint64  `json:"online_count"`
	OfflineCount             uint64  `json:"offline_count"`
	PendingTotalCount        uint64  `json:"pending_total_count"`
	PendingCount             uint64  `json:"pending_count"`
	QueuedDepositCount       uint64  `json:"queued_deposit_count"`
	ExitingCount             uint64  `json:"exiting_count"`
	ExitedCount              uint64  `json:"exited_count"`
	WithdrawableCount        uint64  `json:"withdrawable_count"`
	WithdrawnCount           uint64  `json:"withdrawn_count"`
	SlashedCount             uint64  `json:"slashed_count"`
	TotalBalance             uint64  `json:"total_balance"`
	TotalEffectiveBalance    uint64  `json:"total_effective_balance"`
	OnlineRate               float64 `json:"online_rate"`
	OfflineRate              float64 `json:"offline_rate"`
	ParticipationRate        float64 `json:"participation_rate"`
	ParticipationLoading     bool    `json:"participation_loading"`
	ParticipationLoadingPct  float64 `json:"participation_loading_pct"`
	ParticipationLoadingText string  `json:"participation_loading_text"`
	HealthStatus             string  `json:"health_status"`

	ValidatorsLink string `json:"validators_link"`
	ActivityLink   string `json:"activity_link"`

	Validators []*ValidatorsWithdrawalDashboardValidator `json:"validators"`
}

type ValidatorsWithdrawalDashboardValidator struct {
	Index             uint64  `json:"index"`
	Name              string  `json:"name"`
	PublicKey         []byte  `json:"pubkey"`
	Balance           uint64  `json:"balance"`
	EffectiveBalance  uint64  `json:"effective_balance"`
	State             string  `json:"state"`
	Liveness          uint8   `json:"liveness"`
	LivenessMax       uint8   `json:"liveness_max"`
	LivenessPercent   float64 `json:"liveness_percent"`
	ShowLiveness      bool    `json:"show_liveness"`
	WithdrawalAddress []byte  `json:"withdrawal_address"`
}
