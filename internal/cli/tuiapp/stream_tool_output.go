package tuiapp

import (
	"strconv"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

// resolveCallAnchor returns the block ID of the tool-call TranscriptBlock
// ("▸ TOOLNAME ...") that corresponds to the given callID and toolName.
// It first checks the stable callAnchorIndex; if not found, it claims the
// oldest pending anchor matching toolName (FIFO).
func (m *Model) resolveCallAnchor(callID, toolName string) string {
	if m.callAnchorIndex == nil {
		m.callAnchorIndex = map[string]string{}
	}
	if callID != "" {
		if bid, ok := m.callAnchorIndex[callID]; ok {
			return bid
		}
	}
	// Claim oldest pending anchor matching the tool name.
	normalized := strings.ToUpper(strings.TrimSpace(toolName))
	for i, a := range m.pendingToolAnchors {
		if strings.EqualFold(a.toolName, normalized) {
			m.pendingToolAnchors = append(m.pendingToolAnchors[:i], m.pendingToolAnchors[i+1:]...)
			if callID != "" {
				m.callAnchorIndex[callID] = a.blockID
			}
			return a.blockID
		}
	}
	// No matching anchor by name — claim the oldest one (best-effort).
	if len(m.pendingToolAnchors) > 0 {
		a := m.pendingToolAnchors[0]
		m.pendingToolAnchors = m.pendingToolAnchors[1:]
		if callID != "" {
			m.callAnchorIndex[callID] = a.blockID
		}
		return a.blockID
	}
	return ""
}

// extractToolCallName extracts the tool name from a "▸ TOOLNAME ..." log line.
// Returns the name and true if the line is a tool call start; empty and false otherwise.
func extractToolCallName(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "▸") {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "▸"))
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", false
	}
	return strings.ToUpper(fields[0]), true
}

// panelProducingTools lists the tool names that generate companion panels.
// Only these tools are tracked as pending anchors; others (READ, WRITE, etc.)
// are one-shot transcript lines that never need a panel insertion point.
var panelProducingTools = map[string]bool{
	"BASH":  true,
	"SPAWN": true,
}

func (m *Model) toolOutputKey(msg tuievents.TaskStreamMsg) string {
	taskID := strings.TrimSpace(msg.TaskID)
	callID := strings.TrimSpace(msg.CallID)
	hasOriginPanel := callID != "" && m.findBashPanelBlock(callID) != nil

	// Register taskID → callID mapping when both are present.
	// The kernel's task_snapshot sets CallID == TaskID, so the first event
	// from a yielded task is self-referential.  bash_watch.go later sends
	// the real original BASH CallID.  Accept a mapping update when:
	//   1. No mapping exists yet, OR
	//   2. The existing mapping is self-referential (callID == taskID) and
	//      the incoming callID is genuinely different (the real origin).
	// Ignore TASK/status response IDs unless they already resolve to an
	// existing origin panel; otherwise they can poison taskID → callID.
	if taskID != "" && callID != "" {
		if m.taskOriginCallID == nil {
			m.taskOriginCallID = map[string]string{}
		}
		existing, mapped := m.taskOriginCallID[taskID]
		switch {
		case !mapped && (callID == taskID || hasOriginPanel):
			m.taskOriginCallID[taskID] = callID
		case existing == taskID && callID != taskID && hasOriginPanel:
			m.taskOriginCallID[taskID] = callID
		}
	}

	// Resolve taskID to origin callID if mapped.
	if taskID != "" {
		if origin, ok := m.taskOriginCallID[taskID]; ok {
			return origin
		}
	}

	if callID != "" {
		return callID
	}
	toolName := strings.TrimSpace(msg.Label)
	if toolName == "" {
		toolName = strings.TrimSpace(msg.Tool)
	}
	return toolName
}

// findBashPanelBlock looks up an existing BASH panel by key without creating one.
func (m *Model) findBashPanelBlock(key string) *BashPanelBlock {
	key = strings.TrimSpace(key)
	if key == "" || m.toolOutputBlockIDs == nil {
		return nil
	}
	blockID, ok := m.toolOutputBlockIDs[key]
	if !ok {
		return nil
	}
	block := m.doc.Find(blockID)
	if block == nil {
		return nil
	}
	bp, _ := block.(*BashPanelBlock)
	return bp
}

