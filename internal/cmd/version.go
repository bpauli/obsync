package cmd

import (
	"fmt"
	"strings"
)

var (
	version = "0.2.0-dev"
	commit  = ""
	date    = ""
)

// VersionString returns a human-readable version string.
func VersionString() string {
	v := strings.TrimSpace(version)
	if v == "" {
		v = "dev"
	}
	if strings.TrimSpace(commit) == "" && strings.TrimSpace(date) == "" {
		return v
	}
	if strings.TrimSpace(commit) == "" {
		return fmt.Sprintf("%s (%s)", v, strings.TrimSpace(date))
	}
	if strings.TrimSpace(date) == "" {
		return fmt.Sprintf("%s (%s)", v, strings.TrimSpace(commit))
	}
	return fmt.Sprintf("%s (%s %s)", v, strings.TrimSpace(commit), strings.TrimSpace(date))
}
