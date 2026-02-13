package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsurePromptFiles_WritesDefaultsAndPreservesExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()

	files, err := ensurePromptFiles("demo-app", "", workspace)
	if err != nil {
		t.Fatalf("ensurePromptFiles failed: %v", err)
	}
	for _, path := range []string{files.IdentityPath, files.GlobalAgentsPath, files.UserPath} {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read default prompt %q: %v", path, readErr)
		}
		if !strings.Contains(string(raw), "version: v1") {
			t.Fatalf("expected version marker in %q", path)
		}
	}

	custom := "custom user prompt\n"
	if err := os.WriteFile(files.UserPath, []byte(custom), 0o600); err != nil {
		t.Fatalf("seed custom user prompt: %v", err)
	}
	files2, err := ensurePromptFiles("demo-app", "", workspace)
	if err != nil {
		t.Fatalf("ensurePromptFiles second call failed: %v", err)
	}
	raw, err := os.ReadFile(files2.UserPath)
	if err != nil {
		t.Fatalf("read user prompt failed: %v", err)
	}
	if string(raw) != custom {
		t.Fatalf("expected existing user prompt preserved, got %q", string(raw))
	}
}

func TestBuildPromptAssembleSpec_LoadsPromptSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("# Workspace\n\nRule."), 0o600); err != nil {
		t.Fatalf("write workspace AGENTS: %v", err)
	}

	result, err := buildPromptAssembleSpec(buildAgentInput{
		AppName:                "demo-app",
		WorkspaceDir:           workspace,
		BasePrompt:             "session override",
		RuntimeHint:            "runtime hint",
		SkillDirs:              []string{filepath.Join(t.TempDir(), "missing-skills-dir")},
		EnableLSPRoutingPolicy: true,
	})
	if err != nil {
		t.Fatalf("buildPromptAssembleSpec failed: %v", err)
	}
	spec := result.Spec
	if !strings.Contains(spec.IdentityPrompt, "Agent Identity") {
		t.Fatalf("expected identity prompt content loaded, got %q", spec.IdentityPrompt)
	}
	if !strings.Contains(spec.GlobalAgentsPrompt, "Global Instructions") {
		t.Fatalf("expected global prompt content loaded, got %q", spec.GlobalAgentsPrompt)
	}
	if !strings.Contains(spec.UserPrompt, "User Custom Instructions") {
		t.Fatalf("expected user prompt content loaded, got %q", spec.UserPrompt)
	}
	if !strings.Contains(spec.WorkspaceAgentsPrompt, "Workspace") {
		t.Fatalf("expected workspace AGENTS loaded, got %q", spec.WorkspaceAgentsPrompt)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings for missing skill dir, got %v", result.Warnings)
	}
}
