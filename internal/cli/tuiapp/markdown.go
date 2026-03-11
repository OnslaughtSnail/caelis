package tuiapp

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

var (
	errMarkdownRendererUnavailable = errors.New("markdown renderer unavailable")
	markdownRendererCache          sync.Map
	blockMathPattern               = regexp.MustCompile("(?ms)(^|\\n)\\$\\$\\s*\\n?(.*?)\\n?\\s*\\$\\$")
	inlineMathPattern              = regexp.MustCompile(`(^|[^\\$])\$([^\n$]+?)\$`)
)

func renderAssistantMarkdown(text string, width int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	normalized := normalizeTerminalMarkdown(trimmed)
	if !looksLikeMarkdown(normalized) {
		return trimmed
	}
	rendered, err := renderMarkdown(normalized, width)
	if err != nil {
		return trimmed
	}
	rendered = strings.TrimSuffix(rendered, "\n")
	if rendered == "" {
		return trimmed
	}
	return rendered
}

func renderMarkdown(input string, width int) (string, error) {
	if width <= 0 {
		width = 80
	}
	renderer, err := markdownRenderer(width)
	if err != nil {
		return "", errMarkdownRendererUnavailable
	}
	return renderer.Render(input)
}

func markdownRenderer(width int) (*glamour.TermRenderer, error) {
	if cached, ok := markdownRendererCache.Load(width); ok {
		if renderer, ok := cached.(*glamour.TermRenderer); ok && renderer != nil {
			return renderer, nil
		}
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(markdownStyleConfig()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	markdownRendererCache.Store(width, renderer)
	return renderer, nil
}

func markdownStyleConfig() ansi.StyleConfig {
	style := styles.DarkStyleConfig
	// Keep headings readable, but hide Markdown heading markers.
	style.H1.Prefix = ""
	style.H2.Prefix = ""
	style.H3.Prefix = ""
	style.H4.Prefix = ""
	style.H5.Prefix = ""
	style.H6.Prefix = ""
	// Avoid excessive accent colors on heading/list markers.
	style.Heading.Color = nil
	style.H1.Color = nil
	style.H2.Color = nil
	style.H3.Color = nil
	style.H4.Color = nil
	style.H5.Color = nil
	style.H6.Color = nil
	style.Enumeration.Color = nil
	style.Item.Color = nil
	// Reduce code background flashing during streaming rerenders.
	style.Code.BackgroundColor = nil
	style.Code.Color = stringPtr("#f5c451")
	style.CodeBlock.BackgroundColor = nil
	style.CodeBlock.Color = stringPtr("#d4d4d8")
	if style.CodeBlock.Chroma == nil {
		style.CodeBlock.Chroma = &ansi.Chroma{}
	}
	style.CodeBlock.Chroma.Background.BackgroundColor = nil
	style.CodeBlock.Chroma.Background.Color = stringPtr("#d4d4d8")
	return style
}

func looksLikeMarkdown(text string) bool {
	if text == "" {
		return false
	}
	markers := []string{
		"```", "\n#", "\n- ", "\n* ", "\n1. ", "\n> ", "`", "**", "__", "![", "](", "$$",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	if containsInlineMath(text) {
		return true
	}
	if strings.HasPrefix(text, "#") ||
		strings.HasPrefix(text, "- ") ||
		strings.HasPrefix(text, "* ") ||
		strings.HasPrefix(text, "1. ") ||
		strings.HasPrefix(text, "> ") ||
		strings.HasPrefix(text, "$$") {
		return true
	}
	return false
}

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
		return prefix + "```text\n" + body + "\n```"
	})
	output = replaceInlineMath(output)
	return output
}

func containsInlineMath(text string) bool {
	matches := inlineMathPattern.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		if isInlineMathBody(match[2]) {
			return true
		}
	}
	return false
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
		b.WriteByte('`')
		b.WriteString(body)
		b.WriteByte('`')
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

func stringPtr(value string) *string {
	return &value
}
