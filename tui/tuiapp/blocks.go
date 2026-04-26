package tuiapp

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/OnslaughtSnail/caelis/tui/tuikit"

	"github.com/charmbracelet/x/ansi"
)

// ---------------------------------------------------------------------------
// TranscriptBlock — a single committed log/system/user line.
// ---------------------------------------------------------------------------

type TranscriptBlock struct {
	id        string
	Raw       string
	Style     tuikit.LineStyle
	PreStyled bool // if true, Raw already contains ANSI styling
}

func NewTranscriptBlock(raw string, style tuikit.LineStyle) *TranscriptBlock {
	return &TranscriptBlock{id: nextBlockID(), Raw: raw, Style: style}
}

func (b *TranscriptBlock) BlockID() string { return b.id }
func (b *TranscriptBlock) Kind() BlockKind { return BlockTranscript }
func (b *TranscriptBlock) Render(ctx BlockRenderContext) []RenderedRow {
	if b.PreStyled {
		return []RenderedRow{StyledRow(b.id, b.Raw)}
	}
	colored := tuikit.ColorizeLogLine(b.Raw, b.Style, ctx.Theme)
	gutter := tuikit.LineExtraGutter(b.Style)
	styled := gutter + colored
	return []RenderedRow{StyledRow(b.id, styled)}
}

// ---------------------------------------------------------------------------
// SpacerBlock — an empty line for visual separation. Reuses BlockTranscript.
// ---------------------------------------------------------------------------

func NewSpacerBlock() *TranscriptBlock {
	return &TranscriptBlock{id: nextBlockID(), Raw: "", Style: tuikit.LineStyleDefault}
}

// ---------------------------------------------------------------------------
// UserNarrativeBlock — finalized user message rendered through glamour.
// ---------------------------------------------------------------------------

type UserNarrativeBlock struct {
	id          string
	Raw         string // user's display text (without the "> " prefix)
	renderCache narrativeBlockRenderCache
}

func NewUserNarrativeBlock(text string) *UserNarrativeBlock {
	return &UserNarrativeBlock{id: nextBlockID(), Raw: strings.TrimSpace(text)}
}

func (b *UserNarrativeBlock) BlockID() string { return b.id }
func (b *UserNarrativeBlock) Kind() BlockKind { return BlockTranscript }
func (b *UserNarrativeBlock) Render(ctx BlockRenderContext) []RenderedRow {
	return b.renderCache.renderNarrativeRows(b.id, b.Raw, "> ", tuikit.LineStyleUser, ctx, false)
}

type narrativeBlockRenderCache struct {
	width      int
	themeKey   string
	raw        string
	rolePrefix string
	streaming  bool
	rows       []RenderedRow
}

func (c *narrativeBlockRenderCache) renderNarrativeRows(blockID, raw, rolePrefix string, lineStyle tuikit.LineStyle, ctx BlockRenderContext, streaming bool) []RenderedRow {
	if cached := c.cachedRows(raw, rolePrefix, ctx.Width, ctx.Theme, streaming); cached != nil {
		return cached
	}
	rows := renderNarrativeRows(blockID, raw, rolePrefix, lineStyle, ctx.Width, ctx.Theme, streaming)
	c.width = ctx.Width
	c.themeKey = themeRenderCacheKey(ctx.Theme)
	c.raw = raw
	c.rolePrefix = rolePrefix
	c.streaming = streaming
	c.rows = rows
	return rows
}

func (c *narrativeBlockRenderCache) cachedRows(raw, rolePrefix string, width int, theme tuikit.Theme, streaming bool) []RenderedRow {
	if c == nil || len(c.rows) == 0 {
		return nil
	}
	if c.width != width || c.themeKey != themeRenderCacheKey(theme) {
		return nil
	}
	if c.raw != raw || c.rolePrefix != rolePrefix || c.streaming != streaming {
		return nil
	}
	return c.rows
}

// ---------------------------------------------------------------------------
// AssistantBlock — streaming or finalized assistant answer.
// ---------------------------------------------------------------------------

type AssistantBlock struct {
	id          string
	Actor       string
	Raw         string
	Streaming   bool
	LastFinal   string // dedup for duplicate final events
	renderCache narrativeBlockRenderCache
}

func NewAssistantBlock(actor ...string) *AssistantBlock {
	label := ""
	if len(actor) > 0 {
		label = strings.TrimSpace(actor[0])
	}
	return &AssistantBlock{id: nextBlockID(), Actor: label, Streaming: true}
}

func (b *AssistantBlock) BlockID() string { return b.id }
func (b *AssistantBlock) Kind() BlockKind { return BlockAssistant }
func (b *AssistantBlock) Render(ctx BlockRenderContext) []RenderedRow {
	return b.renderCache.renderNarrativeRows(
		b.id,
		b.Raw,
		"* "+assistantActorPrefix(b.Actor),
		tuikit.LineStyleAssistant,
		ctx,
		b.Streaming,
	)
}

func assistantActorPrefix(actor string) string {
	if actor = strings.TrimSpace(actor); actor != "" && !strings.EqualFold(actor, "assistant") {
		return actor + ": "
	}
	return ""
}

// ---------------------------------------------------------------------------
// ReasoningBlock — streaming or finalized reasoning/thinking.
// ---------------------------------------------------------------------------

type ReasoningBlock struct {
	id          string
	Actor       string
	Raw         string
	Streaming   bool
	renderCache narrativeBlockRenderCache
}

func NewReasoningBlock(actor ...string) *ReasoningBlock {
	label := ""
	if len(actor) > 0 {
		label = strings.TrimSpace(actor[0])
	}
	return &ReasoningBlock{id: nextBlockID(), Actor: label, Streaming: true}
}

func (b *ReasoningBlock) BlockID() string { return b.id }
func (b *ReasoningBlock) Kind() BlockKind { return BlockReasoning }
func (b *ReasoningBlock) Render(ctx BlockRenderContext) []RenderedRow {
	prefix := "· "
	if actor := strings.TrimSpace(b.Actor); actor != "" && !strings.EqualFold(actor, "assistant") {
		prefix += actor + ": "
	}
	return b.renderCache.renderNarrativeRows(b.id, b.Raw, prefix, tuikit.LineStyleReasoning, ctx, b.Streaming)
}

// renderNarrativeFallbackRows preserves multi-line structure when glamour
// cannot produce usable output. This is intentionally minimal and should only
// be exercised for empty or degenerate markdown.
func renderNarrativeFallbackRows(blockID, raw, rolePrefix, continuationPrefix string, lineStyle tuikit.LineStyle, theme tuikit.Theme) []RenderedRow {
	normalized := strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	if strings.TrimSpace(normalized) == "" {
		styled := tuikit.ColorizeLogLine(rolePrefix, lineStyle, theme)
		return []RenderedRow{StyledPlainRow(blockID, rolePrefix, styled)}
	}
	normalized = strings.TrimRight(normalized, "\n")
	lines := strings.Split(normalized, "\n")
	rows := make([]RenderedRow, 0, len(lines))
	for i, line := range lines {
		prefix := continuationPrefix
		if i == 0 {
			prefix = rolePrefix
		}
		plain := prefix + line
		styled := tuikit.ColorizeLogLine(plain, lineStyle, theme)
		rows = append(rows, StyledPlainRow(blockID, plain, styled))
	}
	return rows
}

func renderNarrativeRows(blockID, raw, rolePrefix string, lineStyle tuikit.LineStyle, width int, theme tuikit.Theme, streaming bool) []RenderedRow {
	if rows := renderNarrativeGlamourRows(blockID, raw, rolePrefix, lineStyle, width, theme, streaming); len(rows) > 0 {
		return rows
	}
	_, continuationPrefix := narrativeLinePrefixes(lineStyle)
	return renderNarrativeFallbackRows(blockID, raw, rolePrefix, continuationPrefix, lineStyle, theme)
}

