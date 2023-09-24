package utils

import "fmt"

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
