package tuiapp

import (
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

// NarrativeBlockKind identifies the structural type of a narrative line.
type NarrativeBlockKind int

const (
	NarrativePlain      NarrativeBlockKind = iota
	NarrativeHeading                       // "# …" through "###### …"
	NarrativeCodeFence                     // lines inside ``` … ```
	NarrativeCodeFenceDelim                // the ``` line itself
	NarrativeListItem                      // "- …", "* …", "1. …"
	NarrativeBlockquote                    // "> …"
)

// NarrativeLine is one output line from the block splitter, carrying both
// the original text and the structural classification.
type NarrativeLine struct {
	Kind NarrativeBlockKind
	Text string // original text (no markers stripped)
}

// SplitNarrativeBlocks splits raw markdown-ish text into classified lines.
// It uses a simple state machine for fenced code blocks and line-prefix
// detection for everything else. Streaming partial input is fine — an
// unclosed fence simply classifies remaining lines as code.
func SplitNarrativeBlocks(text string) []NarrativeLine {
	lines := strings.Split(text, "\n")
	out := make([]NarrativeLine, 0, len(lines))
	inFence := false
	fencePrefix := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Fenced code block state machine.
		if isFenceDelimiter(trimmed) {
			if !inFence {
				inFence = true
				fencePrefix = extractFencePrefix(trimmed)
				out = append(out, NarrativeLine{Kind: NarrativeCodeFenceDelim, Text: line})
				continue
			}
			// Closing fence: must match opening prefix length.
			if isClosingFence(trimmed, fencePrefix) {
				inFence = false
				fencePrefix = ""
				out = append(out, NarrativeLine{Kind: NarrativeCodeFenceDelim, Text: line})
				continue
			}
		}

		if inFence {
			out = append(out, NarrativeLine{Kind: NarrativeCodeFence, Text: line})
			continue
		}

		// Outside a fence: classify by prefix.
		out = append(out, classifyNarrativeLine(line, trimmed))
	}
	return out
}

// classifyNarrativeLine determines the kind of a non-fence line.
func classifyNarrativeLine(line, trimmed string) NarrativeLine {
	if trimmed == "" {
		return NarrativeLine{Kind: NarrativePlain, Text: line}
	}

	// Heading: up to 3 leading spaces, then 1-6 '#' followed by space.
	leading := countNarrativeLeadingSpaces(line)
	if leading <= 3 {
		rest := line[leading:]
		hashes := 0
		for _, ch := range rest {
			if ch == '#' {
				hashes++
			} else {
				break
			}
		}
		if hashes >= 1 && hashes <= 6 && len(rest) > hashes && rest[hashes] == ' ' {
			return NarrativeLine{Kind: NarrativeHeading, Text: line}
		}
	}

	// Blockquote: "> " or ">"
	if strings.HasPrefix(trimmed, "> ") || trimmed == ">" {
		return NarrativeLine{Kind: NarrativeBlockquote, Text: line}
	}

	// List item: "- ", "* ", or "N. " (where N is 1+ digits)
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return NarrativeLine{Kind: NarrativeListItem, Text: line}
	}
	if isOrderedListPrefix(trimmed) {
		return NarrativeLine{Kind: NarrativeListItem, Text: line}
	}

	return NarrativeLine{Kind: NarrativePlain, Text: line}
}

// isFenceDelimiter returns true for lines like "```" or "```python".
func isFenceDelimiter(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	ch := trimmed[0]
	if ch != '`' && ch != '~' {
		return false
	}
	count := 0
	for _, r := range trimmed {
		if byte(r) == ch {
			count++
		} else {
			break
		}
	}
	return count >= 3
}

// extractFencePrefix returns the fence character sequence (e.g. "```" or "~~~~").
func extractFencePrefix(trimmed string) string {
	ch := trimmed[0]
	i := 0
	for i < len(trimmed) && trimmed[i] == ch {
		i++
	}
	return trimmed[:i]
}

// isClosingFence checks if trimmed is a closing fence matching the opening prefix.
func isClosingFence(trimmed, fencePrefix string) bool {
	if len(trimmed) < len(fencePrefix) {
		return false
	}
	ch := fencePrefix[0]
	count := 0
	for _, r := range trimmed {
		if byte(r) == ch {
			count++
		} else {
			// Closing fence must be only fence chars (no info string).
			return false
		}
	}
	return count >= len(fencePrefix)
}

// isOrderedListPrefix checks for "N. " pattern at start of trimmed line.
func isOrderedListPrefix(trimmed string) bool {
	i := 0
	for i < len(trimmed) && i < 9 && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(trimmed)-1 {
		return false
	}
	return trimmed[i] == '.' && i+1 < len(trimmed) && trimmed[i+1] == ' '
}

