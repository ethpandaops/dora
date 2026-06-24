package models

// SubmitBuilderDepositPageData is the page data for the submit builder deposit page.
type SubmitBuilderDepositPageData struct {
	NetworkName            string `json:"netname"`
	PublicRPCUrl           string `json:"pubrpc"`
	RainbowkitProjectId    string `json:"rainbowkit"`
	ChainId                uint64 `json:"chainid"`
	BuilderDepositContract string `json:"builderdepositcontract"`
	GenesisForkVersion     []byte `json:"genesisforkversion"`
	ExplorerUrl            string `json:"explorerurl"`
}
