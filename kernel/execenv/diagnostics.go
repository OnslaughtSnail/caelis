package execenv

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	diagMaxLines = 4
	diagMaxRunes = 360
)

func commandOutputSummary(result CommandResult) string {
	stderr := streamSnippet(result.Stderr)
	stdout := streamSnippet(result.Stdout)
	switch {
	case stderr != "<empty>" && stdout != "<empty>":
		return fmt.Sprintf("stderr=%s; stdout=%s", stderr, stdout)
	case stderr != "<empty>":
		return fmt.Sprintf("stderr=%s; stdout=<empty>", stderr)
	case stdout != "<empty>":
		return fmt.Sprintf("stderr=<empty>; stdout=%s", stdout)
	default:
		return "stderr=<empty>; stdout=<empty> (no output captured)"
	}
}

func streamSnippet(raw string) string {
	text := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if text == "" {
		return "<empty>"
	}
	lines := strings.Split(text, "\n")
	if len(lines) > diagMaxLines {
		lines = append([]string{"..."}, lines[len(lines)-(diagMaxLines-1):]...)
	}
	snippet := strings.Join(lines, " \\n ")
	if utf8.RuneCountInString(snippet) > diagMaxRunes {
		runes := []rune(snippet)
		snippet = "..." + string(runes[len(runes)-(diagMaxRunes-3):])
	}
	return snippet
}