// countNarrativeLeadingSpaces counts leading spaces (tabs = 4 spaces).
func countNarrativeLeadingSpaces(s string) int {
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

// ---------------------------------------------------------------------------
// Fence-aware math normalization.
// ---------------------------------------------------------------------------

// applyMathNormalization applies math normalization (block $$…$$ and inline
// $…$) only to non-code-fence narrative lines. Code fence content is
// preserved verbatim. When block-math normalization changes line count,
// affected lines are re-classified.
func applyMathNormalization(nls []NarrativeLine) []NarrativeLine {
	result := make([]NarrativeLine, 0, len(nls))
	i := 0
	for i < len(nls) {
		if nls[i].Kind == NarrativeCodeFence || nls[i].Kind == NarrativeCodeFenceDelim {
			result = append(result, nls[i])
			i++
			continue
		}
		// Collect consecutive non-fence lines.
		start := i
		for i < len(nls) && nls[i].Kind != NarrativeCodeFence && nls[i].Kind != NarrativeCodeFenceDelim {
			i++
		}
		texts := make([]string, i-start)
		for j := start; j < i; j++ {
			texts[j-start] = nls[j].Text
		}
		joined := strings.Join(texts, "\n")
		normalized := normalizeTerminalMarkdown(joined)
		normalizedLines := strings.Split(normalized, "\n")

		if len(normalizedLines) == i-start {
			// Same count — update text, keep original kind.
			for j, text := range normalizedLines {
				result = append(result, NarrativeLine{Kind: nls[start+j].Kind, Text: text})
			}
		} else {
			// Count changed (block math collapsed) — re-classify.
			for _, text := range normalizedLines {
				trimmed := strings.TrimSpace(text)
				result = append(result, classifyNarrativeLine(text, trimmed))
			}
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Plain-text extraction from narrative lines.
// ---------------------------------------------------------------------------

// NarrativeToPlainRows converts classified narrative lines to plain text rows
// suitable for display. Headings have markers stripped, code fences are
// preserved verbatim, and basic inline markdown is simplified.
func NarrativeToPlainRows(nls []NarrativeLine) []string {
	rows := make([]string, 0, len(nls))
	for _, nl := range nls {
		switch nl.Kind {
		case NarrativeCodeFenceDelim:
			rows = append(rows, nl.Text)
		case NarrativeCodeFence:
			rows = append(rows, strings.TrimRight(nl.Text, " \t"))
		case NarrativeHeading:
			rows = append(rows, stripHeadingMarker(nl.Text))
		case NarrativeListItem, NarrativeBlockquote:
			rows = append(rows, simplifyInlineMarkers(strings.TrimRight(nl.Text, " \t")))
		default:
			rows = append(rows, simplifyInlineMarkers(strings.TrimRight(nl.Text, " \t")))
		}
	}
	return rows
}

// stripHeadingMarker removes the "#… " prefix and trims trailing whitespace.
func stripHeadingMarker(line string) string {
	leading := countNarrativeLeadingSpaces(line)
	rest := line[leading:]
	i := 0
	for i < len(rest) && rest[i] == '#' {
		i++
	}
	// Skip the space after hashes.
	if i < len(rest) && rest[i] == ' ' {
		i++
	}
	return strings.TrimRight(rest[i:], " \t")
}

// ---------------------------------------------------------------------------
// buildNarrativeRows: consolidated pipeline for assistant/reasoning content.
// ---------------------------------------------------------------------------

// buildNarrativeRows is the single canonical pipeline for producing
// NarrativeLine + plainRow pairs from raw assistant/reasoning text.
//
//	raw → normalize line endings → SplitNarrativeBlocks →
//	  applyMathNormalization (code-fence–safe) → NarrativeToPlainRows →
//	  lockstep trim leading/trailing blanks
//
// Returns nil, nil when content is empty after trimming.
func buildNarrativeRows(raw string) ([]NarrativeLine, []string) {
	raw = strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	nls := SplitNarrativeBlocks(raw)
	nls = applyMathNormalization(nls)
	plainRows := NarrativeToPlainRows(nls)

	// Trim leading blank rows — lockstep.
	for len(plainRows) > 0 && strings.TrimSpace(plainRows[0]) == "" {
		plainRows = plainRows[1:]
		nls = nls[1:]
	}
	if len(plainRows) == 0 {
		return nil, nil
	}

	// Trim trailing blank rows — lockstep.
	for len(plainRows) > 0 && strings.TrimSpace(plainRows[len(plainRows)-1]) == "" {
		plainRows = plainRows[:len(plainRows)-1]
		nls = nls[:len(nls)-1]
	}

	return nls, plainRows
}

// ---------------------------------------------------------------------------
// Styling: derive styled text from plain rows + narrative classification.
// ---------------------------------------------------------------------------

// styleNarrativeLine applies minimal theme styling to a plain-text narrative
// line based on its structural kind. The roleStyle controls the role-level
// colorization (assistant vs reasoning).
func styleNarrativeLine(plain string, kind NarrativeBlockKind, roleStyle tuikit.LineStyle, theme tuikit.Theme) string {
	switch kind {
	case NarrativeHeading:
		return styleHeadingLine(plain, roleStyle, theme)
	case NarrativeCodeFence, NarrativeCodeFenceDelim:
		return theme.TextStyle().Render(plain)
	default:
		return tuikit.ColorizeLogLine(plain, roleStyle, theme)
	}
}

// styleHeadingLine renders a heading in bold TextPrimary, keeping the role
// prefix (e.g. "* ") styled with the role color if present.
func styleHeadingLine(plain string, roleStyle tuikit.LineStyle, theme tuikit.Theme) string {
	prefix, body := splitRolePrefix(plain)
	var styledPrefix string
	if prefix != "" {
		styledPrefix = tuikit.ColorizeLogLine(prefix, roleStyle, theme)
	}
	styledBody := theme.TextStyle().Bold(true).Render(body)
	return styledPrefix + styledBody
}

// splitRolePrefix splits known role prefixes ("* ", "· ", "  ") from body text.
func splitRolePrefix(plain string) (prefix, body string) {
	for _, p := range []string{"* ", "· ", "  "} {
		if strings.HasPrefix(plain, p) {
			return p, plain[len(p):]
		}
	}
	return "", plain
}

// simplifyInlineMarkers strips bold, italic, and strikethrough markers from
// a line. This does not attempt full markdown inline parsing — it simply
// removes the symmetrical delimiters **, __, ~~ and un-backticks inline code.
func simplifyInlineMarkers(line string) string {
	if line == "" {
		return ""
	}
	line = strings.ReplaceAll(line, "**", "")
	line = strings.ReplaceAll(line, "__", "")
	line = strings.ReplaceAll(line, "~~", "")
	return line
}
