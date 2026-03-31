package tuiapp

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	"github.com/charmbracelet/x/ansi"
)

const defaultActivityWaitMS = 5000

func (m *Model) consumeActivityLine(line string) bool {
	kind, entry, ok := parseActivityLine(line)
	if !ok {
		if m.activeActivityID != "" {
			m.finalizeActivityBlock()
		}
		return false
	}
	if m.activeReasoningID != "" {
		m.doc.Remove(m.activeReasoningID)
		m.activeReasoningID = ""
		m.refreshHistoryTailState()
	}
	if m.activeActivityID == "" {
		m.appendActivityEntry(kind, entry)
		return true
	}
	ab := m.findActivityBlock()
	if ab != nil && ab.BlockKindField != kind {
		m.finalizeActivityBlock()
		m.appendActivityEntry(kind, entry)
		return true
	}
	m.appendActivityEntry(kind, entry)
	return true
}

func (m *Model) findActivityBlock() *ActivityBlock {
	if m.activeActivityID == "" {
		return nil
	}
	b := m.doc.Find(m.activeActivityID)
	if b == nil {
		m.activeActivityID = ""
		return nil
	}
	ab, ok := b.(*ActivityBlock)
	if !ok {
		m.activeActivityID = ""
		return nil
	}
	return ab
}

func (m *Model) appendActivityEntry(kind activityBlockKind, entry activityEntry) {
	ab := m.findActivityBlock()
	if ab == nil {
		ab = NewActivityBlock(kind)
		ab.Entries = []activityEntry{entry}
		m.doc.Append(ab)
		m.activeActivityID = ab.BlockID()
	} else {
		ab.Entries = append(ab.Entries, entry)
		ab.Active = true
		ab.Finalized = false
	}
	m.syncActivityBlock()
}

func (m *Model) syncActivityBlock() {
	ab := m.findActivityBlock()
	if ab == nil {
		return
	}
	foldedState := ab.toFoldedState()
	lines := m.renderActivityBlockLines(foldedState)
	rows := make([]RenderedRow, len(lines))
	for i, l := range lines {
		rows[i] = StyledRow(ab.id, l)
	}
	ab.cachedRows = rows
	m.hasCommittedLine = m.doc.Len() > 0
	m.lastCommittedStyle = tuikit.LineStyleDefault
	m.lastCommittedRaw = ""
	m.syncViewportContent()
}

func (m *Model) finalizeActivityBlock() {
	ab := m.findActivityBlock()
	if ab == nil {
		m.activeActivityID = ""
		return
	}
	ab.Active = false
	ab.Finalized = true
	foldedState := ab.toFoldedState()
	ab.Summary = m.activityBlockSummary(foldedState)
	foldedState.summary = ab.Summary
	if ab.BlockKindField == activityBlockTaskMonitor && strings.TrimSpace(ab.Summary) == "" {
		m.doc.Remove(ab.BlockID())
		m.activeActivityID = ""
		m.refreshHistoryTailState()
		m.syncViewportContent()
		return
	}
	// Render finalized summary line.
	summaryLine := m.renderActivitySummaryLine(foldedState)
	rawSummary := ansi.Strip(summaryLine)
	// Try to merge with previous task monitor summary.
	if ab.BlockKindField == activityBlockTaskMonitor {
		if prevBlock := m.findPreviousTranscriptBlock(ab.BlockID()); prevBlock != nil {
			prevText := strings.TrimSpace(ansi.Strip(prevBlock.Raw))
			// The raw field stores uncolored text; check for task monitor summary pattern.
			if prevSummary, ok := parseTaskMonitorSummaryLine(prevText); ok {
				if merged := mergeTaskMonitorSummaryTexts(prevSummary, strings.TrimSpace(ab.Summary)); strings.TrimSpace(merged) != "" {
					// Replace previous block with merged summary.
					mergedText := "▸ " + merged
					prevBlock.Raw = mergedText
					prevBlock.Style = tuikit.DetectLineStyle(mergedText)
					// Remove the activity block from doc.
					m.doc.Remove(ab.BlockID())
					m.activeActivityID = ""
					m.refreshHistoryTailState()
					m.syncViewportContent()
					return
				}
			}
		}
	}
	// Convert activity block to a summary transcript block.
	summaryBlock := NewTranscriptBlock(rawSummary, tuikit.DetectLineStyle(rawSummary))
	if !m.doc.Replace(ab.BlockID(), summaryBlock) {
		m.doc.Remove(ab.BlockID())
		m.doc.Append(summaryBlock)
	}
	m.activeActivityID = ""
	m.refreshHistoryTailState()
	m.syncViewportContent()
}

