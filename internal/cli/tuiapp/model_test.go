package tuiapp

import (
	"fmt"
	"image/color"
	"net/url"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

func keyPress(code rune, mods ...tea.KeyMod) tea.KeyPressMsg {
	var mod tea.KeyMod
	for _, one := range mods {
		mod |= one
	}
	key := tea.Key{Code: code, Mod: mod}
	if code == tea.KeySpace {
		key.Text = " "
	}
	return tea.KeyPressMsg(key)
}

func keyRelease(code rune, mods ...tea.KeyMod) tea.KeyReleaseMsg {
	var mod tea.KeyMod
	for _, one := range mods {
		mod |= one
	}
	key := tea.Key{Code: code, Mod: mod}
	if code == tea.KeySpace {
		key.Text = " "
	}
	return tea.KeyReleaseMsg(key)
}

func keyText(text string) tea.KeyPressMsg {
	key := tea.Key{Text: text, Code: tea.KeyExtended}
	runes := []rune(text)
	if len(runes) == 1 {
		key.Code = runes[0]
	}
	return tea.KeyPressMsg(key)
}

func renderModel(m *Model) string {
	return m.View().Content
}

func stripModelView(m *Model) string {
	return ansi.Strip(renderModel(m))
}

func mouseClick(x int, y int, button tea.MouseButton) tea.MouseClickMsg {
	return tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: button})
}

func mouseRelease(x int, y int, button tea.MouseButton) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: button})
}

func mouseWheel(x int, y int, button tea.MouseButton) tea.MouseWheelMsg {
	return tea.MouseWheelMsg(tea.Mouse{X: x, Y: y, Button: button})
}

func mouseMotion(x int, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg(tea.Mouse{X: x, Y: y})
}

func driveBashPanelCollapse(m *Model, panel *BashPanelBlock) {
	if m == nil || panel == nil || panel.CollapseAt.IsZero() {
		return
	}
	collapseFor := panel.CollapseFor
	if collapseFor <= 0 {
		collapseFor = inlinePanelCollapseDuration
	}
	_, _ = m.Update(frameTickMsg{at: panel.CollapseAt})
	_, _ = m.Update(frameTickMsg{at: panel.CollapseAt.Add(collapseFor)})
}

func driveSubagentPanelCollapse(m *Model, panel *SubagentPanelBlock) {
	if m == nil || panel == nil || panel.CollapseAt.IsZero() {
		return
	}
	collapseFor := panel.CollapseFor
	if collapseFor <= 0 {
		collapseFor = inlinePanelCollapseDuration
	}
	_, _ = m.Update(frameTickMsg{at: panel.CollapseAt})
	_, _ = m.Update(frameTickMsg{at: panel.CollapseAt.Add(collapseFor)})
}

func TestMentionQueryAtCursor(t *testing.T) {
	input := []rune("check @kernel/to")
	start, end, query, ok := mentionQueryAtCursor(input, len(input))
	if !ok {
		t.Fatal("expected mention query")
	}
	if start < 0 || end <= start {
		t.Fatalf("invalid span: %d..%d", start, end)
	}
	if query != "kernel/to" {
		t.Fatalf("unexpected query %q", query)
	}
}

func TestResumeQueryAtCursor(t *testing.T) {
	query, ok := resumeQueryAtCursor([]rune("/resume abc"), len([]rune("/resume abc")))
	if !ok {
		t.Fatal("expected resume query")
	}
	if query != "abc" {
		t.Fatalf("unexpected query %q", query)
	}
	_, ok = resumeQueryAtCursor([]rune("/res"), len([]rune("/res")))
	if ok {
		t.Fatal("did not expect resume query for /res")
	}
}

func TestSlashArgQueryAtCursor(t *testing.T) {
	cmd, query, ok := slashArgQueryAtCursor([]rune("/model gpt"), len([]rune("/model gpt")))
	if !ok {
		t.Fatal("expected slash-arg query for partial /model subcommand")
	}
	if cmd != "model" || query != "gpt" {
		t.Fatalf("unexpected partial model subcommand parse: cmd=%q query=%q", cmd, query)
	}
	cmd, query, ok = slashArgQueryAtCursor([]rune("/model del"), len([]rune("/model del")))
	if !ok {
		t.Fatal("expected model subcommand query")
	}
	if cmd != "model" || query != "del" {
		t.Fatalf("unexpected model subcommand parse: cmd=%q query=%q", cmd, query)
	}
	_, _, ok = slashArgQueryAtCursor([]rune("/model del "), len([]rune("/model del ")))
	if ok {
		t.Fatal("did not expect picker parse for /model del")
	}
	cmd, query, ok = slashArgQueryAtCursor([]rune("/model use xiaomi "), len([]rune("/model use xiaomi ")))
	if !ok {
		t.Fatal("expected model reasoning picker parse")
	}
	if cmd != "model use xiaomi" || query != "" {
		t.Fatalf("unexpected model reasoning parse: cmd=%q query=%q", cmd, query)
	}
	_, _, ok = slashArgQueryAtCursor([]rune("/model"), len([]rune("/model")))
	if ok {
		t.Fatal("did not expect slash-arg query without trailing space")
	}
	_, _, ok = slashArgQueryAtCursor([]rune("/mouse capture"), len([]rune("/mouse capture")))
	if ok {
		t.Fatal("did not expect slash-arg query for removed /mouse command")
	}
	cmd, query, ok = slashArgQueryAtCursor([]rune("/agent add cod"), len([]rune("/agent add cod")))
	if !ok {
		t.Fatal("expected slash-arg query for /agent add builtin")
	}
	if cmd != "agent add" || query != "cod" {
		t.Fatalf("unexpected agent add parse: cmd=%q query=%q", cmd, query)
	}
	cmd, query, ok = slashArgQueryAtCursor([]rune("/agent rm claude"), len([]rune("/agent rm claude")))
	if !ok {
		t.Fatal("expected slash-arg query for /agent rm configured agent")
	}
	if cmd != "agent rm" || query != "claude" {
		t.Fatalf("unexpected agent rm parse: cmd=%q query=%q", cmd, query)
	}
	_, _, ok = slashArgQueryAtCursor([]rune("/agent list "), len([]rune("/agent list ")))
	if ok {
		t.Fatal("did not expect picker parse for /agent list")
	}
}

func TestSlashCommandQueryAtCursor(t *testing.T) {
	query, ok := slashCommandQueryAtCursor([]rune("/res"), len([]rune("/res")))
	if !ok {
		t.Fatal("expected slash-command query")
	}
	if query != "res" {
		t.Fatalf("unexpected query %q", query)
	}
	_, ok = slashCommandQueryAtCursor([]rune("/resume s-1"), len([]rune("/resume s-1")))
	if ok {
		t.Fatal("did not expect slash-command query with args")
	}
}

func TestCurrentInputGhostHint_ForSlashCommand(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"model", "status"},
		ExecuteLine: noopExecute,
	})
	resizeModel(m)
	typeRunes(m, "/mo")
	if got := m.currentInputGhostHint(); got != "del" {
		t.Fatalf("expected ghost hint 'del', got %q", got)
	}
}

func TestCurrentInputGhostHint_ForModelAlias(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			switch command {
			case "model":
				return []SlashArgCandidate{{Value: "use", Display: "use"}}, nil
			case "model use":
				return []SlashArgCandidate{{Value: "xiaomi/mimo-v2-flash", Display: "xiaomi/mimo-v2-flash"}}, nil
			default:
				return nil, nil
			}
		},
	})
	resizeModel(m)
	typeRunes(m, "/model use xiao")
	if got := m.currentInputGhostHint(); got != "mi/mimo-v2-flash" {
		t.Fatalf("expected model alias ghost hint, got %q", got)
	}
}

func TestCurrentInputGhostHint_ForModelActionPrefix(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			if command != "model" {
				return nil, nil
			}
			return []SlashArgCandidate{{Value: "use", Display: "use"}}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/model u")
	if got := m.currentInputGhostHint(); got != "se" {
		t.Fatalf("expected model action ghost hint, got %q", got)
	}
}

func TestCurrentInputGhostHint_ForResume(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		ResumeComplete: func(_ string, _ int) ([]ResumeCandidate, error) {
			return []ResumeCandidate{{SessionID: "s-123", Prompt: "demo", Age: "1m"}}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/resume ")
	if got := m.currentInputGhostHint(); got != "s-123" {
		t.Fatalf("expected resume ghost hint, got %q", got)
	}
}

func TestDefaultKeyMapUsesWSLImagePasteShortcut(t *testing.T) {
	keyMap := defaultKeyMap(true)
	if got := keyMap.ImagePaste.Keys(); len(got) != 1 || got[0] != "ctrl+alt+v" {
		t.Fatalf("expected WSL image paste shortcut ctrl+alt+v, got %#v", got)
	}
	if got := keyMap.TextPaste.Keys(); len(got) != 3 || got[0] != "ctrl+v" {
		t.Fatalf("expected WSL text paste fallbacks to start with ctrl+v, got %#v", got)
	}
}

func TestRenderHelpShowsMinimalFooterHints(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.keys = defaultKeyMap(true)

	bindings := m.currentFooterHelp()
	help := ansi.Strip(m.help.FullHelpView(bindings.FullHelp()))
	for _, want := range []string{"shift+tab", "mode"} {
		if !strings.Contains(help, want) {
			t.Fatalf("expected minimal footer help to include %q, got %q", want, help)
		}
	}
	if strings.Contains(help, "ctrl+o") {
		t.Fatalf("did not expect footer help to include alias key, got %q", help)
	}
	for _, unwanted := range []string{"enter", "history"} {
		if strings.Contains(help, unwanted) {
			t.Fatalf("did not expect verbose footer help to include %q, got %q", unwanted, help)
		}
	}
}

func TestRenderInputBar_ShowsGhostHintWithoutCursor(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"model", "status"},
		ExecuteLine: noopExecute,
	})
	resizeModel(m)
	typeRunes(m, "/mo")

	line := ansi.Strip(m.renderInputBar())
	if !strings.Contains(line, "/model") {
		t.Fatalf("expected ghost completion in input bar, got %q", line)
	}
	if strings.Contains(line, "█") {
		t.Fatalf("expected ghost render without cursor glyph, got %q", line)
	}
}

func TestRenderInputBar_LeftAlignsPrompt(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)

	line := ansi.Strip(m.renderInputBar())
	if !strings.HasPrefix(line, strings.Repeat(" ", inputHorizontalInset)+"> ") {
		t.Fatalf("expected left-aligned prompt, got %q", line)
	}
}

func TestPlanUpdateRendersPlanDrawer(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)
	if _, cmd := m.Update(tuievents.PlanUpdateMsg{
		Entries: []tuievents.PlanEntry{
			{Content: "Inspect repo", Status: "completed"},
			{Content: "Implement fix", Status: "in_progress"},
			{Content: "Run tests", Status: "pending"},
		},
	}); cmd != nil {
		_ = cmd
	}
	got := stripModelView(m)
	if !strings.Contains(got, "✔ Inspect repo") || !strings.Contains(got, "☐ Implement fix") {
		t.Fatalf("expected plan drawer in view, got %q", got)
	}
	if strings.Contains(got, "1. ") || strings.Contains(got, "2. ") {
		t.Fatalf("expected plan drawer without numeric prefixes, got %q", got)
	}
	if !strings.Contains(got, "────") {
		t.Fatalf("expected drawer top boundary in view, got %q", got)
	}
}

func TestPlanUpdateHidesDrawerWhenAllCompleted(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)
	_, _ = m.Update(tuievents.PlanUpdateMsg{
		Entries: []tuievents.PlanEntry{
			{Content: "Inspect repo", Status: "in_progress"},
			{Content: "Run tests", Status: "pending"},
		},
	})
	before := stripModelView(m)
	if !strings.Contains(before, "☐ Inspect repo") {
		t.Fatalf("expected active plan drawer before completion, got %q", before)
	}
	_, _ = m.Update(tuievents.PlanUpdateMsg{
		Entries: []tuievents.PlanEntry{
			{Content: "Inspect repo", Status: "completed"},
			{Content: "Run tests", Status: "completed"},
		},
	})
	after := stripModelView(m)
	if strings.Contains(after, "✔ Inspect repo") || strings.Contains(after, "☐ Inspect repo") {
		t.Fatalf("expected completed plan drawer to collapse, got %q", after)
	}
}

func TestPlanDrawerKeepsPlanOrderAndShowsWindowAroundActiveItem(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)
	_, _ = m.Update(tuievents.PlanUpdateMsg{
		Entries: []tuievents.PlanEntry{
			{Content: "Done one", Status: "completed"},
			{Content: "Pending one", Status: "pending"},
			{Content: "In progress", Status: "in_progress"},
			{Content: "Pending two", Status: "pending"},
			{Content: "Pending three", Status: "pending"},
			{Content: "Done two", Status: "completed"},
		},
	})
	got := stripModelView(m)
	if !strings.Contains(got, "☐ Pending one") || !strings.Contains(got, "☐ In progress") || !strings.Contains(got, "☐ Pending two") {
		t.Fatalf("expected ordered plan window in view, got %q", got)
	}
	if strings.Contains(got, "✔ Done one") || strings.Contains(got, "☐ Pending three") || strings.Contains(got, "✔ Done two") {
		t.Fatalf("expected out-of-window entries to be omitted from ordered drawer, got %q", got)
	}
}

func TestPlanDrawerBudgetShrinksOnSmallTerminal(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 18})
	_, _ = m.Update(tuievents.PlanUpdateMsg{
		Entries: []tuievents.PlanEntry{
			{Content: "Step one", Status: "pending"},
			{Content: "Step two", Status: "in_progress"},
			{Content: "Step three", Status: "pending"},
		},
	})
	got := stripModelView(m)
	if !strings.Contains(got, "☐ Step two") {
		t.Fatalf("expected active item to remain visible on small terminal, got %q", got)
	}
	if strings.Contains(got, "☐ Step one") || strings.Contains(got, "☐ Step three") {
		t.Fatalf("expected small terminal budget to show a single item window, got %q", got)
	}
}

func TestPlanDrawerHidesAfterTurnCompletes(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)
	_, _ = m.Update(tuievents.PlanUpdateMsg{
		Entries: []tuievents.PlanEntry{
			{Content: "Inspect repo", Status: "in_progress"},
			{Content: "Run tests", Status: "pending"},
		},
	})
	before := stripModelView(m)
	if !strings.Contains(before, "☐ Inspect repo") {
		t.Fatalf("expected plan drawer before task result, got %q", before)
	}
	_, _ = m.Update(tuievents.TaskResultMsg{})
	after := stripModelView(m)
	if strings.Contains(after, "☐ Inspect repo") || strings.Contains(after, "☐ Run tests") {
		t.Fatalf("expected plan drawer to hide after turn completion, got %q", after)
	}
}

func TestPaletteAnimation_OpenAndClose(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"help", "status"},
		ExecuteLine: noopExecute,
	})
	resizeModel(m)

	_, cmd := m.Update(keyPress('p', tea.ModCtrl))
	if cmd == nil {
		t.Fatal("expected palette animation command on open")
	}
	if !m.showPalette || !m.paletteAnimating {
		t.Fatalf("expected palette open animation, show=%v anim=%v", m.showPalette, m.paletteAnimating)
	}

	_, _ = m.Update(paletteAnimationMsg{})
	if m.paletteAnimLines <= 0 {
		t.Fatalf("expected palette animation to advance, got %d", m.paletteAnimLines)
	}

	cmd = m.handlePaletteKey(keyPress(tea.KeyEscape))
	if cmd == nil {
		t.Fatal("expected palette animation command on close")
	}
	if m.showPalette {
		t.Fatal("expected palette to begin closing")
	}
}

func TestViewHidesScrollbarWhenViewportOverflowsByDefault(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	for i := 0; i < 40; i++ {
		m.doc.Append(NewTranscriptBlock(fmt.Sprintf("line %02d", i), tuikit.LineStyleDefault))
	}
	m.syncViewportContent()

	view := renderModel(m)
	if strings.Contains(view, "▎") || strings.Contains(view, "▏") {
		t.Fatalf("did not expect viewport scrollbar in idle view, got:\n%s", view)
	}
}

func TestViewShowsViewportScrollbarOnlyWithinVisibleWindow(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	for i := 0; i < 40; i++ {
		m.doc.Append(NewTranscriptBlock(fmt.Sprintf("line %02d", i), tuikit.LineStyleDefault))
	}
	m.syncViewportContent()

	m.viewportScrollbarVisibleUntil = time.Now().Add(time.Second)
	if view := renderModel(m); !strings.Contains(view, "▎") {
		t.Fatalf("expected visible viewport scrollbar while scrolling, got:\n%s", view)
	}

	m.viewportScrollbarVisibleUntil = time.Now().Add(-time.Second)
	if view := renderModel(m); strings.Contains(view, "▎") || strings.Contains(view, "▏") {
		t.Fatalf("did not expect expired viewport scrollbar, got:\n%s", view)
	}
}

func TestViewportScrollbarShowsOnHoverAndDrags(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	for i := 0; i < 60; i++ {
		m.doc.Append(NewTranscriptBlock(fmt.Sprintf("line %02d", i), tuikit.LineStyleDefault))
	}
	m.syncViewportContent()
	m.viewport.GotoTop()
	m.userScrolledUp = true

	x := m.mainColumnX() + tuikit.GutterNarrative + m.viewport.Width()
	y := maxInt(1, m.viewport.Height()/2)

	_, _ = m.Update(mouseMotion(x, y))
	if view := renderModel(m); !strings.Contains(view, "▎") {
		t.Fatalf("expected viewport scrollbar visible on hover, got:\n%s", view)
	}

	before := m.viewport.YOffset()
	_, _ = m.Update(mouseClick(x, y, tea.MouseLeft))
	_, _ = m.Update(mouseMotion(x, m.viewport.Height()-1))
	_, _ = m.Update(mouseRelease(x, m.viewport.Height()-1, tea.MouseLeft))
	if got := m.viewport.YOffset(); got <= before {
		t.Fatalf("expected viewport scrollbar drag to advance offset, before=%d after=%d", before, got)
	}
}

func TestConnectWizardQueryAtCursor(t *testing.T) {
	query, ok := wizardQueryAtCursor("connect", []rune("/connect openai"), len([]rune("/connect openai")))
	if !ok {
		t.Fatal("expected connect wizard query")
	}
	if query != "openai" {
		t.Fatalf("unexpected query %q", query)
	}
	_, ok = wizardQueryAtCursor("connect", []rune("/model x"), len([]rune("/model x")))
	if ok {
		t.Fatal("did not expect connect wizard query for non-connect input")
	}
}

func TestModelWizardQueryAtCursor(t *testing.T) {
	query, ok := wizardQueryAtCursor("model", []rune("/model high"), len([]rune("/model high")))
	if !ok {
		t.Fatal("expected model wizard query")
	}
	if query != "high" {
		t.Fatalf("unexpected query %q", query)
	}
	_, ok = wizardQueryAtCursor("model", []rune("/connect x"), len([]rune("/connect x")))
	if ok {
		t.Fatal("did not expect model wizard query for non-model input")
	}
}

func TestModelEnterExecutesLine(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			called = submission.Text
			return tuievents.TaskResultMsg{}
		},
	})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Type "abc" via rune events.
	typeRunes(m, "abc")

	val := m.textarea.Value()
	if val != "abc" {
		t.Fatalf("textarea value expected 'abc', got %q", val)
	}

	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected batch command on enter")
	}

	// Execute the batch to find TaskResultMsg.
	batchMsg := cmd()
	if batchMsg == nil {
		t.Fatal("expected non-nil batch message")
	}
	found := findAndRunTaskResult(batchMsg, m)
	if !found {
		t.Fatal("expected TaskResultMsg in batch")
	}
	if called != "abc" {
		t.Fatalf("expected line 'abc', got %q", called)
	}
}

func TestWelcomeCardRendersWhenEnabled(t *testing.T) {
	m := NewModel(Config{
		Version:         "0.0.1",
		Workspace:       "/tmp/work",
		ShowWelcomeCard: true,
		ExecuteLine:     noopExecute,
	})
	_ = m.Init()
	resizeModel(m)
	view := stripModelView(m)
	if !strings.Contains(view, "CAELIS") {
		t.Fatalf("expected welcome card title in view, got %q", view)
	}
	if !strings.Contains(view, "workspace") {
		t.Fatalf("expected workspace line in welcome card, got %q", view)
	}
}

func TestWelcomeCardShowsConnectHintWhenModelMissing(t *testing.T) {
	m := NewModel(Config{
		Version:         "0.0.1",
		Workspace:       "/tmp/work",
		ShowWelcomeCard: true,
		ExecuteLine:     noopExecute,
	})
	_ = m.Init()
	resizeModel(m)
	view := stripModelView(m)
	if !strings.Contains(view, "not configured (/connect)") {
		t.Fatalf("expected explicit empty model state, got %q", view)
	}
}

func TestResumeOverlayEnterExecutesSelectedSession(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			called = submission.Text
			return tuievents.TaskResultMsg{}
		},
		ResumeComplete: func(_ string, _ int) ([]ResumeCandidate, error) {
			return []ResumeCandidate{
				{SessionID: "s-1", Prompt: "first prompt", Age: "10m"},
				{SessionID: "s-2", Prompt: "second prompt", Age: "30m"},
			}, nil
		},
	})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	typeRunes(m, "/resume")
	_, _ = m.Update(keyPress(tea.KeyEnter))
	if len(m.resumeCandidates) != 2 {
		t.Fatalf("expected 2 resume candidates, got %d", len(m.resumeCandidates))
	}
	rendered := ansi.Strip(m.renderResumeList())
	if !strings.Contains(rendered, "10m  first prompt") || !strings.Contains(rendered, "30m  second prompt") {
		t.Fatalf("expected age+prompt in resume list, got %q", rendered)
	}

	_, _ = m.Update(keyPress(tea.KeyDown))
	if m.resumeIndex != 1 {
		t.Fatalf("expected resume index 1, got %d", m.resumeIndex)
	}
	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected command on resume enter")
	}
	batchMsg := cmd()
	if batchMsg == nil {
		t.Fatal("expected non-nil batch message")
	}
	found := findAndRunTaskResult(batchMsg, m)
	if !found {
		t.Fatal("expected TaskResultMsg in batch")
	}
	if called != "/resume s-2" {
		t.Fatalf("expected '/resume s-2', got %q", called)
	}
}

