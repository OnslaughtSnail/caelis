package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appprompting "github.com/OnslaughtSnail/caelis/internal/app/prompting"
)

func TestBuildPromptAssembleSpec_UsesStructuredSystemAndUserInstructions(t *testing.T) {
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
		DefaultAgent:                "self",
		SkillDirs:                   []string{filepath.Join(t.TempDir(), "missing-skills-dir")},
		EnableExperimentalLSPPrompt: true,
	})
	if err != nil {
		t.Fatalf("buildPromptAssembleSpec failed: %v", err)
	}

	spec := result.Spec
	if got := strings.TrimSpace(spec.IdentityPrompt); !strings.Contains(got, "## Core Stable Rules") {
		t.Fatalf("unexpected identity prompt: %q", got)
	}
	for _, required := range []string{
		"You are demo-app, a coding-oriented assistant working in the user's workspace.",
		"Prefer a tight loop: understand the goal, inspect the minimum context, act, verify, then report.",
		"Be token disciplined: keep instructions compact, avoid repeating stable rules, and preserve only active state.",
	} {
		if !strings.Contains(spec.IdentityPrompt, required) {
			t.Fatalf("identity prompt missing %q: %q", required, spec.IdentityPrompt)
		}
	}
	if spec.IdentitySource != "cli:built-in-identity" {
		t.Fatalf("unexpected identity source: %q", spec.IdentitySource)
	}
	if len(spec.Additional) != 6 {
		t.Fatalf("expected role + capability + user instructions + agent delegation + environment context + experimental lsp, got %d", len(spec.Additional))
	}

	roleFragment := spec.Additional[0]
	if roleFragment.Kind != appprompting.PromptFragmentKindSystem {
		t.Fatalf("expected system fragment kind for role guidance, got %+v", roleFragment)
	}
	for _, required := range []string{
		"## Main Session Role",
		"You are the primary session responsible for end-to-end progress.",
	} {
		if !strings.Contains(roleFragment.Content, required) {
			t.Fatalf("role guidance missing %q: %q", required, roleFragment.Content)
		}
	}

	capabilityFragment := spec.Additional[1]
	if capabilityFragment.Kind != appprompting.PromptFragmentKindSystem {
		t.Fatalf("expected system fragment kind for capability guidance, got %+v", capabilityFragment)
	}
	for _, required := range []string{
		"## Capability Guidance",
		"Tool families: use READ/SEARCH/GLOB/LIST to inspect",
		"Skills: load a skill only when its description clearly matches the current task",
	} {
		if !strings.Contains(capabilityFragment.Content, required) {
			t.Fatalf("capability guidance missing %q: %q", required, capabilityFragment.Content)
		}
	}

	userFragment := spec.Additional[2]
	if userFragment.Kind != appprompting.PromptFragmentKindUser {
		t.Fatalf("expected user fragment kind, got %+v", userFragment)
	}
	if userFragment.Source != "cli:user-custom-instructions" {
		t.Fatalf("unexpected user fragment source: %+v", userFragment)
	}
	for _, required := range []string{
		"Session overrides workspace instructions, and workspace instructions override global instructions on conflict.",
		"## Session Overrides",
		"session override",
		"## Workspace Instructions",
		"# Workspace",
		"## Global Instructions",
		"# Global",
	} {
		if !strings.Contains(userFragment.Content, required) {
			t.Fatalf("user instructions missing %q: %q", required, userFragment.Content)
		}
	}
	if strings.Contains(userFragment.Content, "Source:") || strings.Contains(userFragment.Content, "Priority:") {
		t.Fatalf("did not expect source/priority metadata, got %q", userFragment.Content)
	}

	agentFragment := spec.Additional[3]
	if agentFragment.Kind != appprompting.PromptFragmentKindSystem {
		t.Fatalf("expected system fragment kind for delegation guidance, got %+v", agentFragment)
	}
	for _, required := range []string{
		"## Agent Delegation",
		"- default_agent=self",
		"- agent=self stability=stable",
		"- Use SPAWN only for bounded delegated work or specialization.",
		"- If a spawned child is still running, use TASK wait instead of TASK write.",
		"- Use TASK write only after a spawned child has completed and needs a follow-up prompt.",
	} {
		if !strings.Contains(agentFragment.Content, required) {
			t.Fatalf("delegation guidance missing %q: %q", required, agentFragment.Content)
		}
	}

	contextFragment := spec.Additional[4]
	if contextFragment.Kind != appprompting.PromptFragmentKindContext {
		t.Fatalf("expected context fragment kind, got %+v", contextFragment)
	}
	if !strings.Contains(contextFragment.Content, "<environment_context>") {
		t.Fatalf("unexpected environment context fragment: %+v", contextFragment)
	}
	if !strings.Contains(contextFragment.Content, "<cwd>"+workspace+"</cwd>") {
		t.Fatalf("expected environment context to include workspace cwd, got %+v", contextFragment)
	}
	if !strings.Contains(contextFragment.Content, "<shell>") {
		t.Fatalf("expected environment context to include shell, got %+v", contextFragment)
	}
	if !strings.Contains(contextFragment.Content, "<current_date>") {
		t.Fatalf("expected environment context to include current_date, got %+v", contextFragment)
	}
	if !strings.Contains(contextFragment.Content, "<timezone>") {
		t.Fatalf("expected environment context to include timezone, got %+v", contextFragment)
	}

	lspFragment := spec.Additional[5]
	if lspFragment.Kind != appprompting.PromptFragmentKindSystem {
		t.Fatalf("expected system fragment kind for lsp routing, got %+v", lspFragment)
	}
	if !strings.Contains(lspFragment.Content, "## Experimental LSP Routing") {
		t.Fatalf("unexpected lsp fragment: %+v", lspFragment)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings for missing skill dir, got %v", result.Warnings)
	}
}

