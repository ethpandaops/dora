package models

type ValidatorsSummaryPageData struct {
	ClientMatrix       [][]ValidatorsSummaryMatrixCell `json:"client_matrix"`
	ExecutionClients   []string                        `json:"execution_clients"`
	ConsensusClients   []string                        `json:"consensus_clients"`
	TotalValidators    uint64                          `json:"total_validators"`
	TotalEffectiveETH  uint64                          `json:"total_effective_eth"`
	OverallHealthy     uint64                          `json:"overall_healthy"`
	ClientBreakdown    []ValidatorsSummaryClientBreak  `json:"client_breakdown"`
	NetworkHealthScore float64                         `json:"network_health_score"`
	AvgInclusionDelay  float64                         `json:"avg_inclusion_delay"`
	HasInclusionData   bool                            `json:"has_inclusion_data"`
	ProposalRate       float64                         `json:"proposal_rate"`
	ProposalsExpected  uint64                          `json:"proposals_expected"`
	ProposalsProposed  uint64                          `json:"proposals_proposed"`
	HasProposalData    bool                            `json:"has_proposal_data"`
	PtcInclusionRate   float64                         `json:"ptc_inclusion_rate"`
	PtcVotesExpected   uint64                          `json:"ptc_votes_expected"`
	PtcVotesIncluded   uint64                          `json:"ptc_votes_included"`
	HasPtcData         bool                            `json:"has_ptc_data"`
}

type ValidatorsSummaryMatrixCell struct {
	ExecutionClient         string  `json:"execution_client"`
	ConsensusClient         string  `json:"consensus_client"`
	ValidatorCount          uint64  `json:"validator_count"`
	EffectiveBalance        uint64  `json:"effective_balance"`
	OnlineEffectiveBalance  uint64  `json:"online_effective_balance"`
	OfflineEffectiveBalance uint64  `json:"offline_effective_balance"`
	BalancePercentage       float64 `json:"balance_percentage"`
	OnlineValidators        uint64  `json:"online_validators"`
	OfflineValidators       uint64  `json:"offline_validators"`
	OnlinePercentage        float64 `json:"online_percentage"`
	HealthStatus            string  `json:"health_status"` // "healthy", "warning", "critical", "empty"
	AvgInclusionDelay       float64 `json:"avg_inclusion_delay"`
	ProposalsExpected       uint64  `json:"proposals_expected"`
	ProposalsProposed       uint64  `json:"proposals_proposed"`
	ProposalRate            float64 `json:"proposal_rate"`
	HasProposalData         bool    `json:"has_proposal_data"`
	PtcVotesExpected        uint64  `json:"ptc_votes_expected"`
	PtcVotesIncluded        uint64  `json:"ptc_votes_included"`
	PtcInclusionRate        float64 `json:"ptc_inclusion_rate"`
	HasPtcData              bool    `json:"has_ptc_data"`
}

type ValidatorsSummaryClientBreak struct {
	ClientType              string  `json:"client_type"`
	ClientName              string  `json:"client_name"`
	Layer                   string  `json:"layer"` // "execution" or "consensus"
	ValidatorCount          uint64  `json:"validator_count"`
	EffectiveBalance        uint64  `json:"effective_balance"`
	OnlineEffectiveBalance  uint64  `json:"online_effective_balance"`
	OfflineEffectiveBalance uint64  `json:"offline_effective_balance"`
	BalancePercentage       float64 `json:"balance_percentage"`
	OnlineValidators        uint64  `json:"online_validators"`
	OfflineValidators       uint64  `json:"offline_validators"`
	OnlinePercentage        float64 `json:"online_percentage"`
	HealthStatus            string  `json:"health_status"`
	AvgInclusionDelay       float64 `json:"avg_inclusion_delay"`
	ProposalsExpected       uint64  `json:"proposals_expected"`
	ProposalsProposed       uint64  `json:"proposals_proposed"`
	ProposalRate            float64 `json:"proposal_rate"`
	HasProposalData         bool    `json:"has_proposal_data"`
	PtcVotesExpected        uint64  `json:"ptc_votes_expected"`
	PtcVotesIncluded        uint64  `json:"ptc_votes_included"`
	PtcInclusionRate        float64 `json:"ptc_inclusion_rate"`
	HasPtcData              bool    `json:"has_ptc_data"`
}
