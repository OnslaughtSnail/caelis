package tuiapp

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

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
	cmd, query, ok = slashArgQueryAtCursor([]rune("/model rm"), len([]rune("/model rm")))
	if !ok {
		t.Fatal("expected model subcommand query")
	}
	if cmd != "model" || query != "rm" {
		t.Fatalf("unexpected model subcommand parse: cmd=%q query=%q", cmd, query)
	}
	cmd, query, ok = slashArgQueryAtCursor([]rune("/model rm "), len([]rune("/model rm ")))
	if !ok {
		t.Fatal("expected model alias picker parse")
	}
	if cmd != "model rm" || query != "" {
		t.Fatalf("unexpected model alias parse: cmd=%q query=%q", cmd, query)
	}
	cmd, query, ok = slashArgQueryAtCursor([]rune("/model use mimo "), len([]rune("/model use mimo ")))
	if !ok {
		t.Fatal("expected model reasoning picker parse")
	}
	if cmd != "model use mimo" || query != "" {
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
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
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
	typeRunes(m, "/model use xia")
	if got := m.currentInputGhostHint(); got != "omi/mimo-v2-flash" {
		t.Fatalf("expected model alias ghost hint, got %q", got)
	}
}

func TestCurrentInputGhostHint_ForModelActionPrefix(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			if command != "model" {
				return nil, nil
			}
			return []SlashArgCandidate{{Value: "list", Display: "list"}}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/model l")
	if got := m.currentInputGhostHint(); got != "ist" {
		t.Fatalf("expected model action ghost hint, got %q", got)
	}
}

func TestCurrentInputGhostHint_ForResume(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		ResumeComplete: func(query string, limit int) ([]ResumeCandidate, error) {
			return []ResumeCandidate{{SessionID: "s-123", Prompt: "demo", Age: "1m"}}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/resume ")
	if got := m.currentInputGhostHint(); got != "s-123" {
		t.Fatalf("expected resume ghost hint, got %q", got)
	}
}

func TestRenderInputBar_ShowsGhostHintWithoutCursor(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"model", "status"},
		ExecuteLine: noopExecute,
	})
	resizeModel(m)
	typeRunes(m, "/mo")

	line := m.renderInputBar()
	if !strings.Contains(line, "/model") {
		t.Fatalf("expected ghost completion in input bar, got %q", line)
	}
	if strings.Contains(line, "█") {
		t.Fatalf("expected ghost render without cursor glyph, got %q", line)
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
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = line
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

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	view := m.View()
	if !strings.Contains(view, "CAELIS") {
		t.Fatalf("expected welcome card title in view, got %q", view)
	}
	if !strings.Contains(view, "workspace:") {
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
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "not configured (/connect)") {
		t.Fatalf("expected explicit empty model state, got %q", view)
	}
}

func TestResumeOverlayEnterExecutesSelectedSession(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = line
			return tuievents.TaskResultMsg{}
		},
		ResumeComplete: func(query string, limit int) ([]ResumeCandidate, error) {
			return []ResumeCandidate{
				{SessionID: "s-1", Prompt: "first prompt", Age: "10m"},
				{SessionID: "s-2", Prompt: "second prompt", Age: "30m"},
			}, nil
		},
	})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	typeRunes(m, "/resume")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.resumeCandidates) != 2 {
		t.Fatalf("expected 2 resume candidates, got %d", len(m.resumeCandidates))
	}
	rendered := m.renderResumeList()
	if !strings.Contains(rendered, "10m  first prompt") || !strings.Contains(rendered, "30m  second prompt") {
		t.Fatalf("expected age+prompt in resume list, got %q", rendered)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.resumeIndex != 1 {
		t.Fatalf("expected resume index 1, got %d", m.resumeIndex)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
		ResumeComplete: func(query string, limit int) ([]ResumeCandidate, error) {
			return []ResumeCandidate{
				{SessionID: "s-1", Prompt: "first", Age: "1m"},
				{SessionID: "s-2", Prompt: "second", Age: "2m"},
			}, nil
		},
	})
	resizeModel(m)

	typeRunes(m, "/resume")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})

	if got := m.textarea.Value(); got != "/resume s-2 " {
		t.Fatalf("expected '/resume s-2 ', got %q", got)
	}
	if len(m.resumeCandidates) != 0 {
		t.Fatalf("expected resume candidates cleared after tab completion, got %d", len(m.resumeCandidates))
	}
}