func renderNarrativeGlamourRows(blockID, raw, rolePrefix string, lineStyle tuikit.LineStyle, width int, theme tuikit.Theme, streaming bool) []RenderedRow {
	if streaming {
		return glamourStreamingNarrativeRows(blockID, raw, rolePrefix, lineStyle, width, theme)
	}
	return glamourNarrativeRows(blockID, raw, rolePrefix, lineStyle, width, theme)
}

// ---------------------------------------------------------------------------
// MainACPTurnBlock — root ACP-controlled turn in the main transcript.
// ---------------------------------------------------------------------------

type MainACPTurnBlock struct {
	id              string
	SessionID       string
	Status          string
	StartedAt       time.Time
	EndedAt         time.Time
	Events          []SubagentEvent
	ExpandedTools   map[string]bool
	ToolPanelScroll map[string]toolPanelScrollState
}

type ToolUpdateMeta struct {
	TaskID string
}

func NewMainACPTurnBlock(sessionID string) *MainACPTurnBlock {
	return &MainACPTurnBlock{
		id:        nextBlockID(),
		SessionID: strings.TrimSpace(sessionID),
		Status:    "running",
		StartedAt: time.Now(),
	}
}

func (b *MainACPTurnBlock) BlockID() string { return b.id }
func (b *MainACPTurnBlock) Kind() BlockKind { return BlockMainACPTurn }

func (b *MainACPTurnBlock) AppendStreamChunk(kind SubagentEventKind, chunk string) {
	if b == nil {
		return
	}
	if idx := latestNarrativeAppendTargetIndex(b.Events, kind); idx >= 0 {
		b.Events[idx].Text = collapseRepeatedNarrativeText(appendDeltaStreamChunk(b.Events[idx].Text, chunk))
		return
	}
	b.Events = append(b.Events, SubagentEvent{Kind: kind, Text: collapseRepeatedNarrativeText(chunk)})
}

func (b *MainACPTurnBlock) ReplaceFinalStreamChunk(kind SubagentEventKind, chunk string) {
	if b == nil {
		return
	}
	if strings.TrimSpace(chunk) == "" {
		return
	}
	if idx := latestNarrativeFinalTargetIndex(b.Events, kind); idx >= 0 {
		b.Events[idx].Text = collapseRepeatedNarrativeText(chunk)
		return
	}
	b.Events = append(b.Events, SubagentEvent{Kind: kind, Text: collapseRepeatedNarrativeText(chunk)})
}

func (b *MainACPTurnBlock) UpdateTool(callID, name, args, output string, final bool, err bool) {
	b.UpdateToolWithMeta(callID, name, args, output, final, err, ToolUpdateMeta{})
}

func (b *MainACPTurnBlock) UpdateToolWithMeta(callID, name, args, output string, final bool, err bool, meta ToolUpdateMeta) {
	if b == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	name = strings.TrimSpace(name)
	args = strings.TrimSpace(args)
	if !isTerminalPanelTool(name) || final {
		output = strings.TrimSpace(output)
	}
	taskID := strings.TrimSpace(meta.TaskID)
	if updateLinkedTerminalEvent(b.Events, name, taskID, output) {
		output = ""
	}
	if !final {
		for i := len(b.Events) - 1; i >= 0; i-- {
			ev := &b.Events[i]
			if ev.Kind != SEToolCall || strings.TrimSpace(ev.CallID) != callID || ev.Done {
				continue
			}
			if strings.TrimSpace(ev.Name) == "" {
				ev.Name = name
			}
			if strings.TrimSpace(ev.Args) == "" {
				ev.Args = args
			}
			if ev.TaskID == "" {
				ev.TaskID = taskID
			}
			if text := output; text != "" {
				ev.Output = mergeSubagentStreamChunk(ev.Output, text)
			}
			return
		}
		b.Events = append(b.Events, SubagentEvent{
			Kind:   SEToolCall,
			CallID: callID,
			Name:   name,
			Args:   args,
			Output: output,
			TaskID: taskID,
		})
		return
	}
	finalEvent := SubagentEvent{
		Kind:   SEToolCall,
		CallID: callID,
		Name:   name,
		Args:   args,
		Output: output,
		Done:   true,
		Err:    err,
		TaskID: taskID,
	}
	for i := len(b.Events) - 1; i >= 0; i-- {
		ev := &b.Events[i]
		if ev.Kind != SEToolCall || strings.TrimSpace(ev.CallID) != callID {
			continue
		}
		if !ev.Done {
			if strings.TrimSpace(finalEvent.Name) == "" {
				finalEvent.Name = strings.TrimSpace(ev.Name)
			}
			if strings.TrimSpace(finalEvent.Args) == "" {
				finalEvent.Args = strings.TrimSpace(ev.Args)
			}
			ev.Name = finalEvent.Name
			ev.Args = finalEvent.Args
			ev.Output = finalEvent.Output
			ev.Done = true
			ev.Err = finalEvent.Err
			if ev.TaskID == "" {
				ev.TaskID = finalEvent.TaskID
			}
			if shouldDefaultCollapseToolPanel(finalEvent.Name) {
				b.setToolPanelExpanded(callID, false)
			}
			return
		}
		if strings.TrimSpace(finalEvent.Name) == "" {
			finalEvent.Name = strings.TrimSpace(ev.Name)
		}
		if strings.TrimSpace(finalEvent.Args) == "" {
			finalEvent.Args = strings.TrimSpace(ev.Args)
		}
		break
	}
	b.Events = append(b.Events, finalEvent)
	if shouldDefaultCollapseToolPanel(finalEvent.Name) {
		b.setToolPanelExpanded(callID, false)
	}
}

func (b *MainACPTurnBlock) UpdatePlan(entries []planEntryState) {
	if b == nil {
		return
	}
	if n := len(b.Events); n > 0 && b.Events[n-1].Kind == SEPlan {
		b.Events[n-1].PlanEntries = entries
		return
	}
	b.Events = append(b.Events, SubagentEvent{
		Kind:        SEPlan,
		PlanEntries: entries,
	})
}

func (b *MainACPTurnBlock) SetStatus(state string, approvalTool string, approvalCommand string, occurredAt time.Time) {
	if b == nil {
		return
	}
	b.Status = strings.ToLower(strings.TrimSpace(state))
	collapseTools := false
	switch b.Status {
	case "completed", "failed", "interrupted", "cancelled", "canceled", "terminated":
		if b.EndedAt.IsZero() {
			collapseTools = true
			if !occurredAt.IsZero() {
				b.EndedAt = occurredAt
			} else {
				b.EndedAt = time.Now()
			}
		}
	default:
		b.EndedAt = time.Time{}
	}
	if collapseTools {
		b.collapseAllToolPanels()
	}
	if !strings.EqualFold(b.Status, "waiting_approval") {
		return
	}
	if n := len(b.Events); n > 0 && b.Events[n-1].Kind == SEApproval {
		b.Events[n-1].ApprovalTool = strings.TrimSpace(approvalTool)
		b.Events[n-1].ApprovalCommand = strings.TrimSpace(approvalCommand)
		return
	}
	b.Events = append(b.Events, SubagentEvent{
		Kind:            SEApproval,
		ApprovalTool:    strings.TrimSpace(approvalTool),
		ApprovalCommand: strings.TrimSpace(approvalCommand),
	})
}