// findPreviousTranscriptBlock returns the TranscriptBlock right before the given block ID.
func (m *Model) findPreviousTranscriptBlock(blockID string) *TranscriptBlock {
	blocks := m.doc.Blocks()
	for i, b := range blocks {
		if b.BlockID() == blockID && i > 0 {
			if tb, ok := blocks[i-1].(*TranscriptBlock); ok {
				return tb
			}
		}
	}
	return nil
}

func (m *Model) renderActivityBlockLines(block *foldedActivityBlockState) []string {
	if block == nil {
		return nil
	}
	if block.kind == activityBlockTaskMonitor {
		return []string{m.renderTaskMonitorInlineLine(block, false)}
	}
	lines := []string{m.renderActivityTitleLine(block)}
	displayEntries := buildActivityDisplayEntries(block.entries)
	if len(displayEntries) > activityBlockPreviewLines {
		displayEntries = displayEntries[len(displayEntries)-activityBlockPreviewLines:]
	}
	for _, entry := range displayEntries {
		lines = append(lines, m.renderActivityEntryLine(block, entry.verb, entry.detail))
	}
	return lines
}

func (m *Model) renderActivityTitleLine(block *foldedActivityBlockState) string {
	title := "Exploring"
	meta := ""
	switch block.kind {
	case activityBlockTaskMonitor:
		title = "Standby"
		meta = m.renderTaskMonitorMeta(block)
	case activityBlockExploration:
		meta = m.renderExplorationMeta(block)
	}
	prefix := m.renderActivityTitlePrefix(block)
	titleText := m.renderActivityTitleText(title, block)
	if meta == "" {
		return prefix + " " + titleText
	}
	metaText := highlightNumericRuns(meta, m.theme.HelpHintTextStyle(), m.theme.TitleStyle())
	return prefix + " " + titleText + " " + metaText
}

func (m *Model) renderExplorationMeta(block *foldedActivityBlockState) string {
	if block == nil {
		return ""
	}
	files := uniqueReadPaths(block.entries)
	searches := countActivityCalls(block.entries, "SEARCH")
	parts := make([]string, 0, 2)
	if len(files) > 0 {
		parts = append(parts, fmt.Sprintf("%d files", len(files)))
	}
	if searches > 0 {
		parts = append(parts, fmt.Sprintf("%d searches", searches))
	}
	return strings.Join(parts, "  ")
}

func (m *Model) renderTaskMonitorMeta(block *foldedActivityBlockState) string {
	if block == nil {
		return ""
	}
	totalWaitMS := totalTaskWaitMS(block.entries)
	parts := make([]string, 0, 1)
	if totalWaitMS > 0 {
		parts = append(parts, friendlyActivityWaitLabel(totalWaitMS))
	}
	return strings.Join(parts, "  ")
}

func (m *Model) renderActivityEntryLine(_ *foldedActivityBlockState, verb string, detail string) string {
	border := lipgloss.NewStyle().Foreground(m.theme.RoleBorderFg).Render("│")
	verbText := m.theme.KeyLabelStyle().Render(verb)
	if detail != "" {
		detail = " " + highlightNumericRuns(detail, m.theme.HelpHintTextStyle(), m.theme.TitleStyle())
	}
	return "  " + border + " " + verbText + detail
}

func (m *Model) renderActivitySummaryLine(block *foldedActivityBlockState) string {
	if block == nil {
		return ""
	}
	if block.kind == activityBlockTaskMonitor {
		return m.renderTaskMonitorInlineLine(block, true)
	}
	text := strings.TrimSpace(block.summary)
	if text == "" {
		switch block.kind {
		case activityBlockTaskMonitor:
			text = "Standby"
		default:
			text = "Explored"
		}
	}
	prefix := m.theme.ToolStyle().Bold(true).Render("▸")
	return prefix + " " + m.renderActivitySummaryText(text)
}

