package promptpipeline

import (
	"strings"
	"testing"
)

func TestAssembleBuildsOrderedPrompt(t *testing.T) {
	result, err := Assemble(AssembleSpec{
		IdentityPrompt:         "# Identity\n\nKernel identity rule.",
		IdentitySource:         "identity.md",
		GlobalAgentsPrompt:     "# Global\n\nGlobal rule.",
		GlobalAgentsSource:     "agents.md",
		WorkspaceAgentsPrompt:  "# Workspace\n\nWorkspace rule.",
		WorkspaceAgentsSource:  "workspace/AGENTS.md",
		EnableLSPRoutingPolicy: true,
		RuntimeHint:            "## Runtime Execution\n- permission_mode=default sandbox_type=seatbelt",
		UserPrompt:             "# User\n\nLong lived preferences.",
		UserSource:             "user.md",
		BasePrompt:             "Session says: be concise.",
		SkillsMetaPrompt:       "Skills Metadata (auto-loaded, all active):\n- name=\"echo\"",
		SkillsMetaSource:       "skills metadata",
	})
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}
	text := result.Prompt
	for _, required := range []string{
		"Priority rule: higher sections override lower sections.",
		"### Identity",
		"### Global Instructions",
		"### Workspace Instructions",
		"### LSP Routing Policy",
		"### Runtime Context",
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
	idxRuntime := strings.Index(text, "### Runtime Context")
	idxUser := strings.Index(text, "### User Custom Instructions")
	idxSkills := strings.Index(text, "### Skills Metadata")
	if !(idxIdentity < idxGlobal &&
		idxGlobal < idxWorkspace &&
		idxWorkspace < idxLSPPolicy &&
		idxLSPPolicy < idxRuntime &&
		idxRuntime < idxUser &&
		idxUser < idxSkills) {
		t.Fatalf("unexpected section order: identity=%d global=%d workspace=%d lsp=%d runtime=%d user=%d skills=%d",
			idxIdentity, idxGlobal, idxWorkspace, idxLSPPolicy, idxRuntime, idxUser, idxSkills)
	}
}

func TestAssembleSkipsLSPPolicyWhenDisabled(t *testing.T) {
	result, err := Assemble(AssembleSpec{
		IdentityPrompt:         "identity",
		GlobalAgentsPrompt:     "global",
		UserPrompt:             "user",
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

func TestAssembleHandlesEmptyInputs(t *testing.T) {
	result, err := Assemble(AssembleSpec{})
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}
	if strings.Contains(result.Prompt, "### ") {
		t.Fatalf("expected no content sections for empty input, got:\n%s", result.Prompt)
	}
	if !strings.Contains(result.Prompt, "Priority rule: higher sections override lower sections.") {
		t.Fatalf("expected prompt header in empty assemble output, got:\n%s", result.Prompt)
	}
}
