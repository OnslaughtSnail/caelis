package agents

import "testing"

func TestLookupRegistryPreset(t *testing.T) {
	preset, ok := LookupRegistryPreset("codex-acp")
	if !ok {
		t.Fatal("expected codex-acp preset")
	}
	if preset.Command == "" || len(preset.Args) == 0 {
		t.Fatalf("expected command defaults, got %+v", preset)
	}
}

func TestResolveDescriptor_RegistryPreset(t *testing.T) {
	desc, err := ResolveDescriptor(Descriptor{
		ID:   "github-copilot-cli",
		Type: TypeRegistry,
	})
	if err != nil {
		t.Fatal(err)
	}
	if desc.Command == "" || len(desc.Args) == 0 {
		t.Fatalf("expected resolved preset command, got %+v", desc)
	}
}
