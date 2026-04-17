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

func TestLookupBuiltin_OpenClawUsesCleanACPBridgeDefaults(t *testing.T) {
	preset, ok := LookupBuiltin("openclaw")
	if !ok {
		t.Fatal("expected openclaw builtin")
	}
	if preset.Command != "openclaw" {
		t.Fatalf("expected openclaw command, got %+v", preset)
	}
	if got := preset.Args; len(got) != 3 || got[0] != "acp" || got[1] != "--session" || got[2] != "agent:main:main" {
		t.Fatalf("expected main-agent ACP bridge args, got %+v", got)
	}
	if preset.Env["OPENCLAW_HIDE_BANNER"] != "1" {
		t.Fatalf("expected banner suppression env, got %+v", preset.Env)
	}
	if preset.Env["OPENCLAW_SUPPRESS_NOTES"] != "1" {
		t.Fatalf("expected note suppression env, got %+v", preset.Env)
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
