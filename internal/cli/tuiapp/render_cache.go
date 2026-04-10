package tuiapp

import (
	"fmt"
	"hash"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
	"github.com/charmbracelet/x/ansi"
)

type viewportRenderEntry struct {
	blockID     string
	cacheKey    string
	styledLines []string
	plainLines  []string
	clickTokens []string
}

func (m *Model) rebuildViewportRenderCache(ctx BlockRenderContext) {
	oldEntries := make(map[string]viewportRenderEntry, len(m.viewportRenderEntries))
	for _, entry := range m.viewportRenderEntries {
		oldEntries[entry.blockID] = entry
	}

	nextEntries := make([]viewportRenderEntry, 0, m.doc.Len())
	for _, block := range m.doc.Blocks() {
		key := viewportBlockRenderKey(block, ctx)
		if cached, ok := oldEntries[block.BlockID()]; ok && cached.cacheKey == key {
			nextEntries = append(nextEntries, cached)
			continue
		}
		nextEntries = append(nextEntries, m.renderViewportEntry(block, key, ctx))
	}
	m.viewportRenderEntries = nextEntries
}

func (m *Model) renderViewportEntry(block Block, cacheKey string, ctx BlockRenderContext) viewportRenderEntry {
	styledLines, plainLines, clickTokens := m.wrapRenderedRowsForViewport(block, block.Render(ctx), ctx.Width)
	return viewportRenderEntry{
		blockID:     block.BlockID(),
		cacheKey:    cacheKey,
		styledLines: styledLines,
		plainLines:  plainLines,
		clickTokens: clickTokens,
	}
}

func (m *Model) wrapRenderedRowsForViewport(block Block, rawRows []RenderedRow, wrapWidth int) ([]string, []string, []string) {
	if wrapWidth <= 0 {
		wrapWidth = 1
	}
	styledLines := make([]string, 0, len(rawRows)+8)
	plainLines := make([]string, 0, len(rawRows)+8)
	clickTokens := make([]string, 0, len(rawRows)+8)

	for _, row := range rawRows {
		styledLine := m.adaptHistoryLineForViewport(row.Styled, wrapWidth)
		plainLine := strings.TrimRight(ansi.Strip(styledLine), " ")

		var wrappedStyled string
		var plainParts []string

		if row.PreWrapped {
			if graphemeWidth(plainLine) > wrapWidth {
				wrappedStyled = hardWrapDisplayLine(styledLine, wrapWidth)
				plainParts = normalizeWrappedPlainSegments(graphemeHardWrap(plainLine, wrapWidth))
			} else {
				wrappedStyled = styledLine
				plainParts = []string{plainLine}
			}
		} else {
			switch block.Kind() {
			case BlockAssistant, BlockReasoning:
				wrappedStyled = m.wrapNarrativeRowStyled(row, wrapWidth)
				plainParts = m.wrapNarrativeRowPlain(row, wrapWidth)
			case BlockMainACPTurn, BlockParticipantTurn:
				wrappedStyled = hardWrapDisplayLine(styledLine, wrapWidth)
				plainParts = normalizeWrappedPlainSegments(graphemeHardWrap(plainLine, wrapWidth))
			default:
				wrappedStyled = hardWrapDisplayLine(styledLine, wrapWidth)
				plainParts = normalizeWrappedPlainSegments(graphemeHardWrap(plainLine, wrapWidth))
			}
		}

		if wrappedStyled == "" {
			styledLines = append(styledLines, "")
			plainLines = append(plainLines, "")
			clickTokens = append(clickTokens, row.ClickToken)
			continue
		}

		sParts := strings.Split(wrappedStyled, "\n")
		if len(plainParts) != len(sParts) {
			plainParts = deriveViewportPlainLines(plainParts[:0], sParts)
		}
		styledLines = append(styledLines, sParts...)
		plainLines = append(plainLines, plainParts...)
		for range sParts {
			clickTokens = append(clickTokens, row.ClickToken)
		}
	}

	return styledLines, plainLines, clickTokens
}

func (m *Model) rebuildViewportLineCaches(ctx BlockRenderContext) {
	styledLines := make([]string, 0, 64)
	plainLines := make([]string, 0, 64)
	blockIDs := make([]string, 0, 64)
	clickTokens := make([]string, 0, 64)

	for _, entry := range m.viewportRenderEntries {
		styledLines = append(styledLines, entry.styledLines...)
		plainLines = append(plainLines, entry.plainLines...)
		clickTokens = append(clickTokens, entry.clickTokens...)
		for range entry.styledLines {
			blockIDs = append(blockIDs, entry.blockID)
		}
	}

	streamStyled, streamPlain, streamBlockIDs := m.renderStreamViewportLines(ctx)
	styledLines = append(styledLines, streamStyled...)
	plainLines = append(plainLines, streamPlain...)
	blockIDs = append(blockIDs, streamBlockIDs...)

	m.viewportStyledLines = append(m.viewportStyledLines[:0], styledLines...)
	m.viewportPlainLines = append(m.viewportPlainLines[:0], plainLines...)
	m.viewportBlockIDs = append(m.viewportBlockIDs[:0], blockIDs...)
	m.viewportClickTokens = append(m.viewportClickTokens[:0], clickTokens...)
}

