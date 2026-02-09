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
	parts := []string{}
	if value := strings.TrimSpace(Version); value != "" {
		parts = append(parts, value)
	}
	if value := strings.TrimSpace(Commit); value != "" {
		parts = append(parts, "commit="+value)
	}
	if value := strings.TrimSpace(Date); value != "" {
		parts = append(parts, "date="+value)
	}
	return strings.Join(parts, " ")
}
