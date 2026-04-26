package tuiapp

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/OnslaughtSnail/caelis/tui/tuikit"
	"github.com/charmbracelet/x/ansi"
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
	ToolOutputPanels       bool
	ToolPanelExpanded      func(callID string) bool
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
			if groupRows, consumed, ok := renderACPTaskControlGroupRows(blockID, visible, i, width, ctx); ok {
				rows = append(rows, groupRows...)
				hasContent = true
				i = consumed
				continue
			}
			if groupRows, consumed, ok := renderACPExplorationGroupRows(blockID, visible, i, width, ctx, opts); ok {
				rows = append(rows, groupRows...)
				hasContent = true
				i = consumed
				continue
			}
			toolRows, consumed := renderACPToolLifecycleRows(blockID, visible, i, width, ctx, opts)
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

func renderACPTaskControlGroupRows(blockID string, events []SubagentEvent, idx int, width int, ctx BlockRenderContext) ([]RenderedRow, int, bool) {
	actions, end := compactTaskControlActions(events, idx)
	if len(actions) == 0 {
		return nil, idx, false
	}
	plain := "↻ " + strings.Join(actions, " · ")
	styled := ctx.Theme.TranscriptMetaStyle().Width(width).Render(plain)
	return []RenderedRow{StyledPlainRow(blockID, plain, styled)}, end, true
}