func TestResumeOverlayTabFillsSessionID(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		ResumeComplete: func(_ string, _ int) ([]ResumeCandidate, error) {
			return []ResumeCandidate{
				{SessionID: "s-1", Prompt: "first", Age: "1m"},
				{SessionID: "s-2", Prompt: "second", Age: "2m"},
			}, nil
		},
	})
	resizeModel(m)

	typeRunes(m, "/resume")
	_, _ = m.Update(keyPress(tea.KeyEnter))
	_, _ = m.Update(keyPress(tea.KeyDown))
	_, _ = m.Update(keyPress(tea.KeyTab))

	if got := m.textarea.Value(); got != "/resume s-2 " {
		t.Fatalf("expected '/resume s-2 ', got %q", got)
	}
	if len(m.resumeCandidates) != 0 {
		t.Fatalf("expected resume candidates cleared after tab completion, got %d", len(m.resumeCandidates))
	}
}

func TestResumeOverlayIgnoresKeyReleaseNavigation(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		ResumeComplete: func(_ string, _ int) ([]ResumeCandidate, error) {
			return []ResumeCandidate{
				{SessionID: "s-1", Prompt: "first", Age: "1m"},
				{SessionID: "s-2", Prompt: "second", Age: "2m"},
				{SessionID: "s-3", Prompt: "third", Age: "3m"},
			}, nil
		},
	})
	resizeModel(m)

	typeRunes(m, "/resume")
	_, _ = m.Update(keyPress(tea.KeyEnter))
	_, _ = m.Update(keyPress(tea.KeyDown))
	_, _ = m.Update(keyRelease(tea.KeyDown))

	if m.resumeIndex != 1 {
		t.Fatalf("expected resume index 1 after key press+release, got %d", m.resumeIndex)
	}
}

func TestResumeOverlayEscClearsResumeCommand(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		ResumeComplete: func(_ string, _ int) ([]ResumeCandidate, error) {
			return []ResumeCandidate{
				{SessionID: "s-1", Prompt: "first", Age: "1m"},
			}, nil
		},
	})
	resizeModel(m)

	typeRunes(m, "/resume")
	_, _ = m.Update(keyPress(tea.KeyEnter))
	if len(m.resumeCandidates) == 0 {
		t.Fatal("expected resume candidates")
	}
	_, _ = m.Update(keyPress(tea.KeyEscape))
	if got := m.textarea.Value(); got != "" {
		t.Fatalf("expected input cleared on esc, got %q", got)
	}
	if len(m.resumeCandidates) != 0 {
		t.Fatalf("expected resume candidates cleared on esc, got %d", len(m.resumeCandidates))
	}
}

func TestResumeOverlayScrollWindowKeepsSelectedVisible(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		ResumeComplete: func(_ string, _ int) ([]ResumeCandidate, error) {
			out := make([]ResumeCandidate, 0, 20)
			for i := 0; i < 20; i++ {
				out = append(out, ResumeCandidate{
					SessionID: fmt.Sprintf("s-%02d", i),
					Prompt:    fmt.Sprintf("prompt-%02d", i),
					Age:       fmt.Sprintf("%dm", i),
				})
			}
			return out, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/resume")
	_, _ = m.Update(keyPress(tea.KeyEnter))
	for i := 0; i < 12; i++ {
		_, _ = m.Update(keyPress(tea.KeyDown))
	}
	if m.resumeIndex != 12 {
		t.Fatalf("expected resume index 12, got %d", m.resumeIndex)
	}
	rendered := ansi.Strip(m.renderResumeList())
	if !strings.Contains(rendered, "12m  prompt-12") {
		t.Fatalf("expected selected item visible in scrolled window, got %q", rendered)
	}
	if !strings.Contains(rendered, "… and") {
		t.Fatalf("expected window indicator in scrolled list, got %q", rendered)
	}
}

func TestSlashArgOverlayEnterBuildsModelCommand(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			called = submission.Text
			return tuievents.TaskResultMsg{}
		},
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			switch command {
			case "model":
				return []SlashArgCandidate{
					{Value: "use", Display: "use"},
					{Value: "del", Display: "del"},
				}, nil
			case "model use":
				return []SlashArgCandidate{
					{Value: "deepseek/deepseek-chat", Display: "deepseek/deepseek-chat"},
					{Value: "xiaomi/mimo-v2-flash", Display: "xiaomi/mimo-v2-flash"},
				}, nil
			case "model use xiaomi/mimo-v2-flash":
				return []SlashArgCandidate{
					{Value: "off", Display: "off"},
					{Value: "on", Display: "on"},
				}, nil
			default:
				return nil, nil
			}
		},
	})
	resizeModel(m)
	typeRunes(m, "/model")
	_, _ = m.Update(keyPress(tea.KeyEnter))
	if len(m.slashArgCandidates) != 2 {
		t.Fatalf("expected 2 model action candidates, got %d", len(m.slashArgCandidates))
	}
	_, _ = m.Update(keyPress(tea.KeyEnter))
	if got := strings.TrimSpace(m.slashArgCommand); got != "model use" {
		t.Fatalf("expected model alias step, got %q", got)
	}
	if len(m.slashArgCandidates) != 2 {
		t.Fatalf("expected 2 alias candidates, got %d", len(m.slashArgCandidates))
	}
	_, _ = m.Update(keyPress(tea.KeyDown))
	_, _ = m.Update(keyPress(tea.KeyTab))
	if got := strings.TrimSpace(m.textarea.Value()); got != "/model use xiaomi/mimo-v2-flash" {
		t.Fatalf("expected alias completion in input, got %q", got)
	}
	if got := strings.TrimSpace(m.slashArgCommand); got != "model use xiaomi/mimo-v2-flash" {
		t.Fatalf("expected model reasoning step, got %q", m.slashArgCommand)
	}
	if len(m.slashArgCandidates) != 2 {
		t.Fatalf("expected 2 reasoning candidates, got %d", len(m.slashArgCandidates))
	}
	_, _ = m.Update(keyPress(tea.KeyDown))
	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd != nil {
		t.Fatal("expected no command while accepting final reasoning completion")
	}
	if got := strings.TrimSpace(m.textarea.Value()); got != "/model use xiaomi/mimo-v2-flash on" {
		t.Fatalf("expected completed model command in input, got %q", got)
	}
	if m.slashArgActive {
		t.Fatal("expected slash-arg overlay closed after final completion")
	}
	_, cmd = m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected command when executing completed model command")
	}
	batchMsg := cmd()
	if batchMsg == nil {
		t.Fatal("expected non-nil batch message")
	}
	found := findAndRunTaskResult(batchMsg, m)
	if !found {
		t.Fatal("expected TaskResultMsg in batch")
	}
	if called != "/model use xiaomi/mimo-v2-flash on" {
		t.Fatalf("expected '/model use xiaomi/mimo-v2-flash on', got %q", called)
	}
}

func TestModelWizardOpensOnTrailingSpaceAndAdvancesToAliasStep(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			switch command {
			case "model":
				return []SlashArgCandidate{
					{Value: "use", Display: "use"},
					{Value: "del", Display: "del"},
				}, nil
			case "model use":
				return []SlashArgCandidate{
					{Value: "deepseek/deepseek-chat", Display: "deepseek/deepseek-chat"},
					{Value: "xiaomi/mimo-v2-flash", Display: "xiaomi/mimo-v2-flash"},
				}, nil
			default:
				return nil, nil
			}
		},
	})
	resizeModel(m)
	typeRunes(m, "/model ")
	if !m.slashArgActive {
		t.Fatal("expected slash-arg wizard active after trailing space")
	}
	if len(m.slashArgCandidates) != 2 {
		t.Fatalf("expected model action candidates, got %d", len(m.slashArgCandidates))
	}
	_, _ = m.Update(keyPress(tea.KeyEnter))
	if got := strings.TrimSpace(m.slashArgCommand); got != "model use" {
		t.Fatalf("expected alias step command 'model use', got %q", got)
	}
	if len(m.slashArgCandidates) != 2 {
		t.Fatalf("expected alias candidates after selecting use, got %d", len(m.slashArgCandidates))
	}
}

func TestModelWizardTypingSubcommandOpensAliasStep(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			switch command {
			case "model":
				return []SlashArgCandidate{
					{Value: "use", Display: "use"},
					{Value: "del", Display: "del"},
				}, nil
			case "model use":
				return []SlashArgCandidate{
					{Value: "deepseek/deepseek-chat", Display: "deepseek/deepseek-chat"},
					{Value: "xiaomi/mimo-v2-flash", Display: "xiaomi/mimo-v2-flash"},
				}, nil
			default:
				return nil, nil
			}
		},
	})
	resizeModel(m)
	typeRunes(m, "/model use ")
	if !m.slashArgActive {
		t.Fatal("expected slash-arg wizard active after typed subcommand")
	}
	if got := strings.TrimSpace(m.slashArgCommand); got != "model use" {
		t.Fatalf("expected alias step command 'model use', got %q", got)
	}
	if len(m.slashArgCandidates) != 2 {
		t.Fatalf("expected 2 alias candidates, got %d", len(m.slashArgCandidates))
	}
}

func TestAgentSlashArgOpensOnTrailingSpaceAndAdvancesToBuiltinStep(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			switch command {
			case "agent":
				return []SlashArgCandidate{
					{Value: "list", Display: "list"},
					{Value: "add", Display: "add"},
					{Value: "rm", Display: "rm"},
				}, nil
			case "agent add":
				return []SlashArgCandidate{
					{Value: "codex", Display: "codex"},
					{Value: "copilot", Display: "copilot"},
				}, nil
			default:
				return nil, nil
			}
		},
	})
	resizeModel(m)
	typeRunes(m, "/agent ")
	if !m.slashArgActive {
		t.Fatal("expected slash-arg active for /agent")
	}
	if len(m.slashArgCandidates) != 3 {
		t.Fatalf("expected agent action candidates, got %d", len(m.slashArgCandidates))
	}
	_, _ = m.Update(keyPress(tea.KeyDown))
	_, _ = m.Update(keyPress(tea.KeyEnter))
	if got := strings.TrimSpace(m.slashArgCommand); got != "agent add" {
		t.Fatalf("expected builtin step command 'agent add', got %q", got)
	}
	if len(m.slashArgCandidates) != 2 {
		t.Fatalf("expected builtin candidates after selecting add, got %d", len(m.slashArgCandidates))
	}
}

func TestAgentAddExecutesOnSingleEnterWhenBuiltinAlreadyComplete(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			called = strings.TrimSpace(submission.Text)
			return tuievents.TaskResultMsg{}
		},
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			if command != "agent add" {
				return nil, nil
			}
			return []SlashArgCandidate{{Value: "codex", Display: "codex"}}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/agent add codex")

	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected command for exact /agent add builtin")
	}
	if !findAndRunTaskResult(cmd(), m) {
		t.Fatal("expected TaskResultMsg in submit command")
	}
	if called != "/agent add codex" {
		t.Fatalf("expected '/agent add codex', got %q", called)
	}
}

func TestModelDelExecutesOnSingleEnterWhenAliasAlreadyComplete(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			called = strings.TrimSpace(submission.Text)
			return tuievents.TaskResultMsg{}
		},
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			if command != "model del" {
				return nil, nil
			}
			return []SlashArgCandidate{{Value: "xiaomi/mimo-v2-flash", Display: "xiaomi/mimo-v2-flash"}}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/model del xiaomi/mimo-v2-flash")

	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected command for exact /model del alias")
	}
	if !findAndRunTaskResult(cmd(), m) {
		t.Fatal("expected TaskResultMsg in submit command")
	}
	if called != "/model del xiaomi/mimo-v2-flash" {
		t.Fatalf("expected '/model del xiaomi/mimo-v2-flash', got %q", called)
	}
}

func TestModelDelExecutesWithoutAlias(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			called = strings.TrimSpace(submission.Text)
			return tuievents.TaskResultMsg{}
		},
	})
	resizeModel(m)
	typeRunes(m, "/model del")

	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected command for /model del")
	}
	if !findAndRunTaskResult(cmd(), m) {
		t.Fatal("expected TaskResultMsg in submit command")
	}
	if called != "/model del" {
		t.Fatalf("expected '/model del', got %q", called)
	}
}

func TestSlashArgOverlayTabFillsSelectedValue(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			if command != "sandbox" {
				return nil, nil
			}
			return []SlashArgCandidate{
				{Value: "bwrap", Display: "bwrap"},
				{Value: "seatbelt", Display: "seatbelt"},
			}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/sandbox")
	_, _ = m.Update(keyPress(tea.KeyEnter))
	_, _ = m.Update(keyPress(tea.KeyDown))
	_, _ = m.Update(keyPress(tea.KeyTab))

	if got := m.textarea.Value(); got != "/sandbox seatbelt " {
		t.Fatalf("expected '/sandbox seatbelt ', got %q", got)
	}
	if len(m.slashArgCandidates) != 0 {
		t.Fatalf("expected slash-arg candidates cleared after tab completion, got %d", len(m.slashArgCandidates))
	}
}

func TestSlashArgOverlayEscClearsSlashCommand(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		Wizards:     testWizards(),
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			if command != "connect" {
				return nil, nil
			}
			return []SlashArgCandidate{{Value: "openai", Display: "openai"}}, nil
		},
	})
	resizeModel(m)

	typeRunes(m, "/connect")
	_, _ = m.Update(keyPress(tea.KeyEnter))
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected slash-arg candidates")
	}
	_, _ = m.Update(keyPress(tea.KeyEscape))
	if got := m.textarea.Value(); got != "" {
		t.Fatalf("expected input cleared on esc, got %q", got)
	}
	if len(m.slashArgCandidates) != 0 {
		t.Fatalf("expected slash-arg candidates cleared on esc, got %d", len(m.slashArgCandidates))
	}
}

func TestConnectSlashArgUsesStepPickerWithHiddenArgs(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			called = submission.Text
			return tuievents.TaskResultMsg{}
		},
		Wizards: testWizards(),
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			switch command {
			case "connect":
				return []SlashArgCandidate{
					{Value: "openai", Display: "openai"},
					{Value: "deepseek", Display: "deepseek"},
				}, nil
			case "connect-baseurl:openai":
				return []SlashArgCandidate{
					{Value: "https://api.openai.com/v1", Display: "https://api.openai.com/v1"},
				}, nil
			case "connect-timeout:openai":
				return []SlashArgCandidate{
					{Value: "30", Display: "30s"},
					{Value: "60", Display: "60s"},
				}, nil
			case "connect-model:openai|https%3A%2F%2Fapi.openai.com%2Fv1|60|sk-test|":
				return []SlashArgCandidate{
					{Value: "gpt-4o", Display: "gpt-4o"},
					{Value: "gpt-4o-mini", Display: "gpt-4o-mini"},
				}, nil
			case "connect-context:openai|https%3A%2F%2Fapi.openai.com%2Fv1|60|sk-test|gpt-4o-mini":
				return []SlashArgCandidate{{Value: "128000", Display: "128000"}}, nil
			case "connect-maxout:openai|https%3A%2F%2Fapi.openai.com%2Fv1|60|sk-test|gpt-4o-mini":
				return []SlashArgCandidate{{Value: "4096", Display: "4096"}}, nil
			case "connect-reasoning-levels:openai|https%3A%2F%2Fapi.openai.com%2Fv1|60|sk-test|gpt-4o-mini":
				return []SlashArgCandidate{{Value: "none,minimal,low,medium,high,xhigh", Display: "none,minimal,low,medium,high,xhigh"}}, nil
			default:
				return nil, nil
			}
		},
	})
	resizeModel(m)

	typeRunes(m, "/connect")
	_, _ = m.Update(keyPress(tea.KeyEnter)) // open provider picker
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected provider candidates")
	}
	_, _ = m.Update(keyPress(tea.KeyEnter)) // pick openai, open base_url picker
	if !strings.HasPrefix(m.slashArgCommand, "connect-baseurl:openai") {
		t.Fatalf("expected connect-baseurl step, got %q", m.slashArgCommand)
	}
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected base_url candidates")
	}
	_, _ = m.Update(keyPress(tea.KeyEnter)) // pick base_url, open timeout picker
	if !strings.HasPrefix(m.slashArgCommand, "connect-timeout:openai") {
		t.Fatalf("expected connect-timeout step, got %q", m.slashArgCommand)
	}
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected timeout candidates")
	}
	_, _ = m.Update(keyPress(tea.KeyDown))  // pick 60
	_, _ = m.Update(keyPress(tea.KeyEnter)) // open api_key step
	if !strings.HasPrefix(m.slashArgCommand, "connect-apikey:openai") {
		t.Fatalf("expected connect-apikey step, got %q", m.slashArgCommand)
	}
	if got := m.textarea.Value(); got != "/connect " {
		t.Fatalf("expected connect input kept minimal, got %q", got)
	}
	typeRunes(m, "sk-test")
	_, _ = m.Update(keyPress(tea.KeyEnter)) // open model picker
	if !strings.HasPrefix(m.slashArgCommand, "connect-model:openai|") {
		t.Fatalf("expected connect-model step, got %q", m.slashArgCommand)
	}
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected model candidates")
	}
	_, _ = m.Update(keyPress(tea.KeyDown))  // pick gpt-4o-mini
	_, _ = m.Update(keyPress(tea.KeyEnter)) // open context_window_tokens picker
	if !strings.HasPrefix(m.slashArgCommand, "connect-context:openai|") {
		t.Fatalf("expected connect-context step, got %q", m.slashArgCommand)
	}
	_, _ = m.Update(keyPress(tea.KeyEnter)) // pick context_window_tokens
	if !strings.HasPrefix(m.slashArgCommand, "connect-maxout:openai|") {
		t.Fatalf("expected connect-maxout step, got %q", m.slashArgCommand)
	}
	_, _ = m.Update(keyPress(tea.KeyEnter)) // pick max_output_tokens
	if !strings.HasPrefix(m.slashArgCommand, "connect-reasoning-levels:openai|") {
		t.Fatalf("expected connect-reasoning-levels step, got %q", m.slashArgCommand)
	}
	_, cmd := m.Update(keyPress(tea.KeyEnter)) // pick reasoning levels and submit
	if cmd == nil {
		t.Fatal("expected command on connect submit")
	}
	batchMsg := cmd()
	if batchMsg == nil {
		t.Fatal("expected non-nil batch message")
	}
	found := findAndRunTaskResult(batchMsg, m)
	if !found {
		t.Fatal("expected TaskResultMsg in batch")
	}
	if called != "/connect openai gpt-4o-mini https://api.openai.com/v1 60 sk-test 128000 4096 none,minimal,low,medium,high,xhigh" {
		t.Fatalf("unexpected connect command %q", called)
	}
	if len(m.history) != 0 {
		t.Fatalf("expected slash command not recorded into history, got %v", m.history)
	}
}

func TestConnectSlashArgAllowsManualModelInputWhenNoCandidates(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			called = submission.Text
			return tuievents.TaskResultMsg{}
		},
		Wizards: testWizards(),
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			switch command {
			case "connect":
				return []SlashArgCandidate{{Value: "openai", Display: "openai"}}, nil
			case "connect-baseurl:openai":
				return []SlashArgCandidate{{Value: "https://api.openai.com/v1", Display: "https://api.openai.com/v1"}}, nil
			case "connect-timeout:openai":
				return []SlashArgCandidate{{Value: "60", Display: "60s"}}, nil
			case "connect-context:openai|https%3A%2F%2Fapi.openai.com%2Fv1|60|sk-test|gpt-custom":
				return []SlashArgCandidate{{Value: "128000", Display: "128000"}}, nil
			case "connect-maxout:openai|https%3A%2F%2Fapi.openai.com%2Fv1|60|sk-test|gpt-custom":
				return []SlashArgCandidate{{Value: "32768", Display: "32768"}}, nil
			case "connect-reasoning-levels:openai|https%3A%2F%2Fapi.openai.com%2Fv1|60|sk-test|gpt-custom":
				return []SlashArgCandidate{{Value: "-", Display: "(empty, unknown support)"}}, nil
			default:
				return nil, nil // model step intentionally returns empty
			}
		},
	})
	resizeModel(m)

	typeRunes(m, "/connect")
	_, _ = m.Update(keyPress(tea.KeyEnter))
	_, _ = m.Update(keyPress(tea.KeyEnter)) // provider
	_, _ = m.Update(keyPress(tea.KeyEnter)) // base_url
	_, _ = m.Update(keyPress(tea.KeyEnter)) // timeout
	typeRunes(m, "sk-test")
	_, _ = m.Update(keyPress(tea.KeyEnter)) // api_key -> model step (no candidates)
	if !strings.HasPrefix(m.slashArgCommand, "connect-model:openai|") {
		t.Fatalf("expected connect-model step, got %q", m.slashArgCommand)
	}
	if len(m.slashArgCandidates) != 0 {
		t.Fatalf("expected no model candidates for manual fallback, got %d", len(m.slashArgCandidates))
	}
	typeRunes(m, "gpt-custom")
	_, _ = m.Update(keyPress(tea.KeyEnter)) // model -> context step
	if !strings.HasPrefix(m.slashArgCommand, "connect-context:openai|") {
		t.Fatalf("expected connect-context step, got %q", m.slashArgCommand)
	}
	_, _ = m.Update(keyPress(tea.KeyEnter)) // context
	if !strings.HasPrefix(m.slashArgCommand, "connect-maxout:openai|") {
		t.Fatalf("expected connect-maxout step, got %q", m.slashArgCommand)
	}
	_, _ = m.Update(keyPress(tea.KeyEnter)) // max output
	if !strings.HasPrefix(m.slashArgCommand, "connect-reasoning-levels:openai|") {
		t.Fatalf("expected connect-reasoning-levels step, got %q", m.slashArgCommand)
	}
	_, cmd := m.Update(keyPress(tea.KeyEnter)) // reasoning -> submit
	if cmd == nil {
		t.Fatal("expected command on manual model enter")
	}
	batchMsg := cmd()
	if batchMsg == nil {
		t.Fatal("expected non-nil batch message")
	}
	if !findAndRunTaskResult(batchMsg, m) {
		t.Fatal("expected TaskResultMsg in batch")
	}
	if called != "/connect openai gpt-custom https://api.openai.com/v1 60 sk-test 128000 32768 -" {
		t.Fatalf("unexpected connect command %q", called)
	}
}

// findAndRunTaskResult recursively searches for and executes a TaskResultMsg
// within a batch command structure.
func findAndRunTaskResult(msg tea.Msg, m *Model) bool {
	if _, ok := msg.(tuievents.TaskResultMsg); ok {
		m.Update(msg)
		return true
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, cmd := range batch {
			if cmd == nil {
				continue
			}
			subMsg := cmd()
			if subMsg == nil {
				continue
			}
			if findAndRunTaskResult(subMsg, m) {
				return true
			}
		}
	}
	return false
}

