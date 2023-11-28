package types

type TxSignatureBytes [4]byte
type TxSignatureLookupStatus uint8

var (
	TxSigStatusPending TxSignatureLookupStatus = 0
	TxSigStatusFound   TxSignatureLookupStatus = 1
	TxSigStatusUnknown TxSignatureLookupStatus = 2
)