func TestBuildPromptAssembleSpec_SkipsMissingUserInstructions(t *testing.T) {
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
	if len(result.Spec.Additional) != 3 {
		t.Fatalf("expected role + capability + environment context fragments, got %+v", result.Spec.Additional)
	}
	if got := result.Spec.Additional[0].Source; got != "cli:role-guidance" {
		t.Fatalf("unexpected additional fragment source: %q", got)
	}
	if got := result.Spec.Additional[1].Source; got != "cli:capability-guidance" {
		t.Fatalf("unexpected additional fragment source: %q", got)
	}
	if got := result.Spec.Additional[2].Source; got != "cli:workspace-context" {
		t.Fatalf("unexpected additional fragment source: %q", got)
	}
}

func TestBuildPromptAssembleSpec_IncludesEnvironmentContextWithoutOptionalFragments(t *testing.T) {
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
	if len(result.Spec.Additional) != 3 {
		t.Fatalf("expected role + capability + environment context fragments, got %+v", result.Spec.Additional)
	}
	if got := result.Spec.Additional[2].Source; got != "cli:workspace-context" {
		t.Fatalf("unexpected environment context source: %q", got)
	}
	if !strings.Contains(result.Spec.Additional[2].Content, "<cwd>"+workspace+"</cwd>") {
		t.Fatalf("expected environment context to include workspace cwd, got %+v", result.Spec.Additional[2])
	}
	if !strings.Contains(result.Spec.Additional[2].Content, "<shell>") || !strings.Contains(result.Spec.Additional[2].Content, "<current_date>") || !strings.Contains(result.Spec.Additional[2].Content, "<timezone>") {
		t.Fatalf("expected environment context tags, got %+v", result.Spec.Additional[2])
	}
}

func TestBuildPromptAssembleSpec_ACPMainSessionSkipsLocalToolingGuidance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()

	result, err := buildPromptAssembleSpec(buildAgentInput{
		AppName:                     "demo-app",
		PromptRole:                  promptRoleACPMainSession,
		WorkspaceDir:                workspace,
		BasePrompt:                  "session override",
		DefaultAgent:                "codex",
		EnableExperimentalLSPPrompt: true,
	})
	if err != nil {
		t.Fatalf("buildPromptAssembleSpec failed: %v", err)
	}
	if result.Spec.SkillsMetaPrompt != "" {
		t.Fatalf("expected ACP main-session prompt to skip local skills metadata, got %q", result.Spec.SkillsMetaPrompt)
	}
	if len(result.Spec.Additional) != 3 {
		t.Fatalf("expected role + user instructions + environment context, got %+v", result.Spec.Additional)
	}
	roleFragment := result.Spec.Additional[0]
	if !strings.Contains(roleFragment.Content, "## ACP Main Session Role") {
		t.Fatalf("unexpected ACP role fragment: %+v", roleFragment)
	}
	if !strings.Contains(roleFragment.Content, "do not assume local Caelis tool names") {
		t.Fatalf("expected ACP role guidance to strip local tool contract assumptions, got %q", roleFragment.Content)
	}
	if got := result.Spec.Additional[1].Source; got != "cli:user-custom-instructions" {
		t.Fatalf("expected user instruction fragment, got %q", got)
	}
	if got := result.Spec.Additional[2].Source; got != "cli:workspace-context" {
		t.Fatalf("expected environment context fragment, got %q", got)
	}
}

func TestBuildUserCustomInstructionsPrompt_PreservesMarkdownAndSkipsEmptySections(t *testing.T) {
	content := buildUserCustomInstructionsPrompt(
		"",
		"# Workspace\n\n- keep headings\n- keep lists",
		"",
	)
	if strings.Contains(content, "## Session Overrides") || strings.Contains(content, "## Global Instructions") {
		t.Fatalf("did not expect empty sections, got %q", content)
	}
	for _, required := range []string{
		"## Workspace Instructions",
		"# Workspace",
		"- keep headings",
		"- keep lists",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("markdown content missing %q: %q", required, content)
		}
	}
}