func (m *Model) renderActivitySummaryText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	label := ""
	body := text
	switch {
	case strings.HasPrefix(text, "Explored "):
		label = "Explored"
		body = strings.TrimSpace(strings.TrimPrefix(text, label))
	case strings.HasPrefix(text, "Standby "):
		label = "Standby"
		body = strings.TrimSpace(strings.TrimPrefix(text, label))
	case text == "Explored":
		label = text
		body = ""
	case text == "Standby":
		label = text
		body = ""
	}
	if label == "" {
		return highlightNumericRuns(text, m.theme.HelpHintTextStyle(), m.theme.TitleStyle())
	}
	labelText := m.theme.KeyLabelStyle().Bold(true).Render(label)
	if body == "" {
		return labelText
	}
	return labelText + " " + highlightNumericRuns(body, m.theme.HelpHintTextStyle(), m.theme.TitleStyle())
}

func (m *Model) renderTaskMonitorInlineLine(block *foldedActivityBlockState, finalized bool) string {
	if block == nil {
		return ""
	}
	text := strings.TrimSpace(taskMonitorInlineText(block.entries, finalized))
	if text == "" {
		if finalized {
			return ""
		}
		text = "Waiting"
	}
	prefix := m.theme.ToolStyle().Bold(true).Render("▸")
	return prefix + " " + m.renderTaskMonitorSummaryText(text)
}

func (m *Model) renderTaskMonitorSummaryText(text string) string {
	parts := strings.Split(strings.TrimSpace(text), ", ")
	if len(parts) == 0 {
		return ""
	}
	sep := m.theme.HelpHintTextStyle().Render(", ")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		label := m.theme.KeyLabelStyle().Bold(true).Render(fields[0])
		if len(fields) == 1 {
			out = append(out, label)
			continue
		}
		tail := strings.Join(fields[1:], " ")
		out = append(out, label+" "+highlightNumericRuns(tail, m.theme.HelpHintTextStyle(), m.theme.TitleStyle()))
	}
	return strings.Join(out, sep)
}

func (m *Model) renderActivityTitlePrefix(_ *foldedActivityBlockState) string {
	return m.theme.ToolStyle().Bold(true).Render("▸")
}

func (m *Model) renderActivityTitleText(text string, block *foldedActivityBlockState) string {
	if block == nil || !block.active {
		return m.theme.TitleStyle().Render(text)
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return ""
	}
	totalWidth := maxInt(1, displayColumns(text))
	pathWidth := float64(totalWidth) + (runningLightLead * 2)
	head := math.Mod(float64(m.runningTick)*runningLightSpeed, pathWidth) - runningLightLead
	styles := []lipgloss.Style{
		m.theme.HelpHintTextStyle().Bold(true),
		lipgloss.NewStyle().Foreground(m.theme.TextSecondary).Bold(true),
		lipgloss.NewStyle().Foreground(m.theme.Info).Bold(true),
		lipgloss.NewStyle().Foreground(m.theme.PanelTitle).Bold(true),
		lipgloss.NewStyle().Foreground(m.theme.Focus).Bold(true),
	}
	var out strings.Builder
	column := 0
	for _, r := range runes {
		runeWidth := maxInt(1, displayColumns(string(r)))
		center := float64(column) + (float64(runeWidth) / 2)
		distance := math.Abs(center - head)
		level := 0
		intensity := 1 - (distance / runningLightBandRadius)
		switch {
		case intensity >= 0.82:
			level = 4
		case intensity >= 0.62:
			level = 3
		case intensity >= 0.42:
			level = 2
		case intensity >= 0.18:
			level = 1
		}
		out.WriteString(styles[level].Render(string(r)))
		column += runeWidth
	}
	return out.String()
}

