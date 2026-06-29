package models

// SubmitBuilderExitPageData is the page data for the submit builder exit page.
type SubmitBuilderExitPageData struct {
	NetworkName         string `json:"netname"`
	PublicRPCUrl        string `json:"pubrpc"`
	RainbowkitProjectId string `json:"rainbowkit"`
	ChainId             uint64 `json:"chainid"`
	BuilderExitContract string `json:"builderexitcontract"`
	ExplorerUrl         string `json:"explorerurl"`
}

// SubmitBuilderExitPageDataBuilder is a builder owned by the connected wallet, offered for exit.
type SubmitBuilderExitPageDataBuilder struct {
	Index            uint64 `json:"index"`
	Pubkey           string `json:"pubkey"`
	ExecutionAddress string `json:"executionAddress"`
	Status           string `json:"status"`
}