func TestDiagnosticsObserverCalled(t *testing.T) {
	var seen Diagnostics
	m := NewModel(Config{
		OnDiagnostics: func(d Diagnostics) {
			seen = d
		},
	})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	_ = renderModel(m)
	if seen.Frames == 0 {
		t.Fatal("expected diagnostics frames > 0")
	}
	if seen.LastRenderAt.IsZero() {
		t.Fatal("expected render timestamp")
	}
}

func TestTickStatusMsg(t *testing.T) {
	called := false
	m := NewModel(Config{
		RefreshStatus: func() (string, string) {
			called = true
			return "m", "c"
		},
	})
	_, cmd := m.Update(tuievents.TickStatusMsg{})
	if !called {
		t.Fatal("expected refresh status called")
	}
	if cmd == nil {
		t.Fatal("expected next tick cmd")
	}
}

func TestTickStatusMsgRefreshesWorkspace(t *testing.T) {
	m := NewModel(Config{
		Workspace: "before",
		RefreshWorkspace: func() string {
			return "after"
		},
	})
	updated, _ := m.Update(tuievents.TickStatusMsg{})
	next := updated.(*Model)
	if next.cfg.Workspace != "after" {
		t.Fatalf("expected workspace refreshed, got %q", next.cfg.Workspace)
	}
}

func TestSetStatusMsgCanClearContext(t *testing.T) {
	m := NewModel(Config{})
	m.statusContext = "1.2k/128.0k(1%)"
	_, _ = m.Update(tuievents.SetStatusMsg{Model: "m", Context: ""})
	if m.statusContext != "" {
		t.Fatalf("expected status context cleared, got %q", m.statusContext)
	}
}

func TestShiftTabTogglesModeAndRefreshesStatus(t *testing.T) {
	toggled := false
	m := NewModel(Config{
		ToggleMode: func() (string, error) {
			toggled = true
			return "plan mode enabled", nil
		},
		RefreshStatus: func() (string, string) {
			return "model {plan}", "42/128k"
		},
	})
	updated, cmd := m.Update(keyPress(tea.KeyTab, tea.ModShift))
	next := updated.(*Model)
	if !toggled {
		t.Fatal("expected toggle callback")
	}
	if next.hint != "plan mode enabled" {
		t.Fatalf("expected mode hint, got %q", next.hint)
	}
	if next.statusModel != "model {plan}" || next.statusContext != "42/128k" {
		t.Fatalf("unexpected refreshed status %q %q", next.statusModel, next.statusContext)
	}
	if cmd == nil {
		t.Fatal("expected hint clear command")
	}
}

func TestBacktabTogglesModeAndRefreshesStatus(t *testing.T) {
	toggled := false
	m := NewModel(Config{
		ToggleMode: func() (string, error) {
			toggled = true
			return "full access mode enabled", nil
		},
		RefreshStatus: func() (string, string) {
			return "model {full_access}", "42/128k"
		},
	})
	updated, cmd := m.Update(keyText("backtab"))
	next := updated.(*Model)
	if !toggled {
		t.Fatal("expected toggle callback")
	}
	if next.hint != "full access mode enabled" {
		t.Fatalf("expected mode hint, got %q", next.hint)
	}
	if next.statusModel != "model {full_access}" || next.statusContext != "42/128k" {
		t.Fatalf("unexpected refreshed status %q %q", next.statusModel, next.statusContext)
	}
	if cmd == nil {
		t.Fatal("expected hint clear command")
	}
}

func TestCtrlOTogglesModeAndRefreshesStatus(t *testing.T) {
	toggled := false
	m := NewModel(Config{
		ToggleMode: func() (string, error) {
			toggled = true
			return "plan mode enabled", nil
		},
		RefreshStatus: func() (string, string) {
			return "model {plan}", "42/128k"
		},
	})
	updated, cmd := m.Update(keyText("ctrl+o"))
	next := updated.(*Model)
	if !toggled {
		t.Fatal("expected toggle callback")
	}
	if next.hint != "plan mode enabled" {
		t.Fatalf("expected mode hint, got %q", next.hint)
	}
	if next.statusModel != "model {plan}" || next.statusContext != "42/128k" {
		t.Fatalf("unexpected refreshed status %q %q", next.statusModel, next.statusContext)
	}
	if cmd == nil {
		t.Fatal("expected hint clear command")
	}
}

func TestObserveRenderStats(t *testing.T) {
	m := NewModel(Config{})
	m.observeRender(5*time.Millisecond, 100, "incremental")
	m.observeRender(3*time.Millisecond, 80, "full")
	if m.diag.Frames != 2 {
		t.Fatalf("expected 2 frames, got %d", m.diag.Frames)
	}
	if m.diag.RenderBytes != 180 {
		t.Fatalf("expected bytes 180, got %d", m.diag.RenderBytes)
	}
	if m.diag.IncrementalFrames != 1 || m.diag.FullRepaints != 1 {
		t.Fatalf("unexpected redraw counters: incremental=%d full=%d", m.diag.IncrementalFrames, m.diag.FullRepaints)
	}
}

func TestPercentileDuration(t *testing.T) {
	values := []time.Duration{10 * time.Millisecond, 2 * time.Millisecond, 5 * time.Millisecond, 20 * time.Millisecond}
	got := percentileDuration(values, 0.95)
	if got != 10*time.Millisecond {
		t.Fatalf("expected p95=10ms, got %s", got)
	}
}

// ---------------------------------------------------------------------------
// History navigation tests
// ---------------------------------------------------------------------------

func TestHistoryUpDown(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	typeAndEnter(m, "first")
	typeAndEnter(m, "second")
	typeRunes(m, "draft")

	_, _ = m.Update(keyPress(tea.KeyUp))
	if m.textarea.Value() != "second" {
		t.Fatalf("expected 'second', got %q", m.textarea.Value())
	}

	_, _ = m.Update(keyPress(tea.KeyUp))
	if m.textarea.Value() != "first" {
		t.Fatalf("expected 'first', got %q", m.textarea.Value())
	}

	_, _ = m.Update(keyPress(tea.KeyDown))
	if m.textarea.Value() != "second" {
		t.Fatalf("expected 'second', got %q", m.textarea.Value())
	}

	_, _ = m.Update(keyPress(tea.KeyDown))
	if m.textarea.Value() != "draft" {
		t.Fatalf("expected draft restored, got %q", m.textarea.Value())
	}
}

func TestHistoryUpOnEmptyInputEntersHistory(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	typeAndEnter(m, "first")
	typeAndEnter(m, "second")
	m.textarea.SetValue("")
	m.syncInputFromTextarea()

	_, _ = m.Update(keyPress(tea.KeyUp))
	if m.textarea.Value() != "second" {
		t.Fatalf("expected latest history command, got %q", m.textarea.Value())
	}
	if m.historyIndex != 1 {
		t.Fatalf("expected history index 1, got %d", m.historyIndex)
	}
}

func TestHistoryDraftPreserved(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	typeAndEnter(m, "old-cmd")
	typeRunes(m, "new-draft")

	_, _ = m.Update(keyPress(tea.KeyUp))
	if m.textarea.Value() != "old-cmd" {
		t.Fatalf("expected 'old-cmd', got %q", m.textarea.Value())
	}

	_, _ = m.Update(keyPress(tea.KeyDown))
	if m.textarea.Value() != "new-draft" {
		t.Fatalf("expected 'new-draft', got %q", m.textarea.Value())
	}
}

func TestHistoryDeduplication(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	typeAndEnter(m, "same")
	typeAndEnter(m, "same")

	if len(m.history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(m.history))
	}
}

func TestSlashCommandsAreNotRecordedInHistory(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	typeAndEnter(m, "/status")
	typeAndEnter(m, "hello")

	if len(m.history) != 1 {
		t.Fatalf("expected only non-slash entry in history, got %d (%+v)", len(m.history), m.history)
	}
	if m.history[0] != "hello" {
		t.Fatalf("unexpected history entry: %+v", m.history)
	}
}

// ---------------------------------------------------------------------------
// Slash command completion tests
// ---------------------------------------------------------------------------

func TestSlashTabCompletionUnique(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"status", "session", "help"},
		ExecuteLine: noopExecute,
	})
	resizeModel(m)

	typeRunes(m, "/hel")
	_, _ = m.Update(keyPress(tea.KeyTab))
	got := string(m.input)
	if got != "/help " {
		t.Fatalf("expected '/help ', got %q", got)
	}
}

func TestSlashCommandListAppearsOnSlashInput(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"status", "session", "set"},
		ExecuteLine: noopExecute,
	})
	resizeModel(m)

	typeRunes(m, "/")
	if len(m.slashCandidates) == 0 {
		t.Fatal("expected slash candidates to appear on '/' input")
	}
}

func TestSlashCommandsAutoOpenRelevantPickers(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"model", "resume", "connect"},
		ExecuteLine: noopExecute,
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			switch command {
			case "model":
				return []SlashArgCandidate{{Value: "use", Display: "use"}}, nil
			default:
				return nil, nil
			}
		},
		ResumeComplete: func(_ string, _ int) ([]ResumeCandidate, error) {
			return []ResumeCandidate{{SessionID: "s-1", Prompt: "p", Age: "1m"}}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/model ")
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected model picker to auto-open on trailing space")
	}
	m.textarea.SetValue("")
	m.syncInputFromTextarea()
	m.clearInputOverlays()
	typeRunes(m, "/connect ")
	if len(m.slashArgCandidates) != 0 {
		t.Fatal("did not expect connect to open slash-arg picker while typing")
	}
	m.textarea.SetValue("")
	m.syncInputFromTextarea()
	m.clearInputOverlays()
	typeRunes(m, "/resume ")
	if len(m.resumeCandidates) == 0 {
		t.Fatal("expected resume picker to auto-open on trailing space")
	}
}

func TestConnectEnterNormalizesToInteractiveCommand(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			called = submission.Text
			return tuievents.TaskResultMsg{}
		},
	})
	resizeModel(m)
	typeRunes(m, "/connect openai-compatible")
	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected command for /connect enter")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("expected non-nil batch message")
	}
	if !findAndRunTaskResult(msg, m) {
		t.Fatal("expected TaskResultMsg in batch")
	}
	if called != "/connect" {
		t.Fatalf("expected normalized '/connect', got %q", called)
	}
}

func TestSlashTabNoMatch(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"status", "help"},
		ExecuteLine: noopExecute,
	})
	resizeModel(m)

	typeRunes(m, "/xyz")
	_, _ = m.Update(keyPress(tea.KeyTab))
	if string(m.input) != "/xyz" {
		t.Fatalf("expected no change, got %q", string(m.input))
	}
}

func TestSlashOverlayDownTabFillsSelectedCommand(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"status", "session", "set"},
		ExecuteLine: noopExecute,
	})
	resizeModel(m)
	typeRunes(m, "/s")
	if len(m.slashCandidates) < 2 {
		t.Fatalf("expected at least 2 slash candidates, got %d", len(m.slashCandidates))
	}
	_, _ = m.Update(keyPress(tea.KeyDown))
	_, _ = m.Update(keyPress(tea.KeyTab))
	if got := string(m.input); !strings.HasPrefix(got, "/") || !strings.HasSuffix(got, " ") {
		t.Fatalf("expected slash command filled with trailing space, got %q", got)
	}
}

func TestSlashCompletionAutoOpensResumePickerAfterFill(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"resume", "status"},
		ExecuteLine: noopExecute,
		ResumeComplete: func(_ string, _ int) ([]ResumeCandidate, error) {
			return []ResumeCandidate{{SessionID: "s-1", Prompt: "restored", Age: "1m"}}, nil
		},
	})
	resizeModel(m)

	typeRunes(m, "/res")
	_, _ = m.Update(keyPress(tea.KeyTab))

	if got := string(m.input); got != "/resume " {
		t.Fatalf("expected '/resume ' after completion, got %q", got)
	}
	if len(m.resumeCandidates) == 0 {
		t.Fatal("expected resume picker to auto-open after slash completion")
	}
	if len(m.slashCandidates) != 0 {
		t.Fatalf("did not expect slash command overlay to remain visible, got %v", m.slashCandidates)
	}
}

func TestSlashCompletionFreeformCommandDoesNotReopenSlashOverlay(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"btw", "copilot", "status"},
		ExecuteLine: noopExecute,
	})
	resizeModel(m)

	typeRunes(m, "/cop")
	_, _ = m.Update(keyPress(tea.KeyTab))

	if got := string(m.input); got != "/copilot " {
		t.Fatalf("expected '/copilot ' after completion, got %q", got)
	}
	if len(m.slashCandidates) != 0 {
		t.Fatalf("did not expect slash overlay to remain for freeform command, got %v", m.slashCandidates)
	}
	if m.slashArgActive || len(m.slashArgCandidates) > 0 || len(m.resumeCandidates) > 0 {
		t.Fatalf("did not expect follow-up picker for freeform command, slashArgActive=%v slashArgs=%d resume=%d", m.slashArgActive, len(m.slashArgCandidates), len(m.resumeCandidates))
	}
}

func TestSlashCommandEnterOpensModelPicker(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"model", "status"},
		ExecuteLine: noopExecute,
		SlashArgComplete: func(command string, _ string, _ int) ([]SlashArgCandidate, error) {
			if command != "model" {
				return nil, nil
			}
			return []SlashArgCandidate{
				{Value: "use", Display: "use"},
			}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/model")
	_, _ = m.Update(keyPress(tea.KeyEnter))
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected model picker candidates after confirming /model")
	}
}

func TestMouseDragCopiesSelection(t *testing.T) {
	var copied string
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		WriteClipboardText: func(text string) error {
			copied = text
			return nil
		},
	})
	resizeModel(m)
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "hello world\n"})
	_, _ = m.Update(mouseClick(tuikit.GutterNarrative, 0, tea.MouseLeft))
	_, cmd := m.Update(mouseRelease(tuikit.GutterNarrative+5, 0, tea.MouseLeft))
	if cmd == nil {
		t.Fatal("expected clipboard command on mouse selection")
	}
	if !strings.Contains(m.hint, "copied") {
		t.Fatalf("expected copy hint, got %q", m.hint)
	}
	if copied != "hello" {
		t.Fatalf("expected selected text copied, got %q", copied)
	}
	if m.hasSelectionRange() {
		t.Fatal("expected viewport selection to be cleared after copy to prevent styled↔plain artifacts")
	}
}

func TestInputMouseDragCopiesSelection(t *testing.T) {
	var copied string
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		WriteClipboardText: func(text string) error {
			copied = text
			return nil
		},
	})
	resizeModel(m)
	typeRunes(m, "hello world")
	startY, _, ok := m.inputAreaBounds()
	if !ok {
		t.Fatal("expected input area bounds")
	}
	_, _ = m.Update(mouseClick(2, startY, tea.MouseLeft))
	_, cmd := m.Update(mouseRelease(7, startY, tea.MouseLeft))
	if cmd == nil {
		t.Fatal("expected clipboard command on input selection")
	}
	if !strings.Contains(m.hint, "copied") {
		t.Fatalf("expected copy hint, got %q", m.hint)
	}
	if copied != "> he" {
		t.Fatalf("expected selected input copied, got %q", copied)
	}
	start, end, ok := normalizedSelectionRange(m.inputSelectionStart, m.inputSelectionEnd, len(m.inputPlainLines()))
	if !ok || (start.line == end.line && start.col == end.col) {
		t.Fatal("expected input selection to remain after mouse release")
	}
}

func TestShiftEnterInsertsNewlineWithoutSubmitting(t *testing.T) {
	submitted := false
	m := NewModel(Config{
		ExecuteLine: func(Submission) tuievents.TaskResultMsg {
			submitted = true
			return tuievents.TaskResultMsg{}
		},
	})
	resizeModel(m)

	typeRunes(m, "hello")
	_, cmd := m.Update(keyPress(tea.KeyEnter, tea.ModShift))
	if cmd != nil {
		t.Fatalf("expected no submit cmd on shift+enter, got %T", cmd)
	}
	if submitted {
		t.Fatal("did not expect shift+enter to submit")
	}
	if got := m.textarea.Value(); got != "hello\n" {
		t.Fatalf("expected newline inserted on shift+enter, got %q", got)
	}
}

func TestCtrlJInsertsNewlineWithoutSubmitting(t *testing.T) {
	submitted := false
	m := NewModel(Config{
		ExecuteLine: func(Submission) tuievents.TaskResultMsg {
			submitted = true
			return tuievents.TaskResultMsg{}
		},
	})
	resizeModel(m)

	typeRunes(m, "hello")
	_, cmd := m.Update(keyText("ctrl+j"))
	if cmd != nil {
		t.Fatalf("expected no submit cmd on ctrl+j, got %T", cmd)
	}
	if submitted {
		t.Fatal("did not expect ctrl+j to submit")
	}
	if got := m.textarea.Value(); got != "hello\n" {
		t.Fatalf("expected newline inserted on ctrl+j, got %q", got)
	}
}

func TestViewRequestsKeyboardEnhancements(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)

	view := m.View()
	if !view.KeyboardEnhancements.ReportEventTypes {
		t.Fatal("expected view to request keyboard enhancement event types")
	}
}

func TestHeaderMouseDragCopiesSelection(t *testing.T) {
	var copied string
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		Workspace:   "~/WorkDir/xueyongzhi/caelis [main]",
		WriteClipboardText: func(text string) error {
			copied = text
			return nil
		},
		RefreshStatus: func() (string, string) {
			return "claude-opus-4.6 [reasoning on]", "0/200.0k(0%)"
		},
	})
	resizeModel(m)
	layout := m.fixedRowLayout()
	_, _ = m.Update(mouseClick(tuikit.StatusInset, layout.headerY, tea.MouseLeft))
	_, cmd := m.Update(mouseRelease(tuikit.StatusInset+19, layout.headerY, tea.MouseLeft))
	if cmd == nil {
		t.Fatal("expected clipboard command on header selection")
	}
	if !strings.Contains(m.hint, "copied") {
		t.Fatalf("expected copy hint, got %q", m.hint)
	}
	if m.fixedSelectionArea != fixedSelectionHeader {
		t.Fatalf("expected fixed header selection, got %q", m.fixedSelectionArea)
	}
	if got := m.fixedSelectionText(); strings.TrimSpace(got) == "" {
		t.Fatal("expected copied header text")
	}
	if strings.TrimSpace(copied) == "" {
		t.Fatal("expected header selection written to clipboard")
	}
}

func TestMouseDragCopyFailureShowsHintAndLog(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		WriteClipboardText: func(string) error {
			return fmt.Errorf("clipboard offline")
		},
	})
	resizeModel(m)
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "hello world\n"})
	_, _ = m.Update(mouseClick(tuikit.GutterNarrative, 0, tea.MouseLeft))
	_, cmd := m.Update(mouseRelease(tuikit.GutterNarrative+5, 0, tea.MouseLeft))
	if cmd == nil {
		t.Fatal("expected hint command on clipboard copy failure")
	}
	if got := m.hint; got != "copy: clipboard offline" {
		t.Fatalf("expected explicit copy error hint, got %q", got)
	}
	if len(m.renderedStyledLines()) == 0 || !strings.Contains(ansi.Strip(m.renderedStyledLines()[len(m.renderedStyledLines())-1]), "copy: clipboard offline") {
		t.Fatalf("expected clipboard failure logged in history, got %#v", m.renderedStyledLines())
	}
}

func TestCopyHintClearsOnTimerMessage(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine:        noopExecute,
		WriteClipboardText: func(string) error { return nil },
	})
	resizeModel(m)
	typeRunes(m, "hello world")
	startY, _, ok := m.inputAreaBounds()
	if !ok {
		t.Fatal("expected input area bounds")
	}
	_, _ = m.Update(mouseClick(2, startY, tea.MouseLeft))
	_, _ = m.Update(mouseRelease(7, startY, tea.MouseLeft))
	if !strings.Contains(m.hint, "copied") {
		t.Fatalf("expected copy hint, got %q", m.hint)
	}
	if len(m.hintEntries) == 0 {
		t.Fatal("expected managed copy hint entry")
	}
	_, _ = m.Update(clearHintMsg{id: m.hintEntries[0].id})
	if strings.TrimSpace(m.hint) != "" {
		t.Fatalf("expected copy hint cleared, got %q", m.hint)
	}
}

func TestCopyHintTimerDoesNotOverrideNewHint(t *testing.T) {
	m := newTestModel()
	_ = m.showHint("interrupt requested", hintOptions{
		priority:       tuievents.HintPriorityCritical,
		clearOnMessage: true,
		clearAfter:     systemHintDuration,
	})
	_, _ = m.Update(clearHintMsg{id: 9999})
	if m.hint != "interrupt requested" {
		t.Fatalf("expected newer hint preserved, got %q", m.hint)
	}
}

func TestSetHintMsgSchedulesAutoClear(t *testing.T) {
	m := newTestModel()
	_, cmd := m.Update(tuievents.SetHintMsg{Hint: "started new session", ClearAfter: time.Millisecond})
	if m.hint != "started new session" {
		t.Fatalf("expected hint set, got %q", m.hint)
	}
	if cmd == nil {
		t.Fatal("expected auto-clear command")
	}
	msg := cmd()
	clearMsg, ok := msg.(clearHintMsg)
	if !ok {
		t.Fatalf("expected clearHintMsg, got %T", msg)
	}
	if clearMsg.id == 0 {
		t.Fatalf("expected managed clear id, got %#v", clearMsg)
	}
	_, _ = m.Update(clearMsg)
	if strings.TrimSpace(m.hint) != "" {
		t.Fatalf("expected hint cleared, got %q", m.hint)
	}
}

func TestHigherPriorityHintPreemptsAndRestoresLowerHint(t *testing.T) {
	m := newTestModel()

	_ = m.showHint("started new session", hintOptions{
		priority:       tuievents.HintPriorityNormal,
		clearOnMessage: false,
		clearAfter:     time.Second,
	})
	_ = m.showHint("interrupt requested", hintOptions{
		priority:       tuievents.HintPriorityCritical,
		clearOnMessage: true,
		clearAfter:     time.Second,
	})
	if m.hint != "interrupt requested" {
		t.Fatalf("expected high-priority hint visible, got %q", m.hint)
	}
	if len(m.hintEntries) != 2 {
		t.Fatalf("expected both hints tracked, got %#v", m.hintEntries)
	}
	_, _ = m.Update(clearHintMsg{id: m.hintEntries[1].id})
	if m.hint != "started new session" {
		t.Fatalf("expected lower-priority hint restored, got %q", m.hint)
	}
}