func (m *Model) ensureBashPanelBlock(key, toolName, callID string, reset bool) *BashPanelBlock {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	if m.toolOutputBlockIDs == nil {
		m.toolOutputBlockIDs = map[string]string{}
	}
	blockID, ok := m.toolOutputBlockIDs[key]
	if ok && !reset {
		block := m.doc.Find(blockID)
		if block != nil {
			if bp, ok := block.(*BashPanelBlock); ok {
				if strings.TrimSpace(bp.ToolName) == "" {
					bp.ToolName = strings.TrimSpace(toolName)
				}
				if strings.TrimSpace(bp.CallID) == "" {
					bp.CallID = strings.TrimSpace(callID)
				}
				m.syncInlineBashAnchorState(bp)
				return bp
			}
		}
	}
	bp := NewBashPanelBlock(toolName, callID)
	bp.Key = key
	// Anchor panel after its specific tool call line.
	anchorID := m.resolveCallAnchor(callID, toolName)
	if anchorID != "" {
		m.doc.InsertAfter(anchorID, bp)
	} else {
		m.doc.Append(bp)
	}
	m.toolOutputBlockIDs[key] = bp.BlockID()
	m.syncInlineBashAnchorState(bp)
	return bp
}

func (m *Model) applyBashPanelState(panel *BashPanelBlock, state string, final bool) {
	if panel == nil {
		return
	}
	normalized := normalizeToolOutputState(state)
	if normalized != "" {
		panel.State = normalized
	}
	now := time.Now()
	switch panel.State {
	case "running", "waiting_approval", "waiting_input":
		panel.Active = true
		panel.EndedAt = time.Time{}
		cancelInlineCollapse(&panel.CollapseAt, &panel.CollapseFrom, &panel.VisibleLines)
	case "completed", "failed", "interrupted", "cancelled", "canceled", "terminated":
		panel.Active = false
		if panel.EndedAt.IsZero() {
			panel.EndedAt = now
		}
	}
	if final {
		panel.Active = false
		if panel.EndedAt.IsZero() {
			panel.EndedAt = now
		}
	}
	panel.UpdatedAt = now
	m.syncInlineBashAnchorState(panel)
}

func (m *Model) scheduleInlineBashCollapse(panel *BashPanelBlock) {
	if m == nil || !isInlineBashPanel(panel) || panel == nil {
		return
	}
	if m.noAnimation {
		panel.Expanded = false
		panel.CollapseAt = time.Time{}
		panel.CollapseFrom = time.Time{}
		panel.VisibleLines = 0
		m.syncInlineBashAnchorState(panel)
		return
	}
	scheduleInlineCollapse(&panel.CollapseAt, &panel.CollapseFrom, &panel.CollapseFor, &panel.VisibleLines, panel.StartedAt, toolOutputPreviewLines, time.Now())
	m.syncInlineBashAnchorState(panel)
}

func (m *Model) toggleInlineBashPanel(panel *BashPanelBlock) {
	if m == nil || panel == nil {
		return
	}
	cancelInlineCollapse(&panel.CollapseAt, &panel.CollapseFrom, &panel.VisibleLines)
	panel.Expanded = !panel.Expanded
	m.syncInlineBashAnchorState(panel)
}

func isTerminalToolOutputState(state string) bool {
	switch normalizeToolOutputState(state) {
	case "completed", "failed", "interrupted", "cancelled", "canceled", "terminated":
		return true
	default:
		return false
	}
}

func (m *Model) findInlineBashPanelByAnchorBlockID(blockID string) *BashPanelBlock {
	blockID = strings.TrimSpace(blockID)
	if blockID == "" {
		return nil
	}
	for callID, anchorID := range m.callAnchorIndex {
		if strings.TrimSpace(anchorID) != blockID {
			continue
		}
		panel := m.findBashPanelBlock(callID)
		if isInlineBashPanel(panel) {
			return panel
		}
	}
	return nil
}

func (m *Model) syncInlineBashAnchorState(panel *BashPanelBlock) {
	if !isInlineBashPanel(panel) || m == nil {
		return
	}
	callID := strings.TrimSpace(panel.CallID)
	if callID == "" {
		return
	}
	anchorID := strings.TrimSpace(m.callAnchorIndex[callID])
	if anchorID == "" {
		return
	}
	tb := m.findTranscriptBlock(anchorID)
	if tb == nil {
		return
	}
	tb.Raw = inlineBashAnchorLabel(tb.Raw, panel.Expanded)
}

