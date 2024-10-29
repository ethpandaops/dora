package models

type SubmitDepositPageData struct {
	NetworkName         string `json:"netname"`
	DepositContract     string `json:"depaddr"`
	PublicRPCUrl        string `json:"pubrpc"`
	RainbowkitProjectId string `json:"rainbowkit"`
	ChainId             uint64 `json:"chainid"`
}
