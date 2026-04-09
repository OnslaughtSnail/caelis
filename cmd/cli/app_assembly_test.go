package main

import (
	"strings"
	"testing"
)

func TestResolveMainSessionSystemPrompt_UsesACPMainRoleForExternalController(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()

	prompt, err := resolveMainSessionSystemPrompt(buildAgentInput{
		AppName:      "demo-app",
		PromptRole:   promptRoleMainSession,
		WorkspaceDir: workspace,
		BasePrompt:   "keep answers concise",
		DefaultAgent: "codex",
	}, true)
	if err != nil {
		t.Fatalf("resolveMainSessionSystemPrompt failed: %v", err)
	}
	if !strings.Contains(prompt, "## ACP Main Session Role") {
		t.Fatalf("expected ACP main-session role guidance, got %q", prompt)
	}
	if strings.Contains(prompt, "Tool families: use READ/SEARCH/GLOB/LIST to inspect") {
		t.Fatalf("did not expect local Caelis tool guidance in ACP main-session prompt: %q", prompt)
	}
	if strings.Contains(prompt, "## Agent Delegation") {
		t.Fatalf("did not expect local delegation tool guidance in ACP main-session prompt: %q", prompt)
	}
}