func (b *MainACPTurnBlock) Render(ctx BlockRenderContext) []RenderedRow {
	if b == nil {
		return nil
	}
	return renderACPTranscriptRows(b.id, b.Events, b.Status, maxInt(8, ctx.Width), ctx, acpTranscriptRenderOptions{
		UseStatusPlaceholder:   true,
		PlaceholderAsMeta:      true,
		HideWaitingApprovalRow: true,
		HideCompletedRow:       true,
		ToolOutputPanels:       true,
		ToolPanelExpanded:      b.toolPanelExpanded,
		ToolPanelScrollState:   b.toolPanelScrollState,
	})
}

// ---------------------------------------------------------------------------
// ParticipantTurnBlock — inline external-agent turn inside the main transcript.
// ---------------------------------------------------------------------------

type ParticipantTurnBlock struct {
	id              string
	SessionID       string
	Actor           string
	Status          string
	Expanded        bool
	StartedAt       time.Time
	EndedAt         time.Time
	Events          []SubagentEvent
	ExpandedTools   map[string]bool
	ToolPanelScroll map[string]toolPanelScrollState
}

func NewParticipantTurnBlock(sessionID, actor string) *ParticipantTurnBlock {
	return &ParticipantTurnBlock{
		id:        nextBlockID(),
		SessionID: strings.TrimSpace(sessionID),
		Actor:     strings.TrimSpace(actor),
		Status:    "running",
		Expanded:  true,
		StartedAt: time.Now(),
	}
}

func (b *ParticipantTurnBlock) BlockID() string { return b.id }
func (b *ParticipantTurnBlock) Kind() BlockKind { return BlockParticipantTurn }

func (b *ParticipantTurnBlock) AppendStreamChunk(kind SubagentEventKind, chunk string) {
	if b == nil {
		return
	}
	if idx := latestNarrativeAppendTargetIndex(b.Events, kind); idx >= 0 {
		b.Events[idx].Text = collapseRepeatedNarrativeText(appendDeltaStreamChunk(b.Events[idx].Text, chunk))
		return
	}
	b.Events = append(b.Events, SubagentEvent{Kind: kind, Text: collapseRepeatedNarrativeText(chunk)})
}

func (b *ParticipantTurnBlock) ReplaceFinalStreamChunk(kind SubagentEventKind, chunk string) {
	if b == nil {
		return
	}
	if strings.TrimSpace(chunk) == "" {
		return
	}
	if idx := latestNarrativeFinalTargetIndex(b.Events, kind); idx >= 0 {
		b.Events[idx].Text = collapseRepeatedNarrativeText(chunk)
		return
	}
	b.Events = append(b.Events, SubagentEvent{Kind: kind, Text: collapseRepeatedNarrativeText(chunk)})
}

func (b *ParticipantTurnBlock) UpdateTool(callID, name, args, output string, final bool, err bool) {
	b.UpdateToolWithMeta(callID, name, args, output, final, err, ToolUpdateMeta{})
}

func (b *ParticipantTurnBlock) UpdateToolWithMeta(callID, name, args, output string, final bool, err bool, meta ToolUpdateMeta) {
	if b == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	name = strings.TrimSpace(name)
	args = strings.TrimSpace(args)
	if !isTerminalPanelTool(name) || final {
		output = strings.TrimSpace(output)
	}
	taskID := strings.TrimSpace(meta.TaskID)
	if updateLinkedTerminalEvent(b.Events, name, taskID, output) {
		output = ""
	}
	if !final {
		for i := len(b.Events) - 1; i >= 0; i-- {
			ev := &b.Events[i]
			if ev.Kind != SEToolCall || strings.TrimSpace(ev.CallID) != callID || ev.Done {
				continue
			}
			if strings.TrimSpace(ev.Name) == "" {
				ev.Name = name
			}
			if strings.TrimSpace(ev.Args) == "" {
				ev.Args = args
			}
			if ev.TaskID == "" {
				ev.TaskID = taskID
			}
			if text := output; text != "" {
				ev.Output = mergeSubagentStreamChunk(ev.Output, text)
			}
			return
		}
		b.Events = append(b.Events, SubagentEvent{
			Kind:   SEToolCall,
			CallID: callID,
			Name:   name,
			Args:   args,
			Output: output,
			TaskID: taskID,
		})
		return
	}
	finalEvent := SubagentEvent{
		Kind:   SEToolCall,
		CallID: callID,
		Name:   name,
		Args:   args,
		Output: output,
		Done:   true,
		Err:    err,
		TaskID: taskID,
	}
	for i := len(b.Events) - 1; i >= 0; i-- {
		ev := &b.Events[i]
		if ev.Kind != SEToolCall || strings.TrimSpace(ev.CallID) != callID {
			continue
		}
		if !ev.Done {
			if strings.TrimSpace(finalEvent.Name) == "" {
				finalEvent.Name = strings.TrimSpace(ev.Name)
			}
			if strings.TrimSpace(finalEvent.Args) == "" {
				finalEvent.Args = strings.TrimSpace(ev.Args)
			}
			ev.Name = finalEvent.Name
			ev.Args = finalEvent.Args
			ev.Output = finalEvent.Output
			ev.Done = true
			ev.Err = finalEvent.Err
			if ev.TaskID == "" {
				ev.TaskID = finalEvent.TaskID
			}
			if shouldDefaultCollapseToolPanel(finalEvent.Name) {
				b.setToolPanelExpanded(callID, false)
			}
			return
		}
		if strings.TrimSpace(finalEvent.Name) == "" {
			finalEvent.Name = strings.TrimSpace(ev.Name)
		}
		if strings.TrimSpace(finalEvent.Args) == "" {
			finalEvent.Args = strings.TrimSpace(ev.Args)
		}
		break
	}
	b.Events = append(b.Events, finalEvent)
	if shouldDefaultCollapseToolPanel(finalEvent.Name) {
		b.setToolPanelExpanded(callID, false)
	}
}

func (b *ParticipantTurnBlock) UpdatePlan(entries []planEntryState) {
	if b == nil {
		return
	}
	if n := len(b.Events); n > 0 && b.Events[n-1].Kind == SEPlan {
		b.Events[n-1].PlanEntries = entries
		return
	}
	b.Events = append(b.Events, SubagentEvent{
		Kind:        SEPlan,
		PlanEntries: entries,
	})
}

func (b *ParticipantTurnBlock) SetStatus(state string, approvalTool string, approvalCommand string, occurredAt time.Time) {
	if b == nil {
		return
	}
	b.Status = strings.ToLower(strings.TrimSpace(state))
	collapseTools := false
	switch b.Status {
	case "completed", "failed", "interrupted", "cancelled", "canceled", "terminated":
		if b.EndedAt.IsZero() {
			collapseTools = true
			if !occurredAt.IsZero() {
				b.EndedAt = occurredAt
			} else {
				b.EndedAt = time.Now()
			}
		}
	default:
		b.EndedAt = time.Time{}
	}
	if collapseTools {
		b.collapseAllToolPanels()
	}
	if !strings.EqualFold(b.Status, "waiting_approval") {
		return
	}
	if n := len(b.Events); n > 0 && b.Events[n-1].Kind == SEApproval {
		b.Events[n-1].ApprovalTool = strings.TrimSpace(approvalTool)
		b.Events[n-1].ApprovalCommand = strings.TrimSpace(approvalCommand)
		return
	}
	b.Events = append(b.Events, SubagentEvent{
		Kind:            SEApproval,
		ApprovalTool:    strings.TrimSpace(approvalTool),
		ApprovalCommand: strings.TrimSpace(approvalCommand),
	})
}

