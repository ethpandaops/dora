package models

import (
	"time"
)

// BuilderDepositsPageData is a struct to hold info for the builder deposits page
type BuilderDepositsPageData struct {
	FilterMinSlot   uint64 `json:"filter_mins"`
	FilterMaxSlot   uint64 `json:"filter_maxs"`
	FilterPubKey    string `json:"filter_pubkey"`
	FilterMinIndex  uint64 `json:"filter_mini"`
	FilterMaxIndex  uint64 `json:"filter_maxi"`
	FilterMinAmount uint64 `json:"filter_mina"`
	FilterMaxAmount uint64 `json:"filter_maxa"`

	Deposits     []*BuilderDepositsPageDataDeposit `json:"deposits"`
	DepositCount uint64                            `json:"deposit_count"`
	FirstIndex   uint64                            `json:"first_index"`
	LastIndex    uint64                            `json:"last_index"`

	// Pre-Gloas projection: before the fork the real builder_deposits table is empty, so the page
	// instead shows the builders projected to be onboarded from the pending deposit queue at the fork.
	IsProjection                  bool      `json:"is_projection"`
	ProjectionTruncated           bool      `json:"projection_truncated"`
	GloasForkEpoch                uint64    `json:"gloas_fork_epoch"`
	GloasForkTime                 time.Time `json:"gloas_fork_time"`
	OnboardedNewCount             uint64    `json:"onboarded_new_count"`
	OnboardedTopUpCount           uint64    `json:"onboarded_topup_count"`
	TooEarlyCount                 uint64    `json:"too_early_count"`
	InvalidSignatureCount         uint64    `json:"invalid_signature_count"`
	KeptAsValidatorCount          uint64    `json:"kept_as_validator_count"`
	TotalQueueProcessedBeforeFork uint64    `json:"total_queue_processed_before_fork"`

	// "Is it safe to deposit right now" indicator (projection mode only).
	HasSafetyEstimate       bool      `json:"has_safety_estimate"`
	DepositSafe             bool      `json:"deposit_safe"`
	NewDepositEstimateEpoch uint64    `json:"new_deposit_estimate_epoch"`
	NewDepositEstimateTime  time.Time `json:"new_deposit_estimate_time"`

	IsDefaultPage    bool   `json:"default_page"`
	TotalPages       uint64 `json:"total_pages"`
	PageSize         uint64 `json:"page_size"`
	CurrentPageIndex uint64 `json:"page_index"`
	PrevPageIndex    uint64 `json:"prev_page_index"`
	NextPageIndex    uint64 `json:"next_page_index"`
	LastPageIndex    uint64 `json:"last_page_index"`

	FirstPageLink string `json:"first_page_link"`
	PrevPageLink  string `json:"prev_page_link"`
	NextPageLink  string `json:"next_page_link"`
	LastPageLink  string `json:"last_page_link"`

	UrlParams []UrlParam `json:"url_params"`

	EnsNameData
}

type BuilderDepositsPageDataDeposit struct {
	IsIncluded            bool                             `json:"is_included"` // included in a block (CL request) vs pending tx only
	SlotNumber            uint64                           `json:"slot"`
	SlotRoot              []byte                           `json:"slot_root" ssz-size:"32"`
	Time                  time.Time                        `json:"time"`
	Orphaned              bool                             `json:"orphaned"`
	PublicKey             []byte                           `json:"pubkey" ssz-size:"48"`
	WithdrawalCredentials []byte                           `json:"wdcreds" ssz-size:"32"`
	Amount                uint64                           `json:"amount"`
	HasBuilderIndex       bool                             `json:"has_builder_index"`
	BuilderIndex          uint64                           `json:"builder_index"`
	IsInactiveBuilder     bool                             `json:"is_inactive_builder"` // pubkey is a known builder but its index was reused
	IsOnboarding          bool                             `json:"is_onboarding"`       // onboarded from the pending deposit queue at the Gloas fork transition
	Result                uint8                            `json:"result"`
	HasTransaction        bool                             `json:"has_transaction"`
	TransactionHash       []byte                           `json:"tx_hash" ssz-size:"32"`
	TransactionDetails    *BuilderPageDataDepositTxDetails `json:"tx_details"`
	TransactionOrphaned   bool                             `json:"tx_orphaned"`
	BlockNumber           uint64                           `json:"block_number"`

	// Pre-Gloas projection fields (set when the parent page is in projection mode).
	IsProjected               bool      `json:"is_projected"`
	HasDepositIndex           bool      `json:"has_deposit_index"` // EL deposit index of the deposit
	DepositIndex              uint64    `json:"deposit_index"`
	EstimatedTime             time.Time `json:"estimated_time"`              // when the deposit is projected to be processed
	IsQueued                  bool      `json:"is_queued"`                   // found in the current pending deposit queue snapshot
	QueuePosition             uint64    `json:"queue_position"`              // position in the queue (when IsQueued)
	ProjectedOnboarded        bool      `json:"projected_onboarded"`         // onboarded as a builder at the fork
	ProjectedTooEarly         bool      `json:"projected_too_early"`         // processed before the fork -> becomes a validator
	ProjectedAlreadyProcessed bool      `json:"projected_already_processed"` // already applied (refinement of too-early)
	ProjectedKeptAsValidator  bool      `json:"projected_kept_as_validator"` // shares a pubkey with a validator deposit
	ProjectedInvalidSignature bool      `json:"projected_invalid_signature"` // dropped at onboarding (bad proof-of-possession)
}
