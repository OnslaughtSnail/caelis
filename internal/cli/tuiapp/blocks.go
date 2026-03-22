package tuiapp

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuidiff"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
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
// AssistantBlock — streaming or finalized assistant answer.
// ---------------------------------------------------------------------------

type AssistantBlock struct {
	id        string
	Raw       string
	Streaming bool
	LastFinal string // dedup for duplicate final events
}

func NewAssistantBlock() *AssistantBlock {
	return &AssistantBlock{id: nextBlockID(), Streaming: true}
}

func (b *AssistantBlock) BlockID() string { return b.id }
func (b *AssistantBlock) Kind() BlockKind { return BlockAssistant }
func (b *AssistantBlock) Render(ctx BlockRenderContext) []RenderedRow {
	return renderAssistantRows(b.id, b.Raw, ctx)
}

func renderAssistantRows(blockID, raw string, ctx BlockRenderContext) []RenderedRow {
	nls, plainRows := buildNarrativeRows(raw)
	if len(plainRows) == 0 {
		plain := "* "
		styled := tuikit.ColorizeLogLine(plain, tuikit.LineStyleAssistant, ctx.Theme)
		return []RenderedRow{StyledPlainRow(blockID, plain, styled)}
	}
	rows := make([]RenderedRow, 0, len(plainRows))
	for i, pr := range plainRows {
		plain := pr
		if i == 0 {
			plain = "* " + pr
		}
		styled := styleNarrativeLine(plain, nls[i].Kind, tuikit.LineStyleAssistant, ctx.Theme)
		rows = append(rows, StyledPlainRow(blockID, plain, styled))
	}
	return rows
}

// ---------------------------------------------------------------------------
// ReasoningBlock — streaming or finalized reasoning/thinking.
// ---------------------------------------------------------------------------

type ReasoningBlock struct {
	id        string
	Raw       string
	Streaming bool
}

func NewReasoningBlock() *ReasoningBlock {
	return &ReasoningBlock{id: nextBlockID(), Streaming: true}
}

func (b *ReasoningBlock) BlockID() string { return b.id }
func (b *ReasoningBlock) Kind() BlockKind { return BlockReasoning }
func (b *ReasoningBlock) Render(ctx BlockRenderContext) []RenderedRow {
	return renderReasoningRows(b.id, b.Raw, ctx)
}

func renderReasoningRows(blockID, raw string, ctx BlockRenderContext) []RenderedRow {
	nls, plainRows := buildNarrativeRows(raw)
	if len(plainRows) == 0 {
		plain := "· "
		styled := tuikit.ColorizeLogLine(plain, tuikit.LineStyleReasoning, ctx.Theme)
		return []RenderedRow{StyledPlainRow(blockID, plain, styled)}
	}
	rows := make([]RenderedRow, 0, len(plainRows))
	for i, pr := range plainRows {
		prefix := "  "
		if i == 0 {
			prefix = "· "
		}
		plain := prefix + pr
		styled := styleNarrativeLine(plain, nls[i].Kind, tuikit.LineStyleReasoning, ctx.Theme)
		rows = append(rows, StyledPlainRow(blockID, plain, styled))
	}
	return rows
}

// ---------------------------------------------------------------------------
// DiffBlock — structured PATCH diff display.
// ---------------------------------------------------------------------------

type DiffBlock struct {
	id  string
	Msg tuievents.DiffBlockMsg
}

func NewDiffBlock(msg tuievents.DiffBlockMsg) *DiffBlock {
	return &DiffBlock{id: nextBlockID(), Msg: msg}
}

