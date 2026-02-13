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
	promptDirName       = "prompts"
	identityFileName    = "IDENTITY.md"
	globalAgentsFile    = "AGENTS.md"
	userFileName        = "USER.md"
	workspaceAgentsFile = "AGENTS.md"
)

type promptFiles struct {
	ConfigDir           string
	IdentityPath        string
	GlobalAgentsPath    string
	UserPath            string
	WorkspaceAgentsPath string
}

type promptSpecResult struct {
	Spec     promptpipeline.AssembleSpec
	Warnings []error
}

func buildPromptAssembleSpec(in buildAgentInput) (promptSpecResult, error) {
	workspaceDir, err := resolveWorkspaceDir(in.WorkspaceDir)
	if err != nil {
		return promptSpecResult{}, err
	}
	files, err := ensurePromptFiles(in.AppName, in.PromptConfigDir, workspaceDir)
	if err != nil {
		return promptSpecResult{}, err
	}
	identity, err := readRequiredPromptFile(files.IdentityPath)
	if err != nil {
		return promptSpecResult{}, err
	}
	global, err := readRequiredPromptFile(files.GlobalAgentsPath)
	if err != nil {
		return promptSpecResult{}, err
	}
	user, err := readRequiredPromptFile(files.UserPath)
	if err != nil {
		return promptSpecResult{}, err
	}
	workspaceAgents, workspaceWarn := readOptionalPromptFile(files.WorkspaceAgentsPath)

	discovered := skills.DiscoverMeta(in.SkillDirs)
	sort.Slice(discovered.Metas, func(i, j int) bool {
		return discovered.Metas[i].Path < discovered.Metas[j].Path
	})

	warnings := make([]error, 0, len(discovered.Warnings)+1)
	if workspaceWarn != nil {
		warnings = append(warnings, workspaceWarn)
	}
	warnings = append(warnings, discovered.Warnings...)

	return promptSpecResult{
		Spec: promptpipeline.AssembleSpec{
			BasePrompt:             in.BasePrompt,
			RuntimeHint:            in.RuntimeHint,
			EnableLSPRoutingPolicy: in.EnableLSPRoutingPolicy,
			IdentityPrompt:         identity,
			IdentitySource:         files.IdentityPath,
			GlobalAgentsPrompt:     global,
			GlobalAgentsSource:     files.GlobalAgentsPath,
			WorkspaceAgentsPrompt:  workspaceAgents,
			WorkspaceAgentsSource:  files.WorkspaceAgentsPath,
			UserPrompt:             user,
			UserSource:             files.UserPath,
			SkillsMetaPrompt:       skills.BuildMetaPrompt(discovered.Metas),
			SkillsMetaSource:       "skills metadata",
		},
		Warnings: warnings,
	}, nil
}

func ensurePromptFiles(appName string, configDir string, workspaceDir string) (promptFiles, error) {
	resolvedConfigDir, err := resolvePromptConfigDir(appName, configDir)
	if err != nil {
		return promptFiles{}, err
	}
	files := promptFiles{
		ConfigDir:           resolvedConfigDir,
		IdentityPath:        filepath.Join(resolvedConfigDir, identityFileName),
		GlobalAgentsPath:    filepath.Join(resolvedConfigDir, globalAgentsFile),
		UserPath:            filepath.Join(resolvedConfigDir, userFileName),
		WorkspaceAgentsPath: filepath.Join(workspaceDir, workspaceAgentsFile),
	}
	if err := os.MkdirAll(resolvedConfigDir, 0o700); err != nil {
		return promptFiles{}, fmt.Errorf("cli prompt: create config dir: %w", err)
	}
	defaults := promptpipeline.Defaults()
	if err := writePromptFileIfMissing(files.IdentityPath, defaults.Identity); err != nil {
		return promptFiles{}, err
	}
	if err := writePromptFileIfMissing(files.GlobalAgentsPath, defaults.GlobalAgents); err != nil {
		return promptFiles{}, err
	}
	if err := writePromptFileIfMissing(files.UserPath, defaults.User); err != nil {
		return promptFiles{}, err
	}
	return files, nil
}

func resolvePromptConfigDir(appName string, configDir string) (string, error) {
	if text := strings.TrimSpace(configDir); text != "" {
		return resolvePromptPath(text)
	}
	root, err := appDataDir(appName)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, promptDirName), nil
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

func writePromptFileIfMissing(path string, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cli prompt: stat %q: %w", path, err)
	}
	normalized := strings.TrimSpace(content)
	if normalized != "" {
		normalized += "\n"
	}
	if err := os.WriteFile(path, []byte(normalized), 0o600); err != nil {
		return fmt.Errorf("cli prompt: write default file %q: %w", path, err)
	}
	return nil
}

func readRequiredPromptFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cli prompt: read %q: %w", path, err)
	}
	return normalizePromptText(string(raw)), nil
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
