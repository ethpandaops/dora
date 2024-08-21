package config

import (
	"embed"
)

// explorer config
//
//go:embed default.config.yml
var DefaultConfigYml string

// validator names
//
//go:embed *.names.yml
var ValidatorNamesYml embed.FS