func (b *DiffBlock) BlockID() string { return b.id }
func (b *DiffBlock) Kind() BlockKind { return BlockDiff }
func (b *DiffBlock) Render(ctx BlockRenderContext) []RenderedRow {
	model := tuidiff.BuildModel(tuidiff.Payload{
		Tool:      b.Msg.Tool,
		Path:      b.Msg.Path,
		Created:   b.Msg.Created,
		Hunk:      b.Msg.Hunk,
		Old:       b.Msg.Old,
		New:       b.Msg.New,
		Preview:   b.Msg.Preview,
		Truncated: b.Msg.Truncated,
	})
	wrapWidth := maxInt(40, ctx.Width)
	lines := tuidiff.Render(model, wrapWidth, ctx.Theme)
	rows := make([]RenderedRow, len(lines))
	for i, line := range lines {
		rows[i] = StyledRow(b.id, line)
	}
	return rows
}

// ---------------------------------------------------------------------------
// DividerBlock — turn separator.
// ---------------------------------------------------------------------------

type DividerBlock struct {
	id   string
	Text string // pre-rendered divider text
}

func NewDividerBlock(text string) *DividerBlock {
	return &DividerBlock{id: nextBlockID(), Text: text}
}

func (b *DividerBlock) BlockID() string { return b.id }
func (b *DividerBlock) Kind() BlockKind { return BlockDivider }
func (b *DividerBlock) Render(_ BlockRenderContext) []RenderedRow {
	return []RenderedRow{StyledRow(b.id, b.Text)}
}

// ---------------------------------------------------------------------------
// BashPanelBlock — tool output panel (BASH, PATCH, etc. — not SPAWN).
// ---------------------------------------------------------------------------

type BashPanelBlock struct {
	id       string
	Key      string // lookup key (taskID or callID)
	ToolName string
	CallID   string
	State    string // running, completed, failed, etc.

	StartedAt    time.Time
	UpdatedAt    time.Time
	EndedAt      time.Time
	Expanded     bool
	Active       bool
	ScrollOffset int
	FollowTail   bool

	Lines         []toolOutputLine
	StdoutPartial string
	StderrPartial string

	// Delegate-like tool fields
	AssistantPartial string
	ReasoningPartial string
	SubagentFence    bool
	LastStream       string
}

func NewBashPanelBlock(toolName, callID string) *BashPanelBlock {
	now := time.Now()
	return &BashPanelBlock{
		id:         nextBlockID(),
		ToolName:   toolName,
		CallID:     callID,
		StartedAt:  now,
		UpdatedAt:  now,
		Expanded:   true,
		Active:     true,
		FollowTail: true,
	}
}

func (b *BashPanelBlock) BlockID() string { return b.id }
func (b *BashPanelBlock) Kind() BlockKind { return BlockBashPanel }

func (b *BashPanelBlock) Render(ctx BlockRenderContext) []RenderedRow {
	if isInlineBashPanel(b) && !b.Expanded {
		return nil
	}
	if !b.Expanded {
		header := b.renderCollapsedHeader(ctx)
		return []RenderedRow{StyledRow(b.id, header)}
	}
	content := b.currentLines()
	lines := b.renderPanelLines(ctx, content)
	rows := make([]RenderedRow, len(lines))
	for i, line := range lines {
		rows[i] = StyledRow(b.id, line)
	}
	return rows
}

func (b *BashPanelBlock) renderCollapsedHeader(ctx BlockRenderContext) string {
	tool := strings.ToUpper(strings.TrimSpace(b.ToolName))
	if tool == "" {
		tool = "TASK"
	}
	statusText, statusStyle := bashPanelStatus(b, ctx.Theme)
	header := ctx.Theme.KeyLabelStyle().Bold(true).Render("▶ "+tool) + " "
	if statusText != "" {
		header += statusStyle.Render(statusText) + " "
	}
	if age := formatToolOutputAge(b.elapsed()); age != "" {
		header += ctx.Theme.HelpHintTextStyle().Render(age)
	}
	return header
}