func TestMessageClearsTransientHintButPreservesSystemHint(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.SetHintMsg{Hint: "started new session", ClearAfter: time.Second})
	_ = m.showHint("interrupt requested", hintOptions{
		priority:       tuievents.HintPriorityCritical,
		clearOnMessage: true,
		clearAfter:     time.Second,
	})
	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "done", Final: true})
	if m.hint != "started new session" {
		t.Fatalf("expected transient hint dismissed and system hint restored, got %q", m.hint)
	}
}

// ---------------------------------------------------------------------------
// Inline architecture: streaming + commit tests
// ---------------------------------------------------------------------------

func TestLogChunkCommitsCompletedLines(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, cmd := m.Update(tuievents.LogChunkMsg{Chunk: "* hello\n│ reasoning\n"})

	// In fullscreen mode, committed lines go to rendered lines (no tea.Println cmd).
	if cmd != nil {
		t.Fatal("expected nil cmd (no tea.Println in fullscreen mode)")
	}
	if m.streamLine != "" {
		t.Fatalf("expected empty stream buffer, got %q", m.streamLine)
	}
	if !m.hasCommittedLine {
		t.Fatal("expected hasCommittedLine to be true")
	}
	if len(m.renderedStyledLines()) < 2 {
		t.Fatalf("expected at least 2 history lines, got %d", len(m.renderedStyledLines()))
	}
}

func TestLogChunkPartialLineStaysInBuffer(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* partial content"})

	if m.streamLine != "* partial content" {
		t.Fatalf("expected '* partial content' in stream buffer, got %q", m.streamLine)
	}
}

func TestLogChunkMixedCompleteAndPartial(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, cmd := m.Update(tuievents.LogChunkMsg{Chunk: "line1\npartial"})

	// In fullscreen mode, committed lines go to rendered lines (no tea.Println cmd).
	if cmd != nil {
		t.Fatal("expected nil cmd (no tea.Println in fullscreen mode)")
	}
	if m.streamLine != "partial" {
		t.Fatalf("expected 'partial' in stream buffer, got %q", m.streamLine)
	}
	if len(m.renderedStyledLines()) < 1 {
		t.Fatal("expected at least 1 history line for committed 'line1'")
	}
}

func TestFlushStreamOnTaskResult(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* partial"})
	_, cmd := m.Update(tuievents.TaskResultMsg{})

	// In fullscreen mode, flush goes to rendered lines (no tea.Println cmd).
	if cmd != nil {
		t.Fatal("expected nil cmd (no ExitNow, no tea.Println)")
	}
	if m.streamLine != "" {
		t.Fatalf("expected empty stream buffer after task result, got %q", m.streamLine)
	}
	if m.running {
		t.Fatal("expected running to be false after task result")
	}
	if len(m.renderedStyledLines()) < 1 {
		t.Fatal("expected at least 1 history line from flushed stream")
	}
}

func TestBlockContinuationTracking(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Send reasoning line.
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "│ first reasoning line\n"})

	// Send continuation line (no prefix) → should inherit reasoning style.
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "continuation of reasoning\n"})

	// lastCommittedStyle should still be reasoning (via block continuation).
	// LineStyleReasoning = 2 (iota: Default=0, Assistant=1, Reasoning=2)
	if m.lastCommittedStyle != 2 {
		t.Fatalf("expected lastCommittedStyle to remain reasoning (2), got %d", m.lastCommittedStyle)
	}
}

func TestViewShowsStreamingContent(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* streaming text"})

	view := renderModel(m)
	if !strings.Contains(view, "streaming text") {
		t.Fatalf("expected view to contain streaming text, got:\n%s", view)
	}
}

func TestAssistantStreamUpdatesMarkdownBlockInPlace(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: "## He", Final: false})
	if m.activeAssistantID == "" {
		t.Fatal("expected active assistant block after partial stream")
	}
	startID := m.activeAssistantID

	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: "ading\n\n- one", Final: false})
	if m.activeAssistantID == "" {
		t.Fatal("expected active assistant block after second partial stream")
	}
	if m.activeAssistantID != startID {
		t.Fatalf("expected assistant block to be updated in place")
	}

	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: "## Heading\n\n- one\n- two", Final: true})
	if m.activeAssistantID != "" {
		t.Fatal("expected assistant block to be finalized")
	}

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "one") || !strings.Contains(joined, "two") {
		t.Fatalf("expected finalized markdown list content in history, got %q", joined)
	}
	if strings.Contains(joined, "## Heading") {
		t.Fatalf("expected heading markers to be hidden, got %q", joined)
	}
}

func TestAssistantFinalizedAnswerUsesGlamourForCodeFences(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	raw := "## 技术栈\n\n```python\nprint(\"hello\")\n```"

	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: raw[:12], Final: false})
	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: raw, Final: true})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Contains(joined, "```") {
		t.Fatalf("expected finalized answer to render fenced code via glamour, got %q", joined)
	}
	if !strings.Contains(joined, "print(\"hello\")") {
		t.Fatalf("expected rendered code content preserved, got %q", joined)
	}

	var found bool
	for _, block := range m.doc.Blocks() {
		ab, ok := block.(*AssistantBlock)
		if !ok {
			continue
		}
		if strings.Contains(ab.Raw, "print(\"hello\")") {
			found = true
			if ab.Streaming {
				t.Fatal("expected finalized assistant block to exit streaming mode")
			}
		}
	}
	if !found {
		t.Fatal("expected assistant block containing finalized code example")
	}
}

func TestReasoningStreamKeepsBlockStyleAcrossParagraphs(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ReasoningStreamMsg{
		Text:  "第一段\n\n第二段",
		Final: true,
	})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Contains(joined, "第一段") || strings.Contains(joined, "第二段") {
		t.Fatalf("expected finalized reasoning block to auto-collapse, got %q", joined)
	}
}

func TestReasoningBlockLinesRenderMarkdownWhileStreaming(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ReasoningStreamMsg{
		Text:  "## Thinking\n\n- one\n- two",
		Final: false,
	})

	if m.activeReasoningID == "" {
		t.Fatal("expected active reasoning block")
	}
	// Markdown is normalized into plain transcript form during streaming.
	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Contains(joined, "## Thinking") {
		t.Fatalf("expected reasoning stream to hide heading markers, got %q", joined)
	}
	if !strings.Contains(joined, "Thinking") || !strings.Contains(joined, "one") || !strings.Contains(joined, "two") {
		t.Fatalf("expected rendered content to be present, got %q", joined)
	}
	raw := strings.Join(m.renderedStyledLines(), "\n")
	if !strings.Contains(raw, "\x1b[") {
		t.Fatalf("expected reasoning stream to retain ANSI styling, got %q", raw)
	}
}

func TestAssistantFinalRenderPreservesWideCharacters(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{
		Kind:  "answer",
		Text:  "我有以下工具可以使用：\n\n    文件系统工具",
		Final: false,
	})
	_, _ = m.Update(tuievents.AssistantStreamMsg{
		Kind:  "answer",
		Text:  "我有以下工具可以使用：\n\n    文件系统工具",
		Final: true,
	})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "文件系统工具") {
		t.Fatalf("expected wide-character text preserved after final render, got %q", joined)
	}
}

func TestAssistantAfterReasoningHasNoExtraGap(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ReasoningStreamMsg{
		Text:  "thinking",
		Final: true,
	})
	_, _ = m.Update(tuievents.AssistantStreamMsg{
		Text:  "final answer",
		Final: true,
	})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Contains(joined, "thinking") {
		t.Fatalf("expected finalized reasoning hidden before answer render, got %q", joined)
	}
	if !strings.Contains(joined, "* final answer") {
		t.Fatalf("expected assistant answer to remain visible, got %q", joined)
	}
}

func TestAssistantStreamMergesCumulativeChunks(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{
		Kind:  "answer",
		Text:  "Hello",
		Final: false,
	})
	_, _ = m.Update(tuievents.AssistantStreamMsg{
		Kind:  "answer",
		Text:  "Hello world",
		Final: false,
	})
	_, _ = m.Update(tuievents.AssistantStreamMsg{
		Kind:  "answer",
		Text:  "",
		Final: true,
	})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Count(joined, "Hello world") != 1 {
		t.Fatalf("expected merged cumulative output once, got %q", joined)
	}
}

func TestReasoningThenFinalAnswerReusesExistingAnswerBlock(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{
		Kind:  "answer",
		Text:  "Hello",
		Final: false,
	})
	_, _ = m.Update(tuievents.AssistantStreamMsg{
		Kind:  "reasoning",
		Text:  "thinking",
		Final: true,
	})
	_, _ = m.Update(tuievents.AssistantStreamMsg{
		Kind:  "answer",
		Text:  "Hello world",
		Final: true,
	})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Count(joined, "Hello world") != 1 {
		t.Fatalf("expected one finalized answer block, got %q", joined)
	}
	if strings.Contains(joined, "* Hello\n* Hello world") {
		t.Fatalf("expected final answer to replace partial block in place, got %q", joined)
	}
}

func TestMergeStreamChunkDoesNotDropShortDelta(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		incoming string
		want     string
	}{
		{"repeated char delta", "好的，直接输出", "好", "好的，直接输出好"},
		{"heading marker delta", "# Title\n\n", "# ", "# Title\n\n# "},
		{"list marker delta", "- item 1\n", "- ", "- item 1\n- "},
		{"bold marker delta", "**bold**\n", "**", "**bold**\n**"},
		{"blockquote delta", "> quote 1\n", "> ", "> quote 1\n> "},
		{"space delta", " indented", " ", " indented "},
		{"star list delta", "* first\n", "* ", "* first\n* "},
		{"numbered list delta", "1. first\n", "1.", "1. first\n1."},
		{"code fence delta", "text\n", "```", "text\n```"},
		// Cumulative replay detection still works for long text.
		{"long cumulative replay", "Hello world! This is a complete sentence.", "Hello world! This is a comple", "Hello world! This is a complete sentence."},
		// Forward-prefix false-positive: a short accumulated buffer whose
		// content coincidentally appears at the start of the next delta token
		// must NOT be silently dropped (regression: old code treated any
		// incoming that starts with existing as a cumulative update).
		{"short existing prefix of incoming", "好", "好的，直接输出你的问题！", "好好的，直接输出你的问题！"},
		{"single chinese char prefix", "你", "你好，", "你你好，"},
		{"two char prefix collision", "ab", "abcdef", "ababcdef"},
		// Long text where incoming starts with existing: since we removed
		// cumulative-mode support entirely (all providers are delta), this
		// must also concatenate.
		{"long existing prefix of incoming", "Hello world! This is a test.", "Hello world! This is a test. More content.", "Hello world! This is a test.Hello world! This is a test. More content."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeStreamChunk(tt.existing, tt.incoming, false)
			if got != tt.want {
				t.Errorf("mergeStreamChunk(%q, %q, false) =\n  got  %q\n  want %q",
					tt.existing, tt.incoming, got, tt.want)
			}
		})
	}
}

func TestStreamingDeltaTokensPreserveMarkdownStructure(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Simulate token-by-token delta streaming (like OpenAI-compatible API).
	tokens := []string{
		"好的", "：", "\n", "\n",
		"#", " TUI", " 测试",
		"\n", "\n",
		"##", " 格式",
		"\n", "\n",
		"1.", " **", "加粗", "**",
		"\n",
		"2.", " *", "斜体", "*",
	}
	for _, tok := range tokens {
		_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: tok, Final: false})
	}

	full := strings.Join(tokens, "")
	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: full, Final: true})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	// Heading markers should be consumed by markdown renderer.
	if strings.Contains(joined, "# TUI") || strings.Contains(joined, "## 格式") {
		t.Fatalf("expected heading markers hidden by markdown rendering, got:\n%s", joined)
	}
	// Bold markers should be consumed.
	if strings.Contains(joined, "**加粗**") {
		t.Fatalf("expected bold markers hidden, got:\n%s", joined)
	}
	// Content should still be present.
	if !strings.Contains(joined, "TUI") || !strings.Contains(joined, "加粗") {
		t.Fatalf("expected content preserved, got:\n%s", joined)
	}
}

func TestReasoningStreamDoesNotAccumulateAcrossToolTurns(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{
		Kind:  "reasoning",
		Text:  "phase1",
		Final: false,
	})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ a.txt\n"})
	_, _ = m.Update(tuievents.AssistantStreamMsg{
		Kind:  "reasoning",
		Text:  "phase2",
		Final: false,
	})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Contains(joined, "phase1phase2") {
		t.Fatalf("expected reasoning blocks separated by tool turn, got %q", joined)
	}
	if strings.Contains(joined, "· phase1") {
		t.Fatalf("expected first reasoning block removed once tool turn started, got %q", joined)
	}
	if !strings.Contains(joined, "▸ Exploring 1 files") || !strings.Contains(joined, "Read a.txt") || !strings.Contains(joined, "· phase2") {
		t.Fatalf("expected tool turn and next reasoning block rendered, got %q", joined)
	}
}

func TestAssistantFinalDuplicateEventIsSuppressed(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "done", Final: true})
	if m.activeAssistantID != "" {
		t.Fatal("expected final one-shot answer block to close immediately")
	}
	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "done", Final: true})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Count(joined, "* done") != 1 {
		t.Fatalf("expected duplicated final answer suppressed, got %q", joined)
	}
}

func TestApprovalPromptUsesChoiceListAndArrowSubmit(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	baseBottom := m.bottomSectionHeight()

	respCh := make(chan tuievents.PromptResponse, 1)
	_, _ = m.Update(tuievents.PromptRequestMsg{
		Prompt:        "Would you like to run the following command?",
		Details:       []tuievents.PromptDetail{{Label: "BASH", Value: "pfctl -s info", Emphasis: true}},
		DefaultChoice: "y",
		Choices: []tuievents.PromptChoice{
			{Label: "approve", Value: "y", Detail: "this time"},
			{Label: "always", Value: "a", Detail: "remember go test"},
			{Label: "reject", Value: "n", Detail: "skip it"},
		},
		Response: respCh,
	})
	if m.activePrompt == nil {
		t.Fatal("expected active prompt")
	}
	if len(m.activePrompt.choices) != 3 {
		t.Fatalf("expected 3 approval choices, got %d", len(m.activePrompt.choices))
	}
	if m.activePrompt.choiceIndex != 0 {
		t.Fatalf("expected default selection at allow, got %d", m.activePrompt.choiceIndex)
	}
	if got := m.bottomSectionHeight(); got != baseBottom {
		t.Fatalf("expected prompt overlay not to change bottom height, before=%d after=%d", baseBottom, got)
	}
	view := stripModelView(m)
	if !strings.Contains(view, "BASH: pfctl -s info") {
		t.Fatalf("expected compact approval summary in modal, got %q", view)
	}
	if !strings.Contains(view, "approve") || !strings.Contains(view, "always") || !strings.Contains(view, "reject") {
		t.Fatalf("expected approval list options in modal, got %q", view)
	}
	if !strings.Contains(view, "↑/↓ move  enter confirm  esc cancel") {
		t.Fatalf("expected approval footer hint in view, got %q", view)
	}
	if !strings.Contains(view, "╭") || !strings.Contains(view, "Would you like to run the following command?") {
		t.Fatalf("expected boxed approval modal in view, got %q", view)
	}
	var modalLine string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "╭") {
			modalLine = line
			break
		}
	}
	if modalLine == "" {
		t.Fatalf("expected modal border line in view, got %q", view)
	}
	if !strings.HasPrefix(modalLine, strings.Repeat(" ", tuikit.GutterNarrative)+"╭") {
		t.Fatalf("expected modal to align with narrative gutter, got %q", modalLine)
	}

	_, _ = m.Update(keyPress(tea.KeyDown))
	_, _ = m.Update(keyPress(tea.KeyEnter))
	select {
	case resp := <-respCh:
		if resp.Err != nil {
			t.Fatalf("expected successful prompt response, got err=%v", resp.Err)
		}
		if resp.Line != "a" {
			t.Fatalf("expected selected value 'a', got %q", resp.Line)
		}
	default:
		t.Fatal("expected prompt response after enter")
	}
}

func TestApprovalPromptBacktabMovesSelectionUp(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	respCh := make(chan tuievents.PromptResponse, 1)
	_, _ = m.Update(tuievents.PromptRequestMsg{
		Prompt:        "Would you like to run the following command?",
		DefaultChoice: "a",
		Choices: []tuievents.PromptChoice{
			{Label: "approve", Value: "y"},
			{Label: "always", Value: "a"},
			{Label: "reject", Value: "n"},
		},
		Response: respCh,
	})
	if m.activePrompt == nil {
		t.Fatal("expected active prompt")
	}
	if m.activePrompt.choiceIndex != 1 {
		t.Fatalf("expected default selection at always, got %d", m.activePrompt.choiceIndex)
	}

	_, _ = m.Update(keyText("backtab"))

	if m.activePrompt.choiceIndex != 0 {
		t.Fatalf("expected backtab to move selection up, got %d", m.activePrompt.choiceIndex)
	}
}

func TestPromptChoiceRequestUsesExplicitChoicesAndFilter(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	respCh := make(chan tuievents.PromptResponse, 1)
	_, _ = m.Update(tuievents.PromptRequestMsg{
		Prompt: "Select model",
		Choices: []tuievents.PromptChoice{
			{Label: "openai/gpt-4o", Value: "gpt-4o", Detail: "catalog"},
			{Label: "openai/o3", Value: "o3", Detail: "reasoning"},
		},
		DefaultChoice: "gpt-4o",
		Filterable:    true,
		Response:      respCh,
	})
	if m.activePrompt == nil {
		t.Fatal("expected active prompt")
	}
	if len(m.activePrompt.choices) != 2 {
		t.Fatalf("expected explicit prompt choices, got %d", len(m.activePrompt.choices))
	}

	_, _ = m.Update(keyText("o3"))
	if string(m.activePrompt.filter) != "o3" {
		t.Fatalf("expected prompt filter to update, got %q", string(m.activePrompt.filter))
	}
	view := stripModelView(m)
	if !strings.Contains(view, "openai/o3") {
		t.Fatalf("expected filtered choice in modal, got %q", view)
	}

	_, _ = m.Update(keyPress(tea.KeyEnter))
	select {
	case resp := <-respCh:
		if resp.Err != nil {
			t.Fatalf("expected successful prompt response, got err=%v", resp.Err)
		}
		if resp.Line != "o3" {
			t.Fatalf("expected selected value 'o3', got %q", resp.Line)
		}
	default:
		t.Fatal("expected prompt response after enter")
	}
}

func TestPromptChoiceRequestSupportsMultiSelect(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	respCh := make(chan tuievents.PromptResponse, 1)
	_, _ = m.Update(tuievents.PromptRequestMsg{
		Prompt: "Select models",
		Choices: []tuievents.PromptChoice{
			{Label: "openai/gpt-4o", Value: "gpt-4o"},
			{Label: "openai/o3", Value: "o3"},
		},
		MultiSelect: true,
		Response:    respCh,
	})
	_, _ = m.Update(keyPress(tea.KeySpace))
	_, _ = m.Update(keyPress(tea.KeyDown))
	_, _ = m.Update(keyPress(tea.KeySpace))
	view := stripModelView(m)
	if !strings.Contains(view, "[x] openai/gpt-4o") || !strings.Contains(view, "[x] openai/o3") {
		t.Fatalf("expected checked markers in view, got %q", view)
	}
	_, _ = m.Update(keyPress(tea.KeyEnter))
	select {
	case resp := <-respCh:
		if resp.Line != "gpt-4o,o3" {
			t.Fatalf("unexpected multi-select response %q", resp.Line)
		}
	default:
		t.Fatal("expected prompt response after enter")
	}
}

func TestPromptChoiceRequestKeepsAlwaysVisibleChoiceWhenFilterMisses(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	respCh := make(chan tuievents.PromptResponse, 1)
	_, _ = m.Update(tuievents.PromptRequestMsg{
		Prompt: "Select model",
		Choices: []tuievents.PromptChoice{
			{Label: "openai/gpt-4o", Value: "gpt-4o"},
			{Label: "输入自定义模型名", Value: "__custom_model__", AlwaysVisible: true},
		},
		Filterable:  true,
		MultiSelect: true,
		Response:    respCh,
	})

	_, _ = m.Update(keyText("doubao-seed-2-0-code"))
	view := stripModelView(m)
	if !strings.Contains(view, "输入自定义模型名") {
		t.Fatalf("expected always visible custom choice in prompt, got %q", view)
	}

	_, _ = m.Update(keyPress(tea.KeyEnter))
	select {
	case resp := <-respCh:
		if resp.Err != nil {
			t.Fatalf("expected successful prompt response, got err=%v", resp.Err)
		}
		if resp.Line != "__custom_model__" {
			t.Fatalf("expected custom choice selected, got %q", resp.Line)
		}
	default:
		t.Fatal("expected prompt response after enter")
	}
}

func TestPromptChoiceRequestWithCustomChoiceUsesCustomOnEmptyEnter(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	respCh := make(chan tuievents.PromptResponse, 1)
	_, _ = m.Update(tuievents.PromptRequestMsg{
		Prompt: "Select model",
		Choices: []tuievents.PromptChoice{
			{Label: "openai/gpt-4o", Value: "gpt-4o"},
			{Label: "输入自定义模型名", Value: "__custom_model__", AlwaysVisible: true},
		},
		Filterable:  true,
		MultiSelect: true,
		Response:    respCh,
	})

	_, _ = m.Update(keyPress(tea.KeyEnter))
	select {
	case resp := <-respCh:
		if resp.Err != nil {
			t.Fatalf("expected successful prompt response, got err=%v", resp.Err)
		}
		if resp.Line != "__custom_model__" {
			t.Fatalf("expected custom choice selected on empty enter, got %q", resp.Line)
		}
	default:
		t.Fatal("expected prompt response after enter")
	}
}

func TestPromptChoiceScrollKeepsSelectionVisible(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	respCh := make(chan tuievents.PromptResponse, 1)
	choices := make([]tuievents.PromptChoice, 0, 12)
	for i := 1; i <= 12; i++ {
		label := fmt.Sprintf("model-%02d", i)
		choices = append(choices, tuievents.PromptChoice{Label: label, Value: label})
	}
	_, _ = m.Update(tuievents.PromptRequestMsg{
		Prompt:   "Select model",
		Choices:  choices,
		Response: respCh,
	})

	for i := 0; i < 9; i++ {
		_, _ = m.Update(keyPress(tea.KeyDown))
	}

	if m.activePrompt == nil {
		t.Fatal("expected active prompt")
	}
	if m.activePrompt.choiceIndex != 9 {
		t.Fatalf("expected choice index 9, got %d", m.activePrompt.choiceIndex)
	}
	if m.activePrompt.scrollOffset == 0 {
		t.Fatal("expected prompt list to scroll once selection moved past visible window")
	}

	view := stripModelView(m)
	if !strings.Contains(view, "model-10") {
		t.Fatalf("expected selected item to remain visible in view, got %q", view)
	}
	if strings.Contains(view, "model-01") {
		t.Fatalf("expected window to scroll past early items, got %q", view)
	}
}

