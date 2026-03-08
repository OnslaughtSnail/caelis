package promptpipeline

import (
	"bytes"
	"strings"
)

// AssembleSpec describes prompt assembly inputs.
type AssembleSpec struct {
	IdentityPrompt string
	IdentitySource string

	GlobalAgentsPrompt string
	GlobalAgentsSource string

	WorkspaceAgentsPrompt string
	WorkspaceAgentsSource string

	SkillsMetaPrompt string
	SkillsMetaSource string

	Additional []PromptFragment
}

// PromptFragment is one assembled prompt section.
type PromptFragment struct {
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

	for _, fragment := range spec.Additional {
		if text := normalizeText(fragment.Content); text != "" {
			out.Fragments = append(out.Fragments, PromptFragment{
				Stage:   strings.TrimSpace(fragment.Stage),
				Title:   strings.TrimSpace(fragment.Title),
				Source:  strings.TrimSpace(fragment.Source),
				Content: text,
			})
		}
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
	b.WriteString("Priority rule: earlier sections override later sections.")
	for _, f := range fragments {
		text := normalizeText(f.Content)
		if text == "" {
			continue
		}
		b.WriteString("\n\n### ")
		b.WriteString(fragmentTitle(f))
		if strings.TrimSpace(f.Source) != "" {
			b.WriteString("\nsource: ")
			b.WriteString(f.Source)
		}
		b.WriteString("\n\n")
		b.WriteString(text)
	}
	return strings.TrimSpace(b.String())
}

func fragmentTitle(fragment PromptFragment) string {
	if title := strings.TrimSpace(fragment.Title); title != "" {
		return title
	}
	switch strings.TrimSpace(fragment.Stage) {
	case "identity":
		return "Identity"
	case "global_agents":
		return "Global Instructions"
	case "workspace_agents":
		return "Workspace Instructions"
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
