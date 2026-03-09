package sessionmode

import (
	"path/filepath"
	"strings"
)

const (
	DefaultMode = "default"
	PlanMode    = "plan"
	FullMode    = "full_access"

	snapshotKey = "session_mode"
	openTag     = "<caelis-session-mode"
	closeTag    = "</caelis-session-mode>"
)

func Normalize(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case PlanMode:
		return PlanMode
	case FullMode:
		return FullMode
	default:
		return DefaultMode
	}
}

func Next(mode string) string {
	switch Normalize(mode) {
	case DefaultMode:
		return PlanMode
	case PlanMode:
		return FullMode
	default:
		return DefaultMode
	}
}

func DisplayLabel(mode string) string {
	switch Normalize(mode) {
	case PlanMode:
		return "plan"
	case FullMode:
		return "full_access"
	default:
		return ""
	}
}

func IsFullAccess(mode string) bool {
	return Normalize(mode) == FullMode
}

func IsDangerousCommand(command string) bool {
	return isDangerousCommand(command, 0)
}

func isDangerousCommand(command string, depth int) bool {
	if depth > 4 {
		return false
	}
	for _, segment := range shellCommandSegments(command) {
		if isDangerousSegment(shellSegmentTokens(segment), depth) {
			return true
		}
	}
	return false
}

func isDangerousSegment(tokens []string, depth int) bool {
	if len(tokens) == 0 {
		return false
	}
	start := 0
	for start < len(tokens) {
		token := strings.ToLower(strings.TrimSpace(tokens[start]))
		if token == "" {
			start++
			continue
		}
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "=") {
			start++
			continue
		}
		break
	}
	if start >= len(tokens) {
		return false
	}
	base := filepath.Base(strings.ToLower(tokens[start]))
	switch base {
	case "env", "sudo", "command", "builtin", "nohup", "time":
		return isDangerousSegment(tokens[start+1:], depth+1)
	case "sh", "bash", "zsh", "dash", "ksh", "ash", "fish":
		if command, ok := wrappedShellCommand(tokens[start+1:]); ok {
			return isDangerousCommand(command, depth+1)
		}
	}
	hasDD := false
	for _, token := range tokens[start:] {
		lower := strings.ToLower(strings.TrimSpace(token))
		if lower == "" {
			continue
		}
		base := filepath.Base(lower)
		switch base {
		case "rm", "rmdir", "shred", "mkfs", "mkfs.ext4", "mkfs.xfs", "mkfs.fat", "mke2fs":
			return true
		case "dd":
			hasDD = true
		}
		if hasDD && strings.HasPrefix(lower, "of=") {
			return true
		}
	}
	return false
}

func wrappedShellCommand(tokens []string) (string, bool) {
	for i := 0; i < len(tokens); i++ {
		token := strings.TrimSpace(tokens[i])
		if token == "" {
			continue
		}
		if token == "--" {
			return "", false
		}
		if token == "-c" {
			if i+1 >= len(tokens) {
				return "", false
			}
			return tokens[i+1], true
		}
		if strings.HasPrefix(token, "-") {
			flags := strings.TrimLeft(token, "-")
			if strings.Contains(flags, "c") {
				if i+1 >= len(tokens) {
					return "", false
				}
				return tokens[i+1], true
			}
			continue
		}
		return "", false
	}
	return "", false
}

func shellCommandSegments(command string) []string {
	var (
		segments []string
		buf      strings.Builder
		squote   bool
		dquote   bool
		escape   bool
	)
	flush := func() {
		part := strings.TrimSpace(buf.String())
		if part != "" {
			segments = append(segments, part)
		}
		buf.Reset()
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escape {
			buf.WriteRune(r)
			escape = false
			continue
		}
		switch r {
		case '\\':
			escape = true
			buf.WriteRune(r)
		case '\'':
			if !dquote {
				squote = !squote
			}
			buf.WriteRune(r)
		case '"':
			if !squote {
				dquote = !dquote
			}
			buf.WriteRune(r)
		case ';':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			flush()
		case '&':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			if i+1 < len(runes) && runes[i+1] == '&' {
				flush()
				i++
				continue
			}
			buf.WriteRune(r)
		case '|':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			flush()
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
		default:
			buf.WriteRune(r)
		}
	}
	flush()
	return segments
}

func shellSegmentTokens(segment string) []string {
	var (
		tokens []string
		buf    strings.Builder
		squote bool
		dquote bool
		escape bool
	)
	flush := func() {
		token := strings.TrimSpace(buf.String())
		if token == "" {
			buf.Reset()
			return
		}
		tokens = append(tokens, token)
		buf.Reset()
	}
	for _, r := range segment {
		if escape {
			buf.WriteRune(r)
			escape = false
			continue
		}
		switch r {
		case '\\':
			escape = true
		case '\'':
			if !dquote {
				squote = !squote
				continue
			}
			buf.WriteRune(r)
		case '"':
			if !squote {
				dquote = !dquote
				continue
			}
			buf.WriteRune(r)
		case ' ', '\t', '\n':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			flush()
		default:
			buf.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func Inject(input string, mode string) string {
	visible := Strip(input)
	control := controlBlock(Normalize(mode))
	visible = strings.TrimSpace(visible)
	if visible == "" {
		return control
	}
	return control + "\n\n" + visible
}

func Strip(input string) string {
	trimmed := strings.TrimLeft(input, " \t\r\n")
	if !strings.HasPrefix(trimmed, openTag) {
		return input
	}
	end := strings.Index(trimmed, closeTag)
	if end < 0 {
		return input
	}
	rest := trimmed[end+len(closeTag):]
	return strings.TrimLeft(rest, "\r\n")
}

func VisibleText(input string) string {
	return strings.TrimSpace(Strip(input))
}

func LoadSnapshot(values map[string]any) string {
	if values == nil {
		return DefaultMode
	}
	mode, _ := values[snapshotKey].(string)
	return Normalize(mode)
}

func StoreSnapshot(values map[string]any, mode string) map[string]any {
	if values == nil {
		values = map[string]any{}
	}
	values[snapshotKey] = Normalize(mode)
	return values
}

func controlBlock(mode string) string {
	mode = Normalize(mode)
	switch mode {
	case PlanMode:
		return `<caelis-session-mode mode="plan" hidden="true">
This turn is running in PLAN mode. Focus on analysis, planning, tradeoffs, and implementation strategy. Do not make changes unless the user explicitly asks you to execute them.
</caelis-session-mode>`
	case FullMode:
		return `<caelis-session-mode mode="full_access" hidden="true">
This turn is running in FULL_ACCESS mode. You may execute changes directly without waiting for approval, but avoid dangerous destructive commands that could damage the host or delete data.
</caelis-session-mode>`
	default:
		return `<caelis-session-mode mode="default" hidden="true">
This turn is running in DEFAULT mode. Follow the user's request normally.
</caelis-session-mode>`
	}
}
