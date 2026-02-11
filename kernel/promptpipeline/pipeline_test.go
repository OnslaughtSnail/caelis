package promptpipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsurePromptFilesWritesDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	files, err := EnsurePromptFiles("demo-app", "")
	if err != nil {
		t.Fatalf("EnsurePromptFiles failed: %v", err)
	}
	if !strings.HasPrefix(files.ConfigDir, filepath.Join(home, ".demo-app")) {
		t.Fatalf("unexpected config dir: %q", files.ConfigDir)
	}
	for _, path := range []string{files.IdentityPath, files.GlobalAgentsPath, files.UserPath} {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("expected default file %q: %v", path, readErr)
		}
		if !strings.Contains(string(raw), "version: v1") {
			t.Fatalf("expected version marker in %q", path)
		}
	}
}

func TestEnsurePromptFilesDoesNotOverwriteExisting(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "prompt-config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	userPath := filepath.Join(configDir, userFileName)
	if err := os.WriteFile(userPath, []byte("custom user prompt\n"), 0o600); err != nil {
		t.Fatalf("write seed failed: %v", err)
	}

	files, err := EnsurePromptFiles("demo", configDir)
	if err != nil {
		t.Fatalf("EnsurePromptFiles failed: %v", err)
	}
	raw, err := os.ReadFile(files.UserPath)
	if err != nil {
		t.Fatalf("read user file failed: %v", err)
	}
	if strings.TrimSpace(string(raw)) != "custom user prompt" {
		t.Fatalf("expected existing content preserved, got: %q", string(raw))
	}
}

func TestAssembleBuildsOrderedPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("# Workspace\n\nProject rule."), 0o600); err != nil {
		t.Fatalf("write workspace AGENTS failed: %v", err)
	}
	skillRoot := filepath.Join(t.TempDir(), "skills")
	skillDir := filepath.Join(skillRoot, "echo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	skillContent := "---\nname: echo\ndescription: Echo helper.\n---\n# Echo\n\nEcho helper."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	result, err := Assemble(AssembleSpec{
		AppName:                "demo-app",
		WorkspaceDir:           workspace,
		BasePrompt:             "Session says: be concise.",
		SkillDirs:              []string{skillRoot},
		EnableLSPRoutingPolicy: true,
	})
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings, got: %v", result.Warnings)
	}
	text := result.Prompt
	for _, required := range []string{
		"Priority rule: higher sections override lower sections.",
		"### Identity",
		"### Global Instructions",
		"### Workspace Instructions",
		"### LSP Routing Policy",
		"### User Custom Instructions",
		"### Skills Metadata",
		"Session says: be concise.",
		"Skills Metadata (auto-loaded, all active):",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("assembled prompt missing %q:\n%s", required, text)
		}
	}

	idxIdentity := strings.Index(text, "### Identity")
	idxGlobal := strings.Index(text, "### Global Instructions")
	idxWorkspace := strings.Index(text, "### Workspace Instructions")
	idxLSPPolicy := strings.Index(text, "### LSP Routing Policy")
	idxUser := strings.Index(text, "### User Custom Instructions")
	idxSkills := strings.Index(text, "### Skills Metadata")
	if !(idxIdentity < idxGlobal && idxGlobal < idxWorkspace && idxWorkspace < idxLSPPolicy && idxLSPPolicy < idxUser && idxUser < idxSkills) {
		t.Fatalf("unexpected section order: identity=%d global=%d workspace=%d lsp=%d user=%d skills=%d", idxIdentity, idxGlobal, idxWorkspace, idxLSPPolicy, idxUser, idxSkills)
	}
}

func TestAssembleSkipsLSPPolicyWhenDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()

	result, err := Assemble(AssembleSpec{
		AppName:                "demo-app",
		WorkspaceDir:           workspace,
		BasePrompt:             "no lsp policy",
		EnableLSPRoutingPolicy: false,
	})
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}
	if strings.Contains(result.Prompt, "### LSP Routing Policy") {
		t.Fatalf("did not expect LSP policy section when disabled:\n%s", result.Prompt)
	}
}

func TestAssembleIncludesRuntimeContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()

	result, err := Assemble(AssembleSpec{
		AppName:                "demo-app",
		WorkspaceDir:           workspace,
		BasePrompt:             "base override",
		RuntimeHint:            "## Runtime Execution\n- permission_mode=default sandbox_type=docker",
		EnableLSPRoutingPolicy: true,
	})
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}
	text := result.Prompt
	if !strings.Contains(text, "### Runtime Context") {
		t.Fatalf("expected runtime context section:\n%s", text)
	}
	idxRuntime := strings.Index(text, "### Runtime Context")
	idxUser := strings.Index(text, "### User Custom Instructions")
	if !(idxRuntime > 0 && idxUser > 0 && idxRuntime < idxUser) {
		t.Fatalf("unexpected runtime/user ordering: runtime=%d user=%d", idxRuntime, idxUser)
	}
}
