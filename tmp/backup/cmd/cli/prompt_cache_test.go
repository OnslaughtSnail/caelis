package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestEnsureSessionPromptFreezesPerSessionUntilNewSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	globalDir := filepath.Join(home, ".agents")
	if err := os.MkdirAll(globalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	agentsPath := filepath.Join(workspace, "AGENTS.md")
	if err := os.WriteFile(agentsPath, []byte("first prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:         context.Background(),
		appName:         "app",
		userID:          "u",
		sessionID:       "s-1",
		workspace:       workspaceContext{CWD: workspace},
		sessionStore:    inmemory.New(),
		promptSnapshots: map[string]string{},
	}
	first, err := console.ensureSessionPrompt(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "first prompt") {
		t.Fatalf("expected first prompt, got %q", first)
	}
	if err := os.WriteFile(agentsPath, []byte("second prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	sameSession, err := console.ensureSessionPrompt(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sameSession != first {
		t.Fatalf("expected prompt frozen within session, got first=%q second=%q", first, sameSession)
	}
	console.sessionID = "s-2"
	nextSession, err := console.ensureSessionPrompt(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(nextSession, "second prompt") {
		t.Fatalf("expected new session to pick updated prompt, got %q", nextSession)
	}
}