func TestResumeOverlayEscClearsResumeCommand(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		ResumeComplete: func(query string, limit int) ([]ResumeCandidate, error) {
			return []ResumeCandidate{
				{SessionID: "s-1", Prompt: "first", Age: "1m"},
			}, nil
		},
	})
	resizeModel(m)

	typeRunes(m, "/resume")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.resumeCandidates) == 0 {
		t.Fatal("expected resume candidates")
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
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
		ResumeComplete: func(query string, limit int) ([]ResumeCandidate, error) {
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
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	for i := 0; i < 12; i++ {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.resumeIndex != 12 {
		t.Fatalf("expected resume index 12, got %d", m.resumeIndex)
	}
	rendered := m.renderResumeList()
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
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = line
			return tuievents.TaskResultMsg{}
		},
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			switch {
			case command == "model":
				return []SlashArgCandidate{
					{Value: "list", Display: "list"},
					{Value: "use", Display: "use"},
					{Value: "rm", Display: "rm"},
					{Value: "edit", Display: "edit"},
				}, nil
			case command == "model use":
				return []SlashArgCandidate{
					{Value: "deepseek/deepseek-chat", Display: "deepseek/deepseek-chat"},
					{Value: "xiaomi/mimo-v2-flash", Display: "xiaomi/mimo-v2-flash"},
				}, nil
			case command == "model use xiaomi/mimo-v2-flash":
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
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.slashArgCandidates) != 4 {
		t.Fatalf("expected 4 model action candidates, got %d", len(m.slashArgCandidates))
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := strings.TrimSpace(m.slashArgCommand); got != "model use" {
		t.Fatalf("expected model alias step, got %q", got)
	}
	if len(m.slashArgCandidates) != 2 {
		t.Fatalf("expected 2 alias candidates, got %d", len(m.slashArgCandidates))
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := strings.TrimSpace(m.textarea.Value()); got != "/model use xiaomi/mimo-v2-flash" {
		t.Fatalf("expected alias completion in input, got %q", got)
	}
	if got := strings.TrimSpace(m.slashArgCommand); got != "model use xiaomi/mimo-v2-flash" {
		t.Fatalf("expected model reasoning step, got %q", m.slashArgCommand)
	}
	if len(m.slashArgCandidates) != 2 {
		t.Fatalf("expected 2 reasoning candidates, got %d", len(m.slashArgCandidates))
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected no command while accepting final reasoning completion")
	}
	if got := strings.TrimSpace(m.textarea.Value()); got != "/model use xiaomi/mimo-v2-flash on" {
		t.Fatalf("expected completed model command in input, got %q", got)
	}
	if m.slashArgActive {
		t.Fatal("expected slash-arg overlay closed after final completion")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			switch {
			case command == "model":
				return []SlashArgCandidate{
					{Value: "list", Display: "list"},
					{Value: "use", Display: "use"},
					{Value: "rm", Display: "rm"},
					{Value: "edit", Display: "edit"},
				}, nil
			case command == "model use":
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
	if len(m.slashArgCandidates) != 4 {
		t.Fatalf("expected model action candidates, got %d", len(m.slashArgCandidates))
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			switch command {
			case "model":
				return []SlashArgCandidate{
					{Value: "list", Display: "list"},
					{Value: "use", Display: "use"},
					{Value: "rm", Display: "rm"},
					{Value: "edit", Display: "edit"},
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

func TestModelListExecutesOnSingleEnterWhenAlreadyComplete(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = strings.TrimSpace(line)
			return tuievents.TaskResultMsg{}
		},
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			if command != "model" {
				return nil, nil
			}
			return []SlashArgCandidate{
				{Value: "list", Display: "list"},
				{Value: "use", Display: "use"},
			}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/model list")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command for exact /model list")
	}
	if !findAndRunTaskResult(cmd(), m) {
		t.Fatal("expected TaskResultMsg in submit command")
	}
	if called != "/model list" {
		t.Fatalf("expected '/model list', got %q", called)
	}
}

func TestModelListExecutesOnSingleEnterWhenExactQueryHasNoCandidates(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = strings.TrimSpace(line)
			return tuievents.TaskResultMsg{}
		},
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			if command != "model" {
				return nil, nil
			}
			switch strings.TrimSpace(query) {
			case "":
				return []SlashArgCandidate{
					{Value: "list", Display: "list"},
					{Value: "use", Display: "use"},
				}, nil
			case "l":
				return []SlashArgCandidate{{Value: "list", Display: "list"}}, nil
			case "list":
				return nil, nil
			default:
				return nil, nil
			}
		},
	})
	resizeModel(m)
	typeRunes(m, "/model list")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command for exact /model list without candidates")
	}
	if !findAndRunTaskResult(cmd(), m) {
		t.Fatal("expected TaskResultMsg in submit command")
	}
	if called != "/model list" {
		t.Fatalf("expected '/model list', got %q", called)
	}
}

func TestModelListExecutesAfterRemovingTrailingSpaceFromCompletion(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = strings.TrimSpace(line)
			return tuievents.TaskResultMsg{}
		},
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			if command != "model" {
				return nil, nil
			}
			switch strings.TrimSpace(query) {
			case "":
				return []SlashArgCandidate{
					{Value: "list", Display: "list"},
					{Value: "use", Display: "use"},
				}, nil
			case "l":
				return []SlashArgCandidate{{Value: "list", Display: "list"}}, nil
			case "list":
				return nil, nil
			default:
				return nil, nil
			}
		},
	})
	resizeModel(m)
	typeRunes(m, "/model l")

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := m.textarea.Value(); got != "/model list " {
		t.Fatalf("expected '/model list ' after tab completion, got %q", got)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := m.textarea.Value(); got != "/model list" {
		t.Fatalf("expected '/model list' after removing trailing space, got %q", got)
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command after removing trailing completion space")
	}
	if !findAndRunTaskResult(cmd(), m) {
		t.Fatal("expected TaskResultMsg in submit command")
	}
	if called != "/model list" {
		t.Fatalf("expected '/model list', got %q", called)
	}
}

func TestModelRmExecutesOnSingleEnterWhenAliasAlreadyComplete(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = strings.TrimSpace(line)
			return tuievents.TaskResultMsg{}
		},
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			if command != "model rm" {
				return nil, nil
			}
			return []SlashArgCandidate{{Value: "xiaomi/mimo-v2-flash", Display: "xiaomi/mimo-v2-flash"}}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/model rm xiaomi/mimo-v2-flash")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command for exact /model rm alias")
	}
	if !findAndRunTaskResult(cmd(), m) {
		t.Fatal("expected TaskResultMsg in submit command")
	}
	if called != "/model rm xiaomi/mimo-v2-flash" {
		t.Fatalf("expected '/model rm xiaomi/mimo-v2-flash', got %q", called)
	}
}

