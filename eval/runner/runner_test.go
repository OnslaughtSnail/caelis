package runner

import "testing"

func TestResolveModelAliases_UsesConfiguredDefaults(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "token-deepseek")
	t.Setenv("GEMINI_API_KEY", "")
	aliases := resolveModelAliases(Options{})
	if len(aliases) != 1 || aliases[0] != "deepseek-chat" {
		t.Fatalf("unexpected aliases: %v", aliases)
	}
}

func TestResolveModelAliases_ReturnsEmptyWhenNoConfiguredModels(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	aliases := resolveModelAliases(Options{})
	if len(aliases) != 0 {
		t.Fatalf("expected empty aliases, got %v", aliases)
	}
}
