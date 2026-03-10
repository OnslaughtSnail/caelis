package tuiapp

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// ---------------------------------------------------------------------------
// Diagnostics
// ---------------------------------------------------------------------------

func (m *Model) observeRender(duration time.Duration, bytes int, redrawMode string) {
	m.diag.Frames++
	m.diag.LastFrameDuration = duration
	if strings.TrimSpace(redrawMode) == "" {
		redrawMode = "incremental"
	}
	m.diag.RedrawMode = redrawMode
	if redrawMode == "fullscreen" || redrawMode == "full" {
		m.diag.FullRepaints++
	} else {
		m.diag.IncrementalFrames++
	}
	if duration >= 40*time.Millisecond {
		m.diag.SlowFrames++
	}
	if duration > m.diag.MaxFrameDuration {
		m.diag.MaxFrameDuration = duration
	}
	if m.diag.Frames == 1 {
		m.diag.AvgFrameDuration = duration
	} else {
		total := time.Duration(int64(m.diag.AvgFrameDuration)*(int64(m.diag.Frames)-1) + int64(duration))
		m.diag.AvgFrameDuration = total / time.Duration(m.diag.Frames)
	}
	if bytes > 0 {
		m.diag.RenderBytes += uint64(bytes)
		if uint64(bytes) > m.diag.PeakFrameBytes {
			m.diag.PeakFrameBytes = uint64(bytes)
		}
	}
	m.observeInputLatency()
	m.diag.LastRenderAt = time.Now()
	if m.cfg.OnDiagnostics != nil {
		m.cfg.OnDiagnostics(m.diag)
	}
}

func (m *Model) requestFullRepaint() {
	m.pendingFullRepaint = true
}

func (m *Model) observeInputLatency() {
	if m.pendingInputAt.IsZero() {
		return
	}
	latency := time.Since(m.pendingInputAt)
	m.pendingInputAt = time.Time{}
	m.diag.LastInputLatency = latency
	m.inputLatencyCount++
	if m.diag.AvgInputLatency == 0 || m.inputLatencyCount <= 1 {
		m.diag.AvgInputLatency = latency
	} else {
		total := time.Duration(int64(m.diag.AvgInputLatency)*(int64(m.inputLatencyCount)-1) + int64(latency))
		m.diag.AvgInputLatency = total / time.Duration(m.inputLatencyCount)
	}
	const window = 128
	if len(m.inputLatencyWindow) >= window {
		copy(m.inputLatencyWindow, m.inputLatencyWindow[1:])
		m.inputLatencyWindow = m.inputLatencyWindow[:window-1]
	}
	m.inputLatencyWindow = append(m.inputLatencyWindow, latency)
	m.diag.P95InputLatency = percentileDuration(m.inputLatencyWindow, 0.95)
}

// ---------------------------------------------------------------------------
// Utility functions
// ---------------------------------------------------------------------------