func TestSlashArgOverlayTabFillsSelectedValue(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
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
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})

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
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			if command != "connect" {
				return nil, nil
			}
			return []SlashArgCandidate{{Value: "openai", Display: "openai"}}, nil
		},
	})
	resizeModel(m)

	typeRunes(m, "/connect")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected slash-arg candidates")
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
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
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = line
			return tuievents.TaskResultMsg{}
		},
		Wizards: testWizards(),
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
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
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open provider picker
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected provider candidates")
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick openai, open base_url picker
	if !strings.HasPrefix(m.slashArgCommand, "connect-baseurl:openai") {
		t.Fatalf("expected connect-baseurl step, got %q", m.slashArgCommand)
	}
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected base_url candidates")
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick base_url, open timeout picker
	if !strings.HasPrefix(m.slashArgCommand, "connect-timeout:openai") {
		t.Fatalf("expected connect-timeout step, got %q", m.slashArgCommand)
	}
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected timeout candidates")
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})  // pick 60
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open api_key step
	if !strings.HasPrefix(m.slashArgCommand, "connect-apikey:openai") {
		t.Fatalf("expected connect-apikey step, got %q", m.slashArgCommand)
	}
	if got := m.textarea.Value(); got != "/connect " {
		t.Fatalf("expected connect input kept minimal, got %q", got)
	}
	typeRunes(m, "sk-test")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open model picker
	if !strings.HasPrefix(m.slashArgCommand, "connect-model:openai|") {
		t.Fatalf("expected connect-model step, got %q", m.slashArgCommand)
	}
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected model candidates")
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})  // pick gpt-4o-mini
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open context_window_tokens picker
	if !strings.HasPrefix(m.slashArgCommand, "connect-context:openai|") {
		t.Fatalf("expected connect-context step, got %q", m.slashArgCommand)
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick context_window_tokens
	if !strings.HasPrefix(m.slashArgCommand, "connect-maxout:openai|") {
		t.Fatalf("expected connect-maxout step, got %q", m.slashArgCommand)
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick max_output_tokens
	if !strings.HasPrefix(m.slashArgCommand, "connect-reasoning-levels:openai|") {
		t.Fatalf("expected connect-reasoning-levels step, got %q", m.slashArgCommand)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick reasoning levels and submit
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
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = line
			return tuievents.TaskResultMsg{}
		},
		Wizards: testWizards(),
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
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
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // provider
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // base_url
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // timeout
	typeRunes(m, "sk-test")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // api_key -> model step (no candidates)
	if !strings.HasPrefix(m.slashArgCommand, "connect-model:openai|") {
		t.Fatalf("expected connect-model step, got %q", m.slashArgCommand)
	}
	if len(m.slashArgCandidates) != 0 {
		t.Fatalf("expected no model candidates for manual fallback, got %d", len(m.slashArgCandidates))
	}
	typeRunes(m, "gpt-custom")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // model -> context step
	if !strings.HasPrefix(m.slashArgCommand, "connect-context:openai|") {
		t.Fatalf("expected connect-context step, got %q", m.slashArgCommand)
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // context
	if !strings.HasPrefix(m.slashArgCommand, "connect-maxout:openai|") {
		t.Fatalf("expected connect-maxout step, got %q", m.slashArgCommand)
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // max output
	if !strings.HasPrefix(m.slashArgCommand, "connect-reasoning-levels:openai|") {
		t.Fatalf("expected connect-reasoning-levels step, got %q", m.slashArgCommand)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // reasoning -> submit
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
	_ = m.View()
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
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
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

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.textarea.Value() != "second" {
		t.Fatalf("expected 'second', got %q", m.textarea.Value())
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.textarea.Value() != "first" {
		t.Fatalf("expected 'first', got %q", m.textarea.Value())
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.textarea.Value() != "second" {
		t.Fatalf("expected 'second', got %q", m.textarea.Value())
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
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

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
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

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.textarea.Value() != "old-cmd" {
		t.Fatalf("expected 'old-cmd', got %q", m.textarea.Value())
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
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
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	got := string(m.input)
	if got != "/help" {
		t.Fatalf("expected '/help', got %q", got)
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
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			switch command {
			case "model":
				return []SlashArgCandidate{{Value: "use", Display: "use"}}, nil
			default:
				return nil, nil
			}
		},
		ResumeComplete: func(query string, limit int) ([]ResumeCandidate, error) {
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
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = line
			return tuievents.TaskResultMsg{}
		},
	})
	resizeModel(m)
	typeRunes(m, "/connect openai-compatible")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
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
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := string(m.input); !strings.HasPrefix(got, "/") || strings.HasSuffix(got, " ") {
		t.Fatalf("expected slash command filled without trailing space, got %q", got)
	}
}

func TestSlashCommandEnterOpensModelPicker(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"model", "status"},
		ExecuteLine: noopExecute,
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			if command != "model" {
				return nil, nil
			}
			return []SlashArgCandidate{
				{Value: "list", Display: "list"},
				{Value: "use", Display: "use"},
			}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/model")
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.slashArgCandidates) == 0 {
		t.Fatal("expected model picker candidates after confirming /model")
	}
}

func TestMouseDragCopiesSelection(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "hello world\n"})
	_, _ = m.Update(tea.MouseMsg{X: tuikit.GutterNarrative, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	_, cmd := m.Update(tea.MouseMsg{X: tuikit.GutterNarrative + 5, Y: 0, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if cmd == nil {
		t.Fatal("expected clipboard command on mouse selection")
	}
	if !strings.Contains(m.hint, "copied") {
		t.Fatalf("expected copy hint, got %q", m.hint)
	}
	if !m.hasSelectionRange() {
		t.Fatal("expected viewport selection to remain after mouse release")
	}
}

func TestInputMouseDragCopiesSelection(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)
	typeRunes(m, "hello world")
	startY, _, ok := m.inputAreaBounds()
	if !ok {
		t.Fatal("expected input area bounds")
	}
	_, _ = m.Update(tea.MouseMsg{X: 2, Y: startY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	_, cmd := m.Update(tea.MouseMsg{X: 7, Y: startY, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if cmd == nil {
		t.Fatal("expected clipboard command on input selection")
	}
	if !strings.Contains(m.hint, "copied") {
		t.Fatalf("expected copy hint, got %q", m.hint)
	}
	start, end, ok := normalizedSelectionRange(m.inputSelectionStart, m.inputSelectionEnd, len(m.inputPlainLines()))
	if !ok || (start.line == end.line && start.col == end.col) {
		t.Fatal("expected input selection to remain after mouse release")
	}
}

func TestHeaderMouseDragCopiesSelection(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		Workspace:   "~/WorkDir/xueyongzhi/caelis [main]",
		RefreshStatus: func() (string, string) {
			return "claude-opus-4.6 [reasoning on]", "0/200.0k(0%)"
		},
	})
	resizeModel(m)
	layout := m.fixedRowLayout()
	_, _ = m.Update(tea.MouseMsg{X: tuikit.StatusInset, Y: layout.headerY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	_, cmd := m.Update(tea.MouseMsg{X: tuikit.StatusInset + 19, Y: layout.headerY, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
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
}

func TestCopyHintClearsOnTimerMessage(t *testing.T) {
	m := NewModel(Config{ExecuteLine: noopExecute})
	resizeModel(m)
	typeRunes(m, "hello world")
	startY, _, ok := m.inputAreaBounds()
	if !ok {
		t.Fatal("expected input area bounds")
	}
	_, _ = m.Update(tea.MouseMsg{X: 2, Y: startY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	_, _ = m.Update(tea.MouseMsg{X: 7, Y: startY, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if !strings.Contains(m.hint, "copied") {
		t.Fatalf("expected copy hint, got %q", m.hint)
	}
	_, _ = m.Update(clearHintMsg{expected: "selected text copied to clipboard"})
	if strings.TrimSpace(m.hint) != "" {
		t.Fatalf("expected copy hint cleared, got %q", m.hint)
	}
}

func TestCopyHintTimerDoesNotOverrideNewHint(t *testing.T) {
	m := newTestModel()
	m.hint = "interrupt requested"
	_, _ = m.Update(clearHintMsg{expected: "selected text copied to clipboard"})
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
	if clearMsg.expected != "started new session" {
		t.Fatalf("unexpected clear expected %q", clearMsg.expected)
	}
	_, _ = m.Update(clearMsg)
	if strings.TrimSpace(m.hint) != "" {
		t.Fatalf("expected hint cleared, got %q", m.hint)
	}
}

// ---------------------------------------------------------------------------
// Inline architecture: streaming + commit tests
// ---------------------------------------------------------------------------

func TestLogChunkCommitsCompletedLines(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, cmd := m.Update(tuievents.LogChunkMsg{Chunk: "* hello\n│ reasoning\n"})

	// In fullscreen mode, committed lines go to historyLines (no tea.Println cmd).
	if cmd != nil {
		t.Fatal("expected nil cmd (no tea.Println in fullscreen mode)")
	}
	if m.streamLine != "" {
		t.Fatalf("expected empty stream buffer, got %q", m.streamLine)
	}
	if !m.hasCommittedLine {
		t.Fatal("expected hasCommittedLine to be true")
	}
	if len(m.historyLines) < 2 {
		t.Fatalf("expected at least 2 history lines, got %d", len(m.historyLines))
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

	// In fullscreen mode, committed lines go to historyLines (no tea.Println cmd).
	if cmd != nil {
		t.Fatal("expected nil cmd (no tea.Println in fullscreen mode)")
	}
	if m.streamLine != "partial" {
		t.Fatalf("expected 'partial' in stream buffer, got %q", m.streamLine)
	}
	if len(m.historyLines) < 1 {
		t.Fatal("expected at least 1 history line for committed 'line1'")
	}
}

func TestFlushStreamOnTaskResult(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* partial"})
	_, cmd := m.Update(tuievents.TaskResultMsg{})

	// In fullscreen mode, flush goes to historyLines (no tea.Println cmd).
	if cmd != nil {
		t.Fatal("expected nil cmd (no ExitNow, no tea.Println)")
	}
	if m.streamLine != "" {
		t.Fatalf("expected empty stream buffer after task result, got %q", m.streamLine)
	}
	if m.running {
		t.Fatal("expected running to be false after task result")
	}
	if len(m.historyLines) < 1 {
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

	view := m.View()
	if !strings.Contains(view, "streaming text") {
		t.Fatalf("expected view to contain streaming text, got:\n%s", view)
	}
}

func TestAssistantStreamUpdatesMarkdownBlockInPlace(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: "## He", Final: false})
	if m.assistantBlock == nil {
		t.Fatal("expected active assistant block after partial stream")
	}
	start := m.assistantBlock.start

	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: "ading\n\n- one", Final: false})
	if m.assistantBlock == nil {
		t.Fatal("expected active assistant block after second partial stream")
	}
	if m.assistantBlock.start != start {
		t.Fatalf("expected assistant block to be updated in place, got start=%d want=%d", m.assistantBlock.start, start)
	}

	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: "## Heading\n\n- one\n- two", Final: true})
	if m.assistantBlock != nil {
		t.Fatal("expected assistant block to be finalized")
	}

	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
	if !strings.Contains(joined, "• one") || !strings.Contains(joined, "• two") {
		t.Fatalf("expected markdown list rendering in history, got %q", joined)
	}
	if strings.Contains(joined, "## Heading") {
		t.Fatalf("expected heading markers to be hidden, got %q", joined)
	}
}

func TestReasoningStreamKeepsBlockStyleAcrossParagraphs(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ReasoningStreamMsg{
		Text:  "第一段\n\n第二段",
		Final: true,
	})

	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
	if strings.Contains(joined, "第一段") || strings.Contains(joined, "第二段") {
		t.Fatalf("expected finalized reasoning block to auto-collapse, got %q", joined)
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

	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
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

	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
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

	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
	if strings.Count(joined, "Hello world") != 1 {
		t.Fatalf("expected one finalized answer block, got %q", joined)
	}
	if strings.Contains(joined, "* Hello\n* Hello world") {
		t.Fatalf("expected final answer to replace partial block in place, got %q", joined)
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

	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
	if strings.Contains(joined, "phase1phase2") {
		t.Fatalf("expected reasoning blocks separated by tool turn, got %q", joined)
	}
	if !strings.Contains(joined, "· phase1") || !strings.Contains(joined, "· phase2") {
		t.Fatalf("expected both reasoning blocks rendered, got %q", joined)
	}
}

func TestAssistantFinalDuplicateEventIsSuppressed(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "done", Final: true})
	if m.assistantBlock != nil {
		t.Fatal("expected final one-shot answer block to close immediately")
	}
	_, _ = m.Update(tuievents.AssistantStreamMsg{Kind: "answer", Text: "done", Final: true})

	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
	if strings.Count(joined, "* done") != 1 {
		t.Fatalf("expected duplicated final answer suppressed, got %q", joined)
	}
}

func TestApprovalPromptUsesChoiceListAndArrowSubmit(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	respCh := make(chan tuievents.PromptResponse, 1)
	_, _ = m.Update(tuievents.PromptRequestMsg{
		Prompt:        "Would you like to run the following command?",
		DefaultChoice: "y",
		Choices: []tuievents.PromptChoice{
			{Label: "proceed", Value: "y", Detail: "just this once"},
			{Label: "session", Value: "a", Detail: "don't ask again for: go test"},
			{Label: "cancel", Value: "n", Detail: "continue without it"},
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
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "proceed") || !strings.Contains(view, "session") || !strings.Contains(view, "cancel") {
		t.Fatalf("expected approval list options in modal, got %q", view)
	}
	if !strings.Contains(view, "Use ↑/↓ to choose, Enter to confirm, Esc to cancel") {
		t.Fatalf("expected approval footer hint in view, got %q", view)
	}
	if !strings.Contains(view, "╭") || !strings.Contains(view, "Would you like to run the following command?") {
		t.Fatalf("expected boxed approval modal in view, got %q", view)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

	_, _ = m.Update(tea.KeyMsg{Runes: []rune("o3")})
	if string(m.activePrompt.filter) != "o3" {
		t.Fatalf("expected prompt filter to update, got %q", string(m.activePrompt.filter))
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "openai/o3") {
		t.Fatalf("expected filtered choice in modal, got %q", view)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "[x] openai/gpt-4o") || !strings.Contains(view, "[x] openai/o3") {
		t.Fatalf("expected checked markers in view, got %q", view)
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

	_, _ = m.Update(tea.KeyMsg{Runes: []rune("doubao-seed-2-0-code")})
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "输入自定义模型名") {
		t.Fatalf("expected always visible custom choice in prompt, got %q", view)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
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

	view := ansi.Strip(m.View())
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
	if !strings.Contains(strings.Join(m.historyLines, "\n"), "stale line") {
		t.Fatal("expected stale line before clear")
	}
	_, _ = m.Update(tuievents.ClearHistoryMsg{})
	joined := strings.Join(m.historyLines, "\n")
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

	if len(m.diffBlocks) != 1 {
		t.Fatalf("expected one diff block, got %d", len(m.diffBlocks))
	}
	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
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
	before := ansi.Strip(strings.Join(m.historyLines, "\n"))
	_, _ = m.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	after := ansi.Strip(strings.Join(m.historyLines, "\n"))
	if before == after {
		t.Fatalf("expected resize to rerender diff block, got identical output: %q", after)
	}
	if !strings.Contains(after, " │ ") {
		t.Fatalf("expected split diff separator after wide resize, got %q", after)
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
	if len(m.diffBlocks) != 1 {
		t.Fatalf("expected one diff block, got %d", len(m.diffBlocks))
	}
	_, _ = m.Update(tuievents.ClearHistoryMsg{})
	if len(m.diffBlocks) != 0 {
		t.Fatalf("expected diff blocks reset on clear history, got %d", len(m.diffBlocks))
	}
}

func TestViewShowsBreathingHintWhenRunning(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.running = true
	m.startRunningAnimation()
	m.runningTip = 0
	m.syncViewportContent()
	view := m.View()

	if !strings.Contains(view, "Queue your next prompt now; it will run after this one.") {
		t.Fatalf("expected running carousel text in view when running, got:\n%s", view)
	}
	if strings.Contains(view, "thinking") || strings.Contains(view, "Tip:") {
		t.Fatalf("did not expect thinking/tip prefix in running hint, got:\n%s", view)
	}
	if strings.Contains(strings.Join(m.viewportPlainLines, "\n"), "Queue your next prompt now; it will run after this one.") {
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
	view := m.View()
	if !strings.Contains(view, ">") {
		t.Fatalf("expected input prompt while running for queueing, got:\n%s", view)
	}
}

func TestViewShowsPendingQueueWhileRunning(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.running = true
	m.startRunningAnimation()
	m.runningTip = 0
	m.pendingQueue = []pendingPrompt{
		{execLine: "first", displayLine: "first"},
		{execLine: "second", displayLine: "second"},
	}
	view := m.View()
	if !strings.Contains(view, "2 pending") {
		t.Fatalf("expected pending queue hint in running view, got:\n%s", view)
	}
}

func TestViewShowsToolOutputPanelWithLatestFourLines(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	for i := 1; i <= 6; i++ {
		_, _ = m.Update(tuievents.ToolStreamMsg{
			Tool:   "BASH",
			CallID: "call-1",
			Stream: "stdout",
			Chunk:  fmt.Sprintf("line-%d\n", i),
		})
	}

	view := m.View()
	if strings.Contains(view, "terminal output") || strings.Contains(view, "BASH") {
		t.Fatalf("expected boxed tool output without extra header text, got:\n%s", view)
	}
	if !strings.Contains(view, "╭") || !strings.Contains(view, "╰") {
		t.Fatalf("expected bordered tool output box, got:\n%s", view)
	}
	if strings.Contains(view, "line-1") || strings.Contains(view, "line-2") {
		t.Fatalf("expected old tool output lines to scroll out, got:\n%s", view)
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

	view := ansi.Strip(m.View())
	lines := strings.Split(view, "\n")
	var topBorder string
	var contentLine string
	for _, line := range lines {
		if strings.Contains(line, "╭") && strings.Contains(line, "╮") {
			topBorder = line
		}
		if strings.Contains(line, "short") {
			contentLine = line
			break
		}
	}
	if topBorder == "" || contentLine == "" {
		t.Fatalf("expected tool output box, got:\n%s", view)
	}
	topBorder = strings.TrimRight(topBorder, " ")
	contentLine = strings.TrimRight(contentLine, " ")
	if len(topBorder) < 70 {
		t.Fatalf("expected tool output box to fill viewport width, got top border %q", topBorder)
	}
	if len(contentLine) < 70 {
		t.Fatalf("expected tool output content line to fill viewport width, got %q", contentLine)
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

	view := ansi.Strip(m.View())
	if strings.Contains(view, "\n! \n") {
		t.Fatalf("did not expect blank tool output rows, got:\n%s", view)
	}
	if !strings.Contains(view, "line-1") || !strings.Contains(view, "line-2") {
		t.Fatalf("expected non-blank tool output rows, got:\n%s", view)
	}
}

func TestBashFinalCollapsesPanelIntoPlainHistoryText(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH {command=date}\n"})
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
		Final:  true,
	})

	view := ansi.Strip(m.View())
	if strings.Contains(view, "╭") || strings.Contains(view, "╰") {
		t.Fatalf("expected bash panel to collapse on final, got:\n%s", view)
	}
	if !strings.Contains(view, "line-1") || !strings.Contains(view, "line-2") {
		t.Fatalf("expected final bash output to remain in history, got:\n%s", view)
	}
}

func TestDelegatePanelShowsOnlyReasoningAndAssistant(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ DELEGATE {task=inspect}\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "DELEGATE", TaskID: "t-1", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "DELEGATE",
		TaskID: "t-1",
		CallID: "call-1",
		Stream: "reasoning",
		Chunk:  "thinking...\n",
	})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "DELEGATE",
		TaskID: "t-1",
		CallID: "call-1",
		Stream: "tool_result",
		Chunk:  "✓ LIST 10 entries\n",
	})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "DELEGATE",
		TaskID: "t-1",
		CallID: "call-1",
		Stream: "assistant",
		Chunk:  "still working...\n",
	})

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "thinking...") {
		t.Fatalf("expected delegate reasoning in panel, got:\n%s", view)
	}
	if !strings.Contains(view, "· thinking...") {
		t.Fatalf("expected delegate reasoning to use a lighter prefixed style, got:\n%s", view)
	}
	if strings.Contains(view, "✓ LIST 10 entries") {
		t.Fatalf("expected delegate tool result hidden in panel, got:\n%s", view)
	}
	if strings.Contains(view, "▸ LIST") {
		t.Fatalf("expected delegate tool trace hidden in panel, got:\n%s", view)
	}
	if !strings.Contains(view, "still working...") {
		t.Fatalf("expected delegate assistant output in panel, got:\n%s", view)
	}
}

func TestDelegatePanelSkipsFencedCodeBlockContent(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ DELEGATE {task=inspect}\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "DELEGATE", TaskID: "t-1", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "DELEGATE",
		TaskID: "t-1",
		CallID: "call-1",
		Stream: "assistant",
		Chunk:  "working...\n```text\n12\n-rw-r--r-- demo.html\n```\ndone.\n",
	})

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "working...") || !strings.Contains(view, "done.") {
		t.Fatalf("expected prose lines to remain visible, got:\n%s", view)
	}
	if strings.Contains(view, "demo.html") || strings.Contains(view, "\n12\n") {
		t.Fatalf("expected fenced command output hidden from delegate panel, got:\n%s", view)
	}
}

func TestDelegatePanelPrioritizesAssistantLinesOverReasoningNoise(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ DELEGATE {task=inspect}\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "DELEGATE", TaskID: "t-1", CallID: "call-1", Reset: true})
	for i := 1; i <= 5; i++ {
		_, _ = m.Update(tuievents.ToolStreamMsg{
			Tool:   "DELEGATE",
			TaskID: "t-1",
			CallID: "call-1",
			Stream: "reasoning",
			Chunk:  fmt.Sprintf("thinking-%d\n", i),
		})
	}
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "DELEGATE",
		TaskID: "t-1",
		CallID: "call-1",
		Stream: "assistant",
		Chunk:  "final visible update\n",
	})

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "final visible update") {
		t.Fatalf("expected assistant text to remain visible in delegate preview, got:\n%s", view)
	}
}

func TestViewAnchorsToolOutputBelowMatchingCallLines(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ BASH {command=first}\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "BASH", CallID: "call-1", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "BASH",
		CallID: "call-1",
		Stream: "stdout",
		Chunk:  "bash-line\n",
	})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ DELEGATE {task=second}\n"})
	_, _ = m.Update(tuievents.ToolStreamMsg{Tool: "DELEGATE", TaskID: "task-2", CallID: "call-2", Reset: true})
	_, _ = m.Update(tuievents.ToolStreamMsg{
		Tool:   "DELEGATE",
		TaskID: "task-2",
		CallID: "call-2",
		Stream: "assistant",
		Chunk:  "delegate-line\n",
	})

	view := ansi.Strip(m.View())
	bashIdx := strings.Index(view, "▸ BASH {command=first}")
	bashLineIdx := strings.Index(view, "bash-line")
	delegateCallIdx := strings.Index(view, "▸ DELEGATE {task=second}")
	delegateLineIdx := strings.Index(view, "delegate-line")
	if bashIdx < 0 || bashLineIdx < 0 || delegateCallIdx < 0 || delegateLineIdx < 0 {
		t.Fatalf("expected call lines and anchored outputs, got:\n%s", view)
	}
	if !(bashIdx < bashLineIdx && bashLineIdx < delegateCallIdx && delegateCallIdx < delegateLineIdx) {
		t.Fatalf("expected each tool output block below its own call line, got:\n%s", view)
	}
}

