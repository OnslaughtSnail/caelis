package main

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

const richDiffMaxLines = 800

func buildPatchDiffBlockMsg(toolName string, result map[string]any, callArgs map[string]any) (tuievents.DiffBlockMsg, bool, bool) {
	if !strings.EqualFold(strings.TrimSpace(toolName), "PATCH") {
		return tuievents.DiffBlockMsg{}, false, false
	}
	oldValue, _ := callArgs["old"].(string)
	newValue, _ := callArgs["new"].(string)

	preview := patchPreviewFromMetadata(result)
	truncated := strings.Contains(preview, "... (preview truncated)")
	if strings.TrimSpace(oldValue) == "" && strings.TrimSpace(newValue) == "" {
		parsedOld, parsedNew, parsedTruncated, ok := parsePatchPreview(preview)
		if !ok {
			return tuievents.DiffBlockMsg{}, false, false
		}
		oldValue = parsedOld
		newValue = parsedNew
		truncated = truncated || parsedTruncated
	}

	if countLines(oldValue)+countLines(newValue) > richDiffMaxLines {
		return tuievents.DiffBlockMsg{}, true, true
	}

	path := strings.TrimSpace(asString(result["path"]))
	if path == "" {
		path = strings.TrimSpace(asString(callArgs["path"]))
	}
	path = displayFileName(path)
	msg := tuievents.DiffBlockMsg{
		Tool:      strings.ToUpper(strings.TrimSpace(toolName)),
		Path:      path,
		Created:   fmtSprint(result["created"]) == "true",
		Hunk:      patchHunkFromResult(result),
		Old:       oldValue,
		New:       newValue,
		Preview:   preview,
		Truncated: truncated,
	}
	if msg.Tool == "" {
		msg.Tool = "PATCH"
	}
	return msg, false, true
}

func parsePatchPreview(preview string) (oldValue, newValue string, truncated bool, ok bool) {
	if strings.TrimSpace(preview) == "" {
		return "", "", false, false
	}
	normalized := strings.ReplaceAll(strings.ReplaceAll(preview, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(normalized, "\n")
	oldLines := make([]string, 0, len(lines))
	newLines := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if trimmed == "... (preview truncated)" {
			truncated = true
			continue
		}
		switch {
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "@@ "):
			continue
		case strings.HasPrefix(line, "-"):
			oldLines = append(oldLines, strings.TrimPrefix(line, "-"))
		case strings.HasPrefix(line, "+"):
			newLines = append(newLines, strings.TrimPrefix(line, "+"))
		default:
			// Ignore non +/- context lines from fallback previews.
		}
	}
	if len(oldLines) == 0 && len(newLines) == 0 {
		return "", "", truncated, false
	}
	return strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"), truncated, true
}

func fmtSprint(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(asString(v)))
}