func highlightNumericRuns(text string, base lipgloss.Style, accent lipgloss.Style) string {
	if text == "" {
		return ""
	}
	var out strings.Builder
	var buf strings.Builder
	inDigits := false
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		if inDigits {
			out.WriteString(accent.Render(buf.String()))
		} else {
			out.WriteString(base.Render(buf.String()))
		}
		buf.Reset()
	}
	for _, r := range text {
		isDigit := r >= '0' && r <= '9'
		if buf.Len() == 0 {
			inDigits = isDigit
			buf.WriteRune(r)
			continue
		}
		if isDigit == inDigits {
			buf.WriteRune(r)
			continue
		}
		flush()
		inDigits = isDigit
		buf.WriteRune(r)
	}
	flush()
	return out.String()
}

func (m *Model) activityBlockSummary(block *foldedActivityBlockState) string {
	if block == nil {
		return ""
	}
	switch block.kind {
	case activityBlockTaskMonitor:
		return taskMonitorInlineText(block.entries, true)
	default:
		parts := make([]string, 0, 2)
		if fileCount := len(uniqueReadPaths(block.entries)); fileCount > 0 {
			parts = append(parts, fmt.Sprintf("%d files", fileCount))
		}
		if searchCount := countActivityCalls(block.entries, "SEARCH"); searchCount > 0 {
			parts = append(parts, fmt.Sprintf("%d searches", searchCount))
		}
		if listCount := countActivityCalls(block.entries, "LIST"); listCount > 0 && len(parts) == 0 {
			parts = append(parts, fmt.Sprintf("%d paths", listCount))
		}
		if globCount := countActivityCalls(block.entries, "GLOB"); globCount > 0 && len(parts) == 0 {
			parts = append(parts, fmt.Sprintf("%d patterns", globCount))
		}
		if len(parts) == 0 && len(block.entries) > 0 {
			parts = append(parts, fmt.Sprintf("%d actions", len(block.entries)))
		}
		if len(parts) == 0 {
			return "Explored"
		}
		return "Explored " + strings.Join(parts, ", ")
	}
}

func summarizeTaskMonitorAction(verb string, count int) string {
	verb = strings.TrimSpace(verb)
	if verb == "" {
		return ""
	}
	if count <= 0 {
		count = 1
	}
	return fmt.Sprintf("%s %d tasks", verb, count)
}

func taskMonitorInlineText(entries []activityEntry, finalized bool) string {
	parts := taskWaitSummaryParts(entries, finalized)
	parts = append(parts, taskWriteSummaryParts(entries)...)
	if cancelCount := countTaskActions(entries, "cancel"); cancelCount > 0 {
		verb := "Cancelling"
		if finalized {
			verb = "Cancelled"
		}
		parts = append(parts, summarizeTaskMonitorAction(verb, cancelCount))
	}
	if statusCount := countTaskActions(entries, "status"); statusCount > 0 {
		verb := "Checking"
		if finalized {
			verb = "Checked"
		}
		parts = append(parts, summarizeTaskMonitorAction(verb, statusCount))
	}
	if listCount := countTaskActions(entries, "list"); listCount > 0 && len(parts) == 0 {
		verb := "Listing"
		if finalized {
			verb = "Listed"
		}
		parts = append(parts, summarizeTaskMonitorAction(verb, listCount))
	}
	return strings.Join(parts, ", ")
}

func taskWriteSummaryParts(entries []activityEntry) []string {
	parts := make([]string, 0, 2)
	for _, entry := range entries {
		if !strings.EqualFold(entry.tool, "TASK") || !strings.EqualFold(entry.action, "write") {
			continue
		}
		detail := strings.TrimSpace(entry.raw)
		if detail == "" {
			parts = append(parts, "SEND")
			continue
		}
		parts = append(parts, "SEND "+detail)
	}
	return parts
}

func parseTaskMonitorSummaryLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "▸ ") {
		return "", false
	}
	body := strings.TrimSpace(strings.TrimPrefix(trimmed, "▸ "))
	if body == "" {
		return "", false
	}
	parts := strings.Split(body, ", ")
	if len(parts) == 0 {
		return "", false
	}
	for _, part := range parts {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			return "", false
		}
		switch fields[0] {
		case "Waited", "Waiting", "Checked", "Checking", "Cancelled", "Cancelling", "Listed", "Listing", "SEND":
		default:
			return "", false
		}
	}
	return body, true
}

