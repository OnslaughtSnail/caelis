package filesystem

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

const (
	ListToolName = "LIST"
)

type ListTool struct {
	runtime toolexec.Runtime
}

func NewList() *ListTool {
	tool, _ := NewListWithRuntime(nil)
	return tool
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

func (t *ListTool) Capability() toolcap.Capability {
	return toolcap.Capability{
		Operations: []toolcap.Operation{toolcap.OperationFileRead},
		Risk:       toolcap.RiskLow,
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
	items, err := t.runtime.FileSystem().ReadDir(target)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		info, infoErr := item.Info()
		if infoErr != nil {
			continue
		}
		out = append(out, map[string]any{
			"name":     item.Name(),
			"path":     filepath.Join(target, item.Name()),
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