func TestViewShowsInputWhenNotRunning(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.running = false
	view := m.View()

	if !strings.Contains(view, ">") {
		t.Fatalf("expected '>' prompt in view, got:\n%s", view)
	}
}

func TestViewShowsStatusBar(t *testing.T) {
	m := NewModel(Config{
		Workspace: "/test/workspace",
	})
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	view := m.View()
	if !strings.Contains(view, "/test/workspace") {
		t.Fatalf("expected workspace in status bar, got:\n%s", view)
	}
}

func TestCtrlVPasteSetsAttachmentWithoutHint(t *testing.T) {
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		PasteClipboardImage: func() (int, string, error) {
			return 1, "1 image attached — type message and press enter", nil
		},
	})
	resizeModel(m)
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	if m.attachmentCount != 1 {
		t.Fatalf("expected attachment count 1, got %d", m.attachmentCount)
	}
	if strings.TrimSpace(m.buildHintLine()) != "" {
		t.Fatalf("expected no attachment hint line, got %q", m.buildHintLine())
	}
}

func TestAttachmentLabelHiddenInInputBar(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	m.attachmentCount = 1
	line := m.renderInputBar()
	if !strings.Contains(line, ">") {
		t.Fatalf("expected prompt, got %q", line)
	}
	if !strings.HasPrefix(line, strings.Repeat(" ", inputHorizontalInset)+">") {
		t.Fatalf("expected inset input prompt, got %q", line)
	}
	if strings.Contains(line, "[1 image]") || strings.Contains(line, "[1 images]") {
		t.Fatalf("expected attachment label hidden, got %q", line)
	}
}

