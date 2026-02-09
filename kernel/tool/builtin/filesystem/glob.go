package filesystem

import (
	"context"
	"path/filepath"
	"sort"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
)

const (
	GlobToolName = "GLOB"
)

type GlobTool struct {
	runtime toolexec.Runtime
}

func NewGlob() *GlobTool {
	tool, _ := NewGlobWithRuntime(nil)
	return tool
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
	sort.Strings(matches)
	return map[string]any{
		"pattern": pattern,
		"matches": matches,
		"count":   len(matches),
	}, nil
}
