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
		t.Fatal("expected slash-arg query")
	}
	if cmd != "model" || query != "gpt" {
		t.Fatalf("unexpected slash-arg parse: cmd=%q query=%q", cmd, query)
	}
	_, _, ok = slashArgQueryAtCursor([]rune("/model a b"), len([]rune("/model a b")))
	if ok {
		t.Fatal("did not expect slash-arg query for multiple args")
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

func TestSlashArgOverlayEnterExecutesSelectedCandidate(t *testing.T) {
	called := ""
	m := NewModel(Config{
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			called = line
			return tuievents.TaskResultMsg{}
		},
		Wizards: testWizards(),
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			switch {
			case command == "model":
				return []SlashArgCandidate{
					{Value: "deepseek/deepseek-chat", Display: "deepseek/deepseek-chat"},
					{Value: "xiaomi/mimo-v2-flash", Display: "xiaomi/mimo-v2-flash"},
				}, nil
			case strings.HasPrefix(command, "model-reasoning:"):
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
	if len(m.slashArgCandidates) != 2 {
		t.Fatalf("expected 2 slash-arg candidates, got %d", len(m.slashArgCandidates))
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.HasPrefix(m.slashArgCommand, "model-reasoning:") {
		t.Fatalf("expected model reasoning step, got %q", m.slashArgCommand)
	}
	if len(m.slashArgCandidates) != 2 {
		t.Fatalf("expected 2 reasoning candidates, got %d", len(m.slashArgCandidates))
	}
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command on slash-arg enter")
	}
	batchMsg := cmd()
	if batchMsg == nil {
		t.Fatal("expected non-nil batch message")
	}
	found := findAndRunTaskResult(batchMsg, m)
	if !found {
		t.Fatal("expected TaskResultMsg in batch")
	}
	if called != "/model xiaomi/mimo-v2-flash on" {
		t.Fatalf("expected '/model xiaomi/mimo-v2-flash on', got %q", called)
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
				{Value: "docker", Display: "docker"},
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
				return []SlashArgCandidate{{Value: "4096", Display: "4096"}}, nil
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
	if called != "/connect openai gpt-custom https://api.openai.com/v1 60 sk-test 128000 4096 -" {
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

func TestSpecialSlashCommandsDoNotAutoOpenArgPickerWhileTyping(t *testing.T) {
	m := NewModel(Config{
		Commands:    []string{"model", "resume", "connect"},
		ExecuteLine: noopExecute,
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			return []SlashArgCandidate{{Value: "x", Display: "x"}}, nil
		},
		ResumeComplete: func(query string, limit int) ([]ResumeCandidate, error) {
			return []ResumeCandidate{{SessionID: "s-1", Prompt: "p", Age: "1m"}}, nil
		},
	})
	resizeModel(m)
	typeRunes(m, "/model ")
	if len(m.slashArgCandidates) != 0 {
		t.Fatalf("expected no auto slash-arg picker, got %d", len(m.slashArgCandidates))
	}
	m.textarea.SetValue("")
	m.syncInputFromTextarea()
	typeRunes(m, "/resume ")
	if len(m.resumeCandidates) != 0 {
		t.Fatalf("expected no auto resume picker, got %d", len(m.resumeCandidates))
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
		Wizards:     testWizards(),
		SlashArgComplete: func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			if command != "model" {
				return nil, nil
			}
			return []SlashArgCandidate{
				{Value: "deepseek/deepseek-chat", Display: "deepseek/deepseek-chat"},
				{Value: "xiaomi/mimo-v2-flash", Display: "xiaomi/mimo-v2-flash"},
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
	_, _ = m.Update(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	_, cmd := m.Update(tea.MouseMsg{X: 5, Y: 0, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
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
	if !strings.Contains(joined, "· 第一段") {
		t.Fatalf("expected first reasoning paragraph, got %q", joined)
	}
	if !strings.Contains(joined, "  第二段") {
		t.Fatalf("expected second reasoning paragraph with reasoning prefix, got %q", joined)
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
	if strings.Contains(joined, "· thinking\n\n* final answer") {
		t.Fatalf("expected no blank gap between reasoning and assistant, got %q", joined)
	}
	if !strings.Contains(joined, "· thinking\n* final answer") {
		t.Fatalf("expected assistant immediately after reasoning, got %q", joined)
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
		Prompt:   "  [y] allow  [a] always  [N] deny: ",
		Response: respCh,
	})
	if m.activePrompt == nil {
		t.Fatal("expected active prompt")
	}
	if len(m.activePrompt.choices) != 3 {
		t.Fatalf("expected 3 approval choices, got %d", len(m.activePrompt.choices))
	}
	if m.activePrompt.choiceIndex != 2 {
		t.Fatalf("expected default selection at deny, got %d", m.activePrompt.choiceIndex)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "allow") || !strings.Contains(view, "always") || !strings.Contains(view, "deny") {
		t.Fatalf("expected approval list options in modal, got %q", view)
	}

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
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
	if !strings.Contains(view, "2 pending messages") {
		t.Fatalf("expected pending queue hint in running view, got:\n%s", view)
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
	if strings.Contains(line, "[1 image]") || strings.Contains(line, "[1 images]") {
		t.Fatalf("expected attachment label hidden, got %q", line)
	}
}

func TestHintAreaReservedHeightWithoutHintJitter(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	m.hint = ""
	withoutHint := m.bottomSectionHeight()
	withoutHintLine := m.renderHintArea()

	m.hint = "temporary hint"
	withHint := m.bottomSectionHeight()
	withHintLine := m.renderHintArea()

	if withoutHint != withHint {
		t.Fatalf("expected stable bottom height with/without hint, got %d vs %d", withoutHint, withHint)
	}
	if strings.Count(withoutHintLine, "\n") != 0 {
		t.Fatalf("expected reserved hint area to stay single-line when empty, got %q", withoutHintLine)
	}
	if strings.Count(withHintLine, "\n") != 0 {
		t.Fatalf("expected reserved hint area to stay single-line when populated, got %q", withHintLine)
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

func noopCancelRunning() bool { return true }

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
	if !strings.Contains(joined, "▸ READ build.sh\n\n▸ PATCH build.sh") {
		t.Fatalf("expected blank line between consecutive tool calls, got %q", joined)
	}
	if strings.Contains(joined, "▸ PATCH build.sh\n\n✓ PATCH edited build.sh") {
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

func TestHelpHintsNoCopyMode(t *testing.T) {
	m := newTestModel()
	resizeModel(m)

	hints := m.buildHelpHints()
	if !strings.Contains(hints, "pgup/pgdn: scroll") {
		t.Fatalf("expected scroll hints in help text, got %q", hints)
	}
	if strings.Contains(hints, "copy mode") {
		t.Fatalf("did not expect copy mode hints, got %q", hints)
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

func testModelWizard() WizardDef {
	return WizardDef{
		Command: "model",
		Steps: []WizardStepDef{
			{
				Key:       "alias",
				HintLabel: "/model",
				CompletionCommand: func(_ map[string]string) string {
					return "model"
				},
			},
			{
				Key:          "reasoning",
				HintLabel:    "/model reasoning",
				FreeformHint: "/model reasoning: type option and press enter",
				CompletionCommand: func(s map[string]string) string {
					return "model-reasoning:" + url.QueryEscape(strings.ToLower(strings.TrimSpace(s["alias"])))
				},
			},
		},
		BuildExecLine: func(s map[string]string) string {
			return "/model " + s["alias"] + " " + s["reasoning"]
		},
	}
}

func testWizards() []WizardDef {
	return []WizardDef{testConnectWizard(), testModelWizard()}
}
