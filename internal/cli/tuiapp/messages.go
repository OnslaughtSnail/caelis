package tuiapp

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

type clearHintMsg struct {
	id uint64
}

type ctrlCExpireMsg struct {
	armedAt time.Time
	seq     uint64
}

type paletteAnimationMsg struct{}

type toolOutputFadeMsg struct {
	key  string
	step int
}

func animatePaletteCmd() tea.Cmd {
	return tea.Tick(paletteAnimationInterval, func(time.Time) tea.Msg {
		return paletteAnimationMsg{}
	})
}

func clearHintLaterCmd(id uint64, after time.Duration) tea.Cmd {
	if id == 0 || after <= 0 {
		return nil
	}
	return tea.Tick(after, func(time.Time) tea.Msg {
		return clearHintMsg{id: id}
	})
}

func expireCtrlCCmd(armedAt time.Time, seq uint64) tea.Cmd {
	if armedAt.IsZero() {
		return nil
	}
	return tea.Tick(ctrlCExitWindow, func(time.Time) tea.Msg {
		return ctrlCExpireMsg{armedAt: armedAt, seq: seq}
	})
}

func tickStatusCmd() tea.Cmd {
	return tea.Tick(1200*time.Millisecond, func(time.Time) tea.Msg {
		return tuievents.TickStatusMsg{}
	})
}
