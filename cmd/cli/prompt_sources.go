package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	appprompting "github.com/OnslaughtSnail/caelis/internal/app/prompting"
	appskills "github.com/OnslaughtSnail/caelis/internal/app/skills"
)

const (
	globalAgentsFilePath = "~/.agents/AGENTS.md"
	workspaceAgentsFile  = "AGENTS.md"
)

type promptSpecResult struct {
	Spec     appprompting.AssembleSpec
	Warnings []error
}

func buildPromptAssembleSpec(in buildAgentInput) (promptSpecResult, error) {
	workspaceDir, err := resolveWorkspaceDir(in.WorkspaceDir)
	if err != nil {
		return promptSpecResult{}, err
	}
	globalAgentsPath, err := resolvePromptPath(globalAgentsFilePath)
	if err != nil {
		return promptSpecResult{}, err
	}
	workspaceAgentsPath := filepath.Join(workspaceDir, workspaceAgentsFile)
	globalAgents, globalWarn := readOptionalPromptFile(globalAgentsPath)
	workspaceAgents, workspaceWarn := readOptionalPromptFile(workspaceAgentsPath)

	discovered := appskills.DiscoverMeta(in.SkillDirs)
	sort.Slice(discovered.Metas, func(i, j int) bool {
		return discovered.Metas[i].Path < discovered.Metas[j].Path
	})

	warnings := make([]error, 0, len(discovered.Warnings)+2)
	if globalWarn != nil {
		warnings = append(warnings, globalWarn)
	}
	if workspaceWarn != nil {
		warnings = append(warnings, workspaceWarn)
	}
	warnings = append(warnings, discovered.Warnings...)

	additional := make([]appprompting.PromptFragment, 0, 4)
	if userInstructions := buildUserCustomInstructionsPrompt(in.BasePrompt, workspaceAgents, globalAgents); userInstructions != "" {
		additional = append(additional, appprompting.PromptFragment{
			Kind:    appprompting.PromptFragmentKindUser,
			Stage:   "user_custom_instructions",
			Source:  "cli:user-custom-instructions",
			Content: userInstructions,
		})
	}
	if agentSupport := buildSystemAgentDelegationPrompt(in.DefaultAgent, in.AgentDescriptors); agentSupport != "" {
		additional = append(additional, appprompting.PromptFragment{
			Kind:    appprompting.PromptFragmentKindSystem,
			Stage:   "acp_agents",
			Source:  "cli:acp-agent-support",
			Content: agentSupport,
		})
	}
	if workspaceContext := builtInEnvironmentContextPrompt(workspaceDir); workspaceContext != "" {
		additional = append(additional, appprompting.PromptFragment{
			Kind:    appprompting.PromptFragmentKindContext,
			Stage:   "workspace_context",
			Source:  "cli:workspace-context",
			Content: workspaceContext,
		})
	}
	if in.EnableExperimentalLSPPrompt {
		additional = append(additional, appprompting.PromptFragment{
			Kind:    appprompting.PromptFragmentKindSystem,
			Stage:   "experimental_lsp",
			Source:  "cli:experimental-lsp-routing",
			Content: "## Experimental LSP Routing\n\n" + defaultExperimentalLSPRoutingPrompt,
		})
	}

	return promptSpecResult{
		Spec: appprompting.AssembleSpec{
			IdentityPrompt:   builtInSystemIdentityPrompt(in.AppName),
			IdentitySource:   "cli:built-in-identity",
			SkillsMetaPrompt: appskills.BuildMetaPrompt(discovered.Metas),
			SkillsMetaSource: "skills metadata",
			Additional:       additional,
		},
		Warnings: warnings,
	}, nil
}

func resolveWorkspaceDir(workspaceDir string) (string, error) {
	if strings.TrimSpace(workspaceDir) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("cli prompt: resolve cwd: %w", err)
		}
		workspaceDir = cwd
	}
	return resolvePromptPath(workspaceDir)
}

func resolvePromptPath(path string) (string, error) {
	value := strings.TrimSpace(path)
	if value == "" {
		return "", fmt.Errorf("cli prompt: empty path")
	}
	if strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cli prompt: resolve user home: %w", err)
		}
		value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	if !filepath.IsAbs(value) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("cli prompt: resolve cwd: %w", err)
		}
		value = filepath.Join(cwd, value)
	}
	return filepath.Clean(value), nil
}

func readOptionalPromptFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("cli prompt: read %q: %w", path, err)
	}
	return normalizePromptText(string(raw)), nil
}

func normalizePromptText(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	input = strings.TrimPrefix(input, "\ufeff")
	return strings.TrimSpace(input)
}

func buildUserCustomInstructionsPrompt(sessionPrompt string, workspaceAgents string, globalAgents string) string {
	sections := make([]string, 0, 3)
	if text := normalizePromptText(sessionPrompt); text != "" {
		sections = append(sections, strings.Join([]string{
			"## Session Overrides",
			"",
			text,
		}, "\n"))
	}
	if text := normalizePromptText(workspaceAgents); text != "" {
		sections = append(sections, strings.Join([]string{
			"## Workspace Instructions",
			"",
			text,
		}, "\n"))
	}
	if text := normalizePromptText(globalAgents); text != "" {
		sections = append(sections, strings.Join([]string{
			"## Global Instructions",
			"",
			text,
		}, "\n"))
	}
	if len(sections) == 0 {
		return ""
	}

	lines := []string{}
	if len(sections) > 1 {
		lines = append(lines, "Session overrides workspace instructions, and workspace instructions override global instructions on conflict.")
		lines = append(lines, "")
	}
	lines = append(lines, sections...)
	return strings.Join(lines, "\n\n")
}

func buildSystemAgentDelegationPrompt(defaultAgent string, configured []appagents.Descriptor) string {
	if strings.TrimSpace(defaultAgent) == "" && len(configured) == 0 {
		return ""
	}
	agents := make([]appagents.Descriptor, 0, len(configured)+1)
	agents = append(agents, appagents.SelfDescriptor())
	seen := map[string]struct{}{"self": {}}
	for _, desc := range configured {
		id := strings.TrimSpace(desc.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		agents = append(agents, desc)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	lines := []string{"## Agent Delegation"}
	if value := strings.TrimSpace(defaultAgent); value != "" {
		lines = append(lines, "- default_agent="+value)
	}
	for _, desc := range agents {
		stability := strings.TrimSpace(desc.Stability)
		if stability == "" {
			stability = appagents.StabilityExperimental
		}
		line := fmt.Sprintf("- agent=%s stability=%s", desc.ID, stability)
		if text := strings.TrimSpace(desc.Description); text != "" {
			line += " desc=" + text
		}
		lines = append(lines, line)
	}
	lines = append(lines,
		"- Use SPAWN to start a delegated child session when the task benefits from delegation.",
		"- SPAWN accepts prompt(required), agent, yield_seconds, timeout_seconds.",
		"- Use TASK write with the SPAWN task_id to continue an existing child session.",
	)
	return strings.Join(lines, "\n")
}
