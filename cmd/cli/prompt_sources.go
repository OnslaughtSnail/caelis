package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/promptpipeline"
	"github.com/OnslaughtSnail/caelis/kernel/skills"
)

const (
	globalAgentsFilePath = "~/.agents/AGENTS.md"
	workspaceAgentsFile  = "AGENTS.md"
)

type promptSpecResult struct {
	Spec     promptpipeline.AssembleSpec
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

	discovered := skills.DiscoverMeta(in.SkillDirs)
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

	additional := make([]promptpipeline.PromptFragment, 0, 3)
	if activePolicies := buildActiveAgentPoliciesPrompt(globalAgents, workspaceAgents, workspaceAgentsPath); activePolicies != "" {
		additional = append(additional, promptpipeline.PromptFragment{
			Stage:   "active_agent_policies",
			Source:  "cli:active-agent-policies",
			Content: activePolicies,
		})
	}
	if sessionOverrides := buildSessionOverridePrompt(in.BasePrompt); sessionOverrides != "" {
		additional = append(additional, promptpipeline.PromptFragment{
			Stage:   "session_overrides",
			Source:  "cli:session-overrides",
			Content: sessionOverrides,
		})
	}
	if in.EnableExperimentalLSPPrompt {
		additional = append(additional, promptpipeline.PromptFragment{
			Stage:   "experimental_lsp",
			Source:  "cli:experimental-lsp-routing",
			Content: "## Experimental LSP Routing\n\n" + defaultExperimentalLSPRoutingPrompt,
		})
	}

	return promptSpecResult{
		Spec: promptpipeline.AssembleSpec{
			IdentityPrompt:   builtInIdentityPrompt(in.AppName),
			IdentitySource:   "cli:built-in-identity",
			SkillsMetaPrompt: skills.BuildMetaPrompt(discovered.Metas),
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

func buildActiveAgentPoliciesPrompt(globalAgents string, workspaceAgents string, workspaceAgentsPath string) string {
	_ = workspaceAgentsPath
	sections := make([]string, 0, 2)
	if text := normalizePromptText(globalAgents); text != "" {
		sections = append(sections, strings.Join([]string{
			"## Global User Policy",
			"",
			text,
		}, "\n"))
	}
	if text := normalizePromptText(workspaceAgents); text != "" {
		sections = append(sections, strings.Join([]string{
			"## Project Policy",
			"Overrides conflicting global instructions.",
			"",
			text,
		}, "\n"))
	}
	if len(sections) == 0 {
		return ""
	}
	return "# Active Agent Policies\n\n" + strings.Join(sections, "\n\n")
}

func buildSessionOverridePrompt(basePrompt string) string {
	value := normalizePromptText(basePrompt)
	if value == "" {
		return ""
	}
	return "## Session Overrides\n\n" + value
}
