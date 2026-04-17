package filesystem

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
	"github.com/OnslaughtSnail/caelis/kernel/tool/capability"
)

const (
	GlobToolName = "GLOB"
)

type GlobTool struct {
	runtime toolexec.Runtime
}

func NewGlobWithRuntime(runtime toolexec.Runtime) (*GlobTool, error) {
	resolvedRuntime, err := runtimeOrDefault(runtime)
	if err != nil {
		return nil, err
	}
	return &GlobTool{runtime: resolvedRuntime}, nil
}

func (t *GlobTool) Name() string {
	return GlobToolName
}

func (t *GlobTool) Description() string {
	return "Match files by glob pattern."
}

func (t *GlobTool) Capability() capability.Capability {
	return capability.Capability{
		Operations: []capability.Operation{capability.OperationFileRead},
		Risk:       capability.RiskLow,
	}
}

func (t *GlobTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "glob pattern"},
				"exclude": map[string]any{
					"type":        "array",
					"description": "Optional relative path patterns to exclude after gitignore filtering.",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required": []string{"pattern"},
		},
	}
}

func (t *GlobTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	pattern, err := argparse.String(args, "pattern", true)
	if err != nil {
		return nil, err
	}
	exclude, err := parseStringSliceArg(args, "exclude")
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(pattern) {
		wd, err := t.runtime.FileSystem().Getwd()
		if err != nil {
			return nil, err
		}
		pattern = filepath.Join(wd, pattern)
	}
	pattern = filepath.Clean(pattern)
	matches := make([]string, 0, 16)
	if !hasPathGlobMeta(filepath.ToSlash(pattern)) {
		if info, err := t.runtime.FileSystem().Stat(pattern); err == nil {
			root := filepath.Dir(pattern)
			if !shouldExcludePath(root, pattern, info.IsDir(), exclude) {
				matcher, err := newGitignoreMatcher(t.runtime.FileSystem(), root)
				if err != nil {
					return nil, err
				}
				ignored := false
				if matcher != nil {
					ignored, err = matcher.Match(pattern, info.IsDir())
					if err != nil {
						return nil, err
					}
				}
				if !ignored {
					matches = append(matches, pattern)
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		sort.Strings(matches)
		return map[string]any{
			"pattern": pattern,
			"matches": matches,
			"count":   len(matches),
		}, nil
	}
	root, relPattern := splitAbsoluteGlobPattern(pattern)
	if relPattern == "" {
		relPattern = filepath.Base(pattern)
	}
	if _, err := t.runtime.FileSystem().Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{
				"pattern": pattern,
				"matches": matches,
				"count":   0,
			}, nil
		}
		return nil, err
	}
	matcher, err := newGitignoreMatcher(t.runtime.FileSystem(), root)
	if err != nil {
		return nil, err
	}
	err = walkDir(t.runtime.FileSystem(), root, func(candidate string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d == nil {
			return nil
		}
		if candidate != root && matcher != nil {
			ignored, err := matcher.Match(candidate, d.IsDir())
			if err != nil {
				return err
			}
			if ignored {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		if candidate != root && shouldExcludePath(root, candidate, d.IsDir(), exclude) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, candidate)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if pathGlobMatch(relPattern, rel) {
			matches = append(matches, candidate)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return map[string]any{
		"pattern": pattern,
		"matches": matches,
		"count":   len(matches),
	}, nil
}

func (t *GlobTool) WithRuntime(runtime toolexec.Runtime) (*GlobTool, error) {
	return NewGlobWithRuntime(runtime)
}
