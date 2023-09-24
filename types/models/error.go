package models

import (
	"time"
)

type ErrorPageData struct {
	CallTime time.Time
	CallUrl  string
	ErrorMsg string
}
