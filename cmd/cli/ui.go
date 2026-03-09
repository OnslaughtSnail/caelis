package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/fatih/color"
)

// ui centralizes all formatting for the CLI output layer.
// It holds configured color printers and writes to a single io.Writer.
type ui struct {
	out     io.Writer
	verbose bool

	titleColor   *color.Color // Bold – section titles
	labelColor   *color.Color // Cyan – key labels in status/tool prefixes
	successColor *color.Color // Green – confirmations, assistant prefix
	warnColor    *color.Color // Yellow – warnings, system prefix
	errorColor   *color.Color // Red – errors
	dimColor     *color.Color // Faint – secondary info, reasoning prefix
	promptColor  *color.Color // Bold cyan – interactive prompt markers
	cmdColor     *color.Color // Magenta – command text in approvals
}

func newUI(out io.Writer, noColor bool, verbose bool) *ui {
	if noColor {
		color.NoColor = true
	}
	return &ui{
		out:          out,
		verbose:      verbose,
		titleColor:   color.New(color.Bold),
		labelColor:   color.New(color.FgCyan),
		successColor: color.New(color.FgGreen),
		warnColor:    color.New(color.FgYellow),
		errorColor:   color.New(color.FgRed),
		dimColor:     color.New(color.Faint),
		promptColor:  color.New(color.FgCyan, color.Bold),
		cmdColor:     color.New(color.FgMagenta),
	}
}

// ---------------------------------------------------------------------------
// Category 1: Command Output (slash-command results)
// ---------------------------------------------------------------------------

// Section prints a bold section header.
func (u *ui) Section(heading string) {
	u.titleColor.Fprintf(u.out, "%s\n", heading)
}

// KeyValue prints a labeled value with aligned formatting.
func (u *ui) KeyValue(key string, value string) {
	u.labelColor.Fprintf(u.out, "  %-14s", key)
	fmt.Fprintf(u.out, " %s\n", value)
}

// Numbered prints a numbered choice item.
func (u *ui) Numbered(index int, text string) {
	fmt.Fprintf(u.out, "  %d) %s\n", index, text)
}

// Plain prints an undecorated line.
func (u *ui) Plain(format string, args ...any) {
	fmt.Fprintf(u.out, format, args...)
}

// ---------------------------------------------------------------------------
// Category 2: Status Display
// ---------------------------------------------------------------------------

// Success prints a green confirmation.
func (u *ui) Success(format string, args ...any) {
	u.successColor.Fprintf(u.out, format, args...)
}

// Info prints a neutral informational line.
func (u *ui) Info(format string, args ...any) {
	fmt.Fprintf(u.out, format, args...)
}

// Separator prints a thin horizontal rule for visual grouping.
func (u *ui) Separator() {
	fmt.Fprintln(u.out, "---")
}

// ---------------------------------------------------------------------------
// Category 3: Approval Prompts
// ---------------------------------------------------------------------------

const approvalPromptAllowDeny = "  proceed / cancel (Esc): "
const approvalPromptAllowAlwaysDeny = "  proceed / session / cancel (Esc): "
const toolAuthPrompt = "  proceed / session / cancel (Esc): "

// ApprovalTitle prints the approval request title.
func (u *ui) ApprovalTitle(title string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	u.promptColor.Fprintf(u.out, "\n? %s\n", title)
}

// ApprovalHeader prints the approval request header.
func (u *ui) ApprovalHeader(toolName, action string) {
	u.promptColor.Fprintf(u.out, "\n? Approval: %s", toolName)
	if action != "" {
		fmt.Fprintf(u.out, " (%s)", action)
	}
	fmt.Fprintln(u.out)
}

// ToolAuthHeader prints the tool authorization header.
func (u *ui) ToolAuthHeader(toolName string) {
	u.promptColor.Fprintf(u.out, "\n? Authorize tool: %s\n", toolName)
}

// ApprovalReason prints the reason line.
func (u *ui) ApprovalReason(reason string) {
	fmt.Fprintf(u.out, "  %s\n", reason)
}

// ApprovalMeta prints one labeled approval context line.
func (u *ui) ApprovalMeta(label, value string) {
	label = strings.TrimSpace(label)
	value = strings.TrimSpace(value)
	if label == "" || value == "" {
		return
	}
	u.labelColor.Fprintf(u.out, "  %s:", label)
	fmt.Fprintf(u.out, " %s\n", value)
}

// ApprovalPath prints the resolved path for one file authorization request.
func (u *ui) ApprovalPath(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	u.labelColor.Fprintf(u.out, "  Path:")
	fmt.Fprintf(u.out, " %s\n", path)
}

