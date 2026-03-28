package prompting

import (
	"bytes"
	"strings"
)

type PromptFragmentKind string

const (
	PromptFragmentKindSystem   PromptFragmentKind = "system"
	PromptFragmentKindUser     PromptFragmentKind = "user"
	PromptFragmentKindContext  PromptFragmentKind = "context"
	PromptFragmentKindMetadata PromptFragmentKind = "metadata"
)

// AssembleSpec describes prompt assembly inputs.
type AssembleSpec struct {
	// Legacy system fragment input. Prefer classified PromptFragment inputs in Additional.
	IdentityPrompt string
	IdentitySource string

	// Legacy user fragment input. Prefer classified PromptFragment inputs in Additional.
	GlobalAgentsPrompt string
	GlobalAgentsSource string

	// Legacy user fragment input. Prefer classified PromptFragment inputs in Additional.
	WorkspaceAgentsPrompt string
	WorkspaceAgentsSource string

	// Legacy metadata input. Prefer classified PromptFragment inputs in Additional.
	SkillsMetaPrompt string
	SkillsMetaSource string

	// Preferred path for new prompt assembly inputs.
	Additional []PromptFragment
}

// PromptFragment is one assembled prompt section.
type PromptFragment struct {
	Kind    PromptFragmentKind
	Stage   string
	Title   string
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
			Kind:    PromptFragmentKindSystem,
			Stage:   "identity",
			Source:  strings.TrimSpace(spec.IdentitySource),
			Content: text,
		})
	}

	if text := normalizeText(spec.GlobalAgentsPrompt); text != "" {
		out.Fragments = append(out.Fragments, PromptFragment{
			Kind:    PromptFragmentKindUser,
			Stage:   "global_agents",
			Source:  strings.TrimSpace(spec.GlobalAgentsSource),
			Content: text,
		})
	}

	if text := normalizeText(spec.WorkspaceAgentsPrompt); text != "" {
		out.Fragments = append(out.Fragments, PromptFragment{
			Kind:    PromptFragmentKindUser,
			Stage:   "workspace_agents",
			Source:  strings.TrimSpace(spec.WorkspaceAgentsSource),
			Content: text,
		})
	}

	for _, fragment := range spec.Additional {
		if text := normalizeText(fragment.Content); text != "" {
			fragment.Kind = normalizeFragmentKind(fragment.Kind, fragment.Stage, text)
			out.Fragments = append(out.Fragments, PromptFragment{
				Kind:    fragment.Kind,
				Stage:   strings.TrimSpace(fragment.Stage),
				Title:   strings.TrimSpace(fragment.Title),
				Source:  strings.TrimSpace(fragment.Source),
				Content: text,
			})
		}
	}

	if skillText := normalizeText(spec.SkillsMetaPrompt); skillText != "" {
		out.Fragments = append(out.Fragments, PromptFragment{
			Kind:    PromptFragmentKindMetadata,
			Stage:   "skills_meta",
			Source:  strings.TrimSpace(spec.SkillsMetaSource),
			Content: skillText,
		})
	}

	out.Prompt = renderPrompt(out.Fragments)
	return out, nil
}

func renderPrompt(fragments []PromptFragment) string {
	systemFragments := make([]PromptFragment, 0, len(fragments))
	userFragments := make([]PromptFragment, 0, len(fragments))
	contextFragments := make([]PromptFragment, 0, len(fragments))
	metadataFragments := make([]PromptFragment, 0, len(fragments))

	for _, f := range fragments {
		switch normalizeFragmentKind(f.Kind, f.Stage, f.Content) {
		case PromptFragmentKindUser:
			userFragments = append(userFragments, f)
		case PromptFragmentKindContext:
			contextFragments = append(contextFragments, f)
		case PromptFragmentKindMetadata:
			metadataFragments = append(metadataFragments, f)
		default:
			systemFragments = append(systemFragments, f)
		}
	}

	parts := make([]string, 0, 4)
	if block := renderInstructionBlock("system_instructions", systemFragments, ""); block != "" {
		parts = append(parts, block)
	}
	if block := renderInstructionBlock("user_custom_instructions", userFragments, renderLegacyUserPrecedenceNote(userFragments)); block != "" {
		parts = append(parts, block)
	}
	if block := renderRawFragments(metadataFragments); block != "" {
		parts = append(parts, block)
	}
	if block := renderRawFragments(contextFragments); block != "" {
		parts = append(parts, block)
	}
	return strings.Join(parts, "\n\n")
}

func normalizeFragmentKind(kind PromptFragmentKind, stage string, content string) PromptFragmentKind {
	switch kind {
	case PromptFragmentKindSystem, PromptFragmentKindUser, PromptFragmentKindContext, PromptFragmentKindMetadata:
		return kind
	}

	stage = strings.ToLower(strings.TrimSpace(stage))
	switch stage {
	case "global_agents", "workspace_agents", "active_agent_policies", "session_overrides", "user_custom", "user_custom_instructions":
		return PromptFragmentKindUser
	case "workspace_context", "environment_context", "dynamic_runtime_context":
		return PromptFragmentKindContext
	case "skills_meta", "metadata":
		return PromptFragmentKindMetadata
	default:
		if strings.HasPrefix(strings.TrimSpace(content), "<environment_context>") {
			return PromptFragmentKindContext
		}
		return PromptFragmentKindSystem
	}
}

func renderInstructionBlock(tag string, fragments []PromptFragment, prefix string) string {
	body := renderRawFragments(fragments)
	if body == "" {
		return ""
	}

	var b bytes.Buffer
	b.WriteString("<")
	b.WriteString(tag)
	b.WriteString(">\n")
	if prefix = normalizeText(prefix); prefix != "" {
		b.WriteString(prefix)
		b.WriteString("\n\n")
	}
	b.WriteString(body)
	b.WriteString("\n</")
	b.WriteString(tag)
	b.WriteString(">")
	return b.String()
}

func renderRawFragments(fragments []PromptFragment) string {
	parts := make([]string, 0, len(fragments))
	for _, f := range fragments {
		text := normalizeText(f.Content)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}

func renderLegacyUserPrecedenceNote(fragments []PromptFragment) string {
	hasSession := false
	hasWorkspace := false
	hasGlobal := false
	for _, f := range fragments {
		switch strings.ToLower(strings.TrimSpace(f.Stage)) {
		case "session_overrides":
			hasSession = true
		case "workspace_agents":
			hasWorkspace = true
		case "global_agents":
			hasGlobal = true
		}
	}
	count := 0
	if hasSession {
		count++
	}
	if hasWorkspace {
		count++
	}
	if hasGlobal {
		count++
	}
	if count < 2 {
		return ""
	}
	return "Session overrides workspace instructions, and workspace instructions override global instructions on conflict."
}

func normalizeText(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	input = strings.TrimPrefix(input, "\ufeff")
	return strings.TrimSpace(input)
}