func compactTaskControlActions(events []SubagentEvent, idx int) ([]string, int) {
	if idx < 0 || idx >= len(events) || !isTaskControlEvent(events[idx]) {
		return nil, idx
	}
	actions := make([]string, 0, 2)
	seen := map[string]struct{}{}
	end := idx - 1
	for i := idx; i < len(events); i++ {
		ev := events[i]
		if !isTaskControlEvent(ev) {
			break
		}
		end = i
		key := strings.TrimSpace(ev.CallID)
		if key == "" {
			key = strconv.Itoa(i)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		action := strings.TrimSpace(ev.Args)
		if action == "" {
			continue
		}
		seen[key] = struct{}{}
		actions = append(actions, action)
	}
	return actions, end
}

func isTaskControlEvent(ev SubagentEvent) bool {
	return ev.Kind == SEToolCall && strings.EqualFold(strings.TrimSpace(ev.Name), "TASK")
}

func renderACPExplorationGroupRows(blockID string, events []SubagentEvent, idx int, width int, ctx BlockRenderContext, opts acpTranscriptRenderOptions) ([]RenderedRow, int, bool) {
	group, end := compactExplorationGroup(events, idx, opts)
	if len(group) < 2 {
		return nil, idx, false
	}
	summary := explorationGroupSummary(group)
	if summary == "" {
		return nil, idx, false
	}
	token := explorationGroupClickToken(group)
	styled := styleToolEventLine(ctx.Theme, summary, tuikit.LineStyleTool)
	rows := []RenderedRow{StyledPlainClickableRow(blockID, summary, styled, token)}
	if detail := explorationGroupDetail(group, width); detail != "" {
		plain := "  " + detail
		rows = append(rows, StyledPlainClickableRow(blockID, plain, ctx.Theme.TranscriptMetaStyle().Width(width).Render(plain), token))
	}
	return rows, end, true
}

func compactExplorationGroup(events []SubagentEvent, idx int, opts acpTranscriptRenderOptions) ([]SubagentEvent, int) {
	if idx < 0 || idx >= len(events) {
		return nil, idx
	}
	group := make([]SubagentEvent, 0, 4)
	end := idx - 1
	for i := idx; i < len(events); i++ {
		ev := events[i]
		if !isCompactExplorationTool(ev) {
			break
		}
		callID := strings.TrimSpace(ev.CallID)
		if opts.ToolPanelExpanded != nil && opts.ToolPanelExpanded(callID) {
			break
		}
		group = append(group, ev)
		end = i
	}
	return group, end
}

func isCompactExplorationTool(ev SubagentEvent) bool {
	if ev.Kind != SEToolCall || !ev.Done || ev.Err {
		return false
	}
	if strings.TrimSpace(ev.CallID) == "" {
		return false
	}
	return shouldDefaultCollapseToolPanel(ev.Name)
}

func explorationGroupSummary(events []SubagentEvent) string {
	fileCount, searchCount := 0, 0
	for _, ev := range events {
		switch explorationToolClass(ev.Name) {
		case "search":
			searchCount++
		default:
			fileCount++
		}
	}
	parts := make([]string, 0, 2)
	if fileCount > 0 {
		parts = append(parts, pluralizeUnit(fileCount, "file"))
	}
	if searchCount > 0 {
		parts = append(parts, pluralizeUnit(searchCount, "search"))
	}
	if len(parts) == 0 {
		return ""
	}
	return "▸ explored " + strings.Join(parts, ", ")
}

func explorationGroupDetail(events []SubagentEvent, width int) string {
	parts := make([]string, 0, len(events))
	for _, ev := range events {
		name := strings.ToUpper(strings.TrimSpace(ev.Name))
		if name == "" {
			name = "TOOL"
		}
		arg := strings.TrimSpace(ev.Args)
		if arg != "" {
			parts = append(parts, name+" "+arg)
		} else {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	budget := maxInt(16, width-2)
	return truncateTailDisplay(strings.Join(parts, " · "), budget)
}

func explorationGroupClickToken(events []SubagentEvent) string {
	ids := make([]string, 0, len(events))
	for _, ev := range events {
		if callID := strings.TrimSpace(ev.CallID); callID != "" {
			ids = append(ids, callID)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	return "acp_exploration_group:" + strings.Join(ids, ",")
}

func explorationToolClass(name string) string {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "RG", "SEARCH", "FIND":
		return "search"
	default:
		return "file"
	}
}

func pluralizeUnit(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	switch unit {
	case "entry":
		return strconv.Itoa(n) + " entries"
	case "match":
		return strconv.Itoa(n) + " matches"
	case "search":
		return strconv.Itoa(n) + " searches"
	}
	return strconv.Itoa(n) + " " + unit + "s"
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

func renderACPToolLifecycleRows(blockID string, events []SubagentEvent, idx int, width int, ctx BlockRenderContext, opts acpTranscriptRenderOptions) ([]RenderedRow, int) {
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
			return renderACPStandaloneFinalToolRows(blockID, final, width, ctx, opts), end
		}
		if shouldRenderToolEvent(ev) {
			return renderParticipantTurnToolRows(blockID, ev, width, ctx), end
		}
		return nil, end
	}

	start.Args = compactACPToolInline(start.Args, width)
	panelExpanded := true
	if opts.ToolPanelExpanded != nil {
		panelExpanded = opts.ToolPanelExpanded(start.CallID)
	}
	rows := renderParticipantTurnToolRows(blockID, start, width, ctx)
	if opts.ToolOutputPanels && !isSpawnLikeTool(start.Name) && !isSpawnLikeTool(final.Name) {
		if hasFinal && shouldDefaultCollapseToolPanel(final.Name) && !panelExpanded {
			return renderParticipantTurnToolRows(blockID, final, width, ctx), end
		}
		rows = renderACPToolHeaderRows(blockID, start, width, ctx, panelExpanded)
		if !panelExpanded {
			return rows, end
		}
		panelText := strings.TrimSpace(preview)
		panelErr := false
		if hasFinal {
			panelText = strings.TrimSpace(final.Output)
			panelErr = final.Err
			if panelText == "" && !panelErr {
				panelText = "completed"
			}
		}
		if shouldRenderACPToolPanel(panelText, panelErr) {
			rows = append(rows, renderACPToolPanelRows(blockID, finalPanelToolName(start, final, hasFinal), panelText, width, ctx, panelErr)...)
		}
		return rows, end
	}
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

func renderACPStandaloneFinalToolRows(blockID string, ev SubagentEvent, width int, ctx BlockRenderContext, opts acpTranscriptRenderOptions) []RenderedRow {
	output := strings.TrimSpace(ev.Output)
	if opts.ToolOutputPanels && !isSpawnLikeTool(ev.Name) && shouldRenderACPToolPanel(output, ev.Err) {
		panelExpanded := true
		if opts.ToolPanelExpanded != nil {
			panelExpanded = opts.ToolPanelExpanded(ev.CallID)
		}
		if shouldDefaultCollapseToolPanel(ev.Name) && !panelExpanded {
			return renderParticipantTurnToolRows(blockID, ev, width, ctx)
		}
		rows := renderACPToolHeaderRows(blockID, ev, width, ctx, panelExpanded)
		if !panelExpanded {
			return rows
		}
		rows = append(rows, renderACPToolPanelRows(blockID, ev.Name, output, width, ctx, ev.Err)...)
		return rows
	}
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

func shouldRenderACPToolPanel(text string, err bool) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return err
	}
	if !err && strings.EqualFold(text, "completed") {
		return false
	}
	return true
}

func finalPanelToolName(start SubagentEvent, final SubagentEvent, hasFinal bool) string {
	if hasFinal && strings.TrimSpace(final.Name) != "" {
		return final.Name
	}
	return start.Name
}

func renderACPToolPanelRows(blockID string, toolName string, text string, width int, ctx BlockRenderContext, err bool) []RenderedRow {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if isDiffPanelText(text) && !err {
		return renderACPDiffPanelRows(blockID, text, width, ctx)
	}
	if isTerminalPanelTool(toolName) {
		return renderACPTerminalPanelRows(blockID, toolName, text, width, ctx, err)
	}
	boxWidth := maxInt(20, width)
	bodyWidth := maxInt(1, boxWidth-6)
	body := renderACPToolPanelBody(text, bodyWidth, ctx, err)
	if len(body) == 0 {
		return nil
	}
	lines := renderPanelViewModel(ctx.Theme, PanelViewModel{
		Variant: tuikit.PanelShellVariantDrawer,
		Width:   boxWidth,
		Body:    body,
	})
	rows := make([]RenderedRow, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, StyledPlainRow(blockID, ansi.Strip(line), line))
	}
	return rows
}

func isTerminalPanelTool(name string) bool {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "BASH", "SPAWN":
		return true
	default:
		return false
	}
}

func renderACPTerminalPanelRows(blockID string, toolName string, text string, width int, ctx BlockRenderContext, err bool) []RenderedRow {
	lines := renderACPTerminalPanelBody(text, maxInt(1, width-2), ctx, err)
	rows := make([]RenderedRow, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, StyledPlainRow(blockID, ansi.Strip(line), line))
	}
	return rows
}

