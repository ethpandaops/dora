package uipackage

import "embed"

// if there's an error building this package, try running `make build-ui` to build the ui package

var (
	//go:embed dist/*
	Files embed.FS
)
