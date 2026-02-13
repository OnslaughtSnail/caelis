package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

const (
	PatchToolName = "PATCH"
)

type PatchTool struct {
	runtime toolexec.Runtime
}

const (
	patchPreviewSideLines = 4
	patchPreviewLineWidth = 120
)

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
	return "Patch one file by exact old->new replacement."
}

func (t *PatchTool) Capability() toolcap.Capability {
	return toolcap.Capability{
		Operations: []toolcap.Operation{toolcap.OperationFileWrite},
		Risk:       toolcap.RiskMedium,
	}
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
	rawOld, exists := args["old"]
	if !exists {
		return nil, fmt.Errorf("tool: missing required arg %q", "old")
	}
	oldValue, ok := rawOld.(string)
	if !ok {
		return nil, fmt.Errorf("tool: arg %q must be string", "old")
	}
	if _, exists := args["new"]; !exists {
		return nil, fmt.Errorf("tool: missing required arg %q", "new")
	}
	rawNew := args["new"]
	newValue, ok := rawNew.(string)
	if !ok {
		return nil, fmt.Errorf("tool: arg %q must be string", "new")
	}
	replaceAll := false
	if raw, ok := args["replace_all"].(bool); ok {
		replaceAll = raw
	}

	target, err := normalizePathWithFS(t.runtime.FileSystem(), pathArg)
	if err != nil {
		return nil, err
	}
	fileInfo, statErr := t.runtime.FileSystem().Stat(target)
	fileExists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	count := 0
	next := ""
	replaced := 0
	created := false
	preview := buildPatchPreview(oldValue, newValue)
	lineStart := 0
	oldLines := 0
	newLines := 0
	if !fileExists {
		if oldValue != "" {
			return nil, fmt.Errorf("tool: PATCH target %q does not exist; set %q to empty string to create file", target, "old")
		}
		next = newValue
		count = 1
		replaced = 1
		created = true
		lineStart = 1
		oldLines = 0
		newLines = patchLineCount(newValue)
		if err := t.runtime.FileSystem().WriteFile(target, []byte(next), 0o644); err != nil {
			return nil, err
		}
		return map[string]any{
			"path":      target,
			"replaced":  replaced,
			"old_count": count,
			"created":   created,
			"metadata":  buildPatchMetadata(preview, lineStart, oldLines, newLines),
		}, nil
	}

	contentRaw, err := t.runtime.FileSystem().ReadFile(target)
	if err != nil {
		return nil, err
	}
	content := string(contentRaw)
	if oldValue == "" {
		if content != "" {
			return nil, fmt.Errorf("tool: PATCH arg %q can be empty only when target file is empty", "old")
		}
		next = newValue
		count = 1
		replaced = 1
		lineStart = 1
		oldLines = 0
		newLines = patchLineCount(newValue)
	} else {
		count = strings.Count(content, oldValue)
		if count == 0 {
			return nil, fmt.Errorf("tool: PATCH old content not found in file")
		}
		if !replaceAll && count != 1 {
			return nil, fmt.Errorf("tool: PATCH requires exact single match, found %d; set replace_all=true to replace all", count)
		}
		if replaceAll {
			next = strings.ReplaceAll(content, oldValue, newValue)
			replaced = count
		} else {
			index := strings.Index(content, oldValue)
			if index >= 0 {
				lineStart = 1 + strings.Count(content[:index], "\n")
			}
			oldLines = patchLineCount(oldValue)
			newLines = patchLineCount(newValue)
			next = strings.Replace(content, oldValue, newValue, 1)
			replaced = 1
		}
	}
	if err := t.runtime.FileSystem().WriteFile(target, []byte(next), fileInfo.Mode()); err != nil {
		return nil, err
	}
	return map[string]any{
		"path":      target,
		"replaced":  replaced,
		"old_count": count,
		"created":   created,
		"metadata":  buildPatchMetadata(preview, lineStart, oldLines, newLines),
	}, nil
}

func buildPatchMetadata(preview string, lineStart, oldLines, newLines int) map[string]any {
	patchMeta := map[string]any{}
	if strings.TrimSpace(preview) != "" {
		patchMeta["preview"] = preview
	}
	if lineStart > 0 {
		patchMeta["line_start"] = lineStart
		patchMeta["old_lines"] = oldLines
		patchMeta["new_lines"] = newLines
		patchMeta["hunk"] = fmt.Sprintf("@@ -%d,%d +%d,%d @@", lineStart, oldLines, lineStart, newLines)
	}
	return map[string]any{
		"patch": patchMeta,
	}
}

func buildPatchPreview(oldValue, newValue string) string {
	oldLines, oldTruncated := buildPatchSide(oldValue, "-")
	newLines, newTruncated := buildPatchSide(newValue, "+")
	if len(oldLines) == 0 && len(newLines) == 0 {
		return ""
	}
	lines := make([]string, 0, 2+len(oldLines)+len(newLines)+1)
	lines = append(lines, "--- old", "+++ new")
	lines = append(lines, oldLines...)
	lines = append(lines, newLines...)
	if oldTruncated || newTruncated {
		lines = append(lines, "... (preview truncated)")
	}
	return strings.Join(lines, "\n")
}

func buildPatchSide(content, prefix string) ([]string, bool) {
	if content == "" {
		return nil, false
	}
	if strings.HasSuffix(content, "\n") {
		content = strings.TrimSuffix(content, "\n")
	}
	rawLines := strings.Split(content, "\n")
	truncated := false
	if len(rawLines) > patchPreviewSideLines {
		rawLines = rawLines[:patchPreviewSideLines]
		truncated = true
	}
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		lines = append(lines, prefix+truncatePatchLine(line, patchPreviewLineWidth))
	}
	return lines, truncated
}

func truncatePatchLine(line string, width int) string {
	rs := []rune(line)
	if width <= 0 || len(rs) <= width {
		return line
	}
	if width <= 3 {
		return string(rs[:width])
	}
	return string(rs[:width-3]) + "..."
}

func patchLineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}