func renderACPTerminalPanelBody(text string, width int, ctx BlockRenderContext, err bool) []string {
	style := ctx.Theme.SecondaryTextStyle().Background(ctx.Theme.ToolOutputBg)
	if err {
		style = ctx.Theme.ErrorStyle().Background(ctx.Theme.ToolOutputBg)
	}
	lines := make([]string, 0, 8)
	for _, raw := range strings.Split(text, "\n") {
		prefix := "  "
		bodyWidth := maxInt(1, width-displayColumns(prefix))
		if raw == "" {
			lines = append(lines, style.Width(width).Render(prefix))
			continue
		}
		wrapped := strings.Split(hardWrapDisplayLine(raw, bodyWidth), "\n")
		for i, segment := range wrapped {
			linePrefix := prefix
			if i > 0 {
				linePrefix = strings.Repeat(" ", displayColumns(prefix))
			}
			lines = append(lines, style.Width(width).Render(linePrefix+tuikit.LinkifyText(segment, ctx.Theme.LinkStyle())))
		}
	}
	return lines
}

func isDiffPanelText(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "diff / hunk") {
			return true
		}
	}
	return false
}

func renderACPDiffPanelRows(blockID string, text string, width int, ctx BlockRenderContext) []RenderedRow {
	bodyWidth := maxInt(1, width-2)
	lines := renderACPToolPanelBody(text, bodyWidth, ctx, false)
	rows := make([]RenderedRow, 0, len(lines))
	for _, line := range lines {
		rows = append(rows, StyledPlainRow(blockID, ansi.Strip(line), line))
	}
	return rows
}

func renderACPToolHeaderRows(blockID string, ev SubagentEvent, width int, ctx BlockRenderContext, expanded bool) []RenderedRow {
	vm := buildToolEventViewModel(ev)
	vm.Done = false
	vm.Err = false
	vm.Output = ""
	vm.Expandable = true
	vm.Expanded = expanded
	vm.ClickToken = acpToolPanelClickToken(ev.CallID)
	return renderToolEventViewModelLines(blockID, vm, width, ctx.Theme)
}

func acpToolPanelClickToken(callID string) string {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return ""
	}
	return "acp_tool_panel:" + callID
}

func renderACPToolPanelBody(text string, width int, ctx BlockRenderContext, err bool) []string {
	prefix := "  "
	lines := make([]string, 0, 8)
	for _, raw := range strings.Split(text, "\n") {
		style := toolPanelLineStyle(raw, ctx, err)
		linePrefix := prefix
		if err {
			linePrefix = "! "
		}
		if raw == "" {
			lines = append(lines, style.Width(width).Render(linePrefix))
			continue
		}
		wrapped := strings.Split(hardWrapDisplayLine(raw, maxInt(1, width-displayColumns(linePrefix))), "\n")
		for i, segment := range wrapped {
			if i > 0 {
				linePrefix = strings.Repeat(" ", displayColumns(linePrefix))
			}
			styled := style.Width(width).Render(linePrefix + tuikit.LinkifyText(segment, ctx.Theme.LinkStyle()))
			lines = append(lines, styled)
		}
	}
	return lines
}

func toolPanelLineStyle(raw string, ctx BlockRenderContext, err bool) lipgloss.Style {
	if err {
		return ctx.Theme.ErrorStyle()
	}
	trimmed := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(trimmed, "+++"), strings.HasPrefix(trimmed, "---"):
		return ctx.Theme.DiffHeaderStyle()
	case strings.HasPrefix(trimmed, "@@"):
		return ctx.Theme.DiffHunkStyle()
	case strings.HasPrefix(trimmed, "+"):
		return ctx.Theme.DiffAddStyle().Background(ctx.Theme.DiffAddBg)
	case strings.HasPrefix(trimmed, "-"):
		return ctx.Theme.DiffRemoveStyle().Background(ctx.Theme.DiffRemoveBg)
	case strings.EqualFold(trimmed, "diff / hunk"):
		return ctx.Theme.TranscriptMetaStyle()
	default:
		return ctx.Theme.SecondaryTextStyle()
	}
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
