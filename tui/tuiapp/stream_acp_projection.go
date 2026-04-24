package tuiapp

import tea "charm.land/bubbletea/v2"

func (m *Model) handleACPProjection(msg ACPProjectionMsg) (tea.Model, tea.Cmd) {
	return m.handleTranscriptEventsMsg(TranscriptEventsMsg{
		Events: ProjectACPProjectionToTranscriptEvents(msg),
	})
}
