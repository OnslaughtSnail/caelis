package promptpipeline

import (
	"strings"
	"testing"
)

func TestAssembleBuildsOrderedPrompt(t *testing.T) {
	result, err := Assemble(AssembleSpec{
		IdentityPrompt:        "# Identity\n\nKernel identity rule.",
		IdentitySource:        "identity.md",
		GlobalAgentsPrompt:    "# Global\n\nGlobal rule.",
		GlobalAgentsSource:    "agents.md",
		WorkspaceAgentsPrompt: "# Workspace\n\nWorkspace rule.",
		WorkspaceAgentsSource: "workspace/AGENTS.md",
		Additional: []PromptFragment{
			{
				Stage:   "runtime_context",
				Title:   "Runtime Context",
				Source:  "runtime",
				Content: "## Runtime Execution\n- permission_mode=default sandbox_type=seatbelt",
			},
			{
				Stage:   "user_custom",
				Title:   "User Custom Instructions",
				Source:  "user.md",
				Content: "# User\n\nLong lived preferences.\n\n## Session Overrides\n\nSession says: be concise.",
			},
			{
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
		"Priority rule: earlier sections override later sections.",
		"### Identity",
		"### Global Instructions",
		"### Workspace Instructions",
		"### Runtime Context",
		"### User Custom Instructions",
		"### Experimental LSP Routing",
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
	idxRuntime := strings.Index(text, "### Runtime Context")
	idxUser := strings.Index(text, "### User Custom Instructions")
	idxLSP := strings.Index(text, "### Experimental LSP Routing")
	idxSkills := strings.Index(text, "### Skills Metadata")
	if !(idxIdentity < idxGlobal &&
		idxGlobal < idxWorkspace &&
		idxWorkspace < idxRuntime &&
		idxRuntime < idxUser &&
		idxUser < idxLSP &&
		idxLSP < idxSkills) {
		t.Fatalf("unexpected section order: identity=%d global=%d workspace=%d runtime=%d user=%d lsp=%d skills=%d",
			idxIdentity, idxGlobal, idxWorkspace, idxRuntime, idxUser, idxLSP, idxSkills)
	}
}

func TestAssembleSkipsOptionalAdditionalFragmentsWhenEmpty(t *testing.T) {
	result, err := Assemble(AssembleSpec{
		IdentityPrompt:     "identity",
		GlobalAgentsPrompt: "global",
	})
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}
	if strings.Contains(result.Prompt, "### Experimental LSP Routing") {
		t.Fatalf("did not expect optional fragment section:\n%s", result.Prompt)
	}
}

func TestAssembleHandlesEmptyInputs(t *testing.T) {
	result, err := Assemble(AssembleSpec{})
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}
	if strings.Contains(result.Prompt, "### ") {
		t.Fatalf("expected no content sections for empty input, got:\n%s", result.Prompt)
	}
	if !strings.Contains(result.Prompt, "Priority rule: earlier sections override later sections.") {
		t.Fatalf("expected prompt header in empty assemble output, got:\n%s", result.Prompt)
	}
}
