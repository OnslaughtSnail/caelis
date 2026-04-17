package filesystem

import (
	"context"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool/capability"
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
	return "Patch one file by exact old-to-new replacement."
}

func (t *PatchTool) Capability() capability.Capability {
	return capability.Capability{
		Operations: []capability.Operation{capability.OperationFileWrite},
		Risk:       capability.RiskMedium,
	}
}

func (t *PatchTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "Target file path."},
				"old":         map[string]any{"type": "string", "description": "Exact original text to replace."},
				"new":         map[string]any{"type": "string", "description": "Replacement text."},
				"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences instead of one."},
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
	plan, err := planPatchMutation(t.runtime.FileSystem(), args)
	if err != nil {
		return nil, err
	}
	if err := t.runtime.FileSystem().WriteFile(plan.path, []byte(plan.after), plan.mode); err != nil {
		return nil, err
	}
	diffStats := CountLineDiff(plan.before, plan.after)
	return map[string]any{
		"path":           plan.path,
		"replaced":       plan.replaced,
		"created":        plan.created,
		"previous_empty": plan.before == "",
		"added_lines":    diffStats.Added,
		"removed_lines":  diffStats.Removed,
	}, nil
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
	content = strings.TrimSuffix(content, "\n")
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

func (t *PatchTool) WithRuntime(runtime toolexec.Runtime) (*PatchTool, error) {
	return NewPatchWithRuntime(runtime)
}