func mergeTaskMonitorSummaryTexts(previous string, current string) string {
	previous = strings.TrimSpace(previous)
	current = strings.TrimSpace(current)
	switch {
	case previous == "":
		return current
	case current == "":
		return previous
	default:
		return previous + ", " + current
	}
}

func taskWaitSummaryParts(entries []activityEntry, finalized bool) []string {
	parts := make([]string, 0, 4)
	pending := make([]int, 0, 4)
	for _, entry := range entries {
		if !strings.EqualFold(entry.tool, "TASK") || !strings.EqualFold(entry.action, "wait") {
			continue
		}
		if !entry.result {
			waitMS := entry.waitMS
			if waitMS <= 0 {
				waitMS = defaultActivityWaitMS
			}
			pending = append(pending, waitMS)
			continue
		}
		waitMS := entry.waitMS
		if len(pending) > 0 {
			requested := pending[0]
			pending = pending[1:]
			if waitMS <= 0 {
				waitMS = requested
			}
		}
		if waitMS <= 0 {
			waitMS = defaultActivityWaitMS
		}
		part := "Waited " + friendlyActivityWaitLabel(waitMS)
		if state := terminalTaskWaitState(entry.raw); state != "" {
			part += " (" + state + ")"
		}
		parts = append(parts, part)
	}
	for _, waitMS := range pending {
		if waitMS <= 0 {
			waitMS = defaultActivityWaitMS
		}
		verb := "Waiting"
		if finalized {
			verb = "Waited"
		}
		parts = append(parts, verb+" "+friendlyActivityWaitLabel(waitMS))
	}
	return parts
}

func terminalTaskWaitState(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "completed":
		return "Completed"
	case "failed":
		return "Failed"
	case "interrupted":
		return "Interrupted"
	case "cancelled", "canceled":
		return "Cancelled"
	case "terminated":
		return "Terminated"
	default:
		return ""
	}
}

func parseActivityLine(line string) (activityBlockKind, activityEntry, bool) {
	tool, remainder, result, ok := parseToolLogLine(line)
	if !ok {
		return "", activityEntry{}, false
	}
	switch tool {
	case "READ", "SEARCH", "LIST", "GLOB":
		if tool == "LIST" && looksLikeTaskList(remainder, result) {
			return activityBlockTaskMonitor, parseTaskListEntry(remainder, result), true
		}
		return activityBlockExploration, parseExplorationEntry(tool, remainder, result), true
	case "WAIT", "WAITED":
		return activityBlockTaskMonitor, parseTaskWaitEntry(tool, remainder, result), true
	case "CHECK":
		return activityBlockTaskMonitor, parseTaskStatusEntry(remainder, result), true
	case "CANCEL", "CANCELLED":
		return activityBlockTaskMonitor, parseTaskCancelEntry(remainder, result), true
	case "TASK":
		return parseRawTaskActivityEntry(remainder, result)
	default:
		return "", activityEntry{}, false
	}
}

func parseToolLogLine(line string) (tool string, remainder string, result bool, ok bool) {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "▸ "):
		trimmed = strings.TrimSpace(trimmed[len("▸ "):])
	case strings.HasPrefix(trimmed, "✓ "):
		trimmed = strings.TrimSpace(trimmed[len("✓ "):])
		result = true
	case strings.HasPrefix(trimmed, "! "):
		trimmed = strings.TrimSpace(trimmed[len("! "):])
		result = true
	case strings.HasPrefix(trimmed, "? "):
		return "", "", false, false
	default:
		return "", "", false, false
	}
	if trimmed == "" {
		return "", "", false, false
	}
	parts := strings.SplitN(trimmed, " ", 2)
	tool = strings.ToUpper(strings.TrimSpace(parts[0]))
	if len(parts) == 2 {
		remainder = strings.TrimSpace(parts[1])
	}
	return tool, remainder, result, true
}

