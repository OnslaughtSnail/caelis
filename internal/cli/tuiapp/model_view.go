package tuiapp

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

func (m *Model) View() tea.View {
	start := time.Now()

	if !m.ready {
		view := tea.NewView("loading...")
		view.AltScreen = true
		view.MouseMode = tea.MouseModeCellMotion
		return view
	}

	// Recalculate layout in case bottom section height changed.
	vpHeight, bottomHeight := m.computeLayout()
	if m.viewport.Height() != vpHeight {
		m.viewport.SetHeight(vpHeight)
		m.syncViewportContent()
	}

	var sections []string

	// 1. Viewport (scrollable history + streaming + spinner) with left gutter.
	vpView := m.viewport.View()
	vpView = strings.TrimRight(vpView, "\n")
	vpView = m.renderViewportScrollbar(vpView)
	if tuikit.GutterNarrative > 0 {
		vpView = indentBlock(vpView, tuikit.GutterNarrative)
	}
	sections = append(sections, vpView)
	sections = append(sections, "")

	if drawerView := m.renderPrimaryDrawer(); drawerView != "" {
		sections = append(sections, drawerView)
		sections = append(sections, "")
	}
	if pendingView := m.renderPendingQueueDrawer(); pendingView != "" {
		sections = append(sections, pendingView)
		sections = append(sections, "")
	}

	// 2. Hint row (contextual guidance).
	sections = append(sections, m.renderHintRow())
	sections = append(sections, "")

	// 3. Workspace + model status.
	sections = append(sections, m.renderStatusHeader())

	// 4. Separator above the composer input.
	if m.width > 0 {
		sep := m.theme.SeparatorStyle().Render(strings.Repeat("─", m.width))
		sections = append(sections, sep)
	}

	// 5. Composer top padding before input.
	for i := 0; i < tuikit.ComposerPadTop; i++ {
		sections = append(sections, "")
	}

	// 6. Input bar.
	sections = append(sections, m.renderInputBar())

	// 7. Composer bottom padding before footer separator.
	for i := 0; i < tuikit.ComposerPadBottom; i++ {
		sections = append(sections, "")
	}

	// 8. Lower separator + secondary status bar.
	if m.width > 0 {
		sep := m.theme.SeparatorStyle().Render(strings.Repeat("─", m.width))
		sections = append(sections, sep)
	}
	sections = append(sections, m.renderStatusFooter())

	// 9. Status bar bottom padding.
	for i := 0; i < tuikit.StatusBarPadBottom; i++ {
		sections = append(sections, "")
	}

	view := strings.Join(sections, "\n")

	if m.activePrompt != nil && m.width > 0 && m.height > 0 {
		if promptView := m.renderPromptModal(); promptView != "" {
			view = overlayAboveBottomArea(view, promptView, m.width, bottomHeight, 0)
		}
	} else if overlayView := m.renderInputOverlay(); overlayView != "" && m.width > 0 && m.height > 0 {
		view = overlayAboveBottomArea(view, overlayView, m.width, bottomHeight, 0)
	}

	// Overlay: command palette.
	if m.shouldRenderPalette() && m.width > 0 && m.height > 0 {
		lineCount := strings.Count(view, "\n") + 1
		if paletteView := m.renderPaletteOverlay(); paletteView != "" {
			view = overlayBottom(view, paletteView, m.width, lineCount)
		}
	}

	duration := time.Since(start)
	m.observeRender(duration, len(view), "fullscreen")
	frame := tea.NewView(view)
	frame.AltScreen = true
	frame.MouseMode = tea.MouseModeCellMotion
	frame.ReportFocus = true
	frame.WindowTitle = m.windowTitle()
	if cursor := m.regularInputCursor(); cursor != nil {
		cursor.Position.Y += m.viewport.Height() + m.preComposerFixedHeight() + tuikit.ComposerPadTop
		frame.Cursor = cursor
	}
	return frame
}
