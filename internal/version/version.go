package version

import "strings"

// These values are injected at build time via -ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns compact human-readable version info.
func String() string {
	value := strings.TrimSpace(Version)
	if value == "" {
		return "unknown"
	}
	return value
}
