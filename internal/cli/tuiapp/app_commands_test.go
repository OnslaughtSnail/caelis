package tuiapp

import (
	"slices"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
)

func TestSetCommandsMsgRefreshesSlashCandidates(t *testing.T) {
	m := NewModel(Config{
		Commands: []string{"help"},
	})
	m.width = 80
	m.height = 24
	m.ready = true
	m.setInputText("/ge")
	m.cursor = len([]rune("/ge"))
	m.refreshSlashCommands()
	if len(m.slashCandidates) != 0 {
		t.Fatalf("did not expect gemini before refresh, got %v", m.slashCandidates)
	}

	_, _ = m.Update(tuievents.SetCommandsMsg{Commands: []string{"help", "gemini"}})
	m.setInputText("/ge")
	m.cursor = len([]rune("/ge"))
	m.refreshSlashCommands()
	if !slices.Contains(m.cfg.Commands, "gemini") {
		t.Fatalf("expected gemini in model commands, got %v", m.cfg.Commands)
	}
	if len(m.slashCandidates) != 1 || m.slashCandidates[0] != "/gemini" {
		t.Fatalf("expected gemini slash candidate after refresh, got %v", m.slashCandidates)
	}
}
