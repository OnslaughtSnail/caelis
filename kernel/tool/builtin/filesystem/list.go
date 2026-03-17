package filesystem

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
	"github.com/OnslaughtSnail/caelis/kernel/tool/capability"
)

const (
	ListToolName = "LIST"
)

type ListTool struct {
	runtime toolexec.Runtime
}

func NewListWithRuntime(runtime toolexec.Runtime) (*ListTool, error) {
	resolvedRuntime, err := runtimeOrDefault(runtime)
	if err != nil {
		return nil, err
	}
	return &ListTool{runtime: resolvedRuntime}, nil
}

func (t *ListTool) Name() string {
	return ListToolName
}

func (t *ListTool) Description() string {
	return "List files and directories in one path."
}

func (t *ListTool) Capability() capability.Capability {
	return capability.Capability{
		Operations: []capability.Operation{capability.OperationFileRead},
		Risk:       capability.RiskLow,
	}
}

func (t *ListTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "directory path"},
			},
		},
	}
}

func (t *ListTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	pathArg, err := argparse.String(args, "path", false)
	if err != nil {
		return nil, err
	}
	if pathArg == "" {
		pathArg = "."
	}
	target, err := normalizePathWithFS(t.runtime.FileSystem(), pathArg)
	if err != nil {
		return nil, err
	}
	matcher, err := newGitignoreMatcher(t.runtime.FileSystem(), target)
	if err != nil {
		return nil, err
	}
	items, err := t.runtime.FileSystem().ReadDir(target)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		itemPath := filepath.Join(target, item.Name())
		if matcher != nil {
			ignored, err := matcher.Match(itemPath, item.IsDir())
			if err != nil {
				return nil, err
			}
			if ignored {
				continue
			}
		}
		info, infoErr := item.Info()
		if infoErr != nil {
			continue
		}
		out = append(out, map[string]any{
			"name":     item.Name(),
			"path":     itemPath,
			"is_dir":   item.IsDir(),
			"size":     info.Size(),
			"mode":     info.Mode().String(),
			"mod_time": info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i]["name"]) < fmt.Sprint(out[j]["name"])
	})
	return map[string]any{
		"path":    target,
		"entries": out,
		"count":   len(out),
	}, nil
}