func parseExplorationEntry(tool string, remainder string, result bool) activityEntry {
	entry := activityEntry{tool: strings.ToUpper(strings.TrimSpace(tool)), raw: remainder, result: result}
	switch entry.tool {
	case "READ":
		entry.path = strings.TrimSpace(remainder)
	case "SEARCH":
		entry.path, entry.query = splitPathAndQuery(remainder)
	case "LIST":
		entry.path = strings.TrimSpace(remainder)
	case "GLOB":
		entry.query = extractBraceValue(remainder, "pattern")
		if entry.query == "" {
			entry.query = strings.TrimSpace(remainder)
		}
	}
	return entry
}

func parseTaskWaitEntry(_ string, remainder string, result bool) activityEntry {
	return activityEntry{
		tool:   "TASK",
		action: "wait",
		raw:    remainder,
		waitMS: parseFriendlyWaitMS(remainder),
		result: result,
	}
}

func parseTaskStatusEntry(remainder string, result bool) activityEntry {
	return activityEntry{
		tool:   "TASK",
		action: "status",
		raw:    remainder,
		result: result,
	}
}

func parseTaskCancelEntry(remainder string, result bool) activityEntry {
	return activityEntry{
		tool:   "TASK",
		action: "cancel",
		raw:    remainder,
		result: result,
	}
}

func parseTaskListEntry(remainder string, result bool) activityEntry {
	return activityEntry{
		tool:   "TASK",
		action: "list",
		raw:    remainder,
		result: result,
	}
}

func parseTaskWriteEntry(remainder string, result bool) activityEntry {
	trimmed := strings.TrimSpace(remainder)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "write "):
		trimmed = strings.TrimSpace(trimmed[len("write"):])
	case lower == "write":
		trimmed = ""
	}
	return activityEntry{
		tool:   "TASK",
		action: "write",
		raw:    trimmed,
		result: result,
	}
}

type activityDisplayEntry struct {
	verb   string
	detail string
}

func buildActivityDisplayEntries(entries []activityEntry) []activityDisplayEntry {
	out := make([]activityDisplayEntry, 0, len(entries))
	type pendingActivityEntry struct {
		displayIndex int
		call         activityEntry
	}
	pending := map[string][]pendingActivityEntry{}
	for _, entry := range entries {
		key := activityEntryMergeKey(entry)
		if !entry.result {
			verb, detail := activityEntryDisplay(entry)
			out = append(out, activityDisplayEntry{verb: verb, detail: detail})
			if key != "" {
				pending[key] = append(pending[key], pendingActivityEntry{
					displayIndex: len(out) - 1,
					call:         entry,
				})
			}
			continue
		}
		if key != "" && len(pending[key]) > 0 {
			ref := pending[key][0]
			pending[key] = pending[key][1:]
			if merged, ok := mergeActivityEntries(ref.call, entry); ok {
				out[ref.displayIndex] = merged
				continue
			}
		}
		verb, detail := activityEntryDisplay(entry)
		out = append(out, activityDisplayEntry{verb: verb, detail: detail})
	}
	return out
}

func activityEntryMergeKey(entry activityEntry) string {
	switch strings.ToUpper(strings.TrimSpace(entry.tool)) {
	case "READ", "SEARCH", "LIST", "GLOB":
		return strings.ToUpper(strings.TrimSpace(entry.tool)) + ":" + strings.ToUpper(strings.TrimSpace(entry.action))
	default:
		return ""
	}
}

func mergeActivityEntries(call activityEntry, result activityEntry) (activityDisplayEntry, bool) {
	if call.result || !result.result {
		return activityDisplayEntry{}, false
	}
	if !strings.EqualFold(call.tool, result.tool) || !strings.EqualFold(call.action, result.action) {
		return activityDisplayEntry{}, false
	}
	switch strings.ToUpper(strings.TrimSpace(call.tool)) {
	case "READ", "SEARCH", "LIST", "GLOB":
	default:
		return activityDisplayEntry{}, false
	}
	verb, detail := activityEntryDisplay(call)
	summary := strings.TrimSpace(result.raw)
	if summary == "" {
		return activityDisplayEntry{}, false
	}
	if detail != "" {
		detail += " " + summary
	} else {
		detail = summary
	}
	return activityDisplayEntry{verb: verb, detail: detail}, true
}

