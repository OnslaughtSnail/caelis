package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"

	clilspadapter "github.com/OnslaughtSnail/caelis/internal/cli/lspadapter/gopls"
	"github.com/OnslaughtSnail/caelis/internal/cli/lspbroker"
	"github.com/OnslaughtSnail/caelis/internal/gitignorefilter"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

const providerLSPTools = "lsp_tools"

type lspServerCandidate struct {
	Command string
	Args    []string
}

type lspLanguageSpec struct {
	Language    string
	LanguageID  string
	Priority    int
	RootMarkers []string
	Extensions  []string
	Servers     []lspServerCandidate
}

var defaultLSPLanguageSpecs = []lspLanguageSpec{
	{
		Language:    "go",
		LanguageID:  "go",
		Priority:    100,
		RootMarkers: []string{"go.mod", "go.work"},
		Extensions:  []string{".go"},
		Servers: []lspServerCandidate{
			{Command: "gopls", Args: []string{"serve"}},
		},
	},
	{
		Language:    "python",
		LanguageID:  "python",
		Priority:    90,
		RootMarkers: []string{"pyproject.toml", "requirements.txt", "setup.py"},
		Extensions:  []string{".py"},
		Servers: []lspServerCandidate{
			{Command: "pyright-langserver", Args: []string{"--stdio"}},
			{Command: "pylsp"},
		},
	},
	{
		Language:    "typescript",
		LanguageID:  "typescript",
		Priority:    85,
		RootMarkers: []string{"tsconfig.json"},
		Extensions:  []string{".ts", ".tsx", ".mts", ".cts"},
		Servers: []lspServerCandidate{
			{Command: "typescript-language-server", Args: []string{"--stdio"}},
		},
	},
	{
		Language:    "javascript",
		LanguageID:  "javascript",
		Priority:    80,
		RootMarkers: []string{"package.json", "jsconfig.json"},
		Extensions:  []string{".js", ".jsx", ".mjs", ".cjs"},
		Servers: []lspServerCandidate{
			{Command: "typescript-language-server", Args: []string{"--stdio"}},
		},
	},
	{
		Language:    "rust",
		LanguageID:  "rust",
		Priority:    75,
		RootMarkers: []string{"Cargo.toml"},
		Extensions:  []string{".rs"},
		Servers: []lspServerCandidate{
			{Command: "rust-analyzer"},
		},
	},
	{
		Language:    "cpp",
		LanguageID:  "cpp",
		Priority:    70,
		RootMarkers: []string{"CMakeLists.txt", "compile_commands.json"},
		Extensions:  []string{".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx"},
		Servers: []lspServerCandidate{
			{Command: "clangd"},
		},
	},
	{
		Language:    "c",
		LanguageID:  "c",
		Priority:    65,
		RootMarkers: []string{"CMakeLists.txt", "compile_commands.json", "Makefile"},
		Extensions:  []string{".c", ".h"},
		Servers: []lspServerCandidate{
			{Command: "clangd"},
		},
	},
}

type cliLSPToolProvider struct {
	workspaceDir string
	runtime      toolexec.Runtime
}

func (p cliLSPToolProvider) Name() string {
	return providerLSPTools
}

func (p cliLSPToolProvider) Tools(ctx context.Context) ([]tool.Tool, error) {
	return resolveWorkspaceLSPTools(ctx, p.workspaceDir, p.runtime)
}

func registerCLILSPToolProvider(registry *plugin.Registry, workspaceDir string, execRuntime toolexec.Runtime) error {
	if registry == nil {
		return errors.New("cli lsp: plugin registry is nil")
	}
	return registry.RegisterToolProvider(cliLSPToolProvider{
		workspaceDir: workspaceDir,
		runtime:      execRuntime,
	})
}

func resolveWorkspaceLSPTools(ctx context.Context, workspaceDir string, execRuntime toolexec.Runtime) ([]tool.Tool, error) {
	root := workspaceRoot(workspaceDir)
	broker := lspbroker.New()
	available := make([]lspLanguageSpec, 0, len(defaultLSPLanguageSpecs))
	for _, spec := range defaultLSPLanguageSpecs {
		command, args, ok := resolveLSPServerCommand(root, spec.Servers)
		if !ok {
			continue
		}
		adapter, err := clilspadapter.New(clilspadapter.Config{
			Runtime:    execRuntime,
			Language:   spec.Language,
			LanguageID: spec.LanguageID,
			Command:    command,
			Args:       args,
		})
		if err != nil {
			continue
		}
		if err := broker.RegisterAdapter(adapter); err != nil {
			continue
		}
		available = append(available, spec)
	}
	if len(available) == 0 {
		return nil, nil
	}
	language := detectPrimaryLanguage(root, available)
	if language == "" {
		return nil, nil
	}
	toolset, err := broker.Resolve(ctx, lspbroker.ActivateRequest{
		Language:  language,
		Workspace: root,
	})
	if err != nil {
		return nil, nil
	}
	if toolset == nil {
		return nil, nil
	}
	return append([]tool.Tool(nil), toolset.Tools...), nil
}

