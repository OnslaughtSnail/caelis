package tuikit

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

// LineStyle identifies the semantic role of a log line for coloring.
type LineStyle int

const (
	LineStyleDefault    LineStyle = iota
	LineStyleAssistant            // "* " prefix
	LineStyleReasoning            // "│ " prefix
	LineStyleUser                 // "> " prefix
	LineStyleTool                 // "▸" / "✓" / "? " prefix
	LineStyleWarn                 // "warn:" prefix
	LineStyleError                // "error:" prefix
	LineStyleNote                 // "note:" prefix
	LineStyleKeyValue             // indented key-value pairs
	LineStyleSection              // top-level header-like text
	LineStyleDiffAdd              // "  +line" (unified diff add)
	LineStyleDiffRemove           // "  -line" (unified diff remove)
	LineStyleDiffHeader           // "  --- old" / "  +++ new"
	LineStyleDiffHunk             // "  @@ -n,m +n,m @@" (hunk header)
)

// DetectLineStyle determines the semantic style of a log line in isolation.
func DetectLineStyle(line string) LineStyle {
	return DetectLineStyleWithContext(line, LineStyleDefault)
}

// DetectLineStyleWithContext determines semantic style considering the
// previous line's style for block continuation. When a line does not have
// its own explicit prefix, it inherits the style of the previous line if
// the previous line was a block-style (assistant, reasoning, tool).
func DetectLineStyleWithContext(line string, prevStyle LineStyle) LineStyle {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return LineStyleDefault
	}

	// Explicit prefix detection first.
	switch {
	case strings.HasPrefix(trimmed, "error:"):
		return LineStyleError
	case strings.HasPrefix(trimmed, "warn:"):
		return LineStyleWarn
	case strings.HasPrefix(trimmed, "note:"):
		return LineStyleNote
	case strings.HasPrefix(trimmed, "* "):
		return LineStyleAssistant
	case strings.HasPrefix(trimmed, "│ ") || strings.HasPrefix(trimmed, "│"):
		return LineStyleReasoning
	case strings.HasPrefix(trimmed, "> "):
		return LineStyleUser
	case strings.HasPrefix(trimmed, "▸"):
		return LineStyleTool
	case strings.HasPrefix(trimmed, "✓"):
		return LineStyleTool
	case strings.HasPrefix(trimmed, "? "):
		return LineStyleTool
	}

	// Check for diff patterns based on indentation.
	leading := countLeadingSpaces(line)
	if leading >= 2 {
		rest := strings.TrimLeft(line, " \t")
		switch {
		case strings.HasPrefix(rest, "+++ ") || strings.HasPrefix(rest, "--- "):
			return LineStyleDiffHeader
		case strings.HasPrefix(rest, "@@ "):
			return LineStyleDiffHunk
		case len(rest) > 0 && rest[0] == '+':
			return LineStyleDiffAdd
		case len(rest) > 0 && rest[0] == '-':
			return LineStyleDiffRemove
		}
	}

	// Block continuation: if the previous line was a block-style content
	// (assistant, reasoning) and this line has no explicit prefix, treat it
	// as a continuation of that block.
	if isBlockContinuable(prevStyle) {
		return prevStyle
	}

	// Indented key-value
	if leading >= 2 {
		rest := line[leading:]
		keyEnd := strings.IndexAny(rest, " \t")
		if keyEnd > 0 {
			return LineStyleKeyValue
		}
	}

	// Section header: no indentation and no structural characters.
	if leading == 0 && !strings.ContainsAny(trimmed, ":{}[]") {
		return LineStyleSection
	}

	return LineStyleDefault
}

// isBlockContinuable returns true for styles whose content can span
// multiple lines without repeating the prefix.
func isBlockContinuable(s LineStyle) bool {
	switch s {
	case LineStyleAssistant, LineStyleReasoning, LineStyleTool:
		return true
	default:
		return false
	}
}

// IsConversationStyle returns true for styles that represent chat turns.
func IsConversationStyle(s LineStyle) bool {
	switch s {
	case LineStyleUser, LineStyleAssistant, LineStyleReasoning:
		return true
	default:
		return false
	}
}

// ShouldInsertGap returns true when a visual gap should be inserted before
// the current line (between conversation turns / blocks).
func ShouldInsertGap(hasLast bool, lastStyle LineStyle, currentStyle LineStyle) bool {
	if !hasLast {
		return false
	}
	return IsConversationStyle(currentStyle)
}

