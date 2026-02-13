package filesystem

import (
	"context"
	"fmt"
	"os"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

const (
	WriteToolName = "WRITE"
)

type WriteTool struct {
	runtime toolexec.Runtime
}

func NewWrite() *WriteTool {
	tool, _ := NewWriteWithRuntime(nil)
	return tool
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

	pathArg, err := argparse.String(args, "path", true)
	if err != nil {
		return nil, err
	}
	rawContent, exists := args["content"]
	if !exists {
		return nil, fmt.Errorf("tool: missing required arg %q", "content")
	}
	content, ok := rawContent.(string)
	if !ok {
		return nil, fmt.Errorf("tool: arg %q must be string", "content")
	}

	target, err := normalizePathWithFS(t.runtime.FileSystem(), pathArg)
	if err != nil {
		return nil, err
	}

	info, statErr := t.runtime.FileSystem().Stat(target)
	created := false
	mode := os.FileMode(0o644)
	if statErr == nil {
		if info.IsDir() {
			return nil, fmt.Errorf("tool: target %q is directory", target)
		}
		mode = info.Mode()
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	} else {
		created = true
	}

	if err := t.runtime.FileSystem().WriteFile(target, []byte(content), mode); err != nil {
		return nil, err
	}

	return map[string]any{
		"path":          target,
		"created":       created,
		"bytes_written": len([]byte(content)),
		"line_count":    lineCount(content),
	}, nil
}

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}
