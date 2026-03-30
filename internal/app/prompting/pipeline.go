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
	Kind          PromptFragmentKind
	Stage         string
	Title         string
	Source        string
	Content       string
	SourceVersion string
	Precedence    int
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
	out.Fragments = compatNormalizeFragments(spec)
	out.Prompt = renderPrompt(out.Fragments)
	return out, nil
}

func renderPrompt(fragments []PromptFragment) string {
	systemFragments := make([]PromptFragment, 0, len(fragments))
	userFragments := make([]PromptFragment, 0, len(fragments))
	contextFragments := make([]PromptFragment, 0, len(fragments))
	metadataFragments := make([]PromptFragment, 0, len(fragments))

	for _, f := range fragments {
		switch f.Kind {
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
	if block := renderInstructionBlock("user_custom_instructions", userFragments, ""); block != "" {
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

func compatNormalizeFragments(spec AssembleSpec) []PromptFragment {
	fragments := make([]PromptFragment, 0, len(spec.Additional)+5)
	if note := legacyUserPrecedenceNote(spec); note != "" {
		fragments = append(fragments, PromptFragment{
			Kind:          PromptFragmentKindUser,
			Stage:         "user_precedence_notice",
			Content:       note,
			SourceVersion: "legacy",
			Precedence:    0,
		})
	}
	if text := normalizeText(spec.IdentityPrompt); text != "" {
		fragments = append(fragments, PromptFragment{
			Kind:          PromptFragmentKindSystem,
			Stage:         "identity",
			Source:        strings.TrimSpace(spec.IdentitySource),
			Content:       text,
			SourceVersion: "legacy",
			Precedence:    10,
		})
	}
	if text := normalizeText(spec.GlobalAgentsPrompt); text != "" {
		fragments = append(fragments, PromptFragment{
			Kind:          PromptFragmentKindUser,
			Stage:         "global_agents",
			Source:        strings.TrimSpace(spec.GlobalAgentsSource),
			Content:       text,
			SourceVersion: "legacy",
			Precedence:    20,
		})
	}
	if text := normalizeText(spec.WorkspaceAgentsPrompt); text != "" {
		fragments = append(fragments, PromptFragment{
			Kind:          PromptFragmentKindUser,
			Stage:         "workspace_agents",
			Source:        strings.TrimSpace(spec.WorkspaceAgentsSource),
			Content:       text,
			SourceVersion: "legacy",
			Precedence:    30,
		})
	}
	for _, fragment := range spec.Additional {
		if text := normalizeText(fragment.Content); text != "" {
			fragments = append(fragments, PromptFragment{
				Kind:          normalizeFragmentKind(fragment.Kind, fragment.Stage),
				Stage:         strings.TrimSpace(fragment.Stage),
				Title:         strings.TrimSpace(fragment.Title),
				Source:        strings.TrimSpace(fragment.Source),
				Content:       text,
				SourceVersion: firstNonEmpty(strings.TrimSpace(fragment.SourceVersion), "vnext"),
				Precedence:    fragment.Precedence,
			})
		}
	}
	if skillText := normalizeText(spec.SkillsMetaPrompt); skillText != "" {
		fragments = append(fragments, PromptFragment{
			Kind:          PromptFragmentKindMetadata,
			Stage:         "skills_meta",
			Source:        strings.TrimSpace(spec.SkillsMetaSource),
			Content:       skillText,
			SourceVersion: "legacy",
			Precedence:    40,
		})
	}
	return fragments
}

func normalizeFragmentKind(kind PromptFragmentKind, stage string) PromptFragmentKind {
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

func legacyUserPrecedenceNote(spec AssembleSpec) string {
	hasSession := false
	hasWorkspace := normalizeText(spec.WorkspaceAgentsPrompt) != ""
	hasGlobal := normalizeText(spec.GlobalAgentsPrompt) != ""
	for _, f := range spec.Additional {
		switch strings.ToLower(strings.TrimSpace(f.Stage)) {
		case "session_overrides":
			hasSession = normalizeText(f.Content) != ""
		case "workspace_agents":
			hasWorkspace = hasWorkspace || normalizeText(f.Content) != ""
		case "global_agents":
			hasGlobal = hasGlobal || normalizeText(f.Content) != ""
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeText(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	input = strings.TrimPrefix(input, "\ufeff")
	return strings.TrimSpace(input)
}