func (b *BashPanelBlock) currentLines() []toolOutputLine {
	content := append([]toolOutputLine(nil), b.Lines...)
	if partial := strings.TrimSpace(b.StdoutPartial); partial != "" {
		content = append(content, toolOutputLine{text: partial, stream: "stdout"})
	}
	if partial := strings.TrimSpace(b.StderrPartial); partial != "" && !isSpawnLikeTool(b.ToolName) {
		content = append(content, toolOutputLine{text: partial, stream: "stderr"})
	}
	if isSpawnLikeTool(b.ToolName) {
		if partial := formatSubagentPreviewText(b.ReasoningPartial, "reasoning"); partial != "" {
			content = append(content, toolOutputLine{text: partial, stream: "reasoning"})
		}
		if partial := formatSubagentPreviewText(b.AssistantPartial, "assistant"); partial != "" {
			content = append(content, toolOutputLine{text: partial, stream: "assistant"})
		}
		if partial := formatSubagentPreviewText(b.StderrPartial, "stderr"); partial != "" {
			content = append(content, toolOutputLine{text: partial, stream: "stderr"})
		}
	}
	// Filter empty
	filtered := content[:0]
	for _, line := range content {
		if strings.TrimSpace(line.text) == "" {
			continue
		}
		if isSpawnLikeTool(b.ToolName) {
			switch strings.ToLower(strings.TrimSpace(line.stream)) {
			case "assistant", "reasoning", "stderr":
			default:
				continue
			}
		}
		filtered = append(filtered, line)
	}
	content = filtered
	return content
}

func (b *BashPanelBlock) renderPanelLines(ctx BlockRenderContext, content []toolOutputLine) []string {
	boxWidth := maxInt(1, ctx.Width-4)
	contentWidth, lines, overflow := b.panelRenderLines(ctx, content, boxWidth)
	totalLines := len(lines)
	start, end, _ := panelScrollWindow(len(lines), toolOutputPreviewLines, b.ScrollOffset, b.FollowTail)
	lines = lines[start:end]
	if overflow {
		lines = addPanelScrollbar(lines, contentWidth, len(lines), start, totalLines, ctx.Theme)
	}
	borderColor := ctx.Theme.PanelBorder
	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(boxWidth)
	return strings.Split(boxStyle.Render(strings.Join(lines, "\n")), "\n")
}

func (b *BashPanelBlock) panelRenderLines(ctx BlockRenderContext, content []toolOutputLine, boxWidth int) (contentWidth int, lines []string, overflow bool) {
	baseWidth := maxInt(1, boxWidth-4)
	scrollWidth := maxInt(1, baseWidth-1)
	scrollLines := b.renderPanelInnerLines(ctx, content, scrollWidth)
	if len(scrollLines) > toolOutputPreviewLines {
		return scrollWidth, scrollLines, true
	}
	return baseWidth, b.renderPanelInnerLines(ctx, content, baseWidth), false
}

func (b *BashPanelBlock) renderPanelInnerLines(ctx BlockRenderContext, content []toolOutputLine, contentWidth int) []string {
	var lines []string

	emptyPlaceholder := ""
	if len(content) == 0 && !isSpawnLikeTool(b.ToolName) {
		emptyPlaceholder = "no output"
		if b.State == "waiting_input" {
			emptyPlaceholder = "waiting for input"
		}
	}

	if header := b.renderHeaderLine(ctx, contentWidth); header != "" {
		lines = append(lines, header)
	}

	if emptyPlaceholder != "" {
		lines = append(lines, ctx.Theme.HelpHintTextStyle().Width(contentWidth).Render("  "+emptyPlaceholder))
	}

	// Delegate-like tools still show the placeholder as a separate line.
	if len(content) == 0 && isSpawnLikeTool(b.ToolName) {
		noOut := ctx.Theme.HelpHintTextStyle().Render("no output")
		lines = append(lines, noOut)
	}

	for _, line := range content {
		text, prefix, style := b.renderOutputLine(ctx, line)
		availableTextWidth := maxInt(1, contentWidth-displayColumns(prefix))
		wrapped := []string{text}
		if isSpawnLikeTool(b.ToolName) {
			wrapped = wrapToolOutputText(text, availableTextWidth)
		} else if displayColumns(text) > availableTextWidth {
			if availableTextWidth == 1 {
				wrapped = []string{"…"}
			} else {
				wrapped = []string{sliceByDisplayColumns(text, 0, availableTextWidth-1) + "…"}
			}
		}
		for _, segment := range wrapped {
			lines = append(lines, style.Width(contentWidth).Render(prefix+segment))
			prefix = strings.Repeat(" ", displayColumns(prefix))
		}
	}
	return lines
}

