package promptpipeline

import (
	"bytes"
	"strings"
)

// AssembleSpec describes prompt assembly inputs.
type AssembleSpec struct {
	BasePrompt             string
	RuntimeHint            string
	EnableLSPRoutingPolicy bool

	IdentityPrompt string
	IdentitySource string

	GlobalAgentsPrompt string
	GlobalAgentsSource string

	WorkspaceAgentsPrompt string
	WorkspaceAgentsSource string

	UserPrompt string
	UserSource string

	SkillsMetaPrompt string
	SkillsMetaSource string
}

// PromptFragment is one assembled prompt section.
type PromptFragment struct {
	Stage   string
	Source  string
	Content string
}

// Conflict represents one dropped lower-priority instruction.
type Conflict struct {
	Key          string
	WinnerStage  string
	DroppedStage string
	Reason       string
}

// AssembleResult is the final output consumed by llm agent config.
type AssembleResult struct {
	Prompt           string
	Fragments        []PromptFragment
	Warnings         []error
	DroppedConflicts []Conflict
}

// Assemble builds final system prompt from ordered pipeline modules.
func Assemble(spec AssembleSpec) (AssembleResult, error) {
	out := AssembleResult{
		Fragments:        []PromptFragment{},
		Warnings:         []error{},
		DroppedConflicts: []Conflict{},
	}

	if text := normalizeText(spec.IdentityPrompt); text != "" {
		out.Fragments = append(out.Fragments, PromptFragment{
			Stage:   "identity",
			Source:  strings.TrimSpace(spec.IdentitySource),
			Content: text,
		})
	}

	if text := normalizeText(spec.GlobalAgentsPrompt); text != "" {
		out.Fragments = append(out.Fragments, PromptFragment{
			Stage:   "global_agents",
			Source:  strings.TrimSpace(spec.GlobalAgentsSource),
			Content: text,
		})
	}

	if text := normalizeText(spec.WorkspaceAgentsPrompt); text != "" {
		out.Fragments = append(out.Fragments, PromptFragment{
			Stage:   "workspace_agents",
			Source:  strings.TrimSpace(spec.WorkspaceAgentsSource),
			Content: text,
		})
	}

	if spec.EnableLSPRoutingPolicy {
		out.Fragments = append(out.Fragments, PromptFragment{
			Stage:   "lsp_routing_policy",
			Source:  "builtin:lsp-routing-policy",
			Content: defaultLSPRoutingPolicy,
		})
	}
	if runtimeHint := normalizeText(spec.RuntimeHint); runtimeHint != "" {
		out.Fragments = append(out.Fragments, PromptFragment{
			Stage:   "runtime_context",
			Source:  "runtime execution context",
			Content: runtimeHint,
		})
	}

	userParts := make([]string, 0, 2)
	if text := normalizeText(spec.UserPrompt); text != "" {
		userParts = append(userParts, text)
	}
	if value := normalizeText(spec.BasePrompt); value != "" {
		userParts = append(userParts, "## Session Overrides\n\n"+value)
	}
	if len(userParts) > 0 {
		out.Fragments = append(out.Fragments, PromptFragment{
			Stage:   "user_custom",
			Source:  strings.TrimSpace(spec.UserSource),
			Content: strings.Join(userParts, "\n\n"),
		})
	}

	if skillText := normalizeText(spec.SkillsMetaPrompt); skillText != "" {
		out.Fragments = append(out.Fragments, PromptFragment{
			Stage:   "skills_meta",
			Source:  strings.TrimSpace(spec.SkillsMetaSource),
			Content: skillText,
		})
	}

	out.Prompt = renderPrompt(out.Fragments)
	return out, nil
}

func renderPrompt(fragments []PromptFragment) string {
	var b bytes.Buffer
	b.WriteString("Priority rule: higher sections override lower sections.\n")
	b.WriteString("Order: identity > global_agents > workspace_agents > lsp_routing_policy > runtime_context > user_custom > skills_meta.")
	for _, f := range fragments {
		text := normalizeText(f.Content)
		if text == "" {
			continue
		}
		b.WriteString("\n\n### ")
		b.WriteString(stageTitle(f.Stage))
		if strings.TrimSpace(f.Source) != "" {
			b.WriteString("\nsource: ")
			b.WriteString(f.Source)
		}
		b.WriteString("\n\n")
		b.WriteString(text)
	}
	return strings.TrimSpace(b.String())
}

func stageTitle(stage string) string {
	switch strings.TrimSpace(stage) {
	case "identity":
		return "Identity"
	case "global_agents":
		return "Global Instructions"
	case "workspace_agents":
		return "Workspace Instructions"
	case "user_custom":
		return "User Custom Instructions"
	case "lsp_routing_policy":
		return "LSP Routing Policy"
	case "runtime_context":
		return "Runtime Context"
	case "skills_meta":
		return "Skills Metadata"
	default:
		return "Instructions"
	}
}

func normalizeText(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	input = strings.TrimPrefix(input, "\ufeff")
	return strings.TrimSpace(input)
}
