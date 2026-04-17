package version

import "testing"

func TestString_ReturnsVersionOnly(t *testing.T) {
	oldVersion, oldCommit, oldDate := Version, Commit, Date
	defer func() {
		Version, Commit, Date = oldVersion, oldCommit, oldDate
	}()

	Version = "v0.0.1"
	Commit = "af954eb"
	Date = "2026-03-01T04:47:19Z"

	if got := String(); got != "v0.0.1" {
		t.Fatalf("expected version-only string, got %q", got)
	}
}

func TestString_EmptyVersionReturnsUnknown(t *testing.T) {
	oldVersion := Version
	defer func() { Version = oldVersion }()
	Version = ""
	if got := String(); got != "unknown" {
		t.Fatalf("expected unknown, got %q", got)
	}
}
