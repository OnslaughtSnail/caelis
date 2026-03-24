package agents

import "testing"

func TestLookupBuiltin(t *testing.T) {
	preset, ok := LookupBuiltin("codex")
	if !ok {
		t.Fatal("expected codex builtin")
	}
	if preset.Command == "" || len(preset.Args) == 0 {
		t.Fatalf("expected command defaults, got %+v", preset)
	}
	if preset.Stability != StabilityStable {
		t.Fatalf("expected stable builtin, got %+v", preset)
	}
}

func TestResolveDescriptor_CommandBackedACP(t *testing.T) {
	desc, err := ResolveDescriptor(Descriptor{
		ID:        "copilot",
		Transport: TransportACP,
		Command:   "copilot",
		Args:      []string{"--acp", "--stdio"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if desc.Command == "" || len(desc.Args) == 0 {
		t.Fatalf("expected resolved preset command, got %+v", desc)
	}
}