func parseRawTaskActivityEntry(remainder string, result bool) (activityBlockKind, activityEntry, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(remainder))
	switch {
	case parseFriendlyWaitMS(remainder) > 0:
		return activityBlockTaskMonitor, parseTaskWaitEntry("TASK", remainder, result), true
	case strings.HasPrefix(trimmed, "write "):
		return activityBlockTaskMonitor, parseTaskWriteEntry(remainder, result), true
	case trimmed == "write":
		return activityBlockTaskMonitor, parseTaskWriteEntry(remainder, result), true
	case strings.Contains(trimmed, "cancel"):
		return activityBlockTaskMonitor, parseTaskCancelEntry(remainder, result), true
	case strings.Contains(trimmed, "list"):
		return activityBlockTaskMonitor, parseTaskListEntry(remainder, result), true
	case trimmed != "":
		return activityBlockTaskMonitor, parseTaskStatusEntry(remainder, result), true
	default:
		return activityBlockTaskMonitor, parseTaskStatusEntry("task status", result), true
	}
}

func activityEntryDisplay(entry activityEntry) (verb string, detail string) {
	switch {
	case entry.tool == "TASK" && entry.action == "wait":
		if entry.result {
			if entry.waitMS > 0 {
				return "Waited", friendlyActivityWaitLabel(entry.waitMS)
			}
			if strings.TrimSpace(entry.raw) != "" {
				return "Status", strings.TrimSpace(entry.raw)
			}
			return "Waited", ""
		}
		if entry.waitMS > 0 {
			return "Waiting", friendlyActivityWaitLabel(entry.waitMS)
		}
		return "Waiting", friendlyActivityWaitLabel(defaultActivityWaitMS)
	case entry.tool == "TASK" && entry.action == "status":
		if entry.result {
			if strings.TrimSpace(entry.raw) != "" {
				return "Status", strings.TrimSpace(entry.raw)
			}
			return "Checked", "task status"
		}
		return "Checking", "task status"
	case entry.tool == "TASK" && entry.action == "cancel":
		if entry.result {
			return "Cancelled", "task"
		}
		return "Cancelling", "task"
	case entry.tool == "TASK" && entry.action == "write":
		return "SEND", strings.TrimSpace(entry.raw)
	case entry.tool == "TASK" && entry.action == "list":
		if entry.result {
			if strings.TrimSpace(entry.raw) != "" {
				return "Listed", strings.TrimSpace(entry.raw)
			}
			return "Listed", "tasks"
		}
		return "Listing", "tasks"
	case entry.tool == "READ":
		return "Read", firstNonEmptyTrim(entry.path, entry.raw)
	case entry.tool == "SEARCH":
		query := strings.TrimSpace(entry.query)
		if query != "" {
			return "Searched", "for " + query
		}
		if strings.TrimSpace(entry.raw) != "" {
			return "Found", strings.TrimSpace(entry.raw)
		}
		return "Searched", ""
	case entry.tool == "LIST":
		if entry.result {
			return "Listed", strings.TrimSpace(entry.raw)
		}
		return "Listed", firstNonEmptyTrim(entry.path, entry.raw)
	case entry.tool == "GLOB":
		if entry.result {
			return "Matched", strings.TrimSpace(entry.raw)
		}
		return "Globbed", firstNonEmptyTrim(entry.query, entry.raw)
	default:
		if entry.result {
			return "Updated", strings.TrimSpace(entry.raw)
		}
		return "Observed", strings.TrimSpace(entry.raw)
	}
}

