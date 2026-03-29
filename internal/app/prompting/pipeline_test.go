package prompting

import (
	"strings"
	"testing"
)

func TestAssembleBuildsOrderedPrompt(t *testing.T) {
	result, err := Assemble(AssembleSpec{
		IdentityPrompt:        "## Identity\n\nKernel identity rule.",
		IdentitySource:        "identity.md",
		GlobalAgentsPrompt:    "# Global\n\nGlobal rule.",
		GlobalAgentsSource:    "agents.md",
		WorkspaceAgentsPrompt: "# Workspace\n\nWorkspace rule.",
		WorkspaceAgentsSource: "workspace/AGENTS.md",
		Additional: []PromptFragment{
			{
				Kind:    PromptFragmentKindSystem,
				Stage:   "capability_guidance",
				Title:   "Capability Guidance",
				Source:  "runtime",
				Content: "## Capability Guidance\n- Tool families and delegation rules",
			},
			{
				Kind:    PromptFragmentKindUser,
				Stage:   "user_custom",
				Title:   "User Custom Instructions",
				Source:  "user.md",
				Content: "# User\n\nLong lived preferences.\n\n## Session Overrides\n\nSession says: be concise.",
			},
			{
				Kind:    PromptFragmentKindContext,
				Stage:   "dynamic_runtime_context",
				Title:   "Environment Context",
				Source:  "runtime",
				Content: "<environment_context>\n  <cwd>/tmp/demo</cwd>\n</environment_context>",
			},
			{
				Kind:    PromptFragmentKindSystem,
				Stage:   "experimental_lsp",
				Title:   "Experimental LSP Routing",
				Source:  "cli:lsp",
				Content: "Use LSP_SYMBOLS first.",
			},
		},
		SkillsMetaPrompt: "Skills Metadata (auto-loaded, all active):\n- name=\"echo\"",
		SkillsMetaSource: "skills metadata",
	})
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	text := result.Prompt
	for _, required := range []string{
		"<system_instructions>",
		"</system_instructions>",
		"<user_custom_instructions>",
		"</user_custom_instructions>",
		"Kernel identity rule.",
		"## Capability Guidance",
		"Use LSP_SYMBOLS first.",
		"Session overrides workspace instructions, and workspace instructions override global instructions on conflict.",
		"# Global",
		"# Workspace",
		"# User",
		"<environment_context>",
		"Skills Metadata (auto-loaded, all active):",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("assembled prompt missing %q:\n%s", required, text)
		}
	}
	if strings.Contains(text, "Priority rule: earlier sections override later sections.") {
		t.Fatalf("did not expect legacy priority header:\n%s", text)
	}
	if strings.Contains(text, "source: ") {
		t.Fatalf("did not expect source metadata in assembled prompt:\n%s", text)
	}

	idxSystem := strings.Index(text, "<system_instructions>")
	idxUser := strings.Index(text, "<user_custom_instructions>")
	idxSkills := strings.Index(text, "Skills Metadata (auto-loaded, all active):")
	idxContext := strings.Index(text, "<environment_context>")
	if idxSystem < 0 || idxSystem >= idxUser || idxUser >= idxSkills || idxSkills >= idxContext {
		t.Fatalf("unexpected section order: system=%d user=%d skills=%d context=%d", idxSystem, idxUser, idxSkills, idxContext)
	}
}

func TestAssembleSkipsOptionalAdditionalFragmentsWhenEmpty(t *testing.T) {
	result, err := Assemble(AssembleSpec{
		IdentityPrompt: "identity",
	})
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}
	if strings.Contains(result.Prompt, "Experimental LSP Routing") {
		t.Fatalf("did not expect optional fragment section:\n%s", result.Prompt)
	}
	if !strings.Contains(result.Prompt, "<system_instructions>") {
		t.Fatalf("expected system wrapper:\n%s", result.Prompt)
	}
	if strings.Contains(result.Prompt, "<user_custom_instructions>") {
		t.Fatalf("did not expect empty user wrapper:\n%s", result.Prompt)
	}
}

func TestAssembleHandlesEmptyInputs(t *testing.T) {
	result, err := Assemble(AssembleSpec{})
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}
	if got := strings.TrimSpace(result.Prompt); got != "" {
		t.Fatalf("expected empty prompt, got:\n%s", got)
	}
}
