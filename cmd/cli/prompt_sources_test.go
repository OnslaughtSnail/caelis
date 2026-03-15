package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPromptAssembleSpec_UsesBuiltInIdentityAndAgentPolicies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	globalDir := filepath.Join(home, ".agents")
	if err := os.MkdirAll(globalDir, 0o700); err != nil {
		t.Fatalf("mkdir global agents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalDir, "AGENTS.md"), []byte("# Global\n\nGlobal rule."), 0o600); err != nil {
		t.Fatalf("write global AGENTS: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("# Workspace\n\nProject rule."), 0o600); err != nil {
		t.Fatalf("write workspace AGENTS: %v", err)
	}

	result, err := buildPromptAssembleSpec(buildAgentInput{
		AppName:                     "demo-app",
		WorkspaceDir:                workspace,
		BasePrompt:                  "session override",
		SkillDirs:                   []string{filepath.Join(t.TempDir(), "missing-skills-dir")},
		EnableExperimentalLSPPrompt: true,
	})
	if err != nil {
		t.Fatalf("buildPromptAssembleSpec failed: %v", err)
	}
	spec := result.Spec
	if got := strings.TrimSpace(spec.IdentityPrompt); got != "# Agent Identity\n\nYou are demo-app." {
		t.Fatalf("unexpected identity prompt: %q", got)
	}
	if spec.IdentitySource != "cli:built-in-identity" {
		t.Fatalf("unexpected identity source: %q", spec.IdentitySource)
	}
	if len(spec.Additional) != 4 {
		t.Fatalf("expected active policies + session override + workspace context + experimental lsp, got %d", len(spec.Additional))
	}
	if !strings.Contains(spec.Additional[0].Content, "# Active Agent Policies") {
		t.Fatalf("expected active policy heading, got %q", spec.Additional[0].Content)
	}
	if !strings.Contains(spec.Additional[0].Content, "## Global User Policy") {
		t.Fatalf("expected global policy section, got %q", spec.Additional[0].Content)
	}
	if !strings.Contains(spec.Additional[0].Content, "## Project Policy") {
		t.Fatalf("expected project policy section, got %q", spec.Additional[0].Content)
	}
	if strings.Contains(spec.Additional[0].Content, "Source:") || strings.Contains(spec.Additional[0].Content, "Priority:") {
		t.Fatalf("did not expect source/priority metadata, got %q", spec.Additional[0].Content)
	}
	if !strings.Contains(spec.Additional[0].Content, "Overrides conflicting global instructions.") {
		t.Fatalf("expected workspace override note, got %q", spec.Additional[0].Content)
	}
	if spec.Additional[1].Content != "## Session Overrides\n\nsession override" {
		t.Fatalf("unexpected session override fragment: %+v", spec.Additional[1])
	}
	if !strings.Contains(spec.Additional[2].Content, "<environment_context>") {
		t.Fatalf("unexpected workspace context fragment: %+v", spec.Additional[2])
	}
	if !strings.Contains(spec.Additional[2].Content, "<cwd>"+workspace+"</cwd>") {
		t.Fatalf("expected workspace context to include workspace cwd, got %+v", spec.Additional[2])
	}
	if !strings.Contains(spec.Additional[2].Content, "<shell>") {
		t.Fatalf("expected workspace context to include shell, got %+v", spec.Additional[2])
	}
	if !strings.Contains(spec.Additional[2].Content, "<current_date>") {
		t.Fatalf("expected workspace context to include current_date, got %+v", spec.Additional[2])
	}
	if !strings.Contains(spec.Additional[2].Content, "<timezone>") {
		t.Fatalf("expected workspace context to include timezone, got %+v", spec.Additional[2])
	}
	if !strings.Contains(spec.Additional[3].Content, "## Experimental LSP Routing") {
		t.Fatalf("unexpected lsp fragment: %+v", spec.Additional[3])
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings for missing skill dir, got %v", result.Warnings)
	}
}

func TestBuildPromptAssembleSpec_SkipsMissingAgentPolicies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()

	result, err := buildPromptAssembleSpec(buildAgentInput{
		AppName:      "demo-app",
		WorkspaceDir: workspace,
	})
	if err != nil {
		t.Fatalf("buildPromptAssembleSpec failed: %v", err)
	}
	if len(result.Spec.Additional) != 1 {
		t.Fatalf("expected only workspace context fragment, got %+v", result.Spec.Additional)
	}
	if got := result.Spec.Additional[0].Source; got != "cli:workspace-context" {
		t.Fatalf("unexpected additional fragment source: %q", got)
	}
}

func TestBuildPromptAssembleSpec_IncludesWorkspaceContextWithoutOptionalFragments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()

	result, err := buildPromptAssembleSpec(buildAgentInput{
		AppName:      "demo-app",
		WorkspaceDir: workspace,
	})
	if err != nil {
		t.Fatalf("buildPromptAssembleSpec failed: %v", err)
	}
	if len(result.Spec.Additional) != 1 {
		t.Fatalf("expected only workspace context fragment, got %+v", result.Spec.Additional)
	}
	if got := result.Spec.Additional[0].Source; got != "cli:workspace-context" {
		t.Fatalf("unexpected workspace context source: %q", got)
	}
	if !strings.Contains(result.Spec.Additional[0].Content, "<cwd>"+workspace+"</cwd>") {
		t.Fatalf("expected workspace context to include workspace cwd, got %+v", result.Spec.Additional[0])
	}
	if !strings.Contains(result.Spec.Additional[0].Content, "<shell>") || !strings.Contains(result.Spec.Additional[0].Content, "<current_date>") || !strings.Contains(result.Spec.Additional[0].Content, "<timezone>") {
		t.Fatalf("expected workspace context tags, got %+v", result.Spec.Additional[0])
	}
}