func (b *ParticipantTurnBlock) Render(ctx BlockRenderContext) []RenderedRow {
	if b == nil {
		return nil
	}
	rows := []RenderedRow{StyledRow(b.id, renderParticipantTurnHeader(b, ctx))}
	if !b.Expanded {
		return rows
	}
	rows = append(rows, renderACPTranscriptRows(b.id, b.Events, b.Status, maxInt(8, ctx.Width), ctx, acpTranscriptRenderOptions{
		UseStatusPlaceholder:   true,
		PlaceholderAsMeta:      true,
		HideWaitingApprovalRow: true,
		HideCompletedRow:       true,
		ToolOutputPanels:       true,
		ToolPanelExpanded:      b.toolPanelExpanded,
		ToolPanelScrollState:   b.toolPanelScrollState,
	})...)
	if b.Expanded && participantTurnIsTerminal(b.Status) {
		rows = append(rows, StyledRow(b.id, renderParticipantTurnFooter(b, ctx)))
	}
	return rows
}

func renderParticipantTurnHeader(b *ParticipantTurnBlock, ctx BlockRenderContext) string {
	if b == nil {
		return ""
	}
	icon := "▾"
	if !b.Expanded {
		icon = "▸"
	}
	iconText := ctx.Theme.PromptStyle().Bold(true).Render(icon)
	actor := renderParticipantActorLabel(ctx.Theme, b.Actor)
	left := iconText + " " + actor
	switch strings.ToLower(strings.TrimSpace(b.Status)) {
	case "waiting_approval":
		left = ctx.Theme.WarnStyle().Bold(true).Render(icon) + " " + actor
	case "failed":
		left = ctx.Theme.ErrorStyle().Bold(true).Render(icon) + " " + actor
	case "interrupted":
		left = ctx.Theme.WarnStyle().Bold(true).Render(icon) + " " + actor
	}
	metaParts := make([]string, 0, 1)
	if label := participantTurnStatusLabel(b.Status); label != "" {
		metaParts = append(metaParts, label)
	}
	if len(metaParts) == 0 {
		return left
	}
	return left + " " + ctx.Theme.TranscriptMetaStyle().Render("· "+strings.Join(metaParts, " · "))
}

func toolPanelExpanded(state map[string]bool, callID string) bool {
	callID = strings.TrimSpace(callID)
	if callID == "" || state == nil {
		return true
	}
	expanded, ok := state[callID]
	if !ok {
		return true
	}
	return expanded
}

func toggleToolPanelExpanded(state *map[string]bool, callID string) bool {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return false
	}
	if *state == nil {
		*state = map[string]bool{}
	}
	next := !toolPanelExpanded(*state, callID)
	(*state)[callID] = next
	return next
}

type toolPanelScrollState struct {
	Offset                int
	FollowTail            bool
	ScrollbarVisibleUntil time.Time
}

func defaultToolPanelScrollState() toolPanelScrollState {
	return toolPanelScrollState{FollowTail: true}
}

func toolPanelScrollStateFromMap(state map[string]toolPanelScrollState, callID string) toolPanelScrollState {
	callID = strings.TrimSpace(callID)
	if callID == "" || state == nil {
		return defaultToolPanelScrollState()
	}
	value, ok := state[callID]
	if !ok {
		return defaultToolPanelScrollState()
	}
	return value
}

func scrollToolPanelState(state *map[string]toolPanelScrollState, callID string, total int, delta int) bool {
	callID = strings.TrimSpace(callID)
	if state == nil || callID == "" {
		return false
	}
	value := defaultToolPanelScrollState()
	if *state != nil {
		value = toolPanelScrollStateFromMap(*state, callID)
	}
	if !scrollPanelState(&value.Offset, &value.FollowTail, total, acpTerminalPanelMaxLines, delta) {
		return false
	}
	value.ScrollbarVisibleUntil = time.Now().Add(scrollbarVisibleDuration)
	if *state == nil {
		*state = map[string]toolPanelScrollState{}
	}
	(*state)[callID] = value
	return true
}

func (b *MainACPTurnBlock) toolPanelExpanded(callID string) bool {
	if b == nil {
		return true
	}
	return toolPanelExpanded(b.ExpandedTools, callID)
}

func (b *MainACPTurnBlock) toggleToolPanelExpanded(callID string) bool {
	if b == nil {
		return false
	}
	return toggleToolPanelExpanded(&b.ExpandedTools, callID)
}

func (b *MainACPTurnBlock) setToolPanelExpanded(callID string, expanded bool) {
	if b == nil || strings.TrimSpace(callID) == "" {
		return
	}
	if b.ExpandedTools == nil {
		b.ExpandedTools = map[string]bool{}
	}
	b.ExpandedTools[strings.TrimSpace(callID)] = expanded
}

func (b *MainACPTurnBlock) toolPanelScrollState(callID string) toolPanelScrollState {
	if b == nil {
		return defaultToolPanelScrollState()
	}
	return toolPanelScrollStateFromMap(b.ToolPanelScroll, callID)
}

func (b *MainACPTurnBlock) ScrollToolPanel(callID string, delta int, ctx BlockRenderContext) bool {
	if b == nil {
		return false
	}
	total := terminalToolPanelLineCount(b.Events, callID, ctx)
	return scrollToolPanelState(&b.ToolPanelScroll, callID, total, delta)
}

func (b *MainACPTurnBlock) CanScrollToolPanel(callID string, delta int, ctx BlockRenderContext) bool {
	if b == nil {
		return false
	}
	state := b.toolPanelScrollState(callID)
	total := terminalToolPanelLineCount(b.Events, callID, ctx)
	return canScrollPanelState(state.Offset, state.FollowTail, total, acpTerminalPanelMaxLines, delta)
}

func (b *MainACPTurnBlock) collapseAllToolPanels() {
	if b == nil {
		return
	}
	b.ExpandedTools = collapseToolPanelsForEvents(b.ExpandedTools, b.Events)
}

func (b *ParticipantTurnBlock) toolPanelExpanded(callID string) bool {
	if b == nil {
		return true
	}
	return toolPanelExpanded(b.ExpandedTools, callID)
}

func (b *ParticipantTurnBlock) toggleToolPanelExpanded(callID string) bool {
	if b == nil {
		return false
	}
	return toggleToolPanelExpanded(&b.ExpandedTools, callID)
}

func (b *ParticipantTurnBlock) setToolPanelExpanded(callID string, expanded bool) {
	if b == nil || strings.TrimSpace(callID) == "" {
		return
	}
	if b.ExpandedTools == nil {
		b.ExpandedTools = map[string]bool{}
	}
	b.ExpandedTools[strings.TrimSpace(callID)] = expanded
}

func (b *ParticipantTurnBlock) toolPanelScrollState(callID string) toolPanelScrollState {
	if b == nil {
		return defaultToolPanelScrollState()
	}
	return toolPanelScrollStateFromMap(b.ToolPanelScroll, callID)
}

func (b *ParticipantTurnBlock) ScrollToolPanel(callID string, delta int, ctx BlockRenderContext) bool {
	if b == nil {
		return false
	}
	total := terminalToolPanelLineCount(b.Events, callID, ctx)
	return scrollToolPanelState(&b.ToolPanelScroll, callID, total, delta)
}

func (b *ParticipantTurnBlock) CanScrollToolPanel(callID string, delta int, ctx BlockRenderContext) bool {
	if b == nil {
		return false
	}
	state := b.toolPanelScrollState(callID)
	total := terminalToolPanelLineCount(b.Events, callID, ctx)
	return canScrollPanelState(state.Offset, state.FollowTail, total, acpTerminalPanelMaxLines, delta)
}

func (b *ParticipantTurnBlock) collapseAllToolPanels() {
	if b == nil {
		return
	}
	b.ExpandedTools = collapseToolPanelsForEvents(b.ExpandedTools, b.Events)
}

func collapseToolPanelsForEvents(state map[string]bool, events []SubagentEvent) map[string]bool {
	callIDs := collectToolPanelCallIDs(events)
	if len(callIDs) == 0 {
		return state
	}
	if state == nil {
		state = map[string]bool{}
	}
	for _, callID := range callIDs {
		state[callID] = false
	}
	return state
}

