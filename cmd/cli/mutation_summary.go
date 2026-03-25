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
		PreviewPath:  preview.Path,
		PreviewNew:   preview.New,
	}
	if shouldSkipRichDiff(preview, visuals.ChangeCounts) {
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
	const richDiffMaxTotalLines = 5000
	if mutationHasNoChanges(preview, counts) {
		return true
	}

	totalLines := countLines(preview.Old) + countLines(preview.New)
	if totalLines > richDiffMaxTotalLines {
		return true
	}
	diffLines := counts.Added + counts.Removed
	if diffLines <= 0 {
		diffLines = totalLines
	}
	return diffLines > richDiffMaxLines
}

func mutationHasNoChanges(preview toolfs.MutationPreview, counts mutationChangeCounts) bool {
	return !preview.Created && counts.Added == 0 && counts.Removed == 0 && preview.Old == preview.New
}