func TestClearHistoryMsgResetsViewportContent(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine:     noopExecute,
		ShowWelcomeCard: true,
		Version:         "0.0.1",
		Workspace:       "/tmp/work",
	})
	_ = m.Init()
	resizeModel(m)
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* stale line\n"})
	if !strings.Contains(strings.Join(m.renderedStyledLines(), "\n"), "stale line") {
		t.Fatal("expected stale line before clear")
	}
	_, _ = m.Update(tuievents.ClearHistoryMsg{})
	joined := strings.Join(m.renderedStyledLines(), "\n")
	if strings.Contains(joined, "stale line") {
		t.Fatalf("expected stale line removed after clear, got %q", joined)
	}
	if !strings.Contains(joined, "CAELIS") {
		t.Fatalf("expected welcome card after clear, got %q", joined)
	}
}

func TestDiffBlockMsgRendersStructuredDiff(t *testing.T) {
	m := newTestModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	_, _ = m.Update(tuievents.DiffBlockMsg{
		Tool:    "PATCH",
		Path:    "a.txt",
		Hunk:    "@@ -1,2 +1,2 @@",
		Old:     "line1\nold",
		New:     "line1\nnew",
		Preview: "--- old\n+++ new\n-line1\n-old\n+line1\n+new",
	})

	{
		count := 0
		for _, b := range m.doc.Blocks() {
			if b.Kind() == BlockDiff {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected one diff block, got %d", count)
		}
	}
	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "PATCH edited a.txt") {
		t.Fatalf("expected diff header, got %q", joined)
	}
	if !strings.Contains(joined, "@@ -1,2 +1,2 @@") {
		t.Fatalf("expected hunk line, got %q", joined)
	}
}

func TestDiffBlockResizeRerendersAdaptiveLayout(t *testing.T) {
	m := newTestModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	_, _ = m.Update(tuievents.DiffBlockMsg{
		Tool: "PATCH",
		Path: "a.txt",
		Old:  "line1\nold",
		New:  "line1\nnew",
	})
	before := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	_, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	after := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if before == after {
		t.Fatalf("expected resize to rerender diff block, got identical output: %q", after)
	}
	if strings.Contains(after, " │ ") {
		t.Fatalf("expected wide diff to stay within centered main column instead of split full-width layout, got %q", after)
	}
}

func TestClearHistoryResetsDiffBlocks(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	_, _ = m.Update(tuievents.DiffBlockMsg{
		Tool: "PATCH",
		Path: "a.txt",
		Old:  "old",
		New:  "new",
	})
	{
		count := 0
		for _, b := range m.doc.Blocks() {
			if b.Kind() == BlockDiff {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected one diff block, got %d", count)
		}
	}
	_, _ = m.Update(tuievents.ClearHistoryMsg{})
	{
		count := 0
		for _, b := range m.doc.Blocks() {
			if b.Kind() == BlockDiff {
				count++
			}
		}
		if count != 0 {
			t.Fatalf("expected diff blocks reset on clear history, got %d", count)
		}
	}
}

func TestViewShowsBreathingHintWhenRunning(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.running = true
	m.startRunningAnimation()
	m.runningTip = 0
	m.syncViewportContent()
	view := stripModelView(m)

	if !strings.Contains(view, "Send follow-up guidance while the current run is still active.") {
		t.Fatalf("expected running carousel text in view when running, got:\n%s", view)
	}
	if strings.Contains(view, "thinking") || strings.Contains(view, "Tip:") {
		t.Fatalf("did not expect thinking/tip prefix in running hint, got:\n%s", view)
	}
	if strings.Contains(strings.Join(m.viewportPlainLines, "\n"), "Send follow-up guidance while the current run is still active.") {
		t.Fatalf("did not expect running hint to be rendered inside viewport history, got: %q", strings.Join(m.viewportPlainLines, "\n"))
	}
}

func TestRunningHintAnimationAdvancesOnSpinnerTicks(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true
	m.startRunningAnimation()
	before := m.buildHintText()

	for i := 0; i < runningHintRotateEveryTicks+2; i++ {
		_, _ = m.Update(spinner.TickMsg{})
	}
	after := m.buildHintText()
	if before == after {
		t.Fatalf("expected running hint to animate/rotate, got unchanged text: %q", after)
	}
}

func TestViewShowsInputWhenRunningForQueueing(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.running = true
	view := stripModelView(m)
	if !strings.Contains(view, ">") {
		t.Fatalf("expected input prompt while running for queueing, got:\n%s", view)
	}
}

func TestViewHidesPendingQueueWhileRunning(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.running = true
	m.startRunningAnimation()
	m.runningTip = 0
	m.pendingQueue = &pendingPrompt{execLine: "first", displayLine: "first"}
	view := stripModelView(m)
	if !strings.Contains(view, "↪ first") {
		t.Fatalf("expected pending queue drawer in running view, got:\n%s", view)
	}
}

func TestViewShowsToolOutputPanelWithScrollableHistory(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true, State: "running"})
	for i := 1; i <= 6; i++ {
		_, _ = m.Update(tuievents.ToolStreamMsg{
			Tool:   "BASH",
			CallID: "call-1",
			Stream: "stdout",
			Chunk:  fmt.Sprintf("line-%d\n", i),
		})
	}

	view := renderModel(m)
	if strings.Contains(view, "terminal output") {
		t.Fatalf("expected rich tool output panel, got:\n%s", view)
	}
	if strings.Contains(view, "shell task") || strings.Contains(view, "<1s") {
		t.Fatalf("did not expect inline bash shell header/timer inside panel, got:\n%s", view)
	}
	for _, want := range []string{"line-3", "line-4", "line-5", "line-6"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected %q in tool output panel, got:\n%s", want, view)
		}
	}
}

func TestViewShowsCompactToolOutputPanelForShortOutput(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "BASH",
		CallID: "call-1",
		Stream: "stdout",
		Chunk:  "short\n",
	})

	view := stripModelView(m)
	if !strings.Contains(view, "short") {
		t.Fatalf("expected compact inline bash output, got:\n%s", view)
	}
}

func TestToolOutputPanelFiltersBlankLines(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "BASH",
		CallID: "call-1",
		Stream: "stderr",
		Chunk:  "line-1\n\n   \nline-2\n",
	})

	view := stripModelView(m)
	if strings.Contains(view, "\n! \n") {
		t.Fatalf("did not expect blank tool output rows, got:\n%s", view)
	}
	if !strings.Contains(view, "line-1") || !strings.Contains(view, "line-2") {
		t.Fatalf("expected non-blank tool output rows, got:\n%s", view)
	}
}

func TestBashFinalKeepsPanelVisibleUntilNewContentArrives(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH date\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "BASH",
		CallID: "call-1",
		Stream: "stdout",
		Chunk:  "line-1\nline-2\n",
	})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "BASH",
		CallID: "call-1",
		State:  "completed",
		Final:  true,
	})

	view := stripModelView(m)
	if !strings.Contains(view, "BASH date") || !strings.Contains(view, "line-1") || !strings.Contains(view, "line-2") {
		t.Fatalf("expected final bash panel to stay visible through the minimum display window, got:\n%s", view)
	}
	blockID := m.toolOutputBlockIDs["call-1"]
	if blockID == "" {
		t.Fatal("expected bash panel to exist in doc after final")
	}
	b := m.doc.Find(blockID)
	if b == nil {
		t.Fatal("expected bash panel block to exist in document")
	}
	bp, ok := b.(*BashPanelBlock)
	if !ok {
		t.Fatalf("expected bash panel block, got %T", b)
	}
	if !bp.Expanded {
		t.Fatal("expected final BASH panel to remain expanded before animation finishes")
	}
	_, _ = m.Update(frameTickMsg{at: bp.CollapseAt})
	_, _ = m.Update(frameTickMsg{at: bp.CollapseAt.Add(bp.CollapseFor / 2)})
	if !bp.Expanded || bp.VisibleLines >= toolOutputPreviewLines {
		t.Fatalf("expected bash panel to be mid-collapse during animation, got expanded=%v visible=%d", bp.Expanded, bp.VisibleLines)
	}
	driveBashPanelCollapse(m, bp)
	view = stripModelView(m)
	if !strings.Contains(view, "BASH date") || strings.Contains(view, "line-1") || strings.Contains(view, "line-2") {
		t.Fatalf("expected bash panel to collapse back into the call line after animation, got:\n%s", view)
	}
}

func TestBashFinalKeepsPanelAfterDivider(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH done\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "BASH",
		CallID: "call-1",
		Stream: "stdout",
		Chunk:  "line-1\nline-2\n",
	})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "BASH",
		CallID: "call-1",
		State:  "completed",
		Final:  true,
	})

	// Panel should still exist in the document.
	blockID := m.toolOutputBlockIDs["call-1"]
	if blockID == "" {
		t.Fatal("expected bash panel to exist after final")
	}
	bp := m.doc.Find(blockID).(*BashPanelBlock)
	driveBashPanelCollapse(m, bp)
	view := stripModelView(m)
	if !strings.Contains(view, "BASH done") || strings.Contains(view, "line-1") || strings.Contains(view, "line-2") {
		t.Fatalf("expected bash panel to persist but stay collapsed, got:\n%s", view)
	}
}

func TestBashPanelPersistsAfterNewContent(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH persist\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "BASH",
		CallID: "call-1",
		Stream: "stdout",
		Chunk:  "line-1\nline-2\nline-3\nline-4\n",
	})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "BASH",
		CallID: "call-1",
		State:  "completed",
		Final:  true,
	})
	blockID := m.toolOutputBlockIDs["call-1"]
	if blockID == "" {
		t.Fatal("expected bash panel to persist in document")
	}
	bp := m.doc.Find(blockID).(*BashPanelBlock)
	driveBashPanelCollapse(m, bp)
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* next block\n"})

	view := stripModelView(m)
	// Panel should persist in collapsed form after final; subsequent content still renders.
	if !strings.Contains(view, "next block") || !strings.Contains(view, "BASH persist") {
		t.Fatalf("expected collapsed bash panel and subsequent content, got:\n%s", view)
	}
	for _, want := range []string{"line-1", "line-2", "line-3", "line-4"} {
		if strings.Contains(view, want) {
			t.Fatalf("expected %q to be hidden by collapsed panel, got:\n%s", want, view)
		}
	}

}

func TestViewAnchorsToolOutputBelowMatchingCallLines(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH first\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "BASH",
		CallID: "call-1",
		Stream: "stdout",
		Chunk:  "bash-line\n",
	})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SPAWN second\n"})
	_, _ = m.Update(tuievents.SubagentStartMsg{SpawnID: "spawn-2", AttachTarget: "spawn-2", Agent: "self", CallID: "call-2"})
	_, _ = m.Update(tuievents.SubagentStreamMsg{SpawnID: "spawn-2", Stream: "assistant", Chunk: "spawn-line\n"})

	view := stripModelView(m)
	bashIdx := strings.Index(view, "BASH first")
	bashLineIdx := strings.Index(view, "bash-line")
	spawnCallIdx := strings.Index(view, "SPAWN second")
	spawnLineIdx := strings.Index(view, "spawn-line")
	if bashIdx < 0 || bashLineIdx < 0 || spawnCallIdx < 0 || spawnLineIdx < 0 {
		t.Fatalf("expected call lines and anchored outputs, got:\n%s", view)
	}
	if bashIdx >= bashLineIdx || bashLineIdx >= spawnCallIdx || spawnCallIdx >= spawnLineIdx {
		t.Fatalf("expected each tool output block below its own call line, got:\n%s", view)
	}
}

func TestSpawnTaskStreamIgnoredInFavorOfSubagentPanel(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SPAWN demo task\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "SPAWN", TaskID: "task-1", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "SPAWN",
		TaskID: "task-1",
		CallID: "call-1",
		Stream: "assistant",
		Chunk:  "this duplicate preview should stay hidden\n",
	})
	// In the document model, SPAWN tool streams are suppressed (no BashPanelBlock for SPAWN).
	if blockID := m.toolOutputBlockIDs["task-1"]; blockID != "" {
		t.Fatalf("did not expect SPAWN tool-output panel in document, got block %s", blockID)
	}

	_, _ = m.Update(tuievents.SubagentStartMsg{
		SpawnID:      "spawn-1",
		AttachTarget: "child-1",
		Agent:        "self",
		CallID:       "call-1",
	})
	_, _ = m.Update(tuievents.SubagentStreamMsg{
		SpawnID: "spawn-1",
		Stream:  "assistant",
		Chunk:   "child output",
	})

	view := stripModelView(m)
	if strings.Count(view, "SPAWN demo task") != 1 {
		t.Fatalf("expected single SPAWN call line, got:\n%s", view)
	}
	if strings.Contains(view, "this duplicate preview should stay hidden") {
		t.Fatalf("did not expect generic SPAWN tool preview in view, got:\n%s", view)
	}
	if !strings.Contains(view, "child output") {
		t.Fatalf("expected subagent panel content visible, got:\n%s", view)
	}
}

func TestViewShowsInputWhenNotRunning(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.running = false
	view := renderModel(m)

	if !strings.Contains(view, ">") {
		t.Fatalf("expected '>' prompt in view, got:\n%s", view)
	}
}

func TestViewShowsStatusBar(t *testing.T) {
	m := NewModel(Config{
		Workspace: "/test/workspace",
	})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	view := renderModel(m)
	if !strings.Contains(view, "/test/workspace") {
		t.Fatalf("expected workspace in status bar, got:\n%s", view)
	}
}

func TestStatusHeaderTruncatesLongWorkspaceAndModelToSingleLine(t *testing.T) {
	m := NewModel(Config{
		Workspace: "~/WorkDir/xueyongzhi/caelis [⎇ codex/tui-beautification-v0.0.34-super-long-branch-name]",
	})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.statusModel = "minimax/minimax-m2.7-highspeed"

	header := ansi.Strip(m.renderStatusHeader())
	if strings.Contains(header, "\n") {
		t.Fatalf("expected single-line status header, got %q", header)
	}
	if got := displayColumns(header); got > m.fixedRowWidth() {
		t.Fatalf("expected status header to fit fixed row width %d, got %d cols: %q", m.fixedRowWidth(), got, header)
	}
	if !strings.Contains(header, "[⎇ ") {
		t.Fatalf("expected branch marker preserved, got %q", header)
	}
	if !strings.Contains(header, "...") {
		t.Fatalf("expected status header truncation, got %q", header)
	}
}

func TestCtrlVPasteShowsAttachmentHint(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		PasteClipboardImage: func() ([]string, string, error) {
			return []string{"screenshot.png"}, "1 image attached — type message and press enter", nil
		},
	})
	resizeModel(m)
	m.keys = defaultKeyMap(false)
	_, _ = m.Update(keyPress('v', tea.ModCtrl))
	if m.attachmentCount != 1 {
		t.Fatalf("expected attachment count 1, got %d", m.attachmentCount)
	}
	if got := strings.TrimSpace(ansi.Strip(m.renderHintRow())); got != "" {
		t.Fatalf("expected attachment hint line to stay empty, got %q", got)
	}
	line := ansi.Strip(m.renderInputBar())
	if !strings.Contains(line, "[screenshot.png]") {
		t.Fatalf("expected attachment token in input bar, got %q", line)
	}
}

func TestAttachmentLabelShownInInputBar(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.setInputAttachments([]inputAttachment{{Name: "image.png", Offset: 0}})
	m.syncTextareaChrome()
	line := ansi.Strip(m.renderInputBar())
	if !strings.Contains(line, ">") {
		t.Fatalf("expected prompt, got %q", line)
	}
	if !strings.Contains(line, "[image.png]") {
		t.Fatalf("expected attachment label shown, got %q", line)
	}
	lines := strings.Split(line, "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "> [image.png]") {
		t.Fatalf("expected attachment inline after prompt, got %q", line)
	}
}

func TestAttachmentCountMsgClearsAttachmentNamesAtZero(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.attachmentCount = 1
	m.attachmentNames = []string{"image.png"}
	_, _ = m.Update(tuievents.AttachmentCountMsg{Count: 0})
	if m.attachmentCount != 0 {
		t.Fatalf("expected attachment count reset, got %d", m.attachmentCount)
	}
	if len(m.attachmentNames) != 0 {
		t.Fatalf("expected attachment names cleared, got %#v", m.attachmentNames)
	}
}

func TestSyncTextareaChromeUsesAttachmentPromptPrefix(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.setInputAttachments([]inputAttachment{{Name: "clip.png", Offset: 0}})
	m.syncTextareaChrome()
	m.textarea.SetValue("你🙂a")
	m.textarea.CursorEnd()
	m.adjustTextareaHeight()
	m.syncInputFromTextarea()

	rendered := ansi.Strip(m.renderInputBar())
	if !strings.Contains(rendered, "[clip.png]") {
		t.Fatalf("expected attachment label in input bar, got %q", rendered)
	}
	if !strings.Contains(rendered, "> [clip.png] 你🙂a") {
		t.Fatalf("expected inline attachment token before text, got %q", rendered)
	}
}

func TestTerminalPasteMsgInsertsTextIntoComposer(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tea.PasteMsg{Content: "hello paste"})

	if got := m.textarea.Value(); got != "hello paste" {
		t.Fatalf("expected pasted text in composer, got %q", got)
	}
}

func TestWSLProfileCtrlAltVPastesImage(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		PasteClipboardImage: func() ([]string, string, error) {
			return []string{"wsl-shot.png"}, "", nil
		},
	})
	resizeModel(m)
	m.keys = defaultKeyMap(true)

	_, _ = m.Update(keyPress('v', tea.ModCtrl, tea.ModAlt))

	if m.attachmentCount != 1 {
		t.Fatalf("expected WSL image paste to attach one image, got %d", m.attachmentCount)
	}
	if got := ansi.Strip(m.renderInputBar()); !strings.Contains(got, "[wsl-shot.png]") {
		t.Fatalf("expected WSL image attachment token, got %q", got)
	}
}

func TestWSLProfileCtrlVPastesClipboardTextFallback(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		ReadClipboardText: func() (string, error) {
			return "hello from WSL", nil
		},
	})
	resizeModel(m)
	m.keys = defaultKeyMap(true)

	_, _ = m.Update(keyPress('v', tea.ModCtrl))

	if got := m.textarea.Value(); got != "hello from WSL" {
		t.Fatalf("expected WSL ctrl+v text fallback, got %q", got)
	}
}

func TestWSLProfilePasteMsgStillInsertsText(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.keys = defaultKeyMap(true)

	_, _ = m.Update(tea.PasteMsg{Content: "paste event text"})

	if got := m.textarea.Value(); got != "paste event text" {
		t.Fatalf("expected paste event text under WSL profile, got %q", got)
	}
}

func TestTextPasteFallbackShowsErrorHint(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		ReadClipboardText: func() (string, error) {
			return "", fmt.Errorf("clipboard unavailable")
		},
	})
	resizeModel(m)
	m.keys = defaultKeyMap(true)

	_, cmd := m.Update(keyPress('v', tea.ModCtrl))
	if cmd == nil {
		t.Fatal("expected hint command when text paste fallback fails")
	}
	if got := m.hint; got != "paste: clipboard unavailable" {
		t.Fatalf("expected explicit paste error hint, got %q", got)
	}
	if len(m.renderedStyledLines()) == 0 || !strings.Contains(ansi.Strip(m.renderedStyledLines()[len(m.renderedStyledLines())-1]), "paste: clipboard unavailable") {
		t.Fatalf("expected paste error logged in history, got %#v", m.renderedStyledLines())
	}
}

func TestPasteImageInsertsAttachmentAtCursor(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		PasteClipboardImage: func() ([]string, string, error) {
			return []string{"mid.png"}, "", nil
		},
	})
	resizeModel(m)
	m.keys = defaultKeyMap(false)
	m.textarea.SetValue("hello world")
	m.moveTextareaCursorToIndex(len([]rune("hello ")))
	m.syncInputFromTextarea()

	_, _ = m.Update(keyPress('v', tea.ModCtrl))

	rendered := ansi.Strip(m.renderInputBar())
	if !strings.Contains(rendered, "> hello [mid.png] world") {
		t.Fatalf("expected attachment inserted at cursor, got %q", rendered)
	}
}

func TestEnterSubmitsImageOnlyPrompt(t *testing.T) {
	var got Submission
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			got = submission
			return tuievents.TaskResultMsg{}
		},
	})
	resizeModel(m)
	m.setInputAttachments([]inputAttachment{{Name: "clip.png", Offset: 0}})

	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	if !findAndRunTaskResult(cmd(), m) {
		t.Fatal("expected TaskResultMsg in submit command")
	}
	if got.Text != "" {
		t.Fatalf("expected empty text payload, got %q", got.Text)
	}
	if len(got.Attachments) != 1 || got.Attachments[0] != (Attachment{Name: "clip.png", Offset: 0}) {
		t.Fatalf("expected single attachment-only submission, got %+v", got.Attachments)
	}
}

func TestHistoryRecallRestoresAttachmentsAsMetadata(t *testing.T) {
	var restored []string
	m := NewModel(Config{
		SetAttachments: func(names []string) []string {
			restored = append([]string(nil), names...)
			return append([]string(nil), names...)
		},
	})
	resizeModel(m)
	m.textarea.SetValue("hello world")
	m.textarea.CursorEnd()
	m.syncInputFromTextarea()
	m.setInputAttachments([]inputAttachment{{Name: "history.png", Offset: len([]rune("hello "))}})
	_, _ = m.submitLine("hello world")

	_, _ = m.Update(keyPress(tea.KeyUp))

	if got := m.textarea.Value(); got != "hello world" {
		t.Fatalf("expected raw text restored without image marker text, got %q", got)
	}
	rendered := ansi.Strip(m.renderInputBar())
	if !strings.Contains(rendered, "> hello [history.png] world") {
		t.Fatalf("expected inline attachment restored from history, got %q", rendered)
	}
	if got := strings.Join(restored, ","); got != "history.png" {
		t.Fatalf("expected backend attachments restored, got %q", got)
	}
}

