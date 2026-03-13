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
}

type mutationChangeCounts struct {
	Added   int
	Removed int
}

func buildToolCallMutationVisuals(runtime toolexec.Runtime, toolName string, callArgs map[string]any) (toolCallMutationVisuals, bool) {
	preview, err := toolfs.BuildMutationPreview(runtime, toolName, callArgs)
	if err != nil {
		return toolCallMutationVisuals{}, false
	}
	visuals := toolCallMutationVisuals{
		ChangeCounts: mutationChangeCountsForTool(strings.ToUpper(strings.TrimSpace(toolName)), preview, callArgs),
	}
	if countLines(preview.Old)+countLines(preview.New) > richDiffMaxLines {
		return visuals, true
	}
	visuals.DiffMsg = tuievents.DiffBlockMsg{
		Tool:      strings.ToUpper(strings.TrimSpace(preview.Tool)),
		Path:      displayFileName(preview.Path),
		Created:   preview.Created,
		Hunk:      preview.Hunk,
		Old:       preview.Old,
		New:       preview.New,
		Preview:   preview.Preview,
		Truncated: strings.Contains(preview.Preview, "... (preview truncated)"),
	}
	if visuals.DiffMsg.Tool == "" {
		visuals.DiffMsg.Tool = strings.ToUpper(strings.TrimSpace(toolName))
	}
	visuals.DiffShown = true
	return visuals, true
}

func mutationChangeCountsForTool(toolName string, preview toolfs.MutationPreview, callArgs map[string]any) mutationChangeCounts {
	switch toolName {
	case "PATCH":
		oldText := asString(callArgs["old"])
		newText := asString(callArgs["new"])
		replaceAll, _ := callArgs["replace_all"].(bool)
		replacements := 1
		if replaceAll && oldText != "" {
			replacements = strings.Count(preview.Old, oldText)
			if replacements <= 0 {
				replacements = 1
			}
		}
		removed := 0
		if oldText != "" {
			removed = countLines(oldText) * replacements
		}
		added := 0
		if newText != "" {
			added = countLines(newText) * replacements
		}
		return mutationChangeCounts{Added: added, Removed: removed}
	case "WRITE":
		return mutationChangeCounts{
			Added:   countLines(preview.New),
			Removed: countLines(preview.Old),
		}
	default:
		return mutationChangeCounts{}
	}
}

func mutationChangeCountsFromResult(toolName string, result map[string]any, callArgs map[string]any) mutationChangeCounts {
	switch strings.ToUpper(strings.TrimSpace(toolName)) {
	case "PATCH":
		replacements, ok := asInt(result["replaced"])
		if !ok || replacements <= 0 {
			replacements = 1
		}
		oldText := asString(callArgs["old"])
		newText := asString(callArgs["new"])
		removed := 0
		if oldText != "" {
			removed = countLines(oldText) * replacements
		}
		added := 0
		if newText != "" {
			added = countLines(newText) * replacements
		}
		return mutationChangeCounts{Added: added, Removed: removed}
	case "WRITE":
		added, ok := asInt(result["line_count"])
		if !ok || added < 0 {
			added = countLines(asString(callArgs["content"]))
		}
		if added < 0 {
			added = 0
		}
		return mutationChangeCounts{Added: added, Removed: 0}
	default:
		return mutationChangeCounts{}
	}
}

func formatMutationChangeSummary(counts mutationChangeCounts) string {
	return fmt.Sprintf("+%d -%d", max(0, counts.Added), max(0, counts.Removed))
}
