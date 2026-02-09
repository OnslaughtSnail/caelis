package filesystem

import (
	"context"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
)

const (
	StatToolName = "STAT"
)

type StatTool struct {
	runtime toolexec.Runtime
}

func NewStat() *StatTool {
	tool, _ := NewStatWithRuntime(nil)
	return tool
}

func NewStatWithRuntime(runtime toolexec.Runtime) (*StatTool, error) {
	resolvedRuntime, err := runtimeOrDefault(runtime)
	if err != nil {
		return nil, err
	}
	return &StatTool{runtime: resolvedRuntime}, nil
}

func (t *StatTool) Name() string {
	return StatToolName
}

func (t *StatTool) Description() string {
	return "Get file or directory metadata."
}

func (t *StatTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "target file path"},
			},
			"required": []string{"path"},
		},
	}
}

func (t *StatTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	pathArg, err := argparse.String(args, "path", true)
	if err != nil {
		return nil, err
	}
	target, err := normalizePathWithFS(t.runtime.FileSystem(), pathArg)
	if err != nil {
		return nil, err
	}
	info, err := t.runtime.FileSystem().Stat(target)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"path":     target,
		"is_dir":   info.IsDir(),
		"size":     info.Size(),
		"mode":     info.Mode().String(),
		"mod_time": info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
	}, nil
}
