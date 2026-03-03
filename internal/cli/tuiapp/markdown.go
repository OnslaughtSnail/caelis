package tuiapp

import (
	"errors"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

var (
	assistantMarkdownRendererOnce  sync.Once
	assistantMarkdownRenderer      *glamour.TermRenderer
	errMarkdownRendererUnavailable = errors.New("markdown renderer unavailable")
)

func renderAssistantMarkdown(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if !looksLikeMarkdown(trimmed) {
		return trimmed
	}
	rendered, err := renderMarkdown(trimmed)
	if err != nil {
		return trimmed
	}
	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return trimmed
	}
	return rendered
}

func renderMarkdown(input string) (string, error) {
	assistantMarkdownRendererOnce.Do(func() {
		renderer, err := glamour.NewTermRenderer(
			glamour.WithStyles(markdownStyleConfig()),
			glamour.WithWordWrap(0),
		)
		if err != nil {
			return
		}
		assistantMarkdownRenderer = renderer
	})
	if assistantMarkdownRenderer == nil {
		return "", errMarkdownRendererUnavailable
	}
	return assistantMarkdownRenderer.Render(input)
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
	return style
}

func looksLikeMarkdown(text string) bool {
	if text == "" {
		return false
	}
	markers := []string{
		"```", "\n#", "\n- ", "\n* ", "\n1. ", "\n> ", "`", "**", "__", "![", "](",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	if strings.HasPrefix(text, "#") ||
		strings.HasPrefix(text, "- ") ||
		strings.HasPrefix(text, "* ") ||
		strings.HasPrefix(text, "1. ") ||
		strings.HasPrefix(text, "> ") {
		return true
	}
	return false
}
