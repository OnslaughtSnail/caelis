package promptpipeline

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/OnslaughtSnail/caelis/kernel/skills"
)

const (
	defaultDirName         = "prompts"
	identityFileName       = "IDENTITY.md"
	globalAgentsFileName   = "AGENTS.md"
	userFileName           = "USER.md"
	workspaceAgentsFile    = "AGENTS.md"
	defaultFallbackAppName = "caelis"
)

// AssembleSpec describes prompt assembly inputs.
type AssembleSpec struct {
	AppName                string
	WorkspaceDir           string
	BasePrompt             string
	SkillDirs              []string
	ConfigDir              string
	EnableLSPRoutingPolicy bool
}

// PromptFiles lists prompt file locations for one app.
type PromptFiles struct {
	ConfigDir           string
	IdentityPath        string
	GlobalAgentsPath    string
	UserPath            string
	WorkspaceAgentsPath string
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
	Files            PromptFiles
}

// EnsurePromptFiles writes default prompt templates when files do not exist.
func EnsurePromptFiles(appName string, configDir string) (PromptFiles, error) {
	resolvedDir, err := resolveConfigDir(appName, configDir)
	if err != nil {
		return PromptFiles{}, err
	}
	files := PromptFiles{
		ConfigDir:        resolvedDir,
		IdentityPath:     filepath.Join(resolvedDir, identityFileName),
		GlobalAgentsPath: filepath.Join(resolvedDir, globalAgentsFileName),
		UserPath:         filepath.Join(resolvedDir, userFileName),
	}
	if err := os.MkdirAll(resolvedDir, 0o700); err != nil {
		return PromptFiles{}, fmt.Errorf("promptpipeline: create config dir: %w", err)
	}
	if err := writeFileIfMissing(files.IdentityPath, defaultIdentityTemplate); err != nil {
		return PromptFiles{}, err
	}
	if err := writeFileIfMissing(files.GlobalAgentsPath, defaultGlobalAgentsTemplate); err != nil {
		return PromptFiles{}, err
	}
	if err := writeFileIfMissing(files.UserPath, defaultUserTemplate); err != nil {
		return PromptFiles{}, err
	}
	return files, nil
}

// Assemble builds final system prompt from ordered pipeline modules.
func Assemble(spec AssembleSpec) (AssembleResult, error) {
	files, err := EnsurePromptFiles(spec.AppName, spec.ConfigDir)
	if err != nil {
		return AssembleResult{}, err
	}

	workspaceDir, err := resolveWorkspaceDir(spec.WorkspaceDir)
	if err != nil {
		return AssembleResult{}, err
	}
	files.WorkspaceAgentsPath = filepath.Join(workspaceDir, workspaceAgentsFile)
	out := AssembleResult{
		Fragments:        []PromptFragment{},
		Warnings:         []error{},
		DroppedConflicts: []Conflict{},
		Files:            files,
	}

	if text, readErr := readRequiredPrompt(files.IdentityPath); readErr != nil {
		return AssembleResult{}, readErr
	} else if text != "" {
		out.Fragments = append(out.Fragments, PromptFragment{
			Stage:   "identity",
			Source:  files.IdentityPath,
			Content: text,
		})
	}

	if text, readErr := readRequiredPrompt(files.GlobalAgentsPath); readErr != nil {
		return AssembleResult{}, readErr
	} else if text != "" {
		out.Fragments = append(out.Fragments, PromptFragment{
			Stage:   "global_agents",
			Source:  files.GlobalAgentsPath,
			Content: text,
		})
	}

	if text, readErr := readOptionalPrompt(files.WorkspaceAgentsPath); readErr != nil {
		out.Warnings = append(out.Warnings, readErr)
	} else if text != "" {
		out.Fragments = append(out.Fragments, PromptFragment{
			Stage:   "workspace_agents",
			Source:  files.WorkspaceAgentsPath,
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

	userParts := make([]string, 0, 2)
	if text, readErr := readRequiredPrompt(files.UserPath); readErr != nil {
		return AssembleResult{}, readErr
	} else if text != "" {
		userParts = append(userParts, text)
	}
	if value := normalizeText(spec.BasePrompt); value != "" {
		userParts = append(userParts, "## Session Overrides\n\n"+value)
	}
	if len(userParts) > 0 {
		out.Fragments = append(out.Fragments, PromptFragment{
			Stage:   "user_custom",
			Source:  files.UserPath,
			Content: strings.Join(userParts, "\n\n"),
		})
	}

	discovered := skills.DiscoverMeta(spec.SkillDirs)
	if len(discovered.Warnings) > 0 {
		out.Warnings = append(out.Warnings, discovered.Warnings...)
	}
	sort.Slice(discovered.Metas, func(i, j int) bool {
		return discovered.Metas[i].Path < discovered.Metas[j].Path
	})
	if skillText := normalizeText(skills.BuildMetaPrompt(discovered.Metas)); skillText != "" {
		out.Fragments = append(out.Fragments, PromptFragment{
			Stage:   "skills_meta",
			Source:  "skills metadata",
			Content: skillText,
		})
	}

	out.Prompt = renderPrompt(out.Fragments)
	return out, nil
}

func resolveConfigDir(appName string, configDir string) (string, error) {
	if text := strings.TrimSpace(configDir); text != "" {
		return resolvePath(text)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("promptpipeline: resolve user home: %w", err)
	}
	return filepath.Join(home, "."+normalizedAppName(appName), defaultDirName), nil
}

func resolveWorkspaceDir(workspaceDir string) (string, error) {
	if strings.TrimSpace(workspaceDir) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("promptpipeline: resolve cwd: %w", err)
		}
		workspaceDir = cwd
	}
	return resolvePath(workspaceDir)
}

func resolvePath(path string) (string, error) {
	value := strings.TrimSpace(path)
	if value == "" {
		return "", fmt.Errorf("promptpipeline: empty path")
	}
	if strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("promptpipeline: resolve user home: %w", err)
		}
		value = filepath.Join(home, strings.TrimPrefix(value, "~/"))
	}
	if !filepath.IsAbs(value) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("promptpipeline: resolve cwd: %w", err)
		}
		value = filepath.Join(cwd, value)
	}
	return filepath.Clean(value), nil
}

func writeFileIfMissing(path string, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("promptpipeline: stat %q: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		return fmt.Errorf("promptpipeline: write default file %q: %w", path, err)
	}
	return nil
}

func readRequiredPrompt(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("promptpipeline: read %q: %w", path, err)
	}
	return normalizeText(string(raw)), nil
}

func readOptionalPrompt(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("promptpipeline: read %q: %w", path, err)
	}
	return normalizeText(string(raw)), nil
}

func renderPrompt(fragments []PromptFragment) string {
	var b bytes.Buffer
	b.WriteString("Priority rule: higher sections override lower sections.\n")
	b.WriteString("Order: identity > global_agents > workspace_agents > lsp_routing_policy > user_custom > skills_meta.")
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

func normalizedAppName(appName string) string {
	name := sanitizeAppName(appName)
	if name == "" {
		return defaultFallbackAppName
	}
	return name
}

func sanitizeAppName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return strings.ToLower(strings.Trim(b.String(), "_"))
}
