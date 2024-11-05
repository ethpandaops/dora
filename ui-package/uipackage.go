package uipackage

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io"
)

// if there's an error building this package, try running `make build-ui` to build the ui package

var (
	//go:embed dist/*
	Files embed.FS
)

var processedBuildHash bool
var cachedBuildHash string

func GetUIBuildHash() string {
	if !processedBuildHash {
		processedBuildHash = true

		f, err := Files.Open("dist/react-ui.js")
		if err != nil {
			h := sha256.New()
			io.Copy(h, f)

			hash := h.Sum(nil)
			cachedBuildHash = fmt.Sprintf("%x", hash[:4])
		}
	}

	return cachedBuildHash
}
