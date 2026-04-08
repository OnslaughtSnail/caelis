package tuiapp

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

// ACP transcript rendering is driven by protocol event classes rather than
// by session source. Internal ACP children and external ACP participants
// should therefore render through the same event timeline.

type acpTranscriptRenderOptions struct {
	EmptyPlaceholder       string
	UseStatusPlaceholder   bool
	PlaceholderAsMeta      bool
	HideWaitingApprovalRow bool
	HideCompletedRow       bool
}

const (
	acpToolInlineArgsMaxWidth  = 72
	acpToolDetailPreviewBudget = 4
)

func renderACPTranscriptRows(blockID string, events []SubagentEvent, status string, width int, ctx BlockRenderContext, opts acpTranscriptRenderOptions) []RenderedRow {
	visible := visibleNarrativeEvents(events, status)
	rows := make([]RenderedRow, 0, len(visible)+2)
	hasContent := false
	for i := 0; i < len(visible); i++ {
		ev := visible[i]
		switch ev.Kind {
		case SEPlan:
			rows = append(rows, renderACPPlanRows(blockID, ev, width, ctx)...)
			hasContent = hasContent || len(ev.PlanEntries) > 0
		case SEReasoning:
			if text := strings.TrimSpace(ev.Text); text != "" {
				rows = append(rows, renderParticipantTurnNarrativeRows(blockID, text, tuikit.LineStyleReasoning, width, ctx, participantNarrativeEventActive(visible, i, status))...)
				hasContent = true
			}
		case SEAssistant:
			if text := strings.TrimSpace(ev.Text); text != "" {
				rows = append(rows, renderParticipantTurnNarrativeRows(blockID, text, tuikit.LineStyleAssistant, width, ctx, participantNarrativeEventActive(visible, i, status))...)
				hasContent = true
			}
		case SEToolCall:
			toolRows, consumed := renderACPToolLifecycleRows(blockID, visible, i, width, ctx)
			if len(toolRows) > 0 {
				rows = append(rows, toolRows...)
				hasContent = true
			}
			i = consumed
		case SEApproval:
			continue
		}
	}
	if !hasContent {
		placeholder := strings.TrimSpace(opts.EmptyPlaceholder)
		if placeholder == "" && opts.UseStatusPlaceholder {
			placeholder = participantTurnEmptyPlaceholder(status)
		}
		if placeholder != "" {
			style := ctx.Theme.HelpHintTextStyle().Width(width)
			if opts.PlaceholderAsMeta {
				style = ctx.Theme.TranscriptMetaStyle().Width(width)
			}
			rows = append(rows, StyledPlainRow(blockID, placeholder, style.Render(placeholder)))
		}
	}
	rows = append(rows, renderACPStatusRows(blockID, status, width, ctx, opts)...)
	return rows
}

func renderACPTranscriptLines(blockID string, events []SubagentEvent, status string, width int, ctx BlockRenderContext, opts acpTranscriptRenderOptions) []string {
	rows := renderACPTranscriptRows(blockID, events, status, width, ctx, opts)
	if len(rows) == 0 {
		return nil
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.Styled)
	}
	return lines
}

func renderACPPlanRows(blockID string, ev SubagentEvent, width int, ctx BlockRenderContext) []RenderedRow {
	rows := make([]RenderedRow, 0, len(ev.PlanEntries))
	for _, pe := range ev.PlanEntries {
		icon := "○"
		switch strings.ToLower(pe.Status) {
		case "done", "completed":
			icon = "✓"
		case "in_progress", "running":
			icon = "▸"
		case "failed":
			icon = "✗"
		case "blocked":
			icon = "⊘"
		}
		plain := icon + " " + strings.TrimSpace(pe.Content)
		rows = append(rows, StyledPlainRow(blockID, plain, ctx.Theme.TextStyle().Width(width).Render(plain)))
	}
	return rows
}

func renderACPToolLifecycleRows(blockID string, events []SubagentEvent, idx int, width int, ctx BlockRenderContext) ([]RenderedRow, int) {
	if idx < 0 || idx >= len(events) {
		return nil, idx
	}
	ev := events[idx]
	if ev.Kind != SEToolCall {
		return nil, idx
	}
	callID := strings.TrimSpace(ev.CallID)
	if callID == "" {
		if !shouldRenderToolEvent(ev) {
			return nil, idx
		}
		return renderParticipantTurnToolRows(blockID, ev, width, ctx), idx
	}

	end := idx
	for end+1 < len(events) {
		next := events[end+1]
		if next.Kind != SEToolCall || strings.TrimSpace(next.CallID) != callID {
			break
		}
		end++
	}

	group := events[idx : end+1]
	start := group[0]
	singleCompletedLifecycle := len(group) == 1 && start.Done && strings.TrimSpace(start.Args) != ""
	if start.Done && len(group) > 1 {
		start = SubagentEvent{}
		for _, item := range group {
			if !item.Done {
				start = item
				break
			}
		}
		if start.Kind == 0 && start.CallID == "" && start.Name == "" {
			start = group[0]
		}
	}

	var final SubagentEvent
	var preview string
	hasStart := (!start.Done || singleCompletedLifecycle) && strings.TrimSpace(start.Name) != ""
	hasFinal := false
	for _, item := range group {
		if !item.Done {
			if text := strings.TrimSpace(item.Output); text != "" {
				preview = text
			}
			continue
		}
		if !shouldRenderToolEvent(item) {
			continue
		}
		final = item
		hasFinal = true
	}
	if singleCompletedLifecycle {
		final = start
		hasFinal = shouldRenderToolEvent(final)
		start.Done = false
		start.Output = ""
	}

	if !hasStart {
		if hasFinal {
			return renderACPStandaloneFinalToolRows(blockID, final, width, ctx), end
		}
		if shouldRenderToolEvent(ev) {
			return renderParticipantTurnToolRows(blockID, ev, width, ctx), end
		}
		return nil, end
	}

	start.Args = compactACPToolInline(start.Args, width)
	rows := renderParticipantTurnToolRows(blockID, start, width, ctx)
	if text := strings.TrimSpace(preview); text != "" {
		rows = append(rows, renderACPToolDetailRows(blockID, "· ", text, width, ctx.Theme.HelpHintTextStyle())...)
	}
	if hasFinal {
		prefix := "✓ "
		style := ctx.Theme.HelpHintTextStyle()
		if final.Err {
			prefix = "✗ "
			style = ctx.Theme.ErrorStyle()
		}
		text := strings.TrimSpace(final.Output)
		if text == "" && !final.Err {
			text = "completed"
		}
		if text != "" {
			rows = append(rows, renderACPToolDetailRows(blockID, prefix, text, width, style)...)
		}
	}
	return rows, end
}