func (b *BashPanelBlock) renderHeaderLine(ctx BlockRenderContext, width int) string {
	if width <= 0 {
		return ""
	}
	if isInlineBashPanel(b) {
		return ""
	}
	right := ctx.Theme.HelpHintTextStyle().Render(formatToolOutputAge(b.elapsed()))
	if isSpawnLikeTool(b.ToolName) {
		return composeStyledFooter(width, "", right)
	}
	tool := strings.ToUpper(strings.TrimSpace(b.ToolName))
	if tool == "" {
		tool = "TASK"
	}
	left := ctx.Theme.KeyLabelStyle().Bold(true).Render(tool)
	statusText, statusStyle := bashPanelStatus(b, ctx.Theme)
	if statusText != "" {
		left += " " + statusStyle.Render(statusText)
	}
	return composeStyledFooter(width, left, right)
}

func (b *BashPanelBlock) elapsed() time.Duration {
	if b == nil || b.StartedAt.IsZero() {
		return 0
	}
	if !b.EndedAt.IsZero() {
		return b.EndedAt.Sub(b.StartedAt)
	}
	return time.Since(b.StartedAt)
}

func isInlineBashPanel(b *BashPanelBlock) bool {
	if b == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(b.ToolName), "BASH")
}

func (b *BashPanelBlock) renderOutputLine(ctx BlockRenderContext, line toolOutputLine) (text string, prefix string, style lipgloss.Style) {
	text = tuikit.LinkifyText(strings.TrimSpace(line.text), ctx.Theme.LinkStyle())
	prefix = "  "
	style = lipgloss.NewStyle().Foreground(ctx.Theme.TextPrimary)
	if isSpawnLikeTool(b.ToolName) {
		return text, "  ", style
	}
	stream := strings.ToLower(strings.TrimSpace(line.stream))
	if stream == "stderr" {
		return text, "! ", ctx.Theme.ErrorStyle()
	}
	return text, prefix, style
}

func bashPanelStatus(b *BashPanelBlock, theme tuikit.Theme) (string, lipgloss.Style) {
	if b == nil {
		return "", theme.HelpHintTextStyle()
	}
	switch b.State {
	case "running":
		return "running", theme.AssistantStyle().Bold(true)
	case "waiting_approval":
		return "approval", theme.WarnStyle().Bold(true)
	case "waiting_input":
		return "input", theme.HelpHintTextStyle().Bold(true)
	case "completed":
		return "done", theme.HelpHintTextStyle()
	case "failed":
		return "failed", theme.ErrorStyle().Bold(true)
	case "interrupted":
		return "interrupted", theme.WarnStyle().Bold(true)
	case "cancelled", "canceled":
		return "cancelled", theme.WarnStyle().Bold(true)
	case "terminated":
		return "terminated", theme.WarnStyle().Bold(true)
	}
	return "", theme.HelpHintTextStyle()
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
	Done   bool
	Err    bool

	// Plan fields.
	PlanEntries []planEntryState

	// Approval fields (derived from context when status becomes waiting_approval).
	ApprovalTool    string
	ApprovalCommand string
}

type SubagentPanelBlock struct {
	id           string
	SpawnID      string
	AttachID     string
	Agent        string
	CallID       string
	Status       string // "running", "completed", "failed", "interrupted", "waiting_approval"
	StartedAt    time.Time
	Expanded     bool
	ScrollOffset int
	FollowTail   bool

	// Events is the chronological stream of child session events.
	Events []SubagentEvent
}

