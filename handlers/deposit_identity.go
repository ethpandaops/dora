package handlers

import (
	"strings"

	v1 "github.com/ethpandaops/go-eth2-client/api/v1"
	"github.com/ethpandaops/go-eth2-client/spec/gloas"
	"github.com/ethpandaops/go-eth2-client/spec/phase0"

	"github.com/ethpandaops/dora/services"
)

// depositPubkeyOwner is the on-chain entity a deposit's public key belongs to. At most one of
// the two is set; both are nil while the deposit has not been applied to anything yet.
type depositPubkeyOwner struct {
	Validator      *v1.Validator
	ValidatorIndex phase0.ValidatorIndex
	IsProjected    bool

	Builder      *gloas.Builder
	BuilderIndex gloas.BuilderIndex
}

// resolveDepositPubkeyOwner resolves a deposit's public key to the validator - or, failing
// that, the builder - it belongs to, for rendering the "Validator" column of the deposit pages.
//
// A pubkey only counts as a validator when the entry it points at can actually be loaded:
// GetValidatorIndexByPubkey also resolves validators that are merely projected from the
// pending deposit queue, and an index whose projection has gone away must not be rendered as
// a link to a validator page that cannot be opened.
//
// Builders share the validator deposit contract's pubkey space - a builder-credential deposit
// made before the Gloas fork onboards a builder rather than a validator - so a pubkey that is
// no validator is looked up in the builder registry before giving up.
func resolveDepositPubkeyOwner(pubkey phase0.BLSPubKey) depositPubkeyOwner {
	owner := depositPubkeyOwner{}

	if index, found := services.GlobalBeaconService.GetValidatorIndexByPubkey(pubkey); found {
		if validator := services.GlobalBeaconService.GetValidatorByIndex(index, false); validator != nil {
			owner.Validator = validator
			owner.ValidatorIndex = index
			owner.IsProjected = services.GlobalBeaconService.IsProjectedValidatorIndex(index)

			return owner
		}
	}

	if index, found := services.GlobalBeaconService.GetBuilderIndexByPubkey(pubkey); found {
		if builder := services.GlobalBeaconService.GetBuilderByIndex(index); builder != nil {
			owner.Builder = builder
			owner.BuilderIndex = index
		}
	}

	return owner
}

// depositValidatorStatus maps a validator state to the label the deposit pages show, together
// with whether the row should render the liveness (upcheck) indicator.
func depositValidatorStatus(validator *v1.Validator) (status string, showUpcheck bool) {
	switch {
	case strings.HasPrefix(validator.Status.String(), "pending"):
		return "Pending", false
	case validator.Status == v1.ValidatorStateActiveOngoing:
		return "Active", true
	case validator.Status == v1.ValidatorStateActiveExiting:
		return "Exiting", true
	case validator.Status == v1.ValidatorStateActiveSlashed:
		return "Slashed", true
	case validator.Status == v1.ValidatorStateExitedUnslashed:
		return "Exited", false
	case validator.Status == v1.ValidatorStateExitedSlashed:
		return "Slashed", false
	default:
		return validator.Status.String(), false
	}
}

// depositBuilderStatus maps a builder's state to the label the deposit pages show.
func depositBuilderStatus(builder *gloas.Builder, currentEpoch phase0.Epoch) string {
	if builder.WithdrawableEpoch <= currentEpoch {
		return "Exited"
	}

	return "Active"
}