func inlineBashAnchorLabel(raw string, expanded bool) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw
	}
	prefix := ""
	rest := trimmed
	for _, candidate := range []string{"▸", "▾", "▶"} {
		if strings.HasPrefix(trimmed, candidate) {
			prefix = candidate
			rest = strings.TrimSpace(strings.TrimPrefix(trimmed, candidate))
			break
		}
	}
	if prefix == "" {
		return raw
	}
	next := "▸"
	if expanded {
		next = "▾"
	}
	leadingEnd := strings.Index(raw, trimmed)
	if leadingEnd < 0 {
		return next + " " + rest
	}
	leading := raw[:leadingEnd]
	return leading + next + " " + rest
}

func (m *Model) appendBashPanelChunk(panel *BashPanelBlock, stream, chunk string) {
	if panel == nil {
		return
	}
	cancelInlineCollapse(&panel.CollapseAt, &panel.CollapseFrom, &panel.VisibleLines)
	normalized := tuikit.SanitizeLogText(chunk)
	normalized = strings.ReplaceAll(strings.ReplaceAll(normalized, "\r\n", "\n"), "\r", "\n")
	stream = strings.ToLower(strings.TrimSpace(stream))
	if stream == "" {
		stream = "stdout"
	}
	if isSpawnLikeTool(panel.ToolName) {
		panelID := panel.BlockID()
		m.flushSpawnPreviewSmoothingExcept(panelID, stream)
		switch stream {
		case "reasoning":
			panel.ReasoningPartial = m.appendSpawnPreviewChunk(panel, panel.ReasoningPartial, normalized, stream)
		case "assistant":
			panel.AssistantPartial = m.appendSpawnPreviewChunk(panel, panel.AssistantPartial, normalized, stream)
		case "stderr":
			panel.StderrPartial = m.appendSpawnPreviewChunk(panel, panel.StderrPartial, normalized, stream)
		default:
			panel.StdoutPartial = m.consumeToolOutputChunkBlock(panel, panel.StdoutPartial, normalized, stream)
		}
		panel.LastStream = stream
		panel.UpdatedAt = time.Now()
		return
	}
	switch stream {
	case "stderr":
		panel.StderrPartial = m.consumeToolOutputChunkBlock(panel, panel.StderrPartial, normalized, stream)
	default:
		panel.StdoutPartial = m.consumeToolOutputChunkBlock(panel, panel.StdoutPartial, normalized, stream)
	}
	panel.LastStream = stream
	panel.UpdatedAt = time.Now()
}

func (m *Model) appendSpawnPreviewChunk(panel *BashPanelBlock, partial, chunk, stream string) string {
	if panel == nil || chunk == "" {
		return partial
	}
	_ = m.enqueueStreamDelta("spawn_preview", panel.BlockID(), stream, "", chunk, false)
	return partial
}

func (m *Model) applySpawnPreviewImmediate(panelID string, stream string, chunk string) {
	if m == nil || strings.TrimSpace(panelID) == "" || chunk == "" {
		return
	}
	panel, _ := m.doc.Find(panelID).(*BashPanelBlock)
	if panel == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(stream)) {
	case "reasoning":
		panel.ReasoningPartial = m.consumeSubagentPreviewChunkBlock(panel, panel.ReasoningPartial, chunk, stream)
	case "assistant":
		panel.AssistantPartial = m.consumeSubagentPreviewChunkBlock(panel, panel.AssistantPartial, chunk, stream)
	case "stderr":
		panel.StderrPartial = m.consumeSubagentPreviewChunkBlock(panel, panel.StderrPartial, chunk, stream)
	default:
		panel.StdoutPartial = m.consumeToolOutputChunkBlock(panel, panel.StdoutPartial, chunk, stream)
	}
	panel.LastStream = strings.ToLower(strings.TrimSpace(stream))
	panel.UpdatedAt = time.Now()
	m.syncViewportContent()
}

func (m *Model) consumeSubagentPreviewChunkBlock(panel *BashPanelBlock, partial, chunk, stream string) string {
	if chunk == "" {
		return partial
	}
	buf := partial + chunk
	for {
		idx := strings.IndexByte(buf, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(buf[:idx], "\r")
		buf = buf[idx+1:]
		if shouldSkipSubagentPreviewLineBlock(panel, line) {
			continue
		}
		if formatted := formatSubagentPreviewText(line, stream); formatted != "" {
			m.appendSubagentPreviewLineBlock(panel, formatted, stream)
		}
	}
	if len(panel.Lines) > toolOutputPreviewLines*3 {
		panel.Lines = append([]toolOutputLine(nil), panel.Lines[len(panel.Lines)-(toolOutputPreviewLines*3):]...)
	}
	return buf
}

func (m *Model) appendSubagentPreviewLineBlock(panel *BashPanelBlock, text string, stream string) {
	if panel == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if len(panel.Lines) > 0 {
		last := &panel.Lines[len(panel.Lines)-1]
		if canMergeSubagentPreviewLine(last, text, stream) {
			last.text = strings.TrimSpace(last.text) + " " + text
			return
		}
	}
	panel.Lines = append(panel.Lines, toolOutputLine{text: text, stream: stream})
}

func canMergeSubagentPreviewLine(last *toolOutputLine, nextText string, stream string) bool {
	if last == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(last.stream), strings.TrimSpace(stream)) {
		return false
	}
	if !isSubagentParagraphText(last.text) || !isSubagentParagraphText(nextText) {
		return false
	}
	return true
}

