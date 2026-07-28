package types

//go:generate go tool dynssz-gen -without-dynamic-expressions -package . -legacy -output execdata_sections_ssz.go -types ReceiptMetaData,BlockReceiptMeta,StateChangeAccount,FlatCallFrame,EventData
