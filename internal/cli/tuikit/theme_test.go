package tuikit

import "testing"

func TestComposeFooter(t *testing.T) {
	got := ComposeFooter(20, "left", "right")
	if len(got) != 20 {
		t.Fatalf("expected width 20, got %d", len(got))
	}
	if got[:4] != "left" {
		t.Fatalf("expected left prefix, got %q", got)
	}
}
