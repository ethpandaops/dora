package models

type SubmitDepositPageData struct {
	NetworkName         string `json:"netname"`
	PublicRPCUrl        string `json:"pubrpc"`
	RainbowkitProjectId string `json:"rainbowkit"`
	ChainId             uint64 `json:"chainid"`
	GenesisForkVersion  []byte `json:"genesisforkversion"`
	DepositContract     []byte `json:"depositcontract"`
}

type SubmitDepositPageDataDeposits struct {
	Deposits []SubmitDepositPageDataDeposit `json:"deposits"`
	Count    uint64                         `json:"count"`
	HaveMore bool                           `json:"havemore"`
}

type SubmitDepositPageDataDeposit struct {
	Pubkey      string `json:"pubkey"`
	Amount      uint64 `json:"amount"`
	BlockNumber uint64 `json:"block"`
	BlockHash   string `json:"block_hash"`
	BlockTime   uint64 `json:"block_time"`
	TxOrigin    string `json:"tx_origin"`
	TxTarget    string `json:"tx_target"`
	TxHash      string `json:"tx_hash"`
}
