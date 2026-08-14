package utils

import (
	"fmt"
	"strings"
	"time"
)

var BuildVersion string
var BuildRelease string
var Buildtime string

func GetExplorerVersion() string {
	if BuildRelease == "" {
		return fmt.Sprintf("git-%v", BuildVersion)
	} else {
		return fmt.Sprintf("%v (git-%v)", BuildRelease, BuildVersion)
	}
}

// assetVersionValue is the cache-busting token appended to static asset URLs
// (?v=...), so CDN/proxy caches (e.g. cloudflare) fetch fresh assets after an
// upgrade. It is the git build version reduced to URL-safe characters (the ldflags
// value is wrapped in literal quotes), or the process start time for dev builds
// without build info so assets bust on every restart.
var assetVersionValue = func() string {
	version := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return -1
		}
	}, BuildVersion)
	if version == "" {
		version = fmt.Sprintf("%d", time.Now().Unix())
	}
	return version
}()

// GetAssetVersion returns the cache-busting version token for static asset URLs.
func GetAssetVersion() string {
	return assetVersionValue
}
