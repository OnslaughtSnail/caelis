package shell

import (
	"path/filepath"
	"strings"
)

func isACPXCommand(command string) bool {
	segments := bashCommandSegments(command)
	for _, segment := range segments {
		if isACPXSegment(bashSegmentTokens(segment)) {
			return true
		}
	}
	return false
}

func isACPXSegment(tokens []string) bool {
	tokens = trimLeadingAssignments(tokens)
	for len(tokens) > 0 {
		base := strings.ToLower(filepath.Base(tokens[0]))
		switch base {
		case "acpx":
			return true
		case "npx":
			return npxInvokesACPX(tokens[1:])
		case "which", "whereis", "type":
			return false
		case "command":
			next, lookup := unwrapCommandBuiltin(tokens[1:])
			if lookup {
				return false
			}
			tokens = next
			continue
		case "env":
			tokens = unwrapEnv(tokens[1:])
			continue
		case "builtin", "nohup", "time":
			tokens = tokens[1:]
			continue
		default:
			return false
		}
	}
	return false
}

func npxOptionNeedsValue(token string) bool {
	switch token {
	case "-c", "--call", "-p", "--package", "--cache", "--userconfig", "--registry", "--prefix":
		return true
	default:
		return false
	}
}

func npxInvokesACPX(tokens []string) bool {
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == "--" {
			if i+1 >= len(tokens) {
				return false
			}
			return strings.EqualFold(filepath.Base(tokens[i+1]), "acpx")
		}
		if strings.HasPrefix(token, "-") {
			if npxOptionNeedsValue(token) && i+1 < len(tokens) {
				i++
			}
			continue
		}
		return strings.EqualFold(filepath.Base(token), "acpx")
	}
	return false
}

func trimLeadingAssignments(tokens []string) []string {
	for len(tokens) > 0 {
		token := tokens[0]
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "=") {
			tokens = tokens[1:]
			continue
		}
		break
	}
	return tokens
}

func unwrapCommandBuiltin(tokens []string) ([]string, bool) {
	for len(tokens) > 0 {
		token := tokens[0]
		if token == "--" {
			return trimLeadingAssignments(tokens[1:]), false
		}
		if !strings.HasPrefix(token, "-") {
			return trimLeadingAssignments(tokens), false
		}
		if token == "-v" || token == "-V" {
			return nil, true
		}
		tokens = tokens[1:]
	}
	return nil, false
}

func unwrapEnv(tokens []string) []string {
	for len(tokens) > 0 {
		token := tokens[0]
		if token == "--" {
			return trimLeadingAssignments(tokens[1:])
		}
		if strings.HasPrefix(token, "-") {
			tokens = tokens[1:]
			continue
		}
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "=") {
			tokens = tokens[1:]
			continue
		}
		return tokens
	}
	return nil
}

func bashCommandSegments(command string) []string {
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
	for _, r := range command {
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
			flush()
		case '|':
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
	return segments
}

func bashSegmentTokens(segment string) []string {
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
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "=") && len(tokens) == 0 {
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