func renderACPStandaloneFinalToolRows(blockID string, ev SubagentEvent, width int, ctx BlockRenderContext) []RenderedRow {
	output := strings.TrimSpace(ev.Output)
	if output == "" || (!strings.Contains(output, "\n") && displayColumns(output) <= maxInt(24, width/2)) {
		return renderParticipantTurnToolRows(blockID, ev, width, ctx)
	}
	header := SubagentEvent{
		Kind: SEToolCall,
		Name: ev.Name,
		Done: true,
		Err:  ev.Err,
	}
	rows := renderParticipantTurnToolRows(blockID, header, width, ctx)
	prefix := "✓ "
	style := ctx.Theme.HelpHintTextStyle()
	if ev.Err {
		prefix = "✗ "
		style = ctx.Theme.ErrorStyle()
	}
	rows = append(rows, renderACPToolDetailRows(blockID, prefix, output, width, style)...)
	return rows
}

func renderACPToolDetailRows(blockID string, prefix string, text string, width int, style lipgloss.Style) []RenderedRow {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	prefix = strings.TrimRight(prefix, " ") + " "
	available := maxInt(1, width-displayColumns(prefix))
	segments := wrapACPToolDetailText(text, available)
	rows := make([]RenderedRow, 0, len(segments))
	for i, segment := range segments {
		linePrefix := prefix
		if i > 0 {
			linePrefix = strings.Repeat(" ", displayColumns(prefix))
		}
		plain := linePrefix + segment
		rows = append(rows, StyledPlainRow(blockID, plain, style.Width(width).Render(plain)))
	}
	return rows
}

func compactACPToolInline(text string, width int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return ""
	}
	budget := minInt(acpToolInlineArgsMaxWidth, maxInt(16, width-12))
	if displayColumns(text) <= budget {
		return text
	}
	return truncateTailDisplay(text, budget)
}

func wrapACPToolDetailText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	lines := strings.Split(text, "\n")
	segments := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		wrapped := wrapToolOutputText(line, width)
		if len(wrapped) == 0 {
			wrapped = []string{line}
		}
		segments = append(segments, wrapped...)
	}
	if len(segments) == 0 {
		return []string{text}
	}
	if len(segments) <= acpToolDetailPreviewBudget {
		return segments
	}
	truncated := append([]string(nil), segments[:acpToolDetailPreviewBudget-1]...)
	truncated = append(truncated, "… "+pluralizeMoreLines(len(segments)-acpToolDetailPreviewBudget+1))
	return truncated
}

func pluralizeMoreLines(n int) string {
	if n <= 1 {
		return "1 more line"
	}
	return strconv.Itoa(n) + " more lines"
}

func renderACPStatusRows(blockID string, status string, width int, ctx BlockRenderContext, opts acpTranscriptRenderOptions) []RenderedRow {
	label := ""
	style := ctx.Theme.HelpHintTextStyle().Width(width)
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "waiting_approval":
		if opts.HideWaitingApprovalRow {
			return nil
		}
		label = "waiting approval"
		style = ctx.Theme.WarnStyle().Width(width)
	case "completed":
		if opts.HideCompletedRow {
			return nil
		}
		label = "✓ completed"
	case "running", "initializing", "prompting":
		return nil
	case "failed":
		label = "✗ failed"
		style = ctx.Theme.ErrorStyle().Width(width)
	case "interrupted":
		label = "⊘ interrupted"
		style = ctx.Theme.WarnStyle().Width(width)
	case "timed_out":
		label = "⌛ timed out"
		style = ctx.Theme.WarnStyle().Width(width)
	}
	if label == "" {
		return nil
	}
	return []RenderedRow{StyledPlainRow(blockID, label, style.Render(label))}
}

func participantTurnEmptyPlaceholder(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "running":
		return "  · waiting for agent output"
	case "initializing":
		return "  · initializing session"
	case "prompting":
		return ""
	case "waiting_approval":
		return "  · waiting approval"
	default:
		return ""
	}
}
