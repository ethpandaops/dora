package uipackage

import "embed"

var (
	//go:embed dist/*
	Files embed.FS
)