func NewSubagentPanelBlock(spawnID, attachID, agent, callID string) *SubagentPanelBlock {
	return &SubagentPanelBlock{
		id:         nextBlockID(),
		SpawnID:    spawnID,
		AttachID:   attachID,
		Agent:      agent,
		CallID:     callID,
		Status:     "running",
		StartedAt:  time.Now(),
		Expanded:   true,
		FollowTail: true,
	}
}

// AppendStreamChunk appends a streaming text chunk (assistant or reasoning).
// If the most recent event is the same kind, the chunk is concatenated;
// otherwise a new event is created, preserving chronological ordering.
func (b *SubagentPanelBlock) AppendStreamChunk(kind SubagentEventKind, chunk string) {
	if len(b.Events) > 0 {
		last := &b.Events[len(b.Events)-1]
		if last.Kind == kind {
			last.Text += chunk
			return
		}
	}
	b.Events = append(b.Events, SubagentEvent{Kind: kind, Text: chunk})
}

// UpdateToolCall creates or updates a tool call event identified by callID.
func (b *SubagentPanelBlock) UpdateToolCall(callID, toolName, args, stream, chunk string, final bool) {
	for i := range b.Events {
		e := &b.Events[i]
		if e.Kind == SEToolCall && e.CallID == callID {
			if chunk != "" {
				e.Output += chunk
			}
			if final {
				e.Done = true
			}
			if stream == "stderr" {
				e.Err = true
			}
			return
		}
	}
	b.Events = append(b.Events, SubagentEvent{
		Kind:   SEToolCall,
		Name:   toolName,
		CallID: callID,
		Args:   args,
		Output: chunk,
		Done:   final,
		Err:    stream == "stderr",
	})
}

// UpdatePlan appends a new plan event or coalesces with the last event if it
// is also a plan (rapid consecutive plan updates). This preserves the
// chronological interleaving: tool→plan→tool→plan shows two plan snapshots.
func (b *SubagentPanelBlock) UpdatePlan(entries []planEntryState) {
	if n := len(b.Events); n > 0 && b.Events[n-1].Kind == SEPlan {
		b.Events[n-1].PlanEntries = entries
		return
	}
	b.Events = append(b.Events, SubagentEvent{
		Kind:        SEPlan,
		PlanEntries: entries,
	})
}

// AddApprovalEvent appends an approval event or coalesces with the last event
// if it is also an approval (rapid consecutive status updates). This preserves
// the chronological interleaving for multiple approval cycles.
func (b *SubagentPanelBlock) AddApprovalEvent(tool, command string) {
	// Derive context from last unfinished tool call if not provided.
	if tool == "" {
		for i := len(b.Events) - 1; i >= 0; i-- {
			e := &b.Events[i]
			if e.Kind == SEToolCall && !e.Done {
				tool = e.Name
				command = e.Args
				break
			}
		}
	}
	// Coalesce with last event if it's also an approval (rapid update).
	if n := len(b.Events); n > 0 && b.Events[n-1].Kind == SEApproval {
		b.Events[n-1].ApprovalTool = tool
		b.Events[n-1].ApprovalCommand = command
		return
	}
	b.Events = append(b.Events, SubagentEvent{
		Kind:            SEApproval,
		ApprovalTool:    tool,
		ApprovalCommand: command,
	})
}