func collectToolPanelCallIDs(events []SubagentEvent) []string {
	if len(events) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	callIDs := make([]string, 0, len(events))
	for _, ev := range events {
		if ev.Kind != SEToolCall {
			continue
		}
		callID := strings.TrimSpace(ev.CallID)
		if callID == "" {
			continue
		}
		if _, ok := seen[callID]; ok {
			continue
		}
		seen[callID] = struct{}{}
		callIDs = append(callIDs, callID)
	}
	return callIDs
}

func shouldDefaultCollapseToolPanel(name string) bool {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "READ", "RG", "LIST", "GLOB", "SEARCH", "FIND":
		return true
	default:
		return false
	}
}

func participantTurnStatusLabel(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "", "running", "initializing", "prompting", "completed":
		return ""
	case "waiting_approval":
		return "waiting approval"
	case "failed":
		return "failed"
	case "interrupted":
		return "interrupted"
	default:
		return strings.TrimSpace(state)
	}
}

func participantNarrativeEventActive(events []SubagentEvent, idx int, status string) bool {
	return narrativeEventActive(events, idx, participantTurnIsTerminal(status))
}

func renderParticipantTurnNarrativeRows(blockID string, raw string, lineStyle tuikit.LineStyle, width int, ctx BlockRenderContext, active bool) []RenderedRow {
	rolePrefix, _ := narrativeLinePrefixes(lineStyle)
	return renderNarrativeRows(blockID, raw, rolePrefix, lineStyle, width, ctx.Theme, active)
}

func renderParticipantTurnToolRows(blockID string, ev SubagentEvent, width int, ctx BlockRenderContext) []RenderedRow {
	return renderToolEventViewModelLines(blockID, buildToolEventViewModel(ev), width, ctx.Theme)
}

func collapseRepeatedNarrativeText(text string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if strings.TrimSpace(text) == "" {
		return text
	}
	parts := strings.Split(text, "\n\n")
	filteredParts := make([]string, 0, len(parts))
	lastPart := ""
	for _, part := range parts {
		part = collapseAdjacentDuplicateLines(part)
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if trimmed == lastPart && len([]rune(trimmed)) >= 16 {
			continue
		}
		filteredParts = append(filteredParts, part)
		lastPart = trimmed
	}
	if len(filteredParts) == 0 {
		return ""
	}
	return strings.Join(filteredParts, "\n\n")
}

func collapseAdjacentDuplicateLines(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	last := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && trimmed == last && len([]rune(trimmed)) >= 16 {
			continue
		}
		out = append(out, line)
		if trimmed != "" {
			last = trimmed
		}
	}
	return strings.Join(out, "\n")
}

func latestNarrativeAppendTargetIndex(events []SubagentEvent, kind SubagentEventKind) int {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Kind == kind {
			return i
		}
		if narrativeStreamBarrier(ev) {
			return -1
		}
	}
	return -1
}

func latestNarrativeFinalTargetIndex(events []SubagentEvent, kind SubagentEventKind) int {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Kind == kind {
			return i
		}
		if narrativeFinalBarrier(ev) {
			return -1
		}
	}
	return -1
}

func narrativeStreamBarrier(ev SubagentEvent) bool {
	switch ev.Kind {
	case SEApproval, SEAssistant, SEReasoning:
		return false
	default:
		return true
	}
}

func narrativeFinalBarrier(ev SubagentEvent) bool {
	switch ev.Kind {
	case SEApproval, SEAssistant, SEReasoning:
		return false
	default:
		return true
	}
}

func participantTurnIsTerminal(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "completed", "failed", "interrupted", "cancelled", "canceled", "terminated":
		return true
	default:
		return false
	}
}

func renderParticipantTurnFooter(b *ParticipantTurnBlock, ctx BlockRenderContext) string {
	label := ""
	if b != nil && !b.StartedAt.IsZero() && !b.EndedAt.IsZero() && !b.EndedAt.Before(b.StartedAt) {
		label = formatTurnDuration(b.EndedAt.Sub(b.StartedAt))
	}
	width := maxInt(12, ctx.Width)
	return ctx.Theme.HelpHintTextStyle().Render(centeredDivider(width, label))
}

// ---------------------------------------------------------------------------
// DividerBlock — turn separator.
// ---------------------------------------------------------------------------

type DividerBlock struct {
	id    string
	Label string
	Text  string // legacy pre-rendered divider text
}

func NewDividerBlock(label string) *DividerBlock {
	return &DividerBlock{id: nextBlockID(), Label: strings.TrimSpace(label)}
}

func (b *DividerBlock) BlockID() string { return b.id }
func (b *DividerBlock) Kind() BlockKind { return BlockDivider }
func (b *DividerBlock) Render(ctx BlockRenderContext) []RenderedRow {
	label := strings.TrimSpace(b.Label)
	if label == "" && strings.TrimSpace(b.Text) != "" {
		label = strings.TrimSpace(ansi.Strip(b.Text))
	}
	plain := centeredDivider(maxInt(12, ctx.Width), label)
	styled := ctx.Theme.HelpHintTextStyle().Render(plain)
	return []RenderedRow{{
		Styled:     styled,
		Plain:      plain,
		BlockID:    b.id,
		PreWrapped: true,
	}}
}

func renderParticipantActorLabel(theme tuikit.Theme, actor string) string {
	name, provider := splitParticipantActor(actor)
	nameStyle := theme.TextStyle().Bold(true)
	if provider == "" {
		return nameStyle.Render(name)
	}
	return nameStyle.Render(name) +
		" " + theme.TranscriptMetaStyle().Render(fmt.Sprintf("[%s]", provider))
}

func narrativeLinePrefixes(lineStyle tuikit.LineStyle) (string, string) {
	switch lineStyle {
	case tuikit.LineStyleAssistant:
		return "* ", "  "
	case tuikit.LineStyleReasoning:
		return "· ", "  "
	default:
		return "", ""
	}
}

func shouldRenderToolEvent(ev SubagentEvent) bool {
	if ev.Kind != SEToolCall {
		return true
	}
	if !ev.Done || ev.Err {
		return true
	}
	output := strings.TrimSpace(ev.Output)
	if output == "" || strings.EqualFold(output, "completed") {
		return false
	}
	return true
}

func visibleNarrativeEvents(events []SubagentEvent, status string) []SubagentEvent {
	if len(events) == 0 {
		return nil
	}
	hidePlan := strings.EqualFold(strings.TrimSpace(status), "waiting_approval") && hasApprovalEvent(events)
	out := make([]SubagentEvent, 0, len(events))
	for i, ev := range events {
		if ev.Kind == SEReasoning && !shouldRenderReasoningEvent(events, i, status) {
			continue
		}
		if hidePlan && ev.Kind == SEPlan {
			continue
		}
		out = append(out, ev)
	}
	return out
}

func updateLinkedTerminalEvent(events []SubagentEvent, toolName string, taskID string, output string) bool {
	if !strings.EqualFold(strings.TrimSpace(toolName), "TASK") {
		return false
	}
	taskID = strings.TrimSpace(taskID)
	output = strings.TrimSpace(output)
	if taskID == "" || output == "" {
		return false
	}
	for i := len(events) - 1; i >= 0; i-- {
		ev := &events[i]
		if ev.Kind != SEToolCall || strings.TrimSpace(ev.TaskID) != taskID || !isTerminalPanelTool(ev.Name) {
			continue
		}
		ev.Output = output
		return true
	}
	return false
}

func hasApprovalEvent(events []SubagentEvent) bool {
	for _, ev := range events {
		if ev.Kind == SEApproval {
			return true
		}
	}
	return false
}