func TestRunningSubmitPreservesAttachments(t *testing.T) {
	var called []Submission
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			called = append(called, submission)
			return tuievents.TaskResultMsg{ContinueRunning: true}
		},
	})
	resizeModel(m)

	m.running = true
	m.textarea.SetValue("queued")
	m.syncInputFromTextarea()
	m.setInputAttachments([]inputAttachment{{Name: "queued.png", Offset: len([]rune("queued"))}})

	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected immediate submit command while running")
	}
	if !findAndRunTaskResult(cmd(), m) {
		t.Fatal("expected running prompt execution")
	}
	if len(called) != 1 {
		t.Fatalf("expected one executed submission, got %d", len(called))
	}
	if called[0].Text != "queued" {
		t.Fatalf("expected submitted text preserved, got %q", called[0].Text)
	}
	if len(called[0].Attachments) != 1 || called[0].Attachments[0] != (Attachment{Name: "queued.png", Offset: len([]rune("queued"))}) {
		t.Fatalf("expected submitted attachments preserved, got %+v", called[0].Attachments)
	}
	if !m.running {
		t.Fatal("expected model to remain running after inline submit")
	}
}

func TestPromptAcceptsPasteMsg(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	respCh := make(chan tuievents.PromptResponse, 1)
	_, _ = m.Update(tuievents.PromptRequestMsg{
		Prompt:   "API key",
		Secret:   true,
		Response: respCh,
	})

	_, _ = m.Update(tea.PasteMsg{Content: "sk-test-123"})
	_, _ = m.Update(keyPress(tea.KeyEnter))

	select {
	case resp := <-respCh:
		if resp.Err != nil {
			t.Fatalf("expected successful prompt paste, got err=%v", resp.Err)
		}
		if resp.Line != "sk-test-123" {
			t.Fatalf("expected pasted prompt content, got %q", resp.Line)
		}
	default:
		t.Fatal("expected prompt response after paste+enter")
	}
}

func TestRenderInputBar_ShowsLastVisibleRows(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 48, Height: 20})
	m.textarea.SetValue("line1 line1 line1 line1 line1\nline2\nline3\nline4\nline5")
	m.textarea.CursorEnd()
	m.syncInputFromTextarea()

	rendered := m.renderInputBar()
	if strings.Contains(rendered, "line1") {
		t.Fatalf("expected older wrapped content hidden, got %q", rendered)
	}
	for _, want := range []string{"line2", "line3", "line4", "line5"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected visible tail row %q in %q", want, rendered)
		}
	}
}

func TestRenderInputBar_UsesTextareaCursorForChineseText(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	m.textarea.SetWidth(12)
	m.textarea.SetValue("你都能做")
	m.textarea.CursorEnd()
	m.adjustTextareaHeight()
	m.syncInputFromTextarea()

	rendered := m.renderInputBar()
	if !strings.Contains(rendered, "你都能做") {
		t.Fatalf("expected chinese text preserved, got %q", rendered)
	}
	if strings.Contains(rendered, "█你") || strings.Contains(rendered, "都█") || strings.Contains(rendered, "能█") || strings.Contains(rendered, "做█你") {
		t.Fatalf("expected cursor not inserted into middle of chinese text, got %q", rendered)
	}
}

func TestFooterLeftShowsModeOnly(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.cfg.ModeLabel = func() string { return "plan" }
	got := m.footerLeftText()
	if !strings.Contains(got, "plan") || !strings.Contains(got, "shift+tab") {
		t.Fatalf("unexpected mode footer text %q", got)
	}
	if strings.Contains(got, "mode") {
		t.Fatalf("did not expect footer mode text to include desc, got %q", got)
	}
	if strings.Contains(got, "ctrl+o") {
		t.Fatalf("did not expect footer mode text to include alias, got %q", got)
	}
}

func TestHintRowUsesHintInsteadOfFooter(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.cfg.ModeLabel = func() string { return "plan" }
	m.hint = "temporary hint"
	if got := strings.TrimSpace(m.hintRowText()); got != "temporary hint" {
		t.Fatalf("expected dedicated hint row text, got %q", got)
	}
	if got := m.footerLeftText(); !strings.Contains(got, "plan") || !strings.Contains(got, "shift+tab") {
		t.Fatalf("expected footer mode text preserved, got %q", got)
	}
}

func TestFooterContextRoundsCompactTokenCountsToIntegers(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.statusContext = "15.4k/204.8k(7%)"
	if got := m.footerContextText(); got != "15k/205k(7%)" {
		t.Fatalf("expected rounded compact footer context, got %q", got)
	}
}

func TestFooterContextDoesNotRoundUpFromLaterFractionDigits(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.statusContext = "15.49k/128.49k(12%)"
	if got := m.footerContextText(); got != "15k/128k(12%)" {
		t.Fatalf("expected numeric rounding for compact footer context, got %q", got)
	}
}

func TestFixedRowLayoutPlacesHintAboveHeader(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		Workspace:   "~/WorkDir/xueyongzhi/demo",
		RefreshStatus: func() (string, string) {
			return "openai-compatible/glm-5 [reasoning on]", "0/200.0k(0%)"
		},
	})
	resizeModel(m)
	layout := m.fixedRowLayout()
	if layout.hintY >= layout.headerY {
		t.Fatalf("expected hint row above header row, got hint=%d header=%d", layout.hintY, layout.headerY)
	}
	inputY, _, ok := m.inputAreaBounds()
	if !ok {
		t.Fatal("expected input area bounds")
	}
	if inputY <= layout.headerY {
		t.Fatalf("expected input below header row, got input=%d header=%d", inputY, layout.headerY)
	}
}

func TestBackspaceRemovesAttachmentsOneByOneWhenInputEmpty(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		PasteClipboardImage: func() ([]string, string, error) {
			return []string{"a.png", "b.png"}, "", nil
		},
	})
	resizeModel(m)
	_, _ = m.Update(keyPress('v', tea.ModCtrl))
	if m.attachmentCount != 2 {
		t.Fatalf("expected attachment count 2, got %d", m.attachmentCount)
	}
	_, _ = m.Update(keyPress(tea.KeyBackspace))
	if m.attachmentCount != 1 {
		t.Fatalf("expected one attachment remaining, got %d", m.attachmentCount)
	}
	if got := strings.Join(m.attachmentNames, ","); got != "a.png" {
		t.Fatalf("expected remaining attachment a.png, got %q", got)
	}
	_, _ = m.Update(keyPress(tea.KeyBackspace))
	if m.attachmentCount != 0 {
		t.Fatalf("expected attachment count cleared, got %d", m.attachmentCount)
	}
}

func TestBackspaceRemovesAttachmentAtCursorBeforeEditingText(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.textarea.SetValue("hello world")
	m.syncInputFromTextarea()
	m.setInputAttachments([]inputAttachment{{Name: "mid.png", Offset: len([]rune("hello "))}})
	m.moveTextareaCursorToIndex(len([]rune("hello ")))

	_, _ = m.Update(keyPress(tea.KeyBackspace))

	if got := m.textarea.Value(); got != "hello world" {
		t.Fatalf("expected text to remain unchanged, got %q", got)
	}
	if m.attachmentCount != 0 {
		t.Fatalf("expected attachment removed at cursor, got %d", m.attachmentCount)
	}
	rendered := ansi.Strip(m.renderInputBar())
	if strings.Contains(rendered, "[mid.png]") {
		t.Fatalf("expected attachment token removed, got %q", rendered)
	}
	if !strings.Contains(rendered, "> hello world") {
		t.Fatalf("expected plain text to remain after attachment delete, got %q", rendered)
	}
}

func TestSubmitLineIncludesAttachmentDisplayTokens(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	line := "Hi豆包这两个是什么APP?"
	m.textarea.SetValue(line)
	m.syncInputFromTextarea()
	m.setInputAttachments([]inputAttachment{
		{Name: "clipboard-a.png", Offset: 0},
		{Name: "clipboard-b.png", Offset: len([]rune("Hi豆包"))},
	})

	_, _ = m.submitLine(line)

	if len(m.renderedStyledLines()) == 0 {
		t.Fatal("expected committed user history line")
	}
	got := strings.TrimSpace(ansi.Strip(m.renderedStyledLines()[len(m.renderedStyledLines())-1]))
	want := "> [image: clipboard-a.png] Hi豆包 [image: clipboard-b.png] 这两个是什么APP?"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestEnterSubmitsAttachmentOffsetsInDisplayOrder(t *testing.T) {
	var got Submission
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			got = submission
			return tuievents.TaskResultMsg{}
		},
	})
	resizeModel(m)
	m.textarea.SetValue("hello world")
	m.syncInputFromTextarea()
	m.setInputAttachments([]inputAttachment{
		{Name: "later.png", Offset: len([]rune("hello "))},
		{Name: "first.png", Offset: 0},
	})

	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	if !findAndRunTaskResult(cmd(), m) {
		t.Fatal("expected TaskResultMsg in submit command")
	}
	if got.Text != "hello world" {
		t.Fatalf("expected raw submit text, got %q", got.Text)
	}
	if len(got.Attachments) != 2 {
		t.Fatalf("expected 2 submitted attachments, got %+v", got.Attachments)
	}
	if got.Attachments[0] != (Attachment{Name: "first.png", Offset: 0}) {
		t.Fatalf("expected first attachment at offset 0, got %+v", got.Attachments[0])
	}
	if got.Attachments[1] != (Attachment{Name: "later.png", Offset: len([]rune("hello "))}) {
		t.Fatalf("expected second attachment after 'hello ', got %+v", got.Attachments[1])
	}
}

func TestLogSanitization(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "\x1b[32m* hello\x1b[0m\n"})

	if !m.hasCommittedLine {
		t.Fatal("expected at least one committed line")
	}
}

func TestEscInterruptsRunning(t *testing.T) {
	interrupted := false
	m := NewModel(Config{
		CancelRunning: func() bool {
			interrupted = true
			return true
		},
		ExecuteLine: noopExecute,
	})
	resizeModel(m)
	m.running = true

	_, _ = m.Update(keyPress(tea.KeyEscape))

	if !interrupted {
		t.Fatal("expected CancelRunning to be called")
	}
}

func TestEscInterruptsRunningEvenIfPendingQueueExists(t *testing.T) {
	interrupted := false
	m := NewModel(Config{
		CancelRunning: func() bool {
			interrupted = true
			return true
		},
		ExecuteLine: noopExecute,
	})
	resizeModel(m)
	m.running = true
	m.pendingQueue = &pendingPrompt{execLine: "first", displayLine: "first"}

	_, _ = m.Update(keyPress(tea.KeyEscape))
	if !interrupted {
		t.Fatal("expected interrupt even when stale pending queue state exists")
	}
}

func TestEscInterruptsRunningEvenIfCompletionOverlayIsVisible(t *testing.T) {
	interrupted := false
	m := NewModel(Config{
		CancelRunning: func() bool {
			interrupted = true
			return true
		},
		ExecuteLine: noopExecute,
	})
	resizeModel(m)
	m.running = true
	m.mentionCandidates = []string{"worker"}
	m.mentionIndex = 0

	_, _ = m.Update(keyPress(tea.KeyEscape))

	if !interrupted {
		t.Fatal("expected interrupt to win over visible completion overlay")
	}
	if len(m.mentionCandidates) != 0 {
		t.Fatal("expected interrupt to clear completion overlays")
	}
}

func TestCtrlCRequiresDoublePressToQuitWhenIdle(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, cmd := m.Update(keyPress('c', tea.ModCtrl))
	if m.quit {
		t.Fatal("expected first Ctrl+C not to quit")
	}
	if cmd == nil {
		t.Fatal("expected expiry cmd on first Ctrl+C")
	}
	if !strings.Contains(m.hint, "again to quit") {
		t.Fatalf("expected double-press hint, got %q", m.hint)
	}

	_, cmd = m.Update(keyPress('c', tea.ModCtrl))

	if !m.quit {
		t.Fatal("expected second Ctrl+C to quit")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit command")
	}
}

func TestCtrlCHintExpiresWithConfirmWindow(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, cmd := m.Update(keyPress('c', tea.ModCtrl))
	if cmd == nil {
		t.Fatal("expected expiry cmd on first Ctrl+C")
	}
	if !m.ctrlCArmed {
		t.Fatal("expected Ctrl+C confirm state to be armed")
	}
	armedAt := m.lastCtrlCAt

	_, _ = m.Update(ctrlCExpireMsg{armedAt: armedAt, seq: m.ctrlCArmSeq})
	if m.ctrlCArmed {
		t.Fatal("expected Ctrl+C confirm state to expire")
	}
	if strings.Contains(m.hint, "again to quit") {
		t.Fatalf("expected quit hint cleared after expiry, got %q", m.hint)
	}
}

func TestCtrlCAfterExpiryRequiresTwoPressesAgain(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(keyPress('c', tea.ModCtrl))
	armedAt := m.lastCtrlCAt
	_, _ = m.Update(ctrlCExpireMsg{armedAt: armedAt, seq: m.ctrlCArmSeq})

	_, cmd := m.Update(keyPress('c', tea.ModCtrl))
	if m.quit {
		t.Fatal("expected first Ctrl+C after expiry not to quit")
	}
	if cmd == nil {
		t.Fatal("expected expiry cmd on re-arming Ctrl+C")
	}
	if !strings.Contains(m.hint, "again to quit") {
		t.Fatalf("expected quit hint after re-arming, got %q", m.hint)
	}
}

func TestCtrlCClearsInputAndSavesDraftToHistory(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	typeRunes(m, "draft text")

	_, _ = m.Update(keyPress('c', tea.ModCtrl))
	if got := m.textarea.Value(); got != "" {
		t.Fatalf("expected input cleared on first Ctrl+C, got %q", got)
	}
	if len(m.history) == 0 || m.history[len(m.history)-1] != "draft text" {
		t.Fatalf("expected draft recorded to history, got %+v", m.history)
	}
}

func TestCtrlCWhileRunningShowsEscHint(t *testing.T) {
	m := NewModel(Config{
		CancelRunning: noopCancelRunning,
		ExecuteLine:   noopExecute,
	})
	resizeModel(m)
	m.running = true

	_, cmd := m.Update(keyPress('c', tea.ModCtrl))
	if cmd == nil {
		t.Fatal("expected auto-clear cmd when pressing Ctrl+C during running")
	}
	if !strings.Contains(strings.ToLower(m.hint), "esc") {
		t.Fatalf("expected hint to use esc, got %q", m.hint)
	}
}

func TestEnterSubmitsMessageWhileRunning(t *testing.T) {
	var called []string
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			called = append(called, strings.TrimSpace(submission.Text))
			return tuievents.TaskResultMsg{ContinueRunning: true}
		},
	})
	resizeModel(m)

	m.running = true
	typeRunes(m, "queued message")
	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected immediate submit command while running")
	}
	if got := m.textarea.Value(); got != "" {
		t.Fatalf("expected input cleared after submit, got %q", got)
	}
	if !m.running {
		t.Fatal("expected model to remain running after queueing")
	}

	msg := cmd()
	if msg == nil {
		t.Fatal("expected non-nil command message")
	}
	if !findAndRunTaskResult(msg, m) {
		t.Fatal("expected TaskResultMsg in running submit command")
	}
	if len(called) != 1 || called[0] != "queued message" {
		t.Fatalf("expected running message to be executed, got %+v", called)
	}
	if len(m.renderedStyledLines()) != 0 {
		t.Fatalf("expected running submit not to commit user line before runtime ack, got %+v", m.renderedStyledLines())
	}
	if m.pendingQueue == nil || m.pendingQueue.displayLine != "queued message" {
		t.Fatalf("expected pending queue to show queued message, got %+v", m.pendingQueue)
	}
}

func TestEnterWhileRunningShowsHintWhenReplacingPendingMessage(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: func(_ Submission) tuievents.TaskResultMsg {
			return tuievents.TaskResultMsg{ContinueRunning: true}
		},
	})
	resizeModel(m)

	m.running = true
	m.pendingQueue = &pendingPrompt{execLine: "old message", displayLine: "old message"}
	typeRunes(m, "new message")

	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected immediate submit command while running")
	}
	if !strings.Contains(strings.ToLower(m.hint), "replaced pending") {
		t.Fatalf("expected replacement hint, got %q", m.hint)
	}
	if m.pendingQueue == nil || m.pendingQueue.displayLine != "new message" {
		t.Fatalf("expected pending queue to keep latest message, got %+v", m.pendingQueue)
	}
}

func TestEnterSubmitsBTWWhileRunningWithoutHistoryOrPendingQueue(t *testing.T) {
	var got Submission
	m := NewModel(Config{
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			got = submission
			return tuievents.TaskResultMsg{ContinueRunning: true}
		},
	})
	resizeModel(m)

	m.running = true
	typeRunes(m, "/btw config file name?")

	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected btw submit command while running")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("expected non-nil command message")
	}
	if !findAndRunTaskResult(msg, m) {
		t.Fatal("expected TaskResultMsg in btw submit command")
	}
	if got.Mode != SubmissionModeOverlay {
		t.Fatalf("expected overlay submission mode, got %+v", got)
	}
	if len(m.renderedStyledLines()) != 0 {
		t.Fatalf("expected /btw not to commit history, got %+v", m.renderedStyledLines())
	}
	if m.pendingQueue != nil {
		t.Fatalf("expected /btw not to use pending queue, got %+v", m.pendingQueue)
	}
	if m.btwOverlay == nil || m.btwOverlay.Question != "config file name?" {
		t.Fatalf("expected btw overlay question, got %+v", m.btwOverlay)
	}
}

func TestUserMessageMsgCommitsPendingUserLine(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: func(_ Submission) tuievents.TaskResultMsg {
			return tuievents.TaskResultMsg{ContinueRunning: true}
		},
	})
	resizeModel(m)

	m.running = true
	typeRunes(m, "queued message")
	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	if !findAndRunTaskResult(cmd(), m) {
		t.Fatal("expected running task result")
	}
	if m.pendingQueue == nil {
		t.Fatalf("expected pending queue entry, got nil")
	}

	_, _ = m.Update(tuievents.UserMessageMsg{Text: "queued message"})

	if m.pendingQueue != nil {
		t.Fatalf("expected pending queue cleared after commit, got %+v", m.pendingQueue)
	}
	if len(m.renderedStyledLines()) == 0 {
		t.Fatal("expected committed user line after runtime ack")
	}
	got := strings.TrimSpace(ansi.Strip(m.renderedStyledLines()[len(m.renderedStyledLines())-1]))
	if got != "> queued message" {
		t.Fatalf("expected committed user line, got %q", got)
	}
}

func TestUserMessageMsgDoesNotDuplicateCommittedAttachmentLine(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
	})
	resizeModel(m)

	m.textarea.SetValue("在你的回答中加一点emoji类似于这样的")
	m.syncInputFromTextarea()
	m.setInputAttachments([]inputAttachment{{
		Name:   "clipboard-demo.png",
		Offset: len([]rune("在你的回答中加一点emoji")),
	}})

	_, _ = m.submitLine("在你的回答中加一点emoji类似于这样的")
	_, _ = m.Update(tuievents.UserMessageMsg{
		Text: "在你的回答中加一点emoji [image: clipboard-demo.png] 类似于这样的",
	})

	var userLines []string
	for _, line := range m.renderedStyledLines() {
		plain := strings.TrimSpace(ansi.Strip(line))
		if strings.HasPrefix(plain, "> ") {
			userLines = append(userLines, plain)
		}
	}
	if len(userLines) != 1 {
		t.Fatalf("expected a single committed user line after runtime replay, got %+v", userLines)
	}
}

func TestUserTurnClearsFinalAnswerDedup(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	const answer = "same final answer"

	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: answer, Final: true})
	_, _ = m.submitLine("再发一遍")
	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: answer, Final: true})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if count := strings.Count(joined, answer); count != 2 {
		t.Fatalf("expected repeated finalized answer after new user turn, got count=%d view=%q", count, joined)
	}
}

func TestUserTurnPreservesLiteralMarkdown(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.commitUserDisplayLine("# Heading\n\n```go\nfmt.Println(1)\n```")
	m.syncViewportContent()

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "> # Heading") {
		t.Fatalf("expected literal heading marker in user history, got %q", joined)
	}
	if !strings.Contains(joined, "```go") || !strings.Contains(joined, "```") {
		t.Fatalf("expected literal fenced code markers in user history, got %q", joined)
	}
}

func TestSubmitWhileRunningReconcilesViewportForPendingQueue(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.running = true
	before := m.viewport.Height()
	typeRunes(m, "queued follow-up")

	_, _ = m.Update(keyPress(tea.KeyEnter))
	if m.pendingQueue == nil {
		t.Fatal("expected pending queue entry")
	}

	want, _ := m.computeLayout()
	if got := m.viewport.Height(); got != want {
		t.Fatalf("expected viewport height %d after pending queue open, got %d", want, got)
	}
	if m.viewport.Height() >= before {
		t.Fatalf("expected pending queue to shrink viewport, before=%d after=%d", before, m.viewport.Height())
	}
}

func TestBTWSubmitReconcilesViewportLayout(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	before := m.viewport.Height()
	typeRunes(m, "/btw config file?")

	_, _ = m.Update(keyPress(tea.KeyEnter))
	if m.btwOverlay == nil {
		t.Fatal("expected btw overlay to open")
	}

	want, _ := m.computeLayout()
	if got := m.viewport.Height(); got != want {
		t.Fatalf("expected viewport height %d after btw open, got %d", want, got)
	}
	if m.viewport.Height() >= before {
		t.Fatalf("expected btw drawer to shrink viewport, before=%d after=%d", before, m.viewport.Height())
	}
}

func TestTaskResultReconcilesViewportAfterDrawerClose(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.running = true
	m.pendingQueue = &pendingPrompt{execLine: "queued", displayLine: "queued"}
	m.planEntries = []planEntryState{{Content: "step", Status: "pending"}}
	m.ensureViewportLayout()
	before := m.viewport.Height()

	_, _ = m.Update(tuievents.TaskResultMsg{})

	want, _ := m.computeLayout()
	if got := m.viewport.Height(); got != want {
		t.Fatalf("expected viewport height %d after drawers close, got %d", want, got)
	}
	if m.viewport.Height() <= before {
		t.Fatalf("expected viewport to grow after drawers close, before=%d after=%d", before, m.viewport.Height())
	}
}

func TestInputAreaBoundsAccountsForPlanDrawer(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.planEntries = []planEntryState{
		{Content: "step 1", Status: "pending"},
		{Content: "step 2", Status: "pending"},
	}
	startY, _, ok := m.inputAreaBounds()
	if !ok {
		t.Fatal("expected input area bounds")
	}
	layout := m.fixedRowLayout()
	if startY <= layout.headerY {
		t.Fatalf("expected input area below header when plan drawer visible, got input=%d header=%d", startY, layout.headerY)
	}
	if got, want := layout.hintY, m.viewport.Height()+2+m.primaryDrawerHeight(); got != want {
		t.Fatalf("unexpected hint row position with plan drawer, got=%d want=%d", got, want)
	}
}