func (b *SubagentPanelBlock) BlockID() string { return b.id }
func (b *SubagentPanelBlock) Kind() BlockKind { return BlockSubagent }
func (b *SubagentPanelBlock) Render(ctx BlockRenderContext) []RenderedRow {
	if b == nil || !b.Expanded {
		return nil
	}
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
		baseWidth = ctx.TermWidth
	}
	if baseWidth <= 0 {
		baseWidth = 80
	}
	boxWidth := maxInt(20, baseWidth-4)
	contentWidth, lines, overflow := subagentPanelRenderLines(panel, ctx, boxWidth)
	totalLines := len(lines)
	start, end, _ := panelScrollWindow(len(lines), subagentOutputPreviewLines, panel.ScrollOffset, panel.FollowTail)
	lines = lines[start:end]
	if overflow {
		lines = addPanelScrollbar(lines, contentWidth, len(lines), start, totalLines, ctx.Theme)
	}
	boxStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(ctx.Theme.PanelBorder).
		Padding(0, 1).
		Width(boxWidth)
	return strings.Split(boxStyle.Render(strings.Join(lines, "\n")), "\n")
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
	var lines []string
	hasContent := false

	for _, ev := range panel.Events {
		switch ev.Kind {
		case SEPlan:
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
				lines = append(lines, ctx.Theme.TextStyle().Width(contentWidth).Render(icon+" "+strings.TrimSpace(pe.Content)))
			}
			hasContent = true

		case SEReasoning:
			if text := strings.TrimSpace(ev.Text); text != "" {
				for _, pl := range wrapToolOutputText(text, maxInt(1, contentWidth-2)) {
					lines = append(lines, ctx.Theme.ReasoningStyle().Width(contentWidth).Render("│ "+pl))
				}
				hasContent = true
			}

		case SEAssistant:
			if text := strings.TrimSpace(ev.Text); text != "" {
				for _, pl := range wrapToolOutputText(text, maxInt(1, contentWidth-2)) {
					lines = append(lines, ctx.Theme.TextStyle().Width(contentWidth).Render("* "+tuikit.LinkifyText(pl, ctx.Theme.LinkStyle())))
				}
				hasContent = true
			}

		case SEToolCall:
			header := "▸ " + strings.TrimSpace(ev.Name)
			if args := strings.TrimSpace(ev.Args); args != "" {
				header += " " + args
			}
			if ev.Done {
				header = "✓ " + strings.TrimSpace(ev.Name)
			}
			if ev.Err {
				header = "✗ " + strings.TrimSpace(ev.Name)
			}
			lines = append(lines, tuikit.ColorizeLogLine(header, tuikit.LineStyleTool, ctx.Theme))
			if output := strings.TrimSpace(ev.Output); output != "" {
				outLines := wrapToolOutputText(output, maxInt(1, contentWidth-4))
				for _, ol := range outLines {
					prefix := "  "
					style := ctx.Theme.HelpHintTextStyle()
					if ev.Err {
						prefix = "! "
						style = ctx.Theme.ErrorStyle()
					}
					lines = append(lines, style.Width(contentWidth).Render(prefix+tuikit.LinkifyText(ol, ctx.Theme.LinkStyle())))
				}
			}
			hasContent = true

		case SEApproval:
			approvalText := "⚠ waiting for user confirmation"
			if ev.ApprovalTool != "" {
				approvalText = fmt.Sprintf("⚠ approval needed: %s", ev.ApprovalTool)
				if ev.ApprovalCommand != "" {
					cmd := ev.ApprovalCommand
					if displayColumns(cmd) > contentWidth-20 {
						cmd = graphemeSlice(cmd, 0, maxInt(1, contentWidth-23)) + "..."
					}
					approvalText += " — " + cmd
				}
			}
			lines = append(lines, ctx.Theme.WarnStyle().Width(contentWidth).Render(approvalText))
			hasContent = true
		}
	}

	if !hasContent && panel.Status == "running" {
		lines = append(lines, ctx.Theme.HelpHintTextStyle().Width(contentWidth).Render("waiting for subagent output"))
	}
	switch strings.ToLower(strings.TrimSpace(panel.Status)) {
	case "waiting_approval":
		lines = append(lines, ctx.Theme.WarnStyle().Width(contentWidth).Render("waiting approval"))
	case "completed":
		lines = append(lines, ctx.Theme.HelpHintTextStyle().Width(contentWidth).Render("✓ completed"))
	case "failed":
		lines = append(lines, ctx.Theme.ErrorStyle().Width(contentWidth).Render("✗ failed"))
	case "interrupted":
		lines = append(lines, ctx.Theme.WarnStyle().Width(contentWidth).Render("⊘ interrupted"))
	}
	return lines
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