func uniqueReadPaths(entries []activityEntry) []string {
	set := map[string]struct{}{}
	for _, entry := range entries {
		if entry.result || !strings.EqualFold(entry.tool, "READ") {
			continue
		}
		path := strings.TrimSpace(entry.path)
		if path == "" {
			path = strings.TrimSpace(entry.raw)
		}
		if path == "" {
			continue
		}
		set[path] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for path := range set {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func countActivityCalls(entries []activityEntry, tool string) int {
	count := 0
	for _, entry := range entries {
		if entry.result {
			continue
		}
		if strings.EqualFold(entry.tool, tool) {
			count++
		}
	}
	return count
}

func countTaskActions(entries []activityEntry, action string) int {
	count := 0
	for _, entry := range entries {
		if entry.result || !strings.EqualFold(entry.tool, "TASK") {
			continue
		}
		if strings.EqualFold(entry.action, action) {
			count++
		}
	}
	return count
}

func totalTaskWaitMS(entries []activityEntry) int {
	pending := make([]int, 0, 4)
	total := 0
	for _, entry := range entries {
		if !strings.EqualFold(entry.tool, "TASK") || !strings.EqualFold(entry.action, "wait") {
			continue
		}
		if !entry.result {
			waitMS := entry.waitMS
			if waitMS <= 0 {
				waitMS = defaultActivityWaitMS
			}
			pending = append(pending, waitMS)
			continue
		}
		if len(pending) == 0 {
			if entry.waitMS > 0 {
				total += entry.waitMS
			}
			continue
		}
		waitMS := pending[0]
		pending = pending[1:]
		if entry.waitMS > 0 {
			waitMS = entry.waitMS
		}
		total += waitMS
	}
	for _, waitMS := range pending {
		if waitMS > 0 {
			total += waitMS
		}
	}
	return total
}

func looksLikeTaskList(remainder string, result bool) bool {
	trimmed := strings.TrimSpace(strings.ToLower(remainder))
	if !result {
		return trimmed == ""
	}
	return strings.HasPrefix(trimmed, "listed ") && strings.Contains(trimmed, "task")
}

func splitPathAndQuery(remainder string) (string, string) {
	query := extractBraceValue(remainder, "query")
	if query == "" {
		query = extractBraceValue(remainder, "q")
	}
	before, _, _ := strings.Cut(remainder, "{")
	path := strings.TrimSpace(before)
	if path == "." {
		path = ""
	}
	return path, query
}

func extractBraceValue(input string, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	idx := strings.Index(strings.ToLower(input), "{"+strings.ToLower(key)+"=")
	if idx < 0 {
		idx = strings.Index(strings.ToLower(input), " "+strings.ToLower(key)+"=")
	}
	if idx < 0 {
		return ""
	}
	segment := input[idx:]
	eq := strings.Index(segment, "=")
	if eq < 0 {
		return ""
	}
	segment = segment[eq+1:]
	end := strings.IndexAny(segment, "}")
	if end < 0 {
		end = len(segment)
	}
	return strings.TrimSpace(segment[:end])
}

func parseFriendlyWaitMS(input string) int {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return 0
	}
	fields := strings.Fields(input)
	for i := range len(fields) {
		field := strings.TrimSpace(fields[i])
		if ms, ok := parseWaitToken(field); ok {
			return ms
		}
		if i+1 < len(fields) {
			if ms, ok := parseWaitPair(field, fields[i+1]); ok {
				return ms
			}
		}
	}
	return 0
}

func parseWaitPair(value string, unit string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	switch strings.TrimSpace(strings.ToLower(unit)) {
	case "s", "sec", "secs", "second", "seconds":
		return n * 1000, true
	case "ms", "msec", "millisecond", "milliseconds":
		return n, true
	default:
		return 0, false
	}
}

func parseWaitToken(token string) (int, bool) {
	token = strings.TrimSpace(strings.ToLower(token))
	if token == "" {
		return 0, false
	}
	if trimmed, ok := strings.CutSuffix(token, "ms"); ok {
		n, err := strconv.Atoi(trimmed)
		return n, err == nil
	}
	if trimmed, ok := strings.CutSuffix(token, "s"); ok {
		n, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, false
		}
		return int(n * 1000), true
	}
	return 0, false
}

func friendlyActivityWaitLabel(waitMS int) string {
	switch {
	case waitMS <= 0:
		return "0s"
	case waitMS%1000 == 0:
		return fmt.Sprintf("%d s", waitMS/1000)
	case waitMS < 1000:
		return fmt.Sprintf("%dms", waitMS)
	default:
		return fmt.Sprintf("%.1f s", float64(waitMS)/1000.0)
	}
}

func firstNonEmptyTrim(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