func shouldRenderReasoningEvent(events []SubagentEvent, idx int, _ string) bool {
	if idx < 0 || idx >= len(events) {
		return false
	}
	ev := events[idx]
	return ev.Kind == SEReasoning && strings.TrimSpace(ev.Text) != ""
}

func splitParticipantActor(actor string) (name string, provider string) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "", ""
	}
	open := strings.LastIndex(actor, "(")
	closeIdx := strings.LastIndex(actor, ")")
	if open <= 0 || closeIdx != len(actor)-1 || closeIdx <= open+1 {
		return actor, ""
	}
	name = strings.TrimSpace(actor[:open])
	provider = strings.TrimSpace(actor[open+1 : closeIdx])
	if name == "" || provider == "" {
		return actor, ""
	}
	return name, provider
}

// ---------------------------------------------------------------------------
// SubagentPanelBlock — SPAWN child ACP session viewer.
// ---------------------------------------------------------------------------

// SubagentEventKind identifies the type of a child session event.
type SubagentEventKind int

const (
	SEAssistant SubagentEventKind = iota
	SEReasoning
	SEToolCall
	SEPlan
	SEApproval
)

// SubagentEvent is a single event in a subagent's chronological event stream.
type SubagentEvent struct {
	Kind SubagentEventKind

	// Assistant/Reasoning: accumulated text.
	Text string

	// ToolCall fields.
	CallID string
	Name   string
	Args   string
	Output string
	TaskID string
	Done   bool
	Err    bool

	// Plan fields.
	PlanEntries []planEntryState

	// Approval fields (derived from context when status becomes waiting_approval).
	ApprovalTool    string
	ApprovalCommand string
}

type SubagentSessionState struct {
	SpawnID   string
	AttachID  string
	Agent     string
	Status    string // "running", "completed", "failed", "interrupted", "timed_out", "waiting_approval"
	StartedAt time.Time
	Events    []SubagentEvent

	// eventsGen is bumped on every Events mutation. Panels use it to
	// detect staleness without reflect.DeepEqual.
	eventsGen uint64
}

func NewSubagentSessionState(spawnID, attachID, agent string) *SubagentSessionState {
	return &SubagentSessionState{
		SpawnID:   strings.TrimSpace(spawnID),
		AttachID:  strings.TrimSpace(attachID),
		Agent:     strings.TrimSpace(agent),
		Status:    "running",
		StartedAt: time.Now(),
	}
}

func (s *SubagentSessionState) AppendStreamChunk(kind SubagentEventKind, chunk string) {
	if s == nil {
		return
	}
	chunk = tuikit.SanitizeLogText(chunk)
	if chunk == "" {
		return
	}
	if idx := latestNarrativeAppendTargetIndex(s.Events, kind); idx >= 0 {
		s.Events[idx].Text = collapseRepeatedNarrativeText(mergeSubagentStreamChunk(s.Events[idx].Text, chunk))
		s.eventsGen++
		return
	}
	s.Events = append(s.Events, SubagentEvent{Kind: kind, Text: collapseRepeatedNarrativeText(chunk)})
	s.eventsGen++
}

func (s *SubagentSessionState) ReplaceFinalStreamChunk(kind SubagentEventKind, chunk string) {
	if s == nil {
		return
	}
	chunk = tuikit.SanitizeLogText(chunk)
	if strings.TrimSpace(chunk) == "" {
		return
	}
	if idx := latestNarrativeFinalTargetIndex(s.Events, kind); idx >= 0 {
		s.Events[idx].Text = collapseRepeatedNarrativeText(chunk)
		s.eventsGen++
		return
	}
	s.Events = append(s.Events, SubagentEvent{Kind: kind, Text: collapseRepeatedNarrativeText(chunk)})
	s.eventsGen++
}

func (s *SubagentSessionState) UpdateToolCall(callID, toolName, args, stream, chunk string, final bool) {
	s.UpdateToolCallWithMeta(callID, toolName, args, stream, chunk, final, ToolUpdateMeta{})
}

func (s *SubagentSessionState) UpdateToolCallWithMeta(callID, toolName, args, stream, chunk string, final bool, meta ToolUpdateMeta) {
	if s == nil {
		return
	}
	callID = strings.TrimSpace(callID)
	toolName = strings.TrimSpace(toolName)
	args = strings.TrimSpace(args)
	stream = strings.ToLower(strings.TrimSpace(stream))
	chunk = normalizeSubagentChunkBoundary("", chunk)
	taskID := strings.TrimSpace(meta.TaskID)
	if updateLinkedTerminalEvent(s.Events, toolName, taskID, strings.TrimSpace(chunk)) {
		chunk = ""
	}
	if !final {
		for i := len(s.Events) - 1; i >= 0; i-- {
			e := &s.Events[i]
			if e.Kind != SEToolCall || e.CallID != callID || e.Done || e.Err {
				continue
			}
			if strings.TrimSpace(e.Name) == "" {
				e.Name = toolName
			}
			if strings.TrimSpace(e.Args) == "" {
				e.Args = args
			}
			if e.TaskID == "" {
				e.TaskID = taskID
			}
			if chunk != "" {
				e.Output = mergeSubagentStreamChunk(e.Output, chunk)
			}
			s.eventsGen++
			return
		}
		s.Events = append(s.Events, SubagentEvent{
			Kind:   SEToolCall,
			Name:   toolName,
			CallID: callID,
			Args:   args,
			Output: chunk,
			TaskID: taskID,
		})
		s.eventsGen++
		return
	}

	finalEvent := SubagentEvent{
		Kind:   SEToolCall,
		Name:   toolName,
		CallID: callID,
		Args:   args,
		Output: chunk,
		Done:   true,
		Err:    stream == "stderr",
		TaskID: taskID,
	}
	for i := len(s.Events) - 1; i >= 0; i-- {
		e := &s.Events[i]
		if e.Kind != SEToolCall || e.CallID != callID {
			continue
		}
		if strings.TrimSpace(finalEvent.Name) == "" {
			finalEvent.Name = e.Name
		}
		if strings.TrimSpace(finalEvent.Args) == "" {
			finalEvent.Args = e.Args
		}
		break
	}
	s.Events = append(s.Events, finalEvent)
	s.eventsGen++
}

func (s *SubagentSessionState) UpdatePlan(entries []planEntryState) {
	if s == nil {
		return
	}
	if n := len(s.Events); n > 0 && s.Events[n-1].Kind == SEPlan {
		s.Events[n-1].PlanEntries = entries
		s.eventsGen++
		return
	}
	s.Events = append(s.Events, SubagentEvent{
		Kind:        SEPlan,
		PlanEntries: entries,
	})
	s.eventsGen++
}

func (s *SubagentSessionState) AddApprovalEvent(tool, command string) {
	if s == nil {
		return
	}
	if tool == "" {
		for i := len(s.Events) - 1; i >= 0; i-- {
			e := &s.Events[i]
			if e.Kind == SEToolCall && !e.Done {
				tool = e.Name
				command = e.Args
				break
			}
		}
	}
	if n := len(s.Events); n > 0 && s.Events[n-1].Kind == SEApproval {
		s.Events[n-1].ApprovalTool = tool
		s.Events[n-1].ApprovalCommand = command
		s.eventsGen++
		return
	}
	s.Events = append(s.Events, SubagentEvent{
		Kind:            SEApproval,
		ApprovalTool:    tool,
		ApprovalCommand: command,
	})
	s.eventsGen++
}