func TestCursorPositionAccountsForPendingQueueDrawer(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true
	m.pendingQueue = &pendingPrompt{execLine: "queued", displayLine: "queued"}

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("expected visible input cursor")
	}

	lines := strings.Split(ansi.Strip(view.Content), "\n")
	inputLine := strings.TrimSpace(m.inputPlainLines()[0])
	if inputLine == "" {
		t.Fatal("expected non-empty input line")
	}
	expectedY := -1
	for idx, line := range lines {
		if strings.TrimSpace(line) == inputLine {
			expectedY = idx
			break
		}
	}
	if expectedY < 0 {
		t.Fatalf("failed to locate input line %q in rendered view", inputLine)
	}
	if got := view.Cursor.Y; got != expectedY {
		start := maxInt(0, expectedY-4)
		end := minInt(len(lines), expectedY+3)
		t.Fatalf("expected cursor Y to match input line with pending drawer, got %d want %d; lines=%q", got, expectedY, lines[start:end])
	}
}

func TestEnterSlashWhileRunningDoesNotQueue(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.running = true
	typeRunes(m, "/help")
	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected hint auto-clear cmd for slash while running")
	}
	if m.pendingQueue != nil {
		t.Fatalf("expected no queued message for slash command, got %+v", m.pendingQueue)
	}
	if got := m.textarea.Value(); got != "/help" {
		t.Fatalf("expected slash input kept for user edit, got %q", got)
	}
	if !strings.Contains(strings.ToLower(m.hint), "slash") {
		t.Fatalf("expected slash-unavailable hint, got %q", m.hint)
	}
}

func TestTaskResultClearsHiddenSlashArgState(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true
	m.slashArgActive = true
	m.slashArgCommand = ""

	_, _ = m.Update(tuievents.TaskResultMsg{Err: noopError("execution interrupted")})

	if m.slashArgActive {
		t.Fatal("expected stale slash arg state to be cleared after task result")
	}
}

func TestInterruptedTaskResultDropsPartialAssistantOutput(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.running = true

	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: "partial answer", Final: false})
	if m.activeAssistantID == "" {
		t.Fatal("expected partial assistant block before interrupt")
	}

	_, _ = m.Update(tuievents.TaskResultMsg{Interrupted: true})

	if m.activeAssistantID != "" {
		t.Fatal("expected assistant block cleared after interrupt")
	}
	if strings.Contains(strings.Join(m.renderedStyledLines(), "\n"), "partial answer") {
		t.Fatalf("expected partial answer removed after interrupt, got %#v", m.renderedStyledLines())
	}
}

func TestEscInterruptThenEnterSubmitsNewMessage(t *testing.T) {
	var interrupted bool
	var called []string
	m := NewModel(Config{
		CancelRunning: func() bool {
			interrupted = true
			return true
		},
		ExecuteLine: func(submission Submission) tuievents.TaskResultMsg {
			called = append(called, strings.TrimSpace(submission.Text))
			return tuievents.TaskResultMsg{}
		},
	})
	resizeModel(m)
	m.running = true
	m.slashArgActive = true
	m.slashArgCommand = ""

	_, _ = m.Update(keyPress(tea.KeyEscape))
	if !interrupted {
		t.Fatal("expected running task to be interrupted")
	}

	_, _ = m.Update(tuievents.TaskResultMsg{Interrupted: true})
	typeRunes(m, "follow-up")
	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected follow-up submit command after interrupt")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("expected non-nil submit result")
	}
	if !findAndRunTaskResult(msg, m) {
		t.Fatal("expected TaskResultMsg in submit command")
	}
	if len(called) != 1 || called[0] != "follow-up" {
		t.Fatalf("expected follow-up prompt to execute, got %+v", called)
	}
}

func noopCancelRunning() bool { return true }

type noopError string

func (e noopError) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func noopExecute(Submission) tuievents.TaskResultMsg {
	return tuievents.TaskResultMsg{}
}

func newTestModel() *Model {
	return NewModel(Config{
		ExecuteLine: noopExecute,
	})
}

func resizeModel(m *Model) {
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
}

func typeRunes(m *Model, text string) {
	for _, r := range text {
		_, _ = m.Update(keyText(string(r)))
	}
}

func typeAndEnter(m *Model, text string) {
	typeRunes(m, text)
	_, cmd := m.Update(keyPress(tea.KeyEnter))
	if cmd != nil {
		msg := cmd()
		if msg != nil {
			findAndRunTaskResult(msg, m)
		}
	}
}

// ---------------------------------------------------------------------------
// Fullscreen viewport tests
// ---------------------------------------------------------------------------

func TestHistoryBufferAppend(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* hello\n"})

	if len(m.renderedStyledLines()) == 0 {
		t.Fatal("expected rendered lines to be non-empty")
	}
}

func TestAutoScrollOnNewContent(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Add enough content to fill viewport.
	for i := 0; i < 50; i++ {
		_, _ = m.Update(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("* line %d\n", i)})
	}
	if !m.viewport.AtBottom() {
		t.Fatal("expected viewport at bottom after auto-scroll")
	}
}

func TestSubmitLineForcesAutoScrollToBottom(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	for i := 0; i < 80; i++ {
		_, _ = m.Update(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("* line %d\n", i)})
	}
	_, _ = m.Update(keyPress(tea.KeyPgUp))
	if !m.userScrolledUp {
		t.Fatal("expected userScrolledUp after pgup")
	}
	typeAndEnter(m, "hello")
	if !m.viewport.AtBottom() {
		t.Fatal("expected viewport at bottom after user submit")
	}
}

func TestToolCallSpacing_GapBetweenCalls_NoGapBeforeResult(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ build.sh\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ PATCH build.sh\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ PATCH +1 -1\n"})
	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Contains(joined, "\n·\n") {
		t.Fatalf("did not expect synthetic dot spacer between tool calls, got %q", joined)
	}
	if !strings.Contains(joined, "▸ Explored 1 files") || !strings.Contains(joined, "▸ PATCH build.sh +1 -1") {
		t.Fatalf("expected exploration summary followed by merged patch call, got %q", joined)
	}
	if strings.Contains(joined, "✓ PATCH +1 -1") {
		t.Fatalf("expected patch result merged into call line, got %q", joined)
	}
}

func TestWriteResultMergesIntoPriorCallLine(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ WRITE build.sh\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ WRITE +3 -0\n"})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "▸ WRITE build.sh +3 -0") {
		t.Fatalf("expected write result merged into call line, got %q", joined)
	}
	if strings.Contains(joined, "✓ WRITE +3 -0") {
		t.Fatalf("did not expect standalone write result line, got %q", joined)
	}
}

func TestWriteResultMergesIntoAnchorLineEvenAfterDiffBlock(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ WRITE build.sh\n"})
	_, _ = m.Update(tuievents.DiffBlockMsg{
		Tool: "WRITE",
		Path: "build.sh",
		Old:  "",
		New:  "echo hi\n",
	})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ WRITE +1 -0\n"})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "▸ WRITE build.sh +1 -0") {
		t.Fatalf("expected write result merged into anchor line after diff block, got %q", joined)
	}
	if strings.Contains(joined, "✓ WRITE +1 -0") {
		t.Fatalf("did not expect standalone write result line after diff block, got %q", joined)
	}
}

func TestMutationResultLineSuppressedWhenAnchorAlreadySummarized(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ WRITE run_demo.sh +23 -0\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ WRITE +43 -0\n"})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Contains(joined, "✓ WRITE +43 -0") {
		t.Fatalf("did not expect standalone aggregate write result line, got %q", joined)
	}
	if !strings.Contains(joined, "▸ WRITE run_demo.sh +23 -0") {
		t.Fatalf("expected original summarized write anchor to stay visible, got %q", joined)
	}
}

func TestAssistantStreamAddsPrefixMarker(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: "hello", Final: true})
	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "* hello") {
		t.Fatalf("expected assistant prefix marker, got %q", joined)
	}
}

func TestViewportHardWrapLongLine(t *testing.T) {
	m := newTestModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 30, Height: 20})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* 这是一段很长很长很长很长很长很长很长很长的文本用于换行测试\n"})

	if m.viewport.TotalLineCount() < 2 {
		t.Fatalf("expected wrapped viewport lines, got %d", m.viewport.TotalLineCount())
	}
}

func TestViewportCompactsSpawnCallLineByWidth(t *testing.T) {
	m := newTestModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SPAWN sleep 8; echo \"Task 1 completed at $(date)\" > task1_result.txt; echo \"All done.\"\n"})

	view := stripModelView(m)
	if !strings.Contains(view, "▸ SPAWN") {
		t.Fatalf("expected spawn line visible, got:\n%s", view)
	}
	if !strings.Contains(view, "......") {
		t.Fatalf("expected spawn line compacted with six-dot middle marker, got:\n%s", view)
	}
}

func TestAdjustTextareaHeightClampsToMaxRows(t *testing.T) {
	m := newTestModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	m.textarea.SetValue(strings.Repeat("x", 2000))
	m.adjustTextareaHeight()
	if got := m.textarea.Height(); got != maxInputBarRows {
		t.Fatalf("expected textarea height clamped to %d, got %d", maxInputBarRows, got)
	}
}

func TestAdjustTextareaHeightGrowsForMultilineInput(t *testing.T) {
	m := newTestModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.textarea.SetValue("line1\nline2\nline3")
	m.adjustTextareaHeight()
	if got := m.textarea.Height(); got < 3 {
		t.Fatalf("expected textarea height >= 3, got %d", got)
	}
}

func TestSliceByDisplayColumnsNoOverlapOnWideRuneBoundary(t *testing.T) {
	line := "你a"
	prefix := sliceByDisplayColumns(line, 0, 1)
	middle := sliceByDisplayColumns(line, 1, 3)
	if got := prefix + middle; got != line {
		t.Fatalf("expected no overlap at wide-rune boundary, got %q want %q", got, line)
	}
}

func TestPageUpPreventsAutoScroll(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Add enough content to fill viewport.
	for i := 0; i < 50; i++ {
		_, _ = m.Update(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("* line %d\n", i)})
	}

	// Scroll up.
	_, _ = m.Update(keyPress(tea.KeyPgUp))
	if !m.userScrolledUp {
		t.Fatal("expected userScrolledUp after pgup")
	}
	view := stripModelView(m)
	if strings.Contains(view, "scroll:") {
		t.Fatalf("did not expect scroll percent indicator, got %q", view)
	}
}

func TestResizeDoesNotClearScreen(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Change width — should not return tea.ClearScreen cmd.
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if cmd != nil {
		t.Fatal("expected nil cmd on resize (no ClearScreen in fullscreen mode)")
	}
}

// ---------------------------------------------------------------------------
// Arrow key behavior tests
// ---------------------------------------------------------------------------

func TestArrowKeysUseInputHistoryEvenWhenViewportHasScrollableContent(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	typeAndEnter(m, "first")
	typeAndEnter(m, "second")

	for i := 0; i < 80; i++ {
		_, _ = m.Update(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("* line %d\n", i)})
	}

	_, _ = m.Update(keyPress(tea.KeyUp))
	if got := m.textarea.Value(); got != "second" {
		t.Fatalf("expected history command on arrow up, got %q", got)
	}

	_, _ = m.Update(keyPress(tea.KeyDown))
	if got := m.textarea.Value(); got != "" {
		t.Fatalf("expected draft restored on arrow down, got %q", got)
	}
}

func TestMultilineInputUsesTextareaVerticalNavigationBeforeHistory(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	typeAndEnter(m, "first")
	m.textarea.SetValue("line1\nline2\nline3")
	m.textarea.CursorEnd()
	m.adjustTextareaHeight()
	m.syncInputFromTextarea()

	if got := m.textarea.Line(); got != 2 {
		t.Fatalf("expected cursor on last input line, got %d", got)
	}

	_, _ = m.Update(keyPress(tea.KeyUp))
	if got := m.textarea.Value(); got != "line1\nline2\nline3" {
		t.Fatalf("expected textarea content preserved on internal up nav, got %q", got)
	}
	if got := m.textarea.Line(); got != 1 {
		t.Fatalf("expected cursor to move within textarea, got line %d", got)
	}

	_, _ = m.Update(keyPress(tea.KeyUp))
	if got := m.textarea.Line(); got != 0 {
		t.Fatalf("expected cursor to reach first textarea line, got %d", got)
	}

	_, _ = m.Update(keyPress(tea.KeyUp))
	if got := m.textarea.Value(); got != "first" {
		t.Fatalf("expected history recall only after leaving first textarea line, got %q", got)
	}

	_, _ = m.Update(keyPress(tea.KeyDown))
	if got := m.textarea.Value(); got != "line1\nline2\nline3" {
		t.Fatalf("expected draft restored from history, got %q", got)
	}
	if got := m.textarea.Line(); got != 2 {
		t.Fatalf("expected restored draft cursor at last line, got %d", got)
	}

	_, _ = m.Update(keyPress(tea.KeyUp))
	if got := m.textarea.Line(); got != 1 {
		t.Fatalf("expected cursor to move within restored multiline draft, got %d", got)
	}
}

func TestViewShowsModeFooterWhenConfigured(t *testing.T) {
	m := NewModel(Config{
		Workspace: "/tmp/work",
		ModeLabel: func() string { return "full_access" },
	})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := stripModelView(m)
	if !strings.Contains(view, "full_access") || !strings.Contains(view, "shift+tab") {
		t.Fatalf("expected mode footer in view, got:\n%s", view)
	}
	if strings.Contains(view, "shift+tab mode") {
		t.Fatalf("did not expect footer mode desc in view, got:\n%s", view)
	}
	if strings.Contains(view, "ctrl+o mode") {
		t.Fatalf("did not expect alias key in main view footer, got:\n%s", view)
	}
}

func TestViewShowsDefaultModeAndContextFooterWhenConfigured(t *testing.T) {
	m := NewModel(Config{
		Workspace: "/tmp/work",
		ModeLabel: func() string { return "default" },
		RefreshStatus: func() (string, string) {
			return "model-x", "42/128k"
		},
	})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := stripModelView(m)
	if !strings.Contains(view, "default") || !strings.Contains(view, "shift+tab") {
		t.Fatalf("expected default mode footer in view, got:\n%s", view)
	}
	if strings.Contains(view, "shift+tab mode") {
		t.Fatalf("did not expect footer mode desc in view, got:\n%s", view)
	}
	if !strings.Contains(view, "42/128k") {
		t.Fatalf("expected context footer in view, got:\n%s", view)
	}
}

func TestTransientRetryReplacement(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	// Emit 5 consecutive retry lines — only the last should remain.
	for i := 1; i <= 5; i++ {
		line := fmt.Sprintf("! llm request failed, retrying in %ds (%d/5): error\n", i, i)
		_, _ = m.Update(tuievents.LogChunkMsg{Chunk: line})
	}

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	// Only the latest retry should be visible.
	if !strings.Contains(joined, "5/5") {
		t.Fatalf("expected latest retry visible, got %q", joined)
	}
	// Earlier retries must be gone.
	if strings.Contains(joined, "1/5") {
		t.Fatalf("expected earlier retries replaced, got %q", joined)
	}
	if strings.Contains(joined, "3/5") {
		t.Fatalf("expected middle retries replaced, got %q", joined)
	}
	// Should occupy exactly 1 history line (the single transient slot).
	count := 0
	for _, l := range m.renderedStyledLines() {
		if strings.Contains(ansi.Strip(l), "retrying") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 retry line in history, got %d", count)
	}
}

func TestTransientWarnReplacement(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "! first warning\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "! second warning\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "! third warning\n"})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "third warning") {
		t.Fatalf("expected latest warn visible, got %q", joined)
	}
	if strings.Contains(joined, "first warning") {
		t.Fatalf("expected earlier warns replaced, got %q", joined)
	}
}

func TestLogChunkKeepsAssistantAndToolBlocksContiguous(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* drafted a plan\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ SKILL.md\n"})

	if len(m.renderedStyledLines()) < 3 {
		t.Fatalf("expected assistant and exploration block lines; got %d lines", len(m.renderedStyledLines()))
	}
	if got := strings.TrimSpace(ansi.Strip(m.renderedStyledLines()[0])); got != "* drafted a plan" {
		t.Fatalf("unexpected first line %q", got)
	}
	if got := strings.TrimSpace(ansi.Strip(m.renderedStyledLines()[1])); got != "▸ Exploring 1 files" {
		t.Fatalf("unexpected exploration title %q", got)
	}
	if got := strings.TrimSpace(ansi.Strip(m.renderedStyledLines()[2])); got != "│ Read SKILL.md" {
		t.Fatalf("unexpected exploration child line %q", got)
	}
}

func TestTaskResultAddsDividerAfterAgentTurn(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.submitLineWithDisplay("continue", "continue")
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* assistant reply\n"})
	m.lastRunDuration = 1250 * time.Millisecond
	m.hasLastRunDuration = true
	m.runStartedAt = time.Time{}
	_, _ = m.Update(tuievents.TaskResultMsg{})

	if len(m.renderedStyledLines()) < 3 {
		t.Fatalf("expected user, assistant, divider; got %d lines", len(m.renderedStyledLines()))
	}
	if got := strings.TrimSpace(ansi.Strip(m.renderedStyledLines()[len(m.renderedStyledLines())-1])); !strings.Contains(got, "1.2s") || !strings.Contains(got, "─") {
		t.Fatalf("expected duration divider after completed turn, got %q", got)
	}
	if got := strings.TrimSpace(ansi.Strip(m.renderedStyledLines()[len(m.renderedStyledLines())-2])); got != "* assistant reply" {
		t.Fatalf("expected assistant line before divider, got %q", got)
	}
	if got := strings.TrimSpace(ansi.Strip(m.renderedStyledLines()[len(m.renderedStyledLines())-3])); got != "> continue" {
		t.Fatalf("unexpected user line %q", got)
	}
}

func TestTaskResultDoesNotAddDividerForSlashCommand(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.submitLineWithDisplay("/status", "/status")
	m.runStartedAt = time.Now().Add(-1500 * time.Millisecond)
	_, _ = m.Update(tuievents.TaskResultMsg{})

	if len(m.renderedStyledLines()) != 1 {
		t.Fatalf("expected slash command to avoid turn divider, got %d lines", len(m.renderedStyledLines()))
	}
	if got := strings.TrimSpace(ansi.Strip(m.renderedStyledLines()[0])); got != "> /status" {
		t.Fatalf("unexpected slash command line %q", got)
	}
}

func TestBTWOverlayMessageDoesNotCommitConversationHistory(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.submitLineWithDisplay("/btw config file name?", "/btw config file name?")
	if len(m.renderedStyledLines()) != 0 {
		t.Fatalf("expected /btw submit to stay out of history, got %+v", m.renderedStyledLines())
	}
	if m.btwOverlay == nil {
		t.Fatal("expected btw overlay to open")
	}
	loadingView := stripModelView(m)
	if !strings.Contains(loadingView, "↪ config file name?") {
		t.Fatalf("expected btw loading state to use pending-style line, got:\n%s", loadingView)
	}
	if strings.Contains(loadingView, "Thinking...") {
		t.Fatalf("did not expect btw loading placeholder text, got:\n%s", loadingView)
	}

	_, _ = m.Update(tuievents.BTWOverlayMsg{Text: "caelis.toml", Final: true})
	if m.btwOverlay == nil || m.btwOverlay.Answer != "caelis.toml" || m.btwOverlay.Loading {
		t.Fatalf("expected btw overlay answer populated, got %+v", m.btwOverlay)
	}
	if len(m.renderedStyledLines()) != 0 {
		t.Fatalf("expected btw answer to stay out of history, got %+v", m.renderedStyledLines())
	}
	view := stripModelView(m)
	if strings.Contains(view, "BTW") || strings.Contains(view, "QUESTION:") {
		t.Fatalf("expected btw drawer without legacy headers, got:\n%s", view)
	}
	if !strings.Contains(view, "config file name?") || !strings.Contains(view, "caelis.toml") {
		t.Fatalf("expected question and answer in btw drawer, got:\n%s", view)
	}
	if !strings.Contains(view, "esc") {
		t.Fatalf("expected close shortcut in footer area, got:\n%s", view)
	}

	_, _ = m.Update(keyPress(tea.KeyEnter))
	if m.btwOverlay == nil {
		t.Fatal("expected btw overlay to remain open on enter")
	}

	_, _ = m.Update(keyPress(tea.KeySpace))
	if m.btwOverlay == nil {
		t.Fatal("expected btw overlay to remain open on space")
	}

	m.openBTWOverlay("/btw config file name?")
	_, _ = m.Update(tuievents.BTWOverlayMsg{Text: "caelis.toml", Final: true})
	_, _ = m.Update(keyPress(tea.KeyEsc))
	if m.btwOverlay != nil {
		t.Fatalf("expected btw overlay closed, got %+v", m.btwOverlay)
	}
}

func TestBTWOverlayBlocksPasteUntilClosed(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.textarea.SetValue("draft")
	m.syncInputFromTextarea()
	m.openBTWOverlay("/btw config file name?")

	_, _ = m.Update(tea.PasteMsg{Content: " blocked"})
	if got := m.textarea.Value(); got != "draft" {
		t.Fatalf("expected btw overlay to block paste, got %q", got)
	}

	_, _ = m.Update(keyPress(tea.KeyEsc))
	if m.btwOverlay != nil {
		t.Fatalf("expected btw overlay closed, got %+v", m.btwOverlay)
	}

	_, _ = m.Update(tea.PasteMsg{Content: " restored"})
	if got := m.textarea.Value(); got != "draft restored" {
		t.Fatalf("expected paste restored after closing btw overlay, got %q", got)
	}
}

func TestBTWDrawerTakesPriorityOverPlanDrawer(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.planEntries = []planEntryState{
		{Content: "step 1", Status: "pending"},
		{Content: "step 2", Status: "in_progress"},
	}
	m.openBTWOverlay("/btw which file?")
	_, _ = m.Update(tuievents.BTWOverlayMsg{Text: "caelis.toml", Final: true})

	view := stripModelView(m)
	if strings.Contains(view, "☐ step 1") || strings.Contains(view, "☐ step 2") {
		t.Fatalf("expected btw drawer to replace plan drawer, got:\n%s", view)
	}
	if !strings.Contains(view, "which file?") || !strings.Contains(view, "caelis.toml") {
		t.Fatalf("expected btw drawer content visible, got:\n%s", view)
	}
}