func (m *Model) renderStreamViewportLines(ctx BlockRenderContext) ([]string, []string, []string) {
	if strings.TrimSpace(m.streamLine) == "" {
		return nil, nil, nil
	}

	wrapWidth := maxInt(1, ctx.Width)
	var styledLines []string
	var plainLines []string
	var blockIDs []string

	streamLines := strings.Split(m.streamLine, "\n")
	prevStyle := m.lastCommittedStyle
	for _, sl := range streamLines {
		style := tuikit.DetectLineStyleWithContext(sl, prevStyle)

		var wrappedStyled string
		var plainParts []string
		switch style {
		case tuikit.LineStyleAssistant, tuikit.LineStyleReasoning:
			segments := graphemeWordWrap(sl, wrapWidth)
			if len(segments) == 0 {
				wrappedStyled = ""
				plainParts = []string{""}
			} else {
				baseStyle := narrativeBodyStyle(style, m.theme)
				styledSegs := make([]string, len(segments))
				for j, seg := range segments {
					styledSegs[j] = renderInlineMarkdown(seg, baseStyle, m.theme)
				}
				wrappedStyled = strings.Join(styledSegs, "\n")
				plainParts = normalizeWrappedPlainSegments(segments)
			}
		default:
			colored := tuikit.ColorizeLogLine(sl, style, m.theme)
			wrappedStyled = hardWrapDisplayLine(colored, wrapWidth)
			plainParts = normalizeWrappedPlainSegments(graphemeHardWrap(sl, wrapWidth))
		}

		if wrappedStyled == "" {
			styledLines = append(styledLines, "")
			plainLines = append(plainLines, "")
			blockIDs = append(blockIDs, "")
		} else {
			sParts := strings.Split(wrappedStyled, "\n")
			if len(plainParts) != len(sParts) {
				plainParts = deriveViewportPlainLines(plainParts[:0], sParts)
			}
			styledLines = append(styledLines, sParts...)
			plainLines = append(plainLines, plainParts...)
			for range sParts {
				blockIDs = append(blockIDs, "")
			}
		}

		prevStyle = style
	}

	return styledLines, plainLines, blockIDs
}

type blockKeyBuilder struct {
	hash hash.Hash64
}

func newBlockKeyBuilder(kind BlockKind, ctx BlockRenderContext) *blockKeyBuilder {
	h := fnv.New64a()
	b := &blockKeyBuilder{hash: h}
	b.addString(string(kind))
	b.addInt(ctx.Width)
	b.addInt(ctx.TermWidth)
	b.addString(themeRenderCacheKey(ctx.Theme))
	return b
}

func (b *blockKeyBuilder) addString(v string) {
	_, _ = b.hash.Write([]byte(v))
	_, _ = b.hash.Write([]byte{0})
}

func (b *blockKeyBuilder) addBool(v bool) {
	if v {
		b.addString("1")
		return
	}
	b.addString("0")
}

func (b *blockKeyBuilder) addInt(v int) {
	b.addString(strconv.Itoa(v))
}

func (b *blockKeyBuilder) addTime(v time.Time) {
	if v.IsZero() {
		b.addString("0")
		return
	}
	b.addString(strconv.FormatInt(v.UnixNano(), 10))
}

func (b *blockKeyBuilder) String() string {
	return fmt.Sprintf("%x", b.hash.Sum64())
}