func TestFooterLeftShowsModeOnly(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.cfg.ModeLabel = func() string { return "plan" }
	if got := m.footerLeftText(); got != "plan  shift+tab switch mode" {
		t.Fatalf("unexpected mode footer text %q", got)
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
	if got := m.footerLeftText(); got != "plan  shift+tab switch mode" {
		t.Fatalf("expected footer mode text preserved, got %q", got)
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

func TestBackspaceClearsAttachmentsWhenInputEmpty(t *testing.T) {
	cleared := 0
	m := NewModel(Config{
		ExecuteLine: noopExecute,
		PasteClipboardImage: func() (int, string, error) {
			return 2, "", nil
		},
		ClearAttachments: func() int {
			cleared++
			return 0
		},
	})
	resizeModel(m)
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
	if m.attachmentCount != 2 {
		t.Fatalf("expected attachment count 2, got %d", m.attachmentCount)
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.attachmentCount != 0 {
		t.Fatalf("expected attachment count cleared, got %d", m.attachmentCount)
	}
	if cleared != 1 {
		t.Fatalf("expected ClearAttachments called once, got %d", cleared)
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

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if !interrupted {
		t.Fatal("expected CancelRunning to be called")
	}
}

func TestEscPopsQueuedMessageBeforeInterruptWhileRunning(t *testing.T) {
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
	m.pendingQueue = []pendingPrompt{
		{execLine: "first", displayLine: "first"},
		{execLine: "second", displayLine: "second"},
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if interrupted {
		t.Fatal("did not expect interrupt while queue still has messages")
	}
	if len(m.pendingQueue) != 1 {
		t.Fatalf("expected one queued message after pop, got %d", len(m.pendingQueue))
	}
	if m.pendingQueue[0].execLine != "first" {
		t.Fatalf("expected newest message to be popped first, remaining=%+v", m.pendingQueue)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if interrupted {
		t.Fatal("did not expect interrupt when popping last queued message")
	}
	if len(m.pendingQueue) != 0 {
		t.Fatalf("expected queue to be empty, got %d", len(m.pendingQueue))
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !interrupted {
		t.Fatal("expected interrupt once queue is empty")
	}
}

func TestCtrlCRequiresDoublePressToQuitWhenIdle(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.quit {
		t.Fatal("expected first Ctrl+C not to quit")
	}
	if cmd != nil {
		t.Fatal("expected nil cmd on first Ctrl+C")
	}
	if !strings.Contains(m.hint, "again to quit") {
		t.Fatalf("expected double-press hint, got %q", m.hint)
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	if !m.quit {
		t.Fatal("expected second Ctrl+C to quit")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit command")
	}
}

func TestCtrlCClearsInputAndSavesDraftToHistory(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	typeRunes(m, "draft text")

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
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

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("expected no cmd when pressing Ctrl+C during running")
	}
	if !strings.Contains(strings.ToLower(m.hint), "esc") {
		t.Fatalf("expected hint to use esc, got %q", m.hint)
	}
}

func TestEnterQueuesMessageWhileRunningAndAutoDispatchesOnTaskResult(t *testing.T) {
	var called []string
	m := NewModel(Config{
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = append(called, strings.TrimSpace(line))
			return tuievents.TaskResultMsg{}
		},
	})
	resizeModel(m)

	m.running = true
	typeRunes(m, "queued message")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected no immediate command when queueing during running")
	}
	if len(m.pendingQueue) != 1 {
		t.Fatalf("expected 1 queued message, got %d", len(m.pendingQueue))
	}
	if got := m.textarea.Value(); got != "" {
		t.Fatalf("expected input cleared after queueing, got %q", got)
	}
	if !m.running {
		t.Fatal("expected model to remain running after queueing")
	}

	_, cmd = m.Update(tuievents.TaskResultMsg{})
	if cmd == nil {
		t.Fatal("expected auto-dispatch command after task result")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("expected non-nil command message")
	}
	if !findAndRunTaskResult(msg, m) {
		t.Fatal("expected TaskResultMsg in auto-dispatch command")
	}
	if len(called) != 1 || called[0] != "queued message" {
		t.Fatalf("expected queued message to be executed, got %+v", called)
	}
	if len(m.pendingQueue) != 0 {
		t.Fatalf("expected pending queue drained, got %d", len(m.pendingQueue))
	}
}

func TestEnterSlashWhileRunningDoesNotQueue(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.running = true
	typeRunes(m, "/help")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected no command for slash while running")
	}
	if len(m.pendingQueue) != 0 {
		t.Fatalf("expected no queued message for slash command, got %d", len(m.pendingQueue))
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
	if m.assistantBlock == nil {
		t.Fatal("expected partial assistant block before interrupt")
	}

	_, _ = m.Update(tuievents.TaskResultMsg{Interrupted: true})

	if m.assistantBlock != nil {
		t.Fatal("expected assistant block cleared after interrupt")
	}
	if strings.Contains(strings.Join(m.historyLines, "\n"), "partial answer") {
		t.Fatalf("expected partial answer removed after interrupt, got %#v", m.historyLines)
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
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = append(called, strings.TrimSpace(line))
			return tuievents.TaskResultMsg{}
		},
	})
	resizeModel(m)
	m.running = true
	m.slashArgActive = true
	m.slashArgCommand = ""

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !interrupted {
		t.Fatal("expected running task to be interrupted")
	}

	_, _ = m.Update(tuievents.TaskResultMsg{Interrupted: true})
	typeRunes(m, "follow-up")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

func noopExecute(line string) tuievents.TaskResultMsg {
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
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func typeAndEnter(m *Model, text string) {
	typeRunes(m, text)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
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

	if len(m.historyLines) == 0 {
		t.Fatal("expected historyLines to be non-empty")
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
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
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
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "✓ PATCH edited build.sh\n"})
	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
	if strings.Contains(joined, "\n·\n") {
		t.Fatalf("did not expect synthetic dot spacer between tool calls, got %q", joined)
	}
	if !strings.Contains(joined, "▸ READ build.sh\n") || !strings.Contains(joined, "▸ PATCH build.sh") {
		t.Fatalf("expected both tool calls preserved, got %q", joined)
	}
	if strings.Contains(joined, "PATCH build.sh\n\n✓ PATCH edited") {
		t.Fatalf("did not expect blank line between PATCH call and result, got %q", joined)
	}
}

func TestAssistantStreamAddsPrefixMarker(t *testing.T) {
	m := newTestModel()
	resizeModel(m)
	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: "hello", Final: true})
	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
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
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if !m.userScrolledUp {
		t.Fatal("expected userScrolledUp after pgup")
	}
	view := ansi.Strip(m.View())
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

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := m.textarea.Value(); got != "second" {
		t.Fatalf("expected history command on arrow up, got %q", got)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
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

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := m.textarea.Value(); got != "line1\nline2\nline3" {
		t.Fatalf("expected textarea content preserved on internal up nav, got %q", got)
	}
	if got := m.textarea.Line(); got != 1 {
		t.Fatalf("expected cursor to move within textarea, got line %d", got)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := m.textarea.Line(); got != 0 {
		t.Fatalf("expected cursor to reach first textarea line, got %d", got)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := m.textarea.Value(); got != "first" {
		t.Fatalf("expected history recall only after leaving first textarea line, got %q", got)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := m.textarea.Value(); got != "line1\nline2\nline3" {
		t.Fatalf("expected draft restored from history, got %q", got)
	}
	if got := m.textarea.Line(); got != 2 {
		t.Fatalf("expected restored draft cursor at last line, got %d", got)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
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
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "full_access  shift+tab switch mode") {
		t.Fatalf("expected mode footer in view, got:\n%s", view)
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

	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
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
	for _, l := range m.historyLines {
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

	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
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

	if len(m.historyLines) < 2 {
		t.Fatalf("expected assistant and tool lines; got %d lines", len(m.historyLines))
	}
	if got := strings.TrimSpace(ansi.Strip(m.historyLines[0])); got != "* drafted a plan" {
		t.Fatalf("unexpected first line %q", got)
	}
	if got := strings.TrimSpace(ansi.Strip(m.historyLines[1])); got != "▸ READ SKILL.md" {
		t.Fatalf("unexpected tool line %q", got)
	}
}

func TestSubmitLineAddsDividerBeforeUserMessage(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "* assistant reply\n"})
	m.hasLastRunDuration = true
	m.lastRunDuration = 1250 * time.Millisecond
	_, _ = m.submitLineWithDisplay("continue", "continue")

	if len(m.historyLines) < 3 {
		t.Fatalf("expected assistant, divider, user; got %d lines", len(m.historyLines))
	}
	if got := strings.TrimSpace(ansi.Strip(m.historyLines[len(m.historyLines)-2])); !strings.Contains(got, "1.2s") || !strings.Contains(got, "─") {
		t.Fatalf("expected duration divider before user message, got %q", got)
	}
	if got := strings.TrimSpace(ansi.Strip(m.historyLines[len(m.historyLines)-1])); got != "> continue" {
		t.Fatalf("unexpected user line %q", got)
	}
}

func TestErrorAlwaysAppended(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "error: first failure\n"})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "error: second failure\n"})

	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
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

	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
	// The second retry (2/3) should be visible (was the last before break).
	if !strings.Contains(joined, "2/3") {
		t.Fatalf("expected last retry before break visible, got %q", joined)
	}
	// The tool line and the new retry (3/3) should both be present.
	if !strings.Contains(joined, "READ file.txt") {
		t.Fatalf("expected tool line preserved, got %q", joined)
	}
	if !strings.Contains(joined, "3/3") {
		t.Fatalf("expected new retry after break visible, got %q", joined)
	}
}

func TestLogBlockGapBetweenNarrativeAndTool(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	_, _ = m.Update(tuievents.AssistantStreamMsg{Text: "hello world", Final: true})
	_, _ = m.Update(tuievents.LogChunkMsg{Chunk: "▸ READ file.txt\n"})

	joined := ansi.Strip(strings.Join(m.historyLines, "\n"))
	if strings.Contains(joined, "\n·\n") {
		t.Fatalf("did not expect synthetic dot spacer between assistant and tool, got %q", joined)
	}
	if !strings.Contains(joined, "* hello world\n") || !strings.Contains(joined, "▸ READ file.txt") {
		t.Fatalf("expected assistant and tool lines preserved, got %q", joined)
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
