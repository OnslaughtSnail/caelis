package filesystem

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
)

const (
	PatchToolName = "PATCH"
)

type PatchTool struct {
	runtime toolexec.Runtime
}

func NewPatch() *PatchTool {
	tool, _ := NewPatchWithRuntime(nil)
	return tool
}

func NewPatchWithRuntime(runtime toolexec.Runtime) (*PatchTool, error) {
	resolvedRuntime, err := runtimeOrDefault(runtime)
	if err != nil {
		return nil, err
	}
	return &PatchTool{runtime: resolvedRuntime}, nil
}

func (t *PatchTool) Name() string {
	return PatchToolName
}

func (t *PatchTool) Description() string {
	return "Patch one file by exact old->new replacement. File must be read by READ before patch."
}

func (t *PatchTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "target file path"},
				"old":         map[string]any{"type": "string", "description": "exact original content to replace"},
				"new":         map[string]any{"type": "string", "description": "replacement content"},
				"replace_all": map[string]any{"type": "boolean", "description": "replace all occurrences"},
			},
			"required": []string{"path", "old", "new"},
		},
	}
}

func (t *PatchTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	pathArg, err := argparse.String(args, "path", true)
	if err != nil {
		return nil, err
	}
	oldValue, err := argparse.String(args, "old", true)
	if err != nil {
		return nil, err
	}
	if _, exists := args["new"]; !exists {
		return nil, fmt.Errorf("tool: missing required arg %q", "new")
	}
	newValue, err := argparse.String(args, "new", false)
	if err != nil {
		return nil, err
	}
	replaceAll := false
	if raw, ok := args["replace_all"].(bool); ok {
		replaceAll = raw
	}

	target, err := normalizePathWithFS(t.runtime.FileSystem(), pathArg)
	if err != nil {
		return nil, err
	}
	if !hasReadEvidence(ctx, target) {
		return nil, fmt.Errorf("tool: permission denied: PATCH requires prior READ of %q", target)
	}

	contentRaw, err := t.runtime.FileSystem().ReadFile(target)
	if err != nil {
		return nil, err
	}
	content := string(contentRaw)
	count := strings.Count(content, oldValue)
	if count == 0 {
		return nil, fmt.Errorf("tool: PATCH old content not found in file")
	}
	if !replaceAll && count != 1 {
		return nil, fmt.Errorf("tool: PATCH requires exact single match, found %d; set replace_all=true to replace all", count)
	}

	next := ""
	replaced := 0
	if replaceAll {
		next = strings.ReplaceAll(content, oldValue, newValue)
		replaced = count
	} else {
		next = strings.Replace(content, oldValue, newValue, 1)
		replaced = 1
	}
	info, err := t.runtime.FileSystem().Stat(target)
	if err != nil {
		return nil, err
	}
	if err := t.runtime.FileSystem().WriteFile(target, []byte(next), info.Mode()); err != nil {
		return nil, err
	}
	return map[string]any{
		"path":      target,
		"replaced":  replaced,
		"old_count": count,
	}, nil
}

func hasReadEvidence(ctx context.Context, normalizedPath string) bool {
	type historyReader interface {
		History() []*session.Event
	}
	h, ok := ctx.(historyReader)
	if !ok {
		return false
	}
	for _, ev := range h.History() {
		if ev == nil || ev.Message.ToolResponse == nil {
			continue
		}
		resp := ev.Message.ToolResponse
		if resp.Name != ReadToolName {
			continue
		}
		path, ok := readPathFromResult(resp.Result)
		if !ok {
			continue
		}
		if path == normalizedPath {
			return true
		}
	}
	return false
}

func readPathFromResult(result map[string]any) (string, bool) {
	if result == nil {
		return "", false
	}
	if value, ok := result["path"].(string); ok && strings.TrimSpace(value) != "" {
		return value, true
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return "", false
	}
	var decoded struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", false
	}
	if strings.TrimSpace(decoded.Path) == "" {
		return "", false
	}
	return decoded.Path, true
}
