package types

//go:generate go run github.com/pk910/dynamic-ssz/dynssz-gen -without-dynamic-expressions -package . -legacy -output execdata_sections_ssz.go -types ReceiptMetaData,BlockReceiptMeta,StateChangeAccount,FlatCallFrame,EventData
