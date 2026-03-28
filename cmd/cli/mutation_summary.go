package main

import (
	"fmt"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	toolfs "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

type toolCallMutationVisuals struct {
	DiffMsg      tuievents.DiffBlockMsg
	DiffShown    bool
	ChangeCounts mutationChangeCounts
	PreviewPath  string
	PreviewNew   string
}

type mutationPreviewRender struct {
	Preview toolfs.MutationPreview
	Counts  mutationChangeCounts
	DiffMsg tuievents.DiffBlockMsg

	HasChanges bool
	TooLarge   bool
}

type mutationChangeCounts struct {
	Added   int
	Removed int
}

func buildToolCallMutationVisuals(runtime toolexec.Runtime, toolName string, callArgs map[string]any) (toolCallMutationVisuals, bool) {
	render, err := buildMutationPreviewRender(runtime, toolName, callArgs)
	if err != nil {
		return toolCallMutationVisuals{}, false
	}
	visuals := toolCallMutationVisuals{
		ChangeCounts: render.Counts,
		PreviewPath:  render.Preview.Path,
		PreviewNew:   render.Preview.New,
	}
	if !render.HasChanges || render.TooLarge {
		return visuals, true
	}
	visuals.DiffMsg = render.DiffMsg
	visuals.DiffShown = true
	return visuals, true
}

func buildMutationPreviewRender(runtime toolexec.Runtime, toolName string, callArgs map[string]any) (mutationPreviewRender, error) {
	preview, err := toolfs.BuildMutationPreview(runtime, toolName, callArgs)
	if err != nil {
		return mutationPreviewRender{}, err
	}
	counts := mutationChangeCountsForTool(strings.ToUpper(strings.TrimSpace(toolName)), preview, callArgs)
	render := mutationPreviewRender{
		Preview:    preview,
		Counts:     counts,
		HasChanges: !mutationHasNoChanges(preview, counts),
	}
	if !render.HasChanges {
		return render, nil
	}
	render.TooLarge = shouldSkipRichDiff(preview, counts)
	render.DiffMsg = tuievents.DiffBlockMsg{
		Tool:      strings.ToUpper(strings.TrimSpace(preview.Tool)),
		Path:      displayFileName(preview.Path),
		Created:   preview.Created,
		Hunk:      preview.Hunk,
		Old:       preview.Old,
		New:       preview.New,
		Preview:   preview.Preview,
		Truncated: strings.Contains(preview.Preview, "... (preview truncated)"),
	}
	if render.DiffMsg.Tool == "" {
		render.DiffMsg.Tool = strings.ToUpper(strings.TrimSpace(toolName))
	}
	return render, nil
}

func mutationChangeCountsForTool(toolName string, preview toolfs.MutationPreview, _ map[string]any) mutationChangeCounts {
	switch toolName {
	case "PATCH", "WRITE":
		stats := toolfs.CountLineDiff(preview.Old, preview.New)
		return mutationChangeCounts{Added: stats.Added, Removed: stats.Removed}
	default:
		return mutationChangeCounts{}
	}
}

func mutationChangeCountsFromResult(toolName string, result map[string]any, callArgs map[string]any) mutationChangeCounts {
	if added, addedOK := asInt(result["added_lines"]); addedOK {
		if removed, removedOK := asInt(result["removed_lines"]); removedOK {
			return mutationChangeCounts{Added: max(0, added), Removed: max(0, removed)}
		}
	}
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "PATCH":
		replacements, ok := asInt(result["replaced"])
		if !ok || replacements <= 0 {
			replacements = 1
		}
		oldText := asString(callArgs["old"])
		newText := asString(callArgs["new"])
		stats := toolfs.CountLineDiff(oldText, newText)
		return mutationChangeCounts{
			Added:   stats.Added * replacements,
			Removed: stats.Removed * replacements,
		}
	case "WRITE":
		return mutationChangeCounts{}
	default:
		return mutationChangeCounts{}
	}
}

func legacyWriteMutationChangeCounts(result map[string]any, callArgs map[string]any) mutationChangeCounts {
	if _, addedOK := asInt(result["added_lines"]); addedOK {
		if _, removedOK := asInt(result["removed_lines"]); removedOK {
			return mutationChangeCounts{}
		}
	}
	added, ok := asInt(result["line_count"])
	if !ok || added < 0 {
		added = countLines(asString(callArgs["content"]))
	}
	if added < 0 {
		added = 0
	}
	return mutationChangeCounts{Added: added, Removed: 0}
}

func formatMutationChangeSummary(counts mutationChangeCounts) string {
	return fmt.Sprintf("+%d -%d", max(0, counts.Added), max(0, counts.Removed))
}

func shouldSkipRichDiff(preview toolfs.MutationPreview, counts mutationChangeCounts) bool {
	if mutationHasNoChanges(preview, counts) {
		return true
	}
	oldChanged, newChanged := mutationChangedWindowLineCounts(preview.Old, preview.New)
	windowLines := oldChanged + newChanged
	if windowLines <= 0 {
		windowLines = counts.Added + counts.Removed
	}
	return windowLines > richDiffMaxLines
}

func mutationHasNoChanges(preview toolfs.MutationPreview, counts mutationChangeCounts) bool {
	return !preview.Created && counts.Added == 0 && counts.Removed == 0 && preview.Old == preview.New
}

func mutationChangedWindowLineCounts(oldText, newText string) (int, int) {
	oldLines := mutationDiffLines(oldText)
	newLines := mutationDiffLines(newText)
	prefix, suffix := mutationCommonAffixLineCounts(oldLines, newLines)
	oldChanged := len(oldLines) - prefix - suffix
	newChanged := len(newLines) - prefix - suffix
	if oldChanged < 0 {
		oldChanged = 0
	}
	if newChanged < 0 {
		newChanged = 0
	}
	return oldChanged, newChanged
}

func mutationCommonAffixLineCounts(oldLines, newLines []string) (int, int) {
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for prefix+suffix < len(oldLines) &&
		prefix+suffix < len(newLines) &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	return prefix, suffix
}

func mutationDiffLines(text string) []string {
	if text == "" {
		return nil
	}
	normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")
	return strings.Split(normalized, "\n")
}
