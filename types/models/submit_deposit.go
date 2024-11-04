package models

type SubmitDepositPageData struct {
	NetworkName         string `json:"netname"`
	PublicRPCUrl        string `json:"pubrpc"`
	RainbowkitProjectId string `json:"rainbowkit"`
	ChainId             uint64 `json:"chainid"`
	GenesisForkVersion  []byte `json:"genesisforkversion"`
	DepositContract     []byte `json:"depositcontract"`
}
