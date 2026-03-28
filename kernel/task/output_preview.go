package task

import "strings"

const (
	previewMaxLines       = 4
	previewLineHeadRunes  = 72
	previewLineTailRunes  = 72
	previewLineMaxRunes   = previewLineHeadRunes + previewLineTailRunes + 3
	previewMiddleEllipsis = "..."
)

func FormatLatestOutput(text string) string {
	text = normalizePreviewInput(text)
	if text == "" {
		return ""
	}
	lines := make([]string, 0, previewMaxLines)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, truncatePreviewLine(line))
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > previewMaxLines {
		lines = lines[len(lines)-previewMaxLines:]
	}
	return strings.Join(lines, "\n")
}

func MergeLatestOutput(existing string, incoming string) string {
	existing = normalizePreviewInput(existing)
	incoming = normalizePreviewInput(incoming)
	switch {
	case existing == "":
		return FormatLatestOutput(incoming)
	case incoming == "":
		return FormatLatestOutput(existing)
	default:
		return FormatLatestOutput(existing + "\n" + incoming)
	}
}

func truncatePreviewLine(line string) string {
	runes := []rune(line)
	if len(runes) <= previewLineMaxRunes {
		return line
	}
	return string(runes[:previewLineHeadRunes]) + previewMiddleEllipsis + string(runes[len(runes)-previewLineTailRunes:])
}

func normalizePreviewInput(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "<nil>" {
		return ""
	}
	return text
}