// ColorizeLogLine applies lipgloss coloring to a log line based on its style.
func ColorizeLogLine(line string, style LineStyle, theme Theme) string {
	switch style {
	case LineStyleAssistant:
		return colorizeAssistantLine(line, theme)
	case LineStyleReasoning:
		return theme.ReasoningStyle().Render(line)
	case LineStyleUser:
		return colorizeUserLine(line, theme)
	case LineStyleTool:
		return colorizeToolLine(line, theme)
	case LineStyleWarn:
		return theme.WarnStyle().Render(line)
	case LineStyleError:
		return theme.ErrorStyle().Render(line)
	case LineStyleNote:
		return theme.NoteStyle().Render(line)
	case LineStyleKeyValue:
		return colorizeKeyValueLine(line, theme)
	case LineStyleSection:
		return theme.SectionStyle().Render(line)
	case LineStyleDiffAdd:
		return theme.DiffAddStyle().Render(line)
	case LineStyleDiffRemove:
		return theme.DiffRemoveStyle().Render(line)
	case LineStyleDiffHeader:
		return theme.DiffHeaderStyle().Render(line)
	case LineStyleDiffHunk:
		return theme.DiffHunkStyle().Render(line)
	default:
		return line
	}
}

func colorizeAssistantLine(line string, theme Theme) string {
	if strings.HasPrefix(line, "* ") {
		prefix := theme.AssistantStyle().Render("* ")
		return prefix + line[len("* "):]
	}
	return theme.AssistantStyle().Render(line)
}

func colorizeUserLine(line string, theme Theme) string {
	content := line
	if strings.HasPrefix(content, "> ") {
		content = content[len("> "):]
	}
	if content == "" {
		return theme.UserPrefixStyle().Render("> ")
	}
	styledBody := styleUserMentions(content, theme)
	return theme.UserPrefixStyle().Render("> ") + styledBody
}

func styleUserMentions(text string, theme Theme) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	var out strings.Builder
	for i := 0; i < len(runes); {
		if runes[i] == '@' {
			j := i + 1
			for j < len(runes) && isUserMentionRune(runes[j]) {
				j++
			}
			if j > i+1 {
				out.WriteString(theme.UserMentionStyle().Render(string(runes[i:j])))
				i = j
				continue
			}
		}
		start := i
		for i < len(runes) && runes[i] != '@' {
			i++
		}
		out.WriteString(theme.UserStyle().Render(string(runes[start:i])))
	}
	return out.String()
}

func isUserMentionRune(r rune) bool {
	if unicode.IsSpace(r) {
		return false
	}
	switch r {
	case ',', '，', '。', ':', '：', ';', '；', '!', '?', '！', '？', '"', '\'', '(', ')', '[', ']', '{', '}', '<', '>', '|':
		return false
	default:
		return true
	}
}

func colorizeToolLine(line string, theme Theme) string {
	trimmed := strings.TrimSpace(line)

	// Tool call: "▸ TOOLNAME {args...}"
	if strings.HasPrefix(trimmed, "▸ ") {
		rest := trimmed[len("▸ "):]
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) >= 1 {
			toolName := parts[0]
			suffix := ""
			if len(parts) == 2 {
				suffix = " " + lipgloss.NewStyle().Foreground(theme.ReasoningFg).Render(parts[1])
			}
			return theme.ToolStyle().Render("▸ ") +
				theme.ToolNameStyle().Render(toolName) +
				suffix
		}
		return theme.ToolStyle().Render(line)
	}

	// Tool result: "✓ TOOLNAME summary"
	if strings.HasPrefix(trimmed, "✓ ") {
		rest := trimmed[len("✓ "):]
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) >= 1 {
			toolName := parts[0]
			suffix := ""
			if len(parts) == 2 {
				suffix = " " + parts[1]
			}
			return theme.ToolStyle().Render("✓ ") +
				theme.AssistantStyle().Render(toolName) +
				suffix
		}
		return theme.ToolStyle().Render(line)
	}

	// Approval prompt: "? ..."
	if strings.HasPrefix(trimmed, "? ") {
		return lipgloss.NewStyle().Foreground(theme.Warning).Bold(true).Render(line)
	}

	return theme.ReasoningStyle().Render(line)
}

func colorizeKeyValueLine(line string, theme Theme) string {
	rest := strings.TrimLeft(line, " \t")
	leading := len(line) - len(rest)
	keyEnd := strings.IndexAny(rest, " \t")
	if keyEnd <= 0 {
		return line
	}
	key := rest[:keyEnd]
	val := rest[keyEnd:]
	return strings.Repeat(" ", leading) +
		theme.KeyLabelStyle().Render(key) +
		val
}

func countLeadingSpaces(s string) int {
	n := 0
	for _, ch := range s {
		if ch == ' ' {
			n++
		} else if ch == '\t' {
			n += 4
		} else {
			break
		}
	}
	return n
}