func (s *SubagentSessionState) ReviveFromTerminal() {
	if s == nil || !isTerminalSubagentState(s.Status) {
		return
	}
	s.Status = "running"
	filtered := s.Events[:0]
	changed := false
	for _, ev := range s.Events {
		if ev.Kind == SEToolCall &&
			ev.Done &&
			ev.Err &&
			strings.Contains(strings.ToLower(strings.TrimSpace(ev.Output)), "interrupted before completion") &&
			(strings.EqualFold(strings.TrimSpace(ev.Name), "SPAWN") || strings.EqualFold(strings.TrimSpace(ev.Name), "TASK")) {
			changed = true
			continue
		}
		filtered = append(filtered, ev)
	}
	s.Events = filtered
	if changed {
		s.eventsGen++
	}
}

type SubagentPanelBlock struct {
	id                    string
	session               *SubagentSessionState
	localEvtGen           uint64 // tracks which session eventsGen was last copied
	SpawnID               string
	AttachID              string
	Agent                 string
	CallID                string
	Status                string // "running", "completed", "failed", "interrupted", "timed_out", "waiting_approval"
	StartedAt             time.Time
	Expanded              bool
	CollapseAt            time.Time
	CollapseFrom          time.Time
	CollapseFor           time.Duration
	VisibleLines          int
	ScrollOffset          int
	FollowTail            bool
	Terminal              bool
	ScrollbarVisibleUntil time.Time

	// PinnedOpenByUser is set when a terminal inline panel is manually
	// reopened from its anchor. That suppresses future auto-collapse until
	// the session resumes active work.
	PinnedOpenByUser bool

	// Events is the chronological stream of child session events.
	Events []SubagentEvent
}

func NewSubagentPanelBlock(spawnID, attachID, agent, callID string) *SubagentPanelBlock {
	return &SubagentPanelBlock{
		id:          nextBlockID(),
		SpawnID:     spawnID,
		AttachID:    attachID,
		Agent:       agent,
		CallID:      callID,
		Status:      "running",
		StartedAt:   time.Now(),
		Expanded:    true,
		CollapseFor: inlinePanelCollapseDuration,
		FollowTail:  true,
	}
}

func (b *SubagentPanelBlock) sessionState() *SubagentSessionState {
	if b == nil {
		return nil
	}
	if b.session == nil {
		state := NewSubagentSessionState(b.SpawnID, b.AttachID, b.Agent)
		state.Status = strings.TrimSpace(b.Status)
		if state.Status == "" {
			state.Status = "running"
		}
		if !b.StartedAt.IsZero() {
			state.StartedAt = b.StartedAt
		}
		state.Events = append(state.Events, b.Events...)
		state.eventsGen++
		b.session = state
		b.localEvtGen = state.eventsGen
		return state
	}
	b.syncMirrorIntoSession()
	return b.session
}

func (b *SubagentPanelBlock) bindSession(state *SubagentSessionState) {
	if b == nil || state == nil {
		return
	}
	b.session = state
	b.syncSessionMirror()
}

func (b *SubagentPanelBlock) syncMirrorIntoSession() {
	if b == nil || b.session == nil {
		return
	}
	if strings.TrimSpace(b.SpawnID) != "" && b.SpawnID != b.session.SpawnID {
		b.session.SpawnID = b.SpawnID
	}
	if strings.TrimSpace(b.AttachID) != "" && b.AttachID != b.session.AttachID {
		b.session.AttachID = b.AttachID
	}
	if strings.TrimSpace(b.Agent) != "" && b.Agent != b.session.Agent {
		b.session.Agent = b.Agent
	}
	if strings.TrimSpace(b.Status) != "" && b.Status != b.session.Status {
		b.session.Status = b.Status
	}
	if !b.StartedAt.IsZero() && !b.StartedAt.Equal(b.session.StartedAt) {
		b.session.StartedAt = b.StartedAt
	}
	if b.localEvtGen != b.session.eventsGen || len(b.Events) != len(b.session.Events) {
		b.session.Events = append(b.session.Events[:0], b.Events...)
		b.session.eventsGen++
		b.localEvtGen = b.session.eventsGen
	}
}

func (b *SubagentPanelBlock) syncSessionMirror() {
	if b == nil || b.session == nil {
		return
	}
	state := b.session
	b.SpawnID = state.SpawnID
	b.AttachID = state.AttachID
	b.Agent = state.Agent
	b.Status = state.Status
	b.StartedAt = state.StartedAt
	if b.localEvtGen != state.eventsGen {
		b.Events = append(b.Events[:0], state.Events...)
		b.localEvtGen = state.eventsGen
	}
}

// AppendStreamChunk appends a streaming text chunk (assistant or reasoning).
// If the most recent event is the same kind, the chunk is concatenated;
// otherwise a new event is created, preserving chronological ordering.
func (b *SubagentPanelBlock) AppendStreamChunk(kind SubagentEventKind, chunk string) {
	state := b.sessionState()
	state.AppendStreamChunk(kind, chunk)
	b.syncSessionMirror()
}

func (b *SubagentPanelBlock) ReplaceFinalStreamChunk(kind SubagentEventKind, chunk string) {
	state := b.sessionState()
	state.ReplaceFinalStreamChunk(kind, chunk)
	b.syncSessionMirror()
}

func mergeSubagentStreamChunk(existing string, incoming string) string {
	incoming = normalizeSubagentChunkBoundary(existing, incoming)
	if incoming == "" {
		return existing
	}
	if existing == "" {
		return incoming
	}
	if incoming == existing {
		return existing
	}

	const stableReplayThreshold = 12
	if runeCount(existing) >= stableReplayThreshold && strings.HasPrefix(incoming, existing) {
		return incoming
	}
	if runeCount(incoming) >= stableReplayThreshold && strings.HasPrefix(existing, incoming) {
		return existing
	}
	if suffix := overlappingSubagentSuffix(existing, incoming, 6); suffix != incoming {
		return existing + suffix
	}
	return existing + incoming
}

func appendDeltaStreamChunk(existing string, incoming string) string {
	incoming = normalizeSubagentChunkBoundary(existing, incoming)
	if incoming == "" {
		return existing
	}
	if existing == "" {
		return incoming
	}
	return existing + incoming
}

func normalizeSubagentChunkBoundary(existing string, incoming string) string {
	if incoming == "" {
		return ""
	}
	if existing == "" {
		return strings.TrimLeft(incoming, "\uFEFF")
	}
	// Some upstream streaming paths occasionally surface a replacement-rune
	// prefix at chunk boundaries when a multibyte rune was split mid-update.
	// Keep the fix narrow: only trim leading U+FFFD/FEFF on continuation chunks.
	incoming = strings.TrimLeft(incoming, "\uFFFD\uFEFF")
	return incoming
}

func overlappingSubagentSuffix(existing string, incoming string, minOverlap int) string {
	existingRunes := []rune(existing)
	incomingRunes := []rune(incoming)
	limit := minInt(len(existingRunes), len(incomingRunes))
	for overlap := limit; overlap >= minOverlap; overlap-- {
		if string(existingRunes[len(existingRunes)-overlap:]) == string(incomingRunes[:overlap]) {
			return string(incomingRunes[overlap:])
		}
	}
	return incoming
}

func runeCount(text string) int {
	return len([]rune(text))
}

// UpdateToolCall creates or updates a tool call event identified by callID.
func (b *SubagentPanelBlock) UpdateToolCall(callID, toolName, args, stream, chunk string, final bool) {
	b.UpdateToolCallWithMeta(callID, toolName, args, stream, chunk, final, ToolUpdateMeta{})
}

func (b *SubagentPanelBlock) UpdateToolCallWithMeta(callID, toolName, args, stream, chunk string, final bool, meta ToolUpdateMeta) {
	state := b.sessionState()
	state.UpdateToolCallWithMeta(callID, toolName, args, stream, chunk, final, meta)
	b.syncSessionMirror()
}