func addPanelScrollbar(lines []string, contentWidth, visible, offset, total int, theme tuikit.Theme) []string {
	if len(lines) == 0 || total <= visible {
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
		glyph := theme.ScrollbarTrackStyle().Render("│")
		if i >= thumbStart && i < thumbStart+thumbHeight {
			glyph = theme.ScrollbarThumbStyle().Render("█")
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
	if next < 0 {
		next = 0
	}
	if next > maxOffset {
		next = maxOffset
	}
	changed := next != current || *followTail != (next == maxOffset)
	*offset = next
	*followTail = next == maxOffset
	return changed
}

func (b *BashPanelBlock) scrollableLineCount(ctx BlockRenderContext) int {
	if b == nil || !b.Expanded {
		return 0
	}
	boxWidth := maxInt(1, ctx.Width-4)
	_, lines, _ := b.panelRenderLines(ctx, b.currentLines(), boxWidth)
	return len(lines)
}

func (b *BashPanelBlock) Scroll(delta int, ctx BlockRenderContext) bool {
	return scrollPanelState(&b.ScrollOffset, &b.FollowTail, b.scrollableLineCount(ctx), toolOutputPreviewLines, delta)
}

func (b *SubagentPanelBlock) scrollableLineCount(ctx BlockRenderContext) int {
	if b == nil || !b.Expanded {
		return 0
	}
	baseWidth := ctx.Width
	if baseWidth <= 0 {
		baseWidth = ctx.TermWidth
	}
	if baseWidth <= 0 {
		baseWidth = 80
	}
	boxWidth := maxInt(20, baseWidth-4)
	_, lines, _ := subagentPanelRenderLines(b, ctx, boxWidth)
	return len(lines)
}

func (b *SubagentPanelBlock) Scroll(delta int, ctx BlockRenderContext) bool {
	return scrollPanelState(&b.ScrollOffset, &b.FollowTail, b.scrollableLineCount(ctx), subagentOutputPreviewLines, delta)
}

// wrapTextLines splits text into lines and truncates to reasonable viewport limits.
func wrapTextLines(text string, width int) []string {
	raw := strings.Split(text, "\n")
	var result []string
	for _, line := range raw {
		line = strings.TrimRight(line, " \t\r")
		if displayColumns(line) > width {
			line = graphemeSlice(line, 0, width-3) + "..."
		}
		result = append(result, line)
	}
	return result
}

func subagentBlockHeader(panel *SubagentPanelBlock) string {
	agentName := strings.TrimSpace(panel.Agent)
	if agentName == "" {
		agentName = "self"
	}
	status := strings.TrimSpace(panel.Status)
	if status == "" {
		status = "running"
	}
	icon := "⟳"
	label := status
	switch status {
	case "waiting_approval":
		icon = "!"
		label = "waiting approval"
	case "completed":
		icon = "✓"
	case "failed":
		icon = "✗"
	case "interrupted":
		icon = "⊘"
	}
	return fmt.Sprintf("%s SPAWN(%s) %s", icon, agentName, label)
}

// ---------------------------------------------------------------------------
// ActivityBlock — folded exploration/task-monitor display.
// ---------------------------------------------------------------------------

type ActivityBlock struct {
	id             string
	BlockKindField activityBlockKind
	Active         bool
	Finalized      bool
	Entries        []activityEntry
	Summary        string
	// cachedRows is updated by Model.syncActivityBlockRender().
	// Activity rendering depends on Model animation state (runningTick),
	// so the block caches pre-rendered output.
	cachedRows []RenderedRow
}

func NewActivityBlock(kind activityBlockKind) *ActivityBlock {
	return &ActivityBlock{
		id:             nextBlockID(),
		BlockKindField: kind,
		Active:         true,
	}
}

func (b *ActivityBlock) BlockID() string { return b.id }
func (b *ActivityBlock) Kind() BlockKind { return BlockActivity }
func (b *ActivityBlock) Render(_ BlockRenderContext) []RenderedRow {
	return b.cachedRows
}

// toFoldedState creates a foldedActivityBlockState for compatibility with
// existing rendering methods in model_activity.go.
func (b *ActivityBlock) toFoldedState() *foldedActivityBlockState {
	return &foldedActivityBlockState{
		kind:      b.BlockKindField,
		active:    b.Active,
		finalized: b.Finalized,
		entries:   b.Entries,
		summary:   b.Summary,
	}
}

// ---------------------------------------------------------------------------
// WelcomeBlock — welcome card.
// ---------------------------------------------------------------------------

type WelcomeBlock struct {
	id        string
	Version   string
	Workspace string
	ModelName string
}

func NewWelcomeBlock(version, workspace, modelName string) *WelcomeBlock {
	return &WelcomeBlock{
		id:        nextBlockID(),
		Version:   version,
		Workspace: workspace,
		ModelName: modelName,
	}
}

func (b *WelcomeBlock) BlockID() string { return b.id }
func (b *WelcomeBlock) Kind() BlockKind { return BlockWelcome }
func (b *WelcomeBlock) Render(ctx BlockRenderContext) []RenderedRow {
	return renderWelcomeRows(b, ctx)
}

func renderWelcomeRows(w *WelcomeBlock, ctx BlockRenderContext) []RenderedRow {
	versionText := strings.TrimSpace(w.Version)
	if versionText == "" {
		versionText = "unknown"
	}
	versionLabel := versionText
	if !strings.HasPrefix(strings.ToLower(versionText), "v") {
		versionLabel = "v" + versionText
	}
	workspace := strings.TrimSpace(w.Workspace)
	if workspace == "" {
		workspace = "."
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		workspace = strings.Replace(workspace, home, "~", 1)
	}
	modelAlias := strings.TrimSpace(w.ModelName)
	if modelAlias == "" {
		modelAlias = "not configured (/connect)"
	}
	prefix := lipgloss.NewStyle().Bold(true).Foreground(ctx.Theme.Accent).Render(">_")
	title := lipgloss.NewStyle().Bold(true).Foreground(ctx.Theme.PanelTitle).Render("CAELIS")
	version := lipgloss.NewStyle().Foreground(ctx.Theme.TextSecondary).Render("(" + versionLabel + ")")
	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(ctx.Theme.Info).Width(10)
	valueStyle := lipgloss.NewStyle().Foreground(ctx.Theme.TextPrimary)
	tipValueStyle := lipgloss.NewStyle().Foreground(ctx.Theme.TextSecondary)
	titleLine := prefix + " " + title + " " + version
	modelLine := labelStyle.Render("model:") + " " + valueStyle.Render(modelAlias)
	workspaceLine := labelStyle.Render("workspace:") + " " + valueStyle.Render(workspace)
	tipLine := labelStyle.Render("tip:") + " " + tipValueStyle.Render("type / for command list")
	body := strings.Join([]string{titleLine, "", modelLine, workspaceLine, tipLine}, "\n")
	vpWidth := maxInt(1, ctx.Width)
	frame := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(ctx.Theme.PanelBorder).
		Foreground(ctx.Theme.TextPrimary).
		Width(maxInt(30, minInt(72, maxInt(30, vpWidth-6)))).
		Padding(0, 2).
		Margin(1, 0, 1, 1).
		Render(body)
	frameLines := strings.Split(frame, "\n")
	rows := make([]RenderedRow, len(frameLines))
	for i, line := range frameLines {
		rows[i] = StyledRow(w.id, line)
	}
	return rows
}