func mentionQueryAtCursor(input []rune, cursor int) (int, int, string, bool) {
	if len(input) == 0 {
		return 0, 0, "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	start := cursor
	for start > 0 && isMentionQueryRune(input[start-1]) {
		start--
	}
	if start == 0 || input[start-1] != '@' {
		return 0, 0, "", false
	}
	at := start - 1
	if at > 0 {
		prev := input[at-1]
		if prev != ' ' && prev != '\t' && prev != '(' && prev != '[' && prev != '{' && prev != ',' && prev != ';' && prev != ':' && prev != '"' && prev != '\'' {
			return 0, 0, "", false
		}
	}
	end := cursor
	for end < len(input) && isMentionQueryRune(input[end]) {
		end++
	}
	return at, end, string(input[start:end]), true
}

func resumeQueryAtCursor(input []rune, cursor int) (string, bool) {
	if len(input) == 0 {
		return "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	text := strings.TrimSpace(string(input[:cursor]))
	if text == "" {
		return "", false
	}
	if text == "/resume" {
		return "", true
	}
	if !strings.HasPrefix(text, "/resume ") {
		return "", false
	}
	query := strings.TrimSpace(strings.TrimPrefix(text, "/resume "))
	return query, true
}

func slashArgQueryAtCursor(input []rune, cursor int) (string, string, bool) {
	if len(input) == 0 {
		return "", "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	raw := string(input[:cursor])
	text := strings.TrimSpace(raw)
	if text == "" || !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", "", false
	}
	command := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fields[0])), "/")
	hasTrailingDelimiter := false
	if len(raw) > 0 {
		last := raw[len(raw)-1]
		hasTrailingDelimiter = last == ' ' || last == '\t'
	}
	switch command {
	case "model":
		if len(fields) == 1 {
			if !hasTrailingDelimiter {
				return "", "", false
			}
			return command, "", true
		}
		action := strings.ToLower(strings.TrimSpace(fields[1]))
		if len(fields) == 2 {
			if hasTrailingDelimiter {
				switch action {
				case "list":
					return "", "", false
				case "use", "rm", "edit":
					return "model " + action, "", true
				default:
					return "", "", false
				}
			}
			if action == "" {
				return "", "", false
			}
			switch action {
			case "list", "use", "rm", "edit":
			default:
				return "model", action, true
			}
			return "model", action, true
		}
		switch action {
		case "list", "use", "rm", "edit":
		default:
			return "", "", false
		}
		if action != "use" {
			return "model " + action, strings.TrimSpace(fields[2]), true
		}
		alias := strings.TrimSpace(fields[2])
		if alias == "" {
			return "", "", false
		}
		if len(fields) == 3 {
			if hasTrailingDelimiter {
				return "model use " + alias, "", true
			}
			return "model use", alias, true
		}
		return "model use " + alias, strings.TrimSpace(strings.Join(fields[3:], " ")), true
	case "sandbox", "permission":
		if len(fields) == 1 {
			if !hasTrailingDelimiter {
				return "", "", false
			}
			return command, "", true
		}
		if len(fields) == 2 {
			return command, strings.TrimSpace(fields[1]), true
		}
		return "", "", false
	default:
		return "", "", false
	}
}