func detectPrimaryLanguage(workspaceDir string, specs []lspLanguageSpec) string {
	if len(specs) == 0 {
		return ""
	}
	bestLanguage := ""
	bestScore := -1
	for _, spec := range specs {
		hasMarker := workspaceHasRootMarker(workspaceDir, spec.RootMarkers)
		extMatches := countWorkspaceExtensionMatches(workspaceDir, spec.Extensions, 1200)
		// Require positive workspace evidence so high static priorities cannot
		// select unrelated languages when multiple servers are installed.
		if !hasMarker && extMatches == 0 {
			continue
		}
		score := spec.Priority
		if hasMarker {
			score += 100
		}
		score += minInt(extMatches, 30)
		if score > bestScore {
			bestScore = score
			bestLanguage = spec.Language
		}
	}
	return strings.TrimSpace(bestLanguage)
}

func resolveLSPServerCommand(workspaceDir string, candidates []lspServerCandidate) (string, []string, bool) {
	for _, candidate := range candidates {
		command := strings.TrimSpace(candidate.Command)
		if command == "" {
			continue
		}
		resolved, ok := lookupExecutable(workspaceDir, command)
		if !ok {
			continue
		}
		args := append([]string(nil), candidate.Args...)
		return resolved, args, true
	}
	return "", nil, false
}

func lookupExecutable(workspaceDir string, command string) (string, bool) {
	if strings.TrimSpace(command) == "" {
		return "", false
	}
	if filepath.IsAbs(command) {
		if isExecutableFile(command) {
			return command, true
		}
		return "", false
	}
	if root := strings.TrimSpace(workspaceDir); root != "" {
		localBin := filepath.Join(root, "node_modules", ".bin", command)
		if path, ok := firstExecutable(localBin, localBin+".cmd"); ok {
			return path, true
		}
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return "", false
	}
	return resolved, true
}

func firstExecutable(paths ...string) (string, bool) {
	for _, one := range paths {
		if isExecutableFile(one) {
			return one, true
		}
	}
	return "", false
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info == nil || info.IsDir() {
		return false
	}
	if goruntime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

func workspaceRoot(workspaceDir string) string {
	root := strings.TrimSpace(workspaceDir)
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return ""
		}
		root = cwd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	return filepath.Clean(abs)
}

func workspaceHasRootMarker(workspaceDir string, markers []string) bool {
	if strings.TrimSpace(workspaceDir) == "" || len(markers) == 0 {
		return false
	}
	for _, marker := range markers {
		trimmed := strings.TrimSpace(marker)
		if trimmed == "" {
			continue
		}
		path := filepath.Join(workspaceDir, trimmed)
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

var errStopLanguageWalk = errors.New("stop language walk")

func countWorkspaceExtensionMatches(workspaceDir string, extensions []string, maxFiles int) int {
	if strings.TrimSpace(workspaceDir) == "" || len(extensions) == 0 || maxFiles <= 0 {
		return 0
	}
	extSet := map[string]struct{}{}
	for _, ext := range extensions {
		normalized := strings.ToLower(strings.TrimSpace(ext))
		if normalized == "" {
			continue
		}
		if !strings.HasPrefix(normalized, ".") {
			normalized = "." + normalized
		}
		extSet[normalized] = struct{}{}
	}
	if len(extSet) == 0 {
		return 0
	}

	matches := 0
	scanned := 0
	root := workspaceRoot(workspaceDir)
	matcher, err := gitignorefilter.NewForPath(osFileSystemAdapter{}, root)
	if err != nil {
		return 0
	}
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipLSPScanDir(root, path, d.Name()) {
				return filepath.SkipDir
			}
			if path != root && matcher != nil {
				ignored, err := matcher.Match(path, true)
				if err != nil {
					return nil
				}
				if ignored {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if matcher != nil {
			ignored, err := matcher.Match(path, false)
			if err != nil {
				return nil
			}
			if ignored {
				return nil
			}
		}
		scanned++
		if scanned > maxFiles {
			return errStopLanguageWalk
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if _, ok := extSet[ext]; ok {
			matches++
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errStopLanguageWalk) {
		return matches
	}
	return matches
}

func shouldSkipLSPScanDir(root string, path string, name string) bool {
	if path == root {
		return false
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	switch trimmed {
	case ".git", ".hg", ".svn", ".idea", ".vscode", "node_modules", "vendor", "build", "dist", "target", ".next", ".cache", ".venv", "venv":
		return true
	}
	return strings.HasPrefix(trimmed, ".")
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