// UpdatePlan appends a new plan event or coalesces with the last event if it
// is also a plan (rapid consecutive plan updates). This preserves the
// chronological interleaving: tool→plan→tool→plan shows two plan snapshots.
func (b *SubagentPanelBlock) UpdatePlan(entries []planEntryState) {
	state := b.sessionState()
	state.UpdatePlan(entries)
	b.syncSessionMirror()
}

// AddApprovalEvent appends an approval event or coalesces with the last event
// if it is also an approval (rapid consecutive status updates). This preserves
// the chronological interleaving for multiple approval cycles.
func (b *SubagentPanelBlock) AddApprovalEvent(tool, command string) {
	state := b.sessionState()
	state.AddApprovalEvent(tool, command)
	b.syncSessionMirror()
}

func (b *SubagentPanelBlock) BlockID() string { return b.id }
func (b *SubagentPanelBlock) Kind() BlockKind { return BlockSubagent }
func (b *SubagentPanelBlock) Render(ctx BlockRenderContext) []RenderedRow {
	if b == nil || !b.Expanded {
		return nil
	}
	_ = b.sessionState()
	b.syncSessionMirror()
	lines := renderSubagentPanelLines(b, ctx)
	rows := make([]RenderedRow, len(lines))
	for i, l := range lines {
		rows[i] = StyledRow(b.id, l)
	}
	return rows
}

func renderSubagentPanelLines(panel *SubagentPanelBlock, ctx BlockRenderContext) []string {
	if panel == nil {
		return nil
	}
	baseWidth := ctx.Width
	if baseWidth <= 0 {
		baseWidth = 80
	}
	boxWidth := maxInt(20, baseWidth-4)
	contentWidth, lines, overflow := subagentPanelRenderLines(panel, ctx, boxWidth)
	totalLines := len(lines)
	start, end, _ := panelScrollWindow(len(lines), panel.previewLines(), panel.ScrollOffset, panel.FollowTail)
	lines = lines[start:end]
	if overflow {
		lines = addScrollbar(lines, contentWidth, len(lines), start, totalLines, ctx.Theme, panel.shouldShowScrollbar(time.Now()))
	}
	vm := PanelViewModel{
		Variant: tuikit.PanelShellVariantDrawer,
		Width:   boxWidth,
		Header:  tuikit.PanelHeaderModel{},
		Body:    lines,
	}
	return renderPanelViewModel(ctx.Theme, vm)
}

func (b *SubagentPanelBlock) previewLines() int {
	if b == nil {
		return subagentOutputPreviewLines
	}
	if b.VisibleLines > 0 {
		return b.VisibleLines
	}
	return subagentOutputPreviewLines
}

func subagentPanelRenderLines(panel *SubagentPanelBlock, ctx BlockRenderContext, boxWidth int) (contentWidth int, lines []string, overflow bool) {
	baseWidth := maxInt(1, boxWidth-4)
	scrollWidth := maxInt(1, baseWidth-1)
	scrollLines := renderSubagentInnerLines(panel, ctx, scrollWidth)
	if len(scrollLines) > subagentOutputPreviewLines {
		return scrollWidth, scrollLines, true
	}
	return baseWidth, renderSubagentInnerLines(panel, ctx, baseWidth), false
}

func renderSubagentInnerLines(panel *SubagentPanelBlock, ctx BlockRenderContext, contentWidth int) []string {
	return renderACPTranscriptLines(panel.id, panel.Events, panel.Status, contentWidth, ctx, acpTranscriptRenderOptions{
		EmptyPlaceholder: "waiting for subagent output",
	})
}

func narrativeEventActive(events []SubagentEvent, idx int, terminal bool) bool {
	if terminal || idx < 0 || idx >= len(events) {
		return false
	}
	ev := events[idx]
	if ev.Kind != SEAssistant && ev.Kind != SEReasoning {
		return false
	}
	for j := idx + 1; j < len(events); j++ {
		if events[j].Kind == SEAssistant || events[j].Kind == SEReasoning {
			return false
		}
	}
	return true
}

func panelScrollWindow(total, visible, offset int, followTail bool) (start int, end int, maxOffset int) {
	if visible <= 0 {
		visible = 1
	}
	if total <= visible {
		return 0, total, 0
	}
	maxOffset = total - visible
	if followTail {
		offset = maxOffset
	} else {
		if offset < 0 {
			offset = 0
		}
		if offset > maxOffset {
			offset = maxOffset
		}
	}
	return offset, minInt(total, offset+visible), maxOffset
}

func canScrollPanelState(offset int, followTail bool, total, visible, delta int) bool {
	if delta == 0 {
		return false
	}
	_, _, maxOffset := panelScrollWindow(total, visible, offset, followTail)
	if maxOffset == 0 {
		return false
	}
	current := offset
	if followTail {
		current = maxOffset
	}
	next := current + delta
	next = max(next, 0)
	next = min(next, maxOffset)
	return next != current
}

func addScrollbar(lines []string, contentWidth, visible, offset, total int, theme tuikit.Theme, visibleNow bool) []string {
	if len(lines) == 0 || total <= visible || !visibleNow {
		return lines
	}
	thumbHeight := maxInt(1, visible*visible/maxInt(visible, total))
	maxStart := maxInt(0, visible-thumbHeight)
	thumbStart := 0
	if total > visible && maxStart > 0 {
		thumbStart = (offset * maxStart) / maxInt(1, total-visible)
	}
	withScrollbar := make([]string, len(lines))
	for i, line := range lines {
		glyph := theme.ScrollbarTrackStyle().Render("▏")
		if i >= thumbStart && i < thumbStart+thumbHeight {
			glyph = theme.ScrollbarThumbStyle().Render("▎")
		}
		if pad := contentWidth - lipgloss.Width(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		withScrollbar[i] = line + glyph
	}
	return withScrollbar
}

func scrollPanelState(offset *int, followTail *bool, total, visible, delta int) bool {
	if offset == nil || followTail == nil || delta == 0 {
		return false
	}
	_, _, maxOffset := panelScrollWindow(total, visible, *offset, *followTail)
	if maxOffset == 0 {
		return false
	}
	current := *offset
	if *followTail {
		current = maxOffset
	}
	next := current + delta
	next = max(next, 0)
	next = min(next, maxOffset)
	changed := next != current || *followTail != (next == maxOffset)
	*offset = next
	*followTail = next == maxOffset
	return changed
}

func (b *SubagentPanelBlock) scrollableLineCount(ctx BlockRenderContext) int {
	if b == nil || !b.Expanded {
		return 0
	}
	_ = b.sessionState()
	b.syncSessionMirror()
	baseWidth := ctx.Width
	if baseWidth <= 0 {
		baseWidth = 80
	}
	boxWidth := maxInt(20, baseWidth-4)
	_, lines, _ := subagentPanelRenderLines(b, ctx, boxWidth)
	return len(lines)
}

func (b *SubagentPanelBlock) Scroll(delta int, ctx BlockRenderContext) bool {
	return scrollPanelState(&b.ScrollOffset, &b.FollowTail, b.scrollableLineCount(ctx), b.previewLines(), delta)
}

func (b *SubagentPanelBlock) CanScroll(delta int, ctx BlockRenderContext) bool {
	if b == nil {
		return false
	}
	return canScrollPanelState(b.ScrollOffset, b.FollowTail, b.scrollableLineCount(ctx), b.previewLines(), delta)
}

func (b *SubagentPanelBlock) scrollState() (*int, *bool) {
	if b == nil {
		return nil, nil
	}
	return &b.ScrollOffset, &b.FollowTail
}

func (b *SubagentPanelBlock) scrollbarVisibleUntilPtr() *time.Time {
	if b == nil {
		return nil
	}
	return &b.ScrollbarVisibleUntil
}