func viewportBlockRenderKey(block Block, ctx BlockRenderContext) string {
	builder := newBlockKeyBuilder(block.Kind(), ctx)

	switch b := block.(type) {
	case *TranscriptBlock:
		builder.addString(b.Raw)
		builder.addInt(int(b.Style))
		builder.addBool(b.PreStyled)
	case *UserNarrativeBlock:
		builder.addString(b.Raw)
	case *AssistantBlock:
		builder.addString(b.Actor)
		builder.addString(b.Raw)
		builder.addBool(b.Streaming)
		builder.addString(b.LastFinal)
	case *ReasoningBlock:
		builder.addString(b.Actor)
		builder.addString(b.Raw)
		builder.addBool(b.Streaming)
	case *ParticipantTurnBlock:
		builder.addString(b.SessionID)
		builder.addString(b.Actor)
		builder.addString(b.Status)
		builder.addBool(b.Expanded)
		builder.addTime(b.StartedAt)
		builder.addTime(b.EndedAt)
		writeExpandedTools(builder, b.ExpandedTools)
		writeSubagentEvents(builder, b.Events)
	case *DiffBlock:
		builder.addBool(b.Inline)
		builder.addBool(b.Expanded)
		builder.addString(b.Msg.Tool)
		builder.addString(b.Msg.Path)
		builder.addBool(b.Msg.Created)
		builder.addString(b.Msg.Hunk)
		builder.addString(b.Msg.Old)
		builder.addString(b.Msg.New)
		builder.addString(b.Msg.Preview)
		builder.addBool(b.Msg.Truncated)
	case *DividerBlock:
		builder.addString(b.Label)
		builder.addString(b.Text)
	case *BashPanelBlock:
		builder.addString(b.Key)
		builder.addString(b.ToolName)
		builder.addString(b.CallID)
		builder.addString(b.State)
		builder.addBool(b.Expanded)
		builder.addBool(b.Active)
		builder.addInt(b.VisibleLines)
		builder.addInt(b.ScrollOffset)
		builder.addBool(b.FollowTail)
		builder.addTime(b.ScrollbarVisibleUntil)
		builder.addTime(b.StartedAt)
		builder.addTime(b.UpdatedAt)
		builder.addTime(b.EndedAt)
		builder.addString(b.StdoutPartial)
		builder.addString(b.StderrPartial)
		builder.addString(b.AssistantPartial)
		builder.addString(b.ReasoningPartial)
		builder.addBool(b.SubagentFence)
		builder.addString(b.LastStream)
		writeToolOutputLines(builder, b.Lines)
	case *SubagentPanelBlock:
		builder.addString(b.SpawnID)
		builder.addString(b.AttachID)
		builder.addString(b.Agent)
		builder.addString(b.CallID)
		builder.addString(b.Status)
		builder.addBool(b.Expanded)
		builder.addInt(b.VisibleLines)
		builder.addInt(b.ScrollOffset)
		builder.addBool(b.FollowTail)
		builder.addBool(b.Terminal)
		builder.addBool(b.PinnedOpenByUser)
		builder.addTime(b.ScrollbarVisibleUntil)
		builder.addTime(b.StartedAt)
		builder.addInt(int(b.localEvtGen))
		writeSubagentEvents(builder, b.Events)
	case *ActivityBlock:
		builder.addString(string(b.BlockKindField))
		builder.addBool(b.Active)
		builder.addBool(b.Finalized)
		builder.addBool(b.Expanded)
		builder.addString(b.Summary)
		writeActivityEntries(builder, b.Entries)
		writeRenderedRows(builder, b.cachedRows)
	case *MainACPTurnBlock:
		builder.addString(b.SessionID)
		builder.addString(b.Status)
		builder.addTime(b.StartedAt)
		builder.addTime(b.EndedAt)
		writeExpandedTools(builder, b.ExpandedTools)
		writeSubagentEvents(builder, b.Events)
	case *WelcomeBlock:
		builder.addString(b.Version)
		builder.addString(b.Workspace)
		builder.addString(b.ModelName)
	}

	return builder.String()
}

func writeToolOutputLines(builder *blockKeyBuilder, lines []toolOutputLine) {
	builder.addInt(len(lines))
	for _, line := range lines {
		builder.addString(line.stream)
		builder.addString(line.text)
	}
}

func writeExpandedTools(builder *blockKeyBuilder, values map[string]bool) {
	if len(values) == 0 {
		builder.addInt(0)
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	builder.addInt(len(keys))
	for _, key := range keys {
		builder.addString(key)
		builder.addBool(values[key])
	}
}

func writeActivityEntries(builder *blockKeyBuilder, entries []activityEntry) {
	builder.addInt(len(entries))
	for _, entry := range entries {
		builder.addString(entry.tool)
		builder.addString(entry.action)
		builder.addString(entry.path)
		builder.addString(entry.query)
		builder.addString(entry.raw)
		builder.addInt(entry.waitMS)
		builder.addBool(entry.result)
	}
}

func writeRenderedRows(builder *blockKeyBuilder, rows []RenderedRow) {
	builder.addInt(len(rows))
	for _, row := range rows {
		builder.addString(row.Styled)
		builder.addString(row.Plain)
		builder.addBool(row.PreWrapped)
	}
}

func writeSubagentEvents(builder *blockKeyBuilder, events []SubagentEvent) {
	builder.addInt(len(events))
	for _, event := range events {
		builder.addInt(int(event.Kind))
		builder.addString(event.Text)
		builder.addString(event.CallID)
		builder.addString(event.Name)
		builder.addString(event.Args)
		builder.addString(event.Output)
		builder.addBool(event.Done)
		builder.addBool(event.Err)
		builder.addString(event.ApprovalTool)
		builder.addString(event.ApprovalCommand)
		builder.addInt(len(event.PlanEntries))
		for _, entry := range event.PlanEntries {
			builder.addString(entry.Content)
			builder.addString(entry.Status)
		}
	}
}
