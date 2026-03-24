package tuiapp

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	blockMathPattern  = regexp.MustCompile(`(?ms)(^|\n)\$\$\s*\n?(.*?)\n?\s*\$\$`)
	inlineMathPattern = regexp.MustCompile(`(^|[^\\$])\$([^\n$]+?)\$`)
)

func normalizeTerminalMarkdown(input string) string {
	if input == "" {
		return ""
	}
	output := blockMathPattern.ReplaceAllStringFunc(input, func(match string) string {
		sub := blockMathPattern.FindStringSubmatch(match)
		if len(sub) != 3 {
			return match
		}
		prefix := sub[1]
		body := strings.TrimSpace(sub[2])
		if body == "" {
			return match
		}
		return prefix + body
	})
	return replaceInlineMath(output)
}

func replaceInlineMath(text string) string {
	indexes := inlineMathPattern.FindAllStringSubmatchIndex(text, -1)
	if len(indexes) == 0 {
		return text
	}
	var b strings.Builder
	last := 0
	for _, idx := range indexes {
		if len(idx) < 6 {
			continue
		}
		body := text[idx[4]:idx[5]]
		if !isInlineMathBody(body) {
			continue
		}
		b.WriteString(text[last:idx[0]])
		b.WriteString(text[idx[2]:idx[3]])
		b.WriteString(body)
		last = idx[1]
	}
	if last == 0 {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}

func isInlineMathBody(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	if strings.ContainsAny(body, "\\^_={}()+-*/<>[]") {
		return true
	}
	if strings.ContainsAny(body, " \t") {
		return false
	}
	hasLetter := false
	for _, r := range body {
		if r > unicode.MaxASCII {
			return true
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
			continue
		}
		if (r >= '0' && r <= '9') || r == '.' || r == ',' {
			continue
		}
		return false
	}
	return hasLetter
}
