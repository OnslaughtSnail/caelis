package filesystem

import (
	"context"
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
	if !filepath.IsAbs(pattern) {
		wd, err := t.runtime.FileSystem().Getwd()
		if err != nil {
			return nil, err
		}
		pattern = filepath.Join(wd, pattern)
	}
	pattern = filepath.Clean(pattern)
	matches, err := t.runtime.FileSystem().Glob(pattern)
	if err != nil {
		return nil, err
	}
	matcher, err := newGitignoreMatcher(t.runtime.FileSystem(), pattern)
	if err != nil {
		return nil, err
	}
	filtered := matches[:0]
	for _, match := range matches {
		info, statErr := t.runtime.FileSystem().Stat(match)
		if statErr != nil {
			continue
		}
		if matcher != nil {
			ignored, err := matcher.Match(match, info.IsDir())
			if err != nil {
				return nil, err
			}
			if ignored {
				continue
			}
		}
		filtered = append(filtered, match)
	}
	matches = filtered
	sort.Strings(matches)
	return map[string]any{
		"pattern": pattern,
		"matches": matches,
		"count":   len(matches),
	}, nil
}