func isSubagentParagraphText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	switch {
	case strings.HasPrefix(text, "▸"),
		strings.HasPrefix(text, "✓"),
		strings.HasPrefix(text, "!"),
		strings.HasPrefix(text, "- "),
		strings.HasPrefix(text, "* "),
		strings.HasPrefix(text, "• "),
		strings.HasPrefix(text, "1. "):
		return false
	}
	return true
}

func (m *Model) consumeToolOutputChunkBlock(panel *BashPanelBlock, partial, chunk, stream string) string {
	if chunk == "" {
		return partial
	}
	buf := partial + chunk
	for {
		idx := strings.IndexByte(buf, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(buf[:idx], "\r")
		buf = buf[idx+1:]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if shouldSkipSubagentPreviewLineBlock(panel, line) {
			continue
		}
		panel.Lines = append(panel.Lines, toolOutputLine{text: line, stream: stream})
	}
	if len(panel.Lines) > toolOutputHistoryLines {
		panel.Lines = append([]toolOutputLine(nil), panel.Lines[len(panel.Lines)-toolOutputHistoryLines:]...)
	}
	return buf
}

func shouldSkipSubagentPreviewLineBlock(panel *BashPanelBlock, line string) bool {
	if panel == nil || !isSpawnLikeTool(panel.ToolName) {
		return false
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "```") {
		panel.SubagentFence = !panel.SubagentFence
		return true
	}
	return panel.SubagentFence
}

func normalizeToolOutputState(state string) string {
	normalized := strings.ToLower(strings.TrimSpace(state))
	switch normalized {
	case "running", "waiting_approval", "waiting_input", "completed", "failed", "interrupted", "cancelled", "canceled", "terminated":
		return normalized
	default:
		return ""
	}
}

func formatToolOutputAge(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return strconv.Itoa(int(d/time.Second)) + "s"
	}
	minutes := int(d / time.Minute)
	seconds := int((d % time.Minute) / time.Second)
	return strconv.Itoa(minutes) + "m" + strconv.Itoa(seconds) + "s"
}

func formatSubagentPreviewText(text string, stream string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\t", " "))
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "__", "")
	text = strings.ReplaceAll(text, "`", "")
	text = strings.TrimLeft(text, "#*- ")
	text = collapseSubagentInlineSpaces(text)
	if text == "" {
		return ""
	}
	if stream == "assistant" {
		if text == "answer" || text == "assistant" {
			return ""
		}
		text = strings.TrimPrefix(text, "answer ")
		text = strings.TrimPrefix(text, "assistant ")
	}
	if stream == "reasoning" {
		if text == "reasoning" {
			return ""
		}
		text = strings.TrimPrefix(text, "reasoning ")
	}
	return strings.TrimSpace(text)
}

func collapseSubagentInlineSpaces(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(text))
	spaceRun := false
	for _, r := range text {
		if r == ' ' || r == '\n' || r == '\r' || r == '\f' || r == '\v' {
			if !spaceRun {
				b.WriteByte(' ')
				spaceRun = true
			}
			continue
		}
		spaceRun = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func wrapToolOutputText(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	width = maxInt(1, width)
	parts := strings.Split(text, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for displayColumns(part) > width {
			cut := width
			slice := sliceByDisplayColumns(part, 0, cut)
			lastSpace := strings.LastIndex(slice, " ")
			if lastSpace > 8 {
				cut = displayColumns(slice[:lastSpace])
				slice = sliceByDisplayColumns(part, 0, cut)
			}
			out = append(out, strings.TrimSpace(slice))
			part = strings.TrimSpace(sliceByDisplayColumns(part, cut, displayColumns(part)))
		}
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return []string{text}
	}
	return out
}

func isSpawnLikeTool(name string) bool {
	name = strings.TrimSpace(name)
	return strings.EqualFold(name, "SPAWN")
}
