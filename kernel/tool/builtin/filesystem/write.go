package filesystem

import (
	"context"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

const (
	WriteToolName = "WRITE"
)

type WriteTool struct {
	runtime toolexec.Runtime
}

func NewWriteWithRuntime(runtime toolexec.Runtime) (*WriteTool, error) {
	resolvedRuntime, err := runtimeOrDefault(runtime)
	if err != nil {
		return nil, err
	}
	return &WriteTool{runtime: resolvedRuntime}, nil
}

func (t *WriteTool) Name() string {
	return WriteToolName
}

func (t *WriteTool) Description() string {
	return "Write full file content by path."
}

func (t *WriteTool) Capability() toolcap.Capability {
	return toolcap.Capability{
		Operations: []toolcap.Operation{toolcap.OperationFileWrite},
		Risk:       toolcap.RiskMedium,
	}
}

func (t *WriteTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "target file path"},
				"content": map[string]any{"type": "string", "description": "full file content to write"},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (t *WriteTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	plan, err := planWriteMutation(t.runtime.FileSystem(), args)
	if err != nil {
		return nil, err
	}
	if err := t.runtime.FileSystem().WriteFile(plan.path, []byte(plan.after), plan.mode); err != nil {
		return nil, err
	}

	return map[string]any{
		"path":          plan.path,
		"created":       plan.created,
		"bytes_written": len([]byte(plan.after)),
		"line_count":    lineCount(plan.after),
	}, nil
}

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}