// ApprovalDiff prints a compact diff preview before approval choices.
func (u *ui) ApprovalDiff(preview string) {
	preview = strings.TrimSpace(preview)
	if preview == "" {
		return
	}
	fmt.Fprintln(u.out, "  diff:")
	for _, line := range strings.Split(preview, "\n") {
		fmt.Fprintf(u.out, "  %s\n", line)
	}
}

// ApprovalCommand prints the command to be approved.
func (u *ui) ApprovalCommand(command string) {
	u.labelColor.Fprintf(u.out, "  Command:")
	fmt.Fprint(u.out, " ")
	u.cmdColor.Fprintf(u.out, "$ %s\n", command)
}

// ApprovalSessionNote prints the session-allow confirmation.
func (u *ui) ApprovalSessionNote(key string) {
	fmt.Fprintf(u.out, "  Allowed for the rest of this session: %s\n", key)
}

// ApprovalOutcome prints a short approval transcript entry.
func (u *ui) ApprovalOutcome(approved bool, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if approved {
		u.successColor.Fprintf(u.out, "✔ %s\n", text)
		return
	}
	u.errorColor.Fprintf(u.out, "✗ %s\n", text)
}

// ---------------------------------------------------------------------------
// Category 4: Alert / Error
// ---------------------------------------------------------------------------

// Warn prints a yellow warning.
func (u *ui) Warn(format string, args ...any) {
	u.warnColor.Fprint(u.out, "! ")
	fmt.Fprintf(u.out, format, args...)
}

// Error prints a red error.
func (u *ui) Error(format string, args ...any) {
	u.errorColor.Fprint(u.out, "error: ")
	fmt.Fprintf(u.out, format, args...)
}

// ErrorWithHint prints error + actionable next step.
func (u *ui) ErrorWithHint(errMsg string, hint string) {
	u.errorColor.Fprint(u.out, "error: ")
	fmt.Fprintln(u.out, errMsg)
	fmt.Fprintf(u.out, "  hint: %s\n", hint)
}

// Note prints a dimmed informational note.
func (u *ui) Note(format string, args ...any) {
	u.dimColor.Fprint(u.out, "  note: ")
	u.dimColor.Fprintf(u.out, format, args...)
}

// ---------------------------------------------------------------------------
// Event Rendering Prefixes
// ---------------------------------------------------------------------------

// AssistantPrefix returns the colored "* " prefix for assistant text.
func (u *ui) AssistantPrefix() string {
	return u.successColor.Sprint("* ")
}

// ReasoningPrefix returns the dimmed "│ " prefix for reasoning text.
func (u *ui) ReasoningPrefix() string {
	return u.dimColor.Sprint("│ ")
}

// ToolCallPrefix returns the cyan "▸ " prefix for tool calls.
func (u *ui) ToolCallPrefix(index int) string {
	return u.labelColor.Sprint("▸ ")
}

// ToolResultPrefix returns the "✓ " prefix for tool results.
func (u *ui) ToolResultPrefix() string {
	return u.labelColor.Sprint("✓ ")
}

// SystemPrefix returns "! " for system messages.
func (u *ui) SystemPrefix() string {
	return u.warnColor.Sprint("! ")
}

// ---------------------------------------------------------------------------
// Text utilities
// ---------------------------------------------------------------------------

const defaultLineWidth = 80

// WrapText wraps text to fit within the given width using word boundaries.
func WrapText(input string, width int, indent string) string {
	if width <= 0 {
		width = defaultLineWidth
	}
	lines := strings.Split(input, "\n")
	var out strings.Builder
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		wrapLine(&out, line, width, indent)
	}
	return out.String()
}

func wrapLine(out *strings.Builder, line string, width int, indent string) {
	if len(line) <= width {
		out.WriteString(line)
		return
	}
	words := strings.Fields(line)
	col := 0
	first := true
	for _, word := range words {
		needed := len(word)
		if !first {
			needed++ // space before word
		}
		if !first && col+needed > width {
			out.WriteByte('\n')
			out.WriteString(indent)
			col = len(indent)
			first = true
		}
		if !first {
			out.WriteByte(' ')
			col++
		}
		out.WriteString(word)
		col += len(word)
		first = false
	}
}

// closestCommand returns the command name with the smallest edit distance
// to input, or "" if no command is within threshold 3.
func closestCommand(input string, commands []string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return ""
	}
	best := ""
	bestDist := 4 // threshold
	for _, cmd := range commands {
		d := levenshtein(input, cmd)
		if d < bestDist {
			bestDist = d
			best = cmd
		}
	}
	return best
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// commandNames extracts sorted command names from a command map.
func commandNames(m map[string]slashCommand) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
