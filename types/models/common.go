package models

type UrlParam struct {
	Key   string
	Value string
}

type KeyValue struct {
	Key   string `json:"k"`
	Value string `json:"v"`
}