func TestBTWDrawerScrollsWithinMaxHeight(t *testing.T) {
	m := newTestModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.openBTWOverlay("/btw long answer?")
	lines := make([]string, 0, 16)
	for i := 1; i <= 16; i++ {
		lines = append(lines, fmt.Sprintf("line %02d", i))
	}
	_, _ = m.Update(tuievents.BTWOverlayMsg{Text: strings.Join(lines, "\n"), Final: true})

	if got := m.primaryDrawerHeight(); got > 7 {
		t.Fatalf("expected btw drawer height capped on 24-row terminal, got %d", got)
	}

	before := stripModelView(m)
	if !strings.Contains(before, "line 01") {
		t.Fatalf("expected top of btw content before scroll, got:\n%s", before)
	}
	if strings.Contains(before, "line 16") {
		t.Fatalf("did not expect bottom of btw content before scroll, got:\n%s", before)
	}

	for i := 0; i < 3; i++ {
		_, _ = m.Update(keyPress(tea.KeyPgDown))
	}
	afterPage := stripModelView(m)
	if !strings.Contains(afterPage, "line 16") {
		t.Fatalf("expected paged btw content to reveal bottom lines, got:\n%s", afterPage)
	}
	if strings.Contains(afterPage, "line 01") {
		t.Fatalf("expected btw scroll to move past the first line, got:\n%s", afterPage)
	}

	_, _ = m.Update(keyPress(tea.KeyUp))
	if m.btwOverlay == nil || m.btwOverlay.Scroll >= m.btwMaxScroll(len(m.btwContentLines())) {
		t.Fatalf("expected up key to scroll btw drawer, got %+v", m.btwOverlay)
	}
}

func TestErrorAlwaysAppended(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "error: first failure\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "error: second failure\n"})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "first failure") {
		t.Fatalf("expected first error preserved, got %q", joined)
	}
	if !strings.Contains(joined, "second failure") {
		t.Fatalf("expected second error preserved, got %q", joined)
	}
}

func TestRetryThenNonRetryBreaksTransient(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "! retrying request (1/3)\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "! retrying request (2/3)\n"})
	// Non-retry line breaks the transient chain.
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ file.txt\n"})
	// New retry starts a fresh transient slot.
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "! retrying request (3/3)\n"})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if !strings.Contains(joined, "▸ Explored 1 files") {
		t.Fatalf("expected read turn collapsed before next retry, got %q", joined)
	}
	if !strings.Contains(joined, "3/3") {
		t.Fatalf("expected new retry after break visible, got %q", joined)
	}
}

func TestRateLimitWarningDisappearsBeforeNextMessage(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ sfs.go\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SEARCH . {query=GetSfsListReq}\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "! llm request hit rate limits (HTTP 429 / Too Many Requests), retrying in 5s (1/7). Waiting longer before retrying.\n"})

	view := stripModelView(m)
	if !strings.Contains(view, "Too Many Requests") {
		t.Fatalf("expected transient rate-limit warning to render, got:\n%s", view)
	}

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ sfs.go\n"})
	view = stripModelView(m)
	if strings.Contains(view, "Too Many Requests") {
		t.Fatalf("expected transient rate-limit warning removed before next message, got:\n%s", view)
	}
}

func TestActiveExplorationBlockReRendersOnSpinnerTick(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ state.go\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SEARCH . {query=enter}\n"})
	m.running = true
	m.startRunningAnimation()

	before := m.renderedStyledLines()[0]
	for i := 0; i < 3; i++ {
		_, _ = m.Update(spinner.TickMsg{})
	}
	after := m.renderedStyledLines()[0]
	if before == after {
		t.Fatalf("expected active exploration header to animate on spinner ticks")
	}
}

func TestBackgroundColorMsgSwitchesAutoThemeToLight(t *testing.T) {
	t.Setenv("CAELIS_THEME", "")

	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)

	if !m.theme.IsDark {
		t.Fatal("expected default auto theme to start on dark fallback")
	}

	_, _ = m.Update(tea.BackgroundColorMsg{Color: color.White})

	if m.theme.IsDark {
		t.Fatal("expected background color message to switch model to light theme")
	}
	if got := ansi.Strip(m.renderStatusHeader()); strings.TrimSpace(got) == "" {
		t.Fatal("expected status header to remain renderable after theme switch")
	}
}

func TestBackgroundColorMsgDoesNotOverrideExplicitTheme(t *testing.T) {
	t.Setenv("CAELIS_THEME", "dark")

	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)

	_, _ = m.Update(tea.BackgroundColorMsg{Color: color.White})

	if !m.theme.IsDark {
		t.Fatal("expected explicit dark theme to ignore terminal background auto-switch")
	}
}

func TestPaletteToggle_NoAnimationSkipsTransition(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute, NoAnimation: true})
	resizeModel(m)
	_, _ = m.Update(keyPress('p', tea.ModCtrl))
	if !m.showPalette || m.paletteAnimating {
		t.Fatalf("expected palette open without animation, show=%v anim=%v", m.showPalette, m.paletteAnimating)
	}
	if m.paletteAnimLines != m.paletteAnimationTarget() {
		t.Fatalf("expected palette lines to jump to target, got=%d want=%d", m.paletteAnimLines, m.paletteAnimationTarget())
	}
}

func TestWideViewportCentersMainColumn(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	if got := m.mainColumnX(); got <= 0 {
		t.Fatalf("expected centered main column in wide terminal, got offset %d", got)
	}
	header := ansi.Strip(m.renderStatusHeader())
	placed := m.placeInMainColumn(header)
	if !strings.HasPrefix(placed, strings.Repeat(" ", m.mainColumnX())) {
		t.Fatalf("expected centered header padding, got %q", placed)
	}
}

func TestComposerSeparatorsUseMainColumnWidth(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})

	view := stripModelView(m)
	if strings.Contains(view, strings.Repeat("─", m.width)) {
		t.Fatalf("did not expect full terminal width separator in view")
	}
	centeredSep := strings.Repeat(" ", m.mainColumnX()) + strings.Repeat("─", m.fixedRowWidth())
	if !strings.Contains(view, centeredSep) {
		t.Fatalf("expected centered composer separator, got:\n%s", view)
	}
}

func TestSlashCompletionOverlayCentersInMainColumn(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		Commands:    []string{"agent", "connect", "exit"},
	})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	m.setInputText("/")
	m.syncTextareaFromInput()
	m.refreshSlashCommands()

	view := stripModelView(m)
	var titleLine string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Commands") {
			titleLine = line
			break
		}
	}
	if titleLine == "" {
		t.Fatalf("expected commands overlay in view, got:\n%s", view)
	}
	if idx := strings.Index(titleLine, "Commands"); idx < m.mainColumnX() {
		t.Fatalf("expected commands overlay centered within main column, idx=%d offset=%d line=%q", idx, m.mainColumnX(), titleLine)
	}
}

func TestPaletteOverlayCentersInMainColumn(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		Commands:    []string{"agent", "connect", "exit"},
		NoAnimation: true,
	})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	m.togglePalette()

	view := stripModelView(m)
	var overlayLine string
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "/agent") {
			overlayLine = line
			break
		}
	}
	if overlayLine == "" {
		t.Fatalf("expected palette overlay in view, got:\n%s", view)
	}
	if idx := strings.Index(overlayLine, "/agent"); idx < m.mainColumnX() {
		t.Fatalf("expected palette overlay centered within main column, idx=%d offset=%d line=%q", idx, m.mainColumnX(), overlayLine)
	}
}

func TestExplicitNoColorModelUsesColorlessTheme(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute, NoColor: true})
	resizeModel(m)
	if !m.theme.NoColor {
		t.Fatal("expected explicit no-color config to produce no-color theme")
	}
}

func TestLogBlockGapBetweenNarrativeAndTool(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: "hello world", Final: true})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ file.txt\n"})

	joined := ansi.Strip(strings.Join(m.renderedStyledLines(), "\n"))
	if strings.Contains(joined, "\n·\n") {
		t.Fatalf("did not expect synthetic dot spacer between assistant and tool, got %q", joined)
	}
	if !strings.Contains(joined, "hello world") || !strings.Contains(joined, "▸ Exploring 1 files") || !strings.Contains(joined, "Read file.txt") {
		t.Fatalf("expected assistant and exploration block preserved, got %q", joined)
	}
}

func TestExplorationBlockGroupsConsecutiveReadOnlyTools(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ state.go\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SEARCH . {query=enter}\n"})

	view := stripModelView(m)
	if !strings.Contains(view, "Exploring") {
		t.Fatalf("expected exploration title, got:\n%s", view)
	}
	if !strings.Contains(view, "Read state.go") {
		t.Fatalf("expected read child line, got:\n%s", view)
	}
	if !strings.Contains(view, "Searched for enter") {
		t.Fatalf("expected search child line, got:\n%s", view)
	}
	if strings.Contains(view, "▸ READ state.go") || strings.Contains(view, "▸ SEARCH . {query=enter}") {
		t.Fatalf("expected raw tool lines replaced by block, got:\n%s", view)
	}
}

func TestExplorationBlockStartsOnFirstReadTool(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ state.go\n"})

	view := stripModelView(m)
	if !strings.Contains(view, "▸ Exploring") {
		t.Fatalf("expected exploration block to start immediately on first read, got:\n%s", view)
	}
	if !strings.Contains(view, "Read state.go") {
		t.Fatalf("expected read entry rendered inside exploration block, got:\n%s", view)
	}
	if strings.Contains(view, "\n▸ READ state.go") {
		t.Fatalf("expected raw read line not rendered before exploration block, got:\n%s", view)
	}
}

func TestExplorationBlockMergesReadAndSearchResultSummaries(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ state.go\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ READ 1-120\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SEARCH . {query=enter}\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ SEARCH 12 matches, 1 files\n"})

	view := stripModelView(m)
	if !strings.Contains(view, "Read state.go 1-120") {
		t.Fatalf("expected merged read summary inside exploration block, got:\n%s", view)
	}
	if !strings.Contains(view, "Searched for enter 12 matches, 1 files") {
		t.Fatalf("expected merged search summary inside exploration block, got:\n%s", view)
	}
	if strings.Contains(view, "✓ READ 1-120") || strings.Contains(view, "✓ SEARCH 12 matches, 1 files") {
		t.Fatalf("expected result lines absorbed by exploration block, got:\n%s", view)
	}
}

func TestExplorationBlockMergesBatchedReadResultsWithEarlierCalls(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ hpfs.go\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ hpfs.go\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ quota.go\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ READ 311-370\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ READ 1321-1360\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ READ 1-21\n"})

	view := stripModelView(m)
	if !strings.Contains(view, "Read hpfs.go 311-370") {
		t.Fatalf("expected first read paired with filename, got:\n%s", view)
	}
	if !strings.Contains(view, "Read hpfs.go 1321-1360") {
		t.Fatalf("expected second read paired with filename, got:\n%s", view)
	}
	if !strings.Contains(view, "Read quota.go 1-21") {
		t.Fatalf("expected trailing read paired with filename, got:\n%s", view)
	}
	if strings.Contains(view, "Read 1321-1360") || strings.Contains(view, "Read 1-21") {
		t.Fatalf("expected no orphaned read ranges, got:\n%s", view)
	}
}

func TestExplorationBlockCollapsesBeforeAssistantAnswer(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ state.go\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SEARCH . {query=enter}\n"})
	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "done", Final: true})

	view := stripModelView(m)
	if !strings.Contains(view, "▸ Explored 1 files, 1 searches") {
		t.Fatalf("expected collapsed exploration summary, got:\n%s", view)
	}
	if !strings.Contains(view, "* done") {
		t.Fatalf("expected assistant answer after collapsed summary, got:\n%s", view)
	}
	if strings.Contains(view, "Exploring") || strings.Contains(view, "Read state.go") {
		t.Fatalf("expected expanded exploration block removed, got:\n%s", view)
	}
}

func TestFinalizeActivityBlock_ReplacesBlockInPlace(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ state.go\n"})
	m.doc.Append(NewTranscriptBlock("* assistant plan", tuikit.LineStyleAssistant))
	m.finalizeActivityBlock()

	view := stripModelView(m)
	summaryIdx := strings.Index(view, "▸ Explored 1 files")
	planIdx := strings.Index(view, "* assistant plan")
	if summaryIdx < 0 || planIdx < 0 {
		t.Fatalf("expected both activity summary and assistant line, got:\n%s", view)
	}
	if summaryIdx > planIdx {
		t.Fatalf("expected activity summary to stay in original position before later content, got:\n%s", view)
	}
	if strings.Contains(view, "Exploring") {
		t.Fatalf("expected expanded activity block to be replaced, got:\n%s", view)
	}
	if got := len(m.doc.Blocks()); got != 2 {
		t.Fatalf("expected 2 blocks after in-place replacement, got %d", got)
	}
}

func TestTaskMonitorBlockCollapsesToSummary(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ WAIT 5 s\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ WAITED 5 s\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ CHECK\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ CANCEL\n"})
	_, _ = m.Update(tuievents.TaskResultMsg{})

	view := stripModelView(m)
	if !strings.Contains(view, "Waited 5 s") || !strings.Contains(view, "Checked 1 tasks") || !strings.Contains(view, "Cancelled 1 tasks") {
		t.Fatalf("expected task monitor summary, got:\n%s", view)
	}
	if strings.Contains(view, "Standby") || strings.Contains(view, "Checking task status") {
		t.Fatalf("expected task monitor rendered as compact single line, got:\n%s", view)
	}
}

func TestTaskListActivityStaysInTaskMonitor(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ TASK list\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ TASK listed 1 task (1 active)\n"})
	_, _ = m.Update(tuievents.TaskResultMsg{})

	view := stripModelView(m)
	if !strings.Contains(view, "Listed 1 task") {
		t.Fatalf("expected task list summary in task monitor, got:\n%s", view)
	}
	if strings.Contains(view, "Explored 1 paths") {
		t.Fatalf("did not expect TASK list to fall back to exploration summary, got:\n%s", view)
	}
}

func TestExplorationBlockIncludesReadInSummary(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ task3_result.txt\n"})
	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "done", Final: true})

	view := stripModelView(m)
	if !strings.Contains(view, "▸ Explored 1 files") {
		t.Fatalf("expected read activity included in exploration summary, got:\n%s", view)
	}
}

func TestExplorationBlockAvoidsEmptyExploredSummary(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ GLOB {pattern=**/*.go}\n"})
	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "done", Final: true})

	view := stripModelView(m)
	if strings.Contains(view, "▸ Explored\n") {
		t.Fatalf("did not expect empty explored summary, got:\n%s", view)
	}
	if !strings.Contains(view, "patterns") && !strings.Contains(view, "actions") {
		t.Fatalf("expected explored fallback summary detail, got:\n%s", view)
	}
}

func TestTaskMonitorStatusCollapsesWithoutStandbyLabel(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ CHECK\n"})
	_, _ = m.Update(tuievents.TaskResultMsg{})

	view := stripModelView(m)
	if !strings.Contains(view, "Checked 1 tasks") {
		t.Fatalf("expected checked summary, got:\n%s", view)
	}
	if strings.Contains(view, "Standby") {
		t.Fatalf("did not expect standby summary, got:\n%s", view)
	}
}

func TestTaskMonitorWriteCollapsesToSendPreview(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ TASK WRITE continue the spawned worker with more detail\n"})
	_, _ = m.Update(tuievents.TaskResultMsg{})

	view := stripModelView(m)
	if !strings.Contains(view, "SEND continue the spawned worker with more detail") {
		t.Fatalf("expected TASK write summary to preserve send preview, got:\n%s", view)
	}
	if strings.Contains(view, "Checked 1 tasks") {
		t.Fatalf("did not expect TASK write summary to be rendered as task status, got:\n%s", view)
	}
}

func TestTaskMonitorCancelCollapsesWithoutStandbyLabel(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ CANCEL\n"})
	_, _ = m.Update(tuievents.TaskResultMsg{})

	view := stripModelView(m)
	if !strings.Contains(view, "Cancelled 1 tasks") {
		t.Fatalf("expected cancelled summary, got:\n%s", view)
	}
	if strings.Contains(view, "Standby") {
		t.Fatalf("did not expect standby summary, got:\n%s", view)
	}
}

func TestAdjacentTaskMonitorSummariesMergeIntoOneLine(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ CHECK\n"})
	_, _ = m.Update(tuievents.TaskResultMsg{})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ CHECK\n"})
	_, _ = m.Update(tuievents.TaskResultMsg{})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ CHECK\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ CHECK\n"})
	_, _ = m.Update(tuievents.TaskResultMsg{})

	view := stripModelView(m)
	if len(m.renderedStyledLines()) != 1 {
		t.Fatalf("expected adjacent task summaries merged into one line, got:\n%s", view)
	}
	if !strings.Contains(view, "Checked 1 tasks, Checked 1 tasks, Checked 2 tasks") {
		t.Fatalf("expected merged adjacent task summary content, got:\n%s", view)
	}
}

func TestTaskMonitorBlockAbsorbsTaskErrorResultLines(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ WAIT 5 s\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "! WAITED 5 s\n"})
	_, _ = m.Update(tuievents.TaskResultMsg{})

	view := stripModelView(m)
	if !strings.Contains(view, "Waited 5 s") {
		t.Fatalf("expected task error line to collapse into standby summary, got:\n%s", view)
	}
	if strings.Contains(view, "! WAITED 5 s") {
		t.Fatalf("expected raw task error line absorbed by standby block, got:\n%s", view)
	}
}

func TestTaskMonitorWaitSummaryPreservesTerminalState(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ WAIT 10 s\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ WAITED Completed\n"})
	_, _ = m.Update(tuievents.TaskResultMsg{})

	view := stripModelView(m)
	if !strings.Contains(view, "Waited 10 s (Completed)") {
		t.Fatalf("expected wait summary to keep terminal state, got:\n%s", view)
	}
	if strings.Contains(view, "✓ WAITED Completed") {
		t.Fatalf("expected raw task wait result absorbed into summary, got:\n%s", view)
	}
}

func TestReasoningTransitionsIntoExplorationBlock(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "reasoning", Text: "think first", Final: false})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ state.go\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SEARCH . {query=enter}\n"})

	view := stripModelView(m)
	if strings.Contains(view, "think first") {
		t.Fatalf("expected transient reasoning removed once exploration starts, got:\n%s", view)
	}
	if !strings.Contains(view, "Exploring") {
		t.Fatalf("expected exploration block after reasoning, got:\n%s", view)
	}
}

func TestReasoningDoesNotSplitActiveExplorationBlock(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ state.go\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ SEARCH . {query=enter}\n"})
	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "reasoning", Text: "transient thinking", Final: false})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ model_view.go\n"})
	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "done", Final: true})

	view := stripModelView(m)
	if strings.Contains(view, "transient thinking") {
		t.Fatalf("expected reasoning hidden while exploration block is active, got:\n%s", view)
	}
	if strings.Count(view, "Explored") != 1 {
		t.Fatalf("expected one collapsed exploration summary, got:\n%s", view)
	}
	if !strings.Contains(view, "▸ Explored 2 files, 1 searches") {
		t.Fatalf("expected merged exploration summary, got:\n%s", view)
	}
}

// ---------------------------------------------------------------------------
// Test wizard definitions (match production definitions in console_tui_tea.go)
// ---------------------------------------------------------------------------

func testConnectWizard() WizardDef {
	return WizardDef{
		Command:     "connect",
		DisplayLine: "/connect",
		Steps: []WizardStepDef{
			{
				Key:       "provider",
				HintLabel: "/connect provider",
				CompletionCommand: func(_ map[string]string) string {
					return "connect"
				},
			},
			{
				Key:       "baseurl",
				HintLabel: "/connect base_url",
				CompletionCommand: func(s map[string]string) string {
					return "connect-baseurl:" + s["provider"]
				},
			},
			{
				Key:       "timeout",
				HintLabel: "/connect timeout",
				Validate:  ValidateInt,
				CompletionCommand: func(s map[string]string) string {
					return "connect-timeout:" + s["provider"]
				},
			},
			{
				Key:          "apikey",
				HintLabel:    "/connect api_key",
				HideInput:    true,
				FreeformHint: "/connect api_key: type and press enter",
				CompletionCommand: func(s map[string]string) string {
					return "connect-apikey:" + s["provider"]
				},
				ShouldSkip: func(s map[string]string) bool {
					return s["_noauth"] == "true"
				},
			},
			{
				Key:          "model",
				HintLabel:    "/connect model",
				FreeformHint: "/connect model: type model name and press enter",
				CompletionCommand: func(s map[string]string) string {
					return "connect-model:" + buildConnectWizardPayloadForTest(s)
				},
			},
			{
				Key:          "context_window_tokens",
				HintLabel:    "/connect context_window_tokens",
				Validate:     ValidateInt,
				FreeformHint: "/connect context_window_tokens: type integer and press enter",
				CompletionCommand: func(s map[string]string) string {
					return "connect-context:" + buildConnectWizardPayloadForTest(s)
				},
			},
			{
				Key:          "max_output_tokens",
				HintLabel:    "/connect max_output_tokens",
				Validate:     ValidateInt,
				FreeformHint: "/connect max_output_tokens: type integer and press enter",
				CompletionCommand: func(s map[string]string) string {
					return "connect-maxout:" + buildConnectWizardPayloadForTest(s)
				},
			},
			{
				Key:          "reasoning_levels",
				HintLabel:    "/connect reasoning_levels(csv)",
				FreeformHint: "/connect reasoning_levels(csv): e.g. minimal,low (use - for empty)",
				CompletionCommand: func(s map[string]string) string {
					return "connect-reasoning-levels:" + buildConnectWizardPayloadForTest(s)
				},
			},
		},
		OnStepConfirm: func(stepKey, value string, candidate *SlashArgCandidate, state map[string]string) {
			if stepKey == "provider" {
				state["provider"] = strings.ToLower(strings.TrimSpace(value))
			}
			if stepKey == "provider" && candidate != nil && candidate.NoAuth {
				state["_noauth"] = "true"
			}
		},
		BuildExecLine: func(s map[string]string) string {
			apiKey := strings.TrimSpace(s["apikey"])
			if apiKey == "" {
				apiKey = "-"
			}
			reasoningLevels := strings.TrimSpace(s["reasoning_levels"])
			if reasoningLevels == "" {
				reasoningLevels = "-"
			}
			return "/connect " + s["provider"] + " " + s["model"] +
				" " + s["baseurl"] + " " + s["timeout"] +
				" " + apiKey +
				" " + s["context_window_tokens"] +
				" " + s["max_output_tokens"] +
				" " + reasoningLevels
		},
	}
}

func buildConnectWizardPayloadForTest(state map[string]string) string {
	return strings.TrimSpace(state["provider"]) +
		"|" + url.QueryEscape(state["baseurl"]) +
		"|" + strings.TrimSpace(state["timeout"]) +
		"|" + url.QueryEscape(strings.TrimSpace(state["apikey"])) +
		"|" + url.QueryEscape(state["model"])
}

func testWizards() []WizardDef {
	return []WizardDef{testConnectWizard()}
}
