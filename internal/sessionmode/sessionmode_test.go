package sessionmode

import "testing"

func TestInjectAndVisibleText(t *testing.T) {
	injected := Inject("Implement the fix.", PlanMode)
	if got := VisibleText(injected); got != "Implement the fix." {
		t.Fatalf("expected visible prompt preserved, got %q", got)
	}
	if got := VisibleText(Inject("Review the diff.", DefaultMode)); got != "Review the diff." {
		t.Fatalf("expected default mode prompt preserved, got %q", got)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	values := StoreSnapshot(nil, PlanMode)
	if got := LoadSnapshot(values); got != PlanMode {
		t.Fatalf("expected plan mode, got %q", got)
	}
	if got := LoadSnapshot(nil); got != DefaultMode {
		t.Fatalf("expected default mode fallback, got %q", got)
	}
}

func TestNextCyclesThroughModes(t *testing.T) {
	if got := Next(DefaultMode); got != PlanMode {
		t.Fatalf("expected default -> plan, got %q", got)
	}
	if got := Next(PlanMode); got != FullMode {
		t.Fatalf("expected plan -> full_access, got %q", got)
	}
	if got := Next(FullMode); got != DefaultMode {
		t.Fatalf("expected full_access -> default, got %q", got)
	}
}

func TestIsDangerousCommand(t *testing.T) {
	for _, command := range []string{
		"rm -rf /tmp/x",
		"echo hi && shred secret.txt",
		"dd if=/dev/zero of=/dev/disk1",
		"sh -c 'rm -rf /tmp/x'",
		`bash -lc "dd if=/dev/zero of=/dev/disk1"`,
		`env FOO=1 bash -lc "shred secret.txt"`,
	} {
		if !IsDangerousCommand(command) {
			t.Fatalf("expected dangerous command detection for %q", command)
		}
	}
	if IsDangerousCommand("go test ./...") {
		t.Fatal("did not expect benign command to be flagged")
	}
}
