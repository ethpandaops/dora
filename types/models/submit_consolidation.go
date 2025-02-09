package models

type SubmitConsolidationPageData struct {
	NetworkName           string `json:"netname"`
	PublicRPCUrl          string `json:"pubrpc"`
	RainbowkitProjectId   string `json:"rainbowkit"`
	ChainId               uint64 `json:"chainid"`
	ConsolidationContract string `json:"consolidationcontract"`
	ExplorerUrl           string `json:"explorerurl"`
}

type SubmitConsolidationPageDataValidator struct {
	Index          uint64 `json:"index"`
	Pubkey         string `json:"pubkey"`
	Balance        uint64 `json:"balance"`
	CredType       string `json:"credtype"`
	Status         string `json:"status"`
	IsConsolidable bool   `json:"isconsolidable"`
}
