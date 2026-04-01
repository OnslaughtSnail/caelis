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
		view.KeyboardEnhancements.ReportEventTypes = true
		return view
	}

	// Compute layout; bottomHeight is needed for overlay positioning.
	// Viewport height is reconciled in Update() via ensureViewportLayout(),
	// so we intentionally do NOT mutate viewport state here.
	_, bottomHeight := m.computeLayout()

	var sections []string

	// 1. Viewport (scrollable history + streaming + spinner) with left gutter.
	vpView := m.renderViewportView()
	if tuikit.GutterNarrative > 0 {
		vpView = indentBlock(vpView, tuikit.GutterNarrative)
	}
	sections = append(sections, m.placeInMainColumn(vpView))
	sections = append(sections, "")

	if drawerView := m.renderPrimaryDrawer(); drawerView != "" {
		sections = append(sections, m.placeInMainColumn(drawerView))
		sections = append(sections, "")
	}
	if pendingView := m.renderPendingQueueDrawer(); pendingView != "" {
		sections = append(sections, m.placeInMainColumn(pendingView))
		sections = append(sections, "")
	}

	// 2. Hint row (contextual guidance).
	sections = append(sections, m.placeInMainColumn(m.renderHintRow()))
	sections = append(sections, "")

	// 3. Workspace + model status.
	sections = append(sections, m.placeInMainColumn(m.renderStatusHeader()))

	// 4. Separator above the composer input.
	if width := m.fixedRowWidth(); width > 0 {
		sep := m.theme.SeparatorStyle().Render(strings.Repeat("─", width))
		sections = append(sections, m.placeInMainColumn(sep))
	}

	// 5. Composer top padding before input.
	for range tuikit.ComposerPadTop {
		sections = append(sections, "")
	}

	// 6. Input bar.
	sections = append(sections, m.placeInMainColumn(m.renderInputBar()))

	// 7. Composer bottom padding before footer separator.
	for range tuikit.ComposerPadBottom {
		sections = append(sections, "")
	}

	// 8. Lower separator + secondary status bar.
	if width := m.fixedRowWidth(); width > 0 {
		sep := m.theme.SeparatorStyle().Render(strings.Repeat("─", width))
		sections = append(sections, m.placeInMainColumn(sep))
	}
	sections = append(sections, m.placeInMainColumn(m.renderStatusFooter()))

	// 9. Status bar bottom padding.
	for range tuikit.StatusBarPadBottom {
		sections = append(sections, "")
	}

	view := strings.Join(sections, "\n")

	if m.activePrompt != nil && m.width > 0 && m.height > 0 {
		if promptView := m.renderPromptModal(); promptView != "" {
			view = overlayAboveBottomArea(view, promptView, m.width, m.mainColumnX(), m.fixedRowWidth(), bottomHeight, 0)
		}
	} else if overlayView := m.renderInputOverlay(); overlayView != "" && m.width > 0 && m.height > 0 {
		view = overlayAboveBottomArea(view, overlayView, m.width, m.mainColumnX(), m.fixedRowWidth(), bottomHeight, 0)
	}

	// Overlay: command palette.
	if m.shouldRenderPalette() && m.width > 0 && m.height > 0 {
		lineCount := strings.Count(view, "\n") + 1
		if paletteView := m.renderPaletteOverlay(); paletteView != "" {
			view = overlayBottom(view, paletteView, m.width, m.mainColumnX(), m.fixedRowWidth(), lineCount)
		}
	}

	duration := time.Since(start)
	m.observeRender(duration, len(view), "fullscreen")
	frame := tea.NewView(view)
	frame.AltScreen = true
	frame.MouseMode = tea.MouseModeCellMotion
	frame.ReportFocus = true
	frame.KeyboardEnhancements.ReportEventTypes = true
	frame.WindowTitle = m.windowTitle()
	if cursor := m.regularInputCursor(); cursor != nil {
		cursor.X += m.mainColumnX()
		cursor.Y += m.viewport.Height() + m.preComposerFixedHeight() + tuikit.ComposerPadTop
		frame.Cursor = cursor
	}
	return frame
}