func slashCommandQueryAtCursor(input []rune, cursor int) (string, bool) {
	if len(input) == 0 {
		return "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	text := strings.TrimSpace(string(input[:cursor]))
	if text == "" || !strings.HasPrefix(text, "/") {
		return "", false
	}
	if strings.Contains(text, " ") {
		return "", false
	}
	query := strings.TrimPrefix(text, "/")
	return query, true
}

func isMentionQueryRune(r rune) bool {
	if r == '_' || r == '-' || r == '.' || r == '/' || r == '\\' {
		return true
	}
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// skillQueryAtCursor detects a $skill token at cursor position.
// Returns the span [start, end) and the query text after '$'.
func skillQueryAtCursor(input []rune, cursor int) (int, int, string, bool) {
	if len(input) == 0 {
		return 0, 0, "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	start := cursor
	for start > 0 && isSkillQueryRune(input[start-1]) {
		start--
	}
	if start == 0 || input[start-1] != '$' {
		return 0, 0, "", false
	}
	dollar := start - 1
	if dollar > 0 {
		prev := input[dollar-1]
		if prev != ' ' && prev != '\t' && prev != '(' && prev != '[' && prev != '{' && prev != ',' && prev != ';' && prev != ':' && prev != '"' && prev != '\'' {
			return 0, 0, "", false
		}
	}
	end := cursor
	for end < len(input) && isSkillQueryRune(input[end]) {
		end++
	}
	return dollar, end, string(input[start:end]), true
}

func isSkillQueryRune(r rune) bool {
	if r == '_' || r == '-' {
		return true
	}
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func replaceRuneSpan(input []rune, start int, end int, replacement string) ([]rune, int) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(input) {
		end = len(input)
	}
	out := append([]rune(nil), input[:start]...)
	repl := []rune(replacement)
	out = append(out, repl...)
	out = append(out, input[end:]...)
	return out, start + len(repl)
}

// overlayBottom places an overlay box near the bottom of the base text.
func overlayBottom(base string, overlay string, width int, baseLineCount int) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")
	if len(baseLines) == 0 {
		return overlay
	}
	startRow := maxInt(0, len(baseLines)-len(overlayLines)-2)
	for i, line := range overlayLines {
		row := startRow + i
		if row < 0 || row >= len(baseLines) {
			continue
		}
		baseLines[row] = padCenter(line, width)
	}
	return strings.Join(baseLines, "\n")
}

func padCenter(text string, width int) string {
	if width <= 0 {
		return text
	}
	textWidth := utf8.RuneCountInString(text)
	if textWidth >= width {
		return text
	}
	left := (width - textWidth) / 2
	right := width - textWidth - left
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}

func percentileDuration(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	if percentile <= 0 {
		percentile = 0
	}
	if percentile >= 1 {
		percentile = 1
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	index := int(float64(len(sorted)-1) * percentile)
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func normalizedSelectionRange(start textSelectionPoint, end textSelectionPoint, lineCount int) (textSelectionPoint, textSelectionPoint, bool) {
	if lineCount <= 0 || start.line < 0 || end.line < 0 {
		return textSelectionPoint{}, textSelectionPoint{}, false
	}
	if start.line >= lineCount {
		start.line = lineCount - 1
	}
	if end.line >= lineCount {
		end.line = lineCount - 1
	}
	if start.line > end.line || (start.line == end.line && start.col > end.col) {
		start, end = end, start
	}
	if start.col < 0 {
		start.col = 0
	}
	if end.col < 0 {
		end.col = 0
	}
	return start, end, true
}

func selectionTextFromLines(lines []string, start textSelectionPoint, end textSelectionPoint) string {
	if len(lines) == 0 {
		return ""
	}
	if start.line == end.line && start.col == end.col {
		return ""
	}
	var out []string
	for i := start.line; i <= end.line && i < len(lines); i++ {
		line := lines[i]
		width := displayColumns(line)
		from := 0
		to := width
		if i == start.line {
			from = start.col
		}
		if i == end.line {
			to = end.col
		}
		if from < 0 {
			from = 0
		}
		if to > width {
			to = width
		}
		if to < from {
			to = from
		}
		out = append(out, sliceByDisplayColumns(line, from, to))
	}
	return strings.Join(out, "\n")
}

func renderSelectionOnLines(lines []string, start textSelectionPoint, end textSelectionPoint) []string {
	if len(lines) == 0 {
		return nil
	}
	highlight := lipgloss.NewStyle().Reverse(true)
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if i < start.line || i > end.line {
			out = append(out, lines[i])
			continue
		}
		line := lines[i]
		width := displayColumns(line)
		from := 0
		to := width
		if i == start.line {
			from = start.col
		}
		if i == end.line {
			to = end.col
		}
		if from < 0 {
			from = 0
		}
		if to > width {
			to = width
		}
		if to < from {
			to = from
		}
		prefix := sliceByDisplayColumns(line, 0, from)
		middle := sliceByDisplayColumns(line, from, to)
		suffix := sliceByDisplayColumns(line, to, width)
		if middle == "" {
			out = append(out, line)
			continue
		}
		out = append(out, prefix+highlight.Render(middle)+suffix)
	}
	return out
}

func displayColumns(s string) int {
	return runewidth.StringWidth(s)
}

func sliceByDisplayColumns(s string, start int, end int) string {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if s == "" || start == end {
		return ""
	}
	var b strings.Builder
	col := 0
	prevIncluded := false
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if w < 0 {
			w = 0
		}
		if w == 0 {
			if prevIncluded {
				b.WriteRune(r)
			}
			continue
		}
		if col >= end {
			break
		}
		include := col >= start && col < end
		if include {
			b.WriteRune(r)
		}
		prevIncluded = include
		col += w
	}
	return b.String()
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
