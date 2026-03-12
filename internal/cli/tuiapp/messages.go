package tuiapp

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

type clearHintMsg struct {
	expected string
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

func clearHintLaterCmd(expected string, after time.Duration) tea.Cmd {
	expected = strings.TrimSpace(expected)
	if expected == "" || after <= 0 {
		return nil
	}
	return tea.Tick(after, func(time.Time) tea.Msg {
		return clearHintMsg{expected: expected}
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
