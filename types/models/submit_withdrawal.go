package models

type SubmitWithdrawalPageData struct {
	NetworkName         string `json:"netname"`
	PublicRPCUrl        string `json:"pubrpc"`
	RainbowkitProjectId string `json:"rainbowkit"`
	ChainId             uint64 `json:"chainid"`
	WithdrawalContract  string `json:"withdrawalcontract"`
	MinValidatorBalance uint64 `json:"minbalance"`
	ExplorerUrl         string `json:"explorerurl"`
}

type SubmitWithdrawalPageDataValidator struct {
	Index    uint64 `json:"index"`
	Pubkey   string `json:"pubkey"`
	Balance  uint64 `json:"balance"`
	CredType string `json:"credtype"`
	Status   string `json:"status"`
}
