package tuiapp

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	tuiadapterruntime "github.com/OnslaughtSnail/caelis/gateway/adapter/tui/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func TestSlashNewClearsHistoryBeforeNotice(t *testing.T) {
	driver := &bridgeTestDriver{
		status:     tuiadapterruntime.StatusSnapshot{Model: "gpt-4o", ModeLabel: "default"},
		newSession: sdksession.Session{SessionRef: sdksession.SessionRef{SessionID: "new-session"}},
	}
	var msgs []tea.Msg
	slashNew(driver, func(msg tea.Msg) { msgs = append(msgs, msg) })
	if len(msgs) < 2 {
		t.Fatalf("slashNew() emitted %d messages, want at least 2", len(msgs))
	}
	if _, ok := msgs[0].(ClearHistoryMsg); !ok {
		t.Fatalf("first msg = %#v, want ClearHistoryMsg", msgs[0])
	}
	if log, ok := msgs[1].(LogChunkMsg); !ok || !strings.Contains(log.Chunk, "new session") {
		t.Fatalf("second msg = %#v, want new session notice", msgs[1])
	}
}

func TestSlashHelpListsMinimalCoreCommands(t *testing.T) {
	var msgs []tea.Msg
	slashHelp(func(msg tea.Msg) { msgs = append(msgs, msg) })
	if len(msgs) != 1 {
		t.Fatalf("slashHelp() emitted %d messages, want 1", len(msgs))
	}
	log, ok := msgs[0].(LogChunkMsg)
	if !ok {
		t.Fatalf("slashHelp() msg = %#v, want LogChunkMsg", msgs[0])
	}
	for _, want := range []string{"/connect", "/model use|del <alias>", "/new", "/resume [session-id]"} {
		if !strings.Contains(log.Chunk, want) {
			t.Fatalf("slashHelp() chunk = %q, want substring %q", log.Chunk, want)
		}
	}
}

func TestSlashResumeClearsHistoryBeforeReplay(t *testing.T) {
	driver := &bridgeTestDriver{
		status:         tuiadapterruntime.StatusSnapshot{Model: "gpt-4o", ModeLabel: "default"},
		resumedSession: sdksession.Session{SessionRef: sdksession.SessionRef{SessionID: "resumed-session"}},
		replay: []appgateway.EventEnvelope{{
			Event: appgateway.Event{
				Kind:         appgateway.EventKindAssistantMessage,
				SessionEvent: &sdksession.Event{Text: "history reply"},
			},
		}},
	}
	var msgs []tea.Msg
	slashResume(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "resumed-session")
	if len(msgs) < 3 {
		t.Fatalf("slashResume() emitted %d messages, want at least 3", len(msgs))
	}
	if _, ok := msgs[0].(ClearHistoryMsg); !ok {
		t.Fatalf("first msg = %#v, want ClearHistoryMsg", msgs[0])
	}
	if log, ok := msgs[1].(LogChunkMsg); !ok || !strings.Contains(log.Chunk, "resumed session") {
		t.Fatalf("second msg = %#v, want resumed session notice", msgs[1])
	}
	var sawReplay bool
	for _, msg := range msgs {
		if env, ok := msg.(appgateway.EventEnvelope); ok {
			switch {
			case env.Event.Narrative != nil && env.Event.Narrative.Text == "history reply":
				sawReplay = true
			case env.Event.SessionEvent != nil && strings.TrimSpace(env.Event.SessionEvent.Text) == "history reply":
				sawReplay = true
			}
		}
	}
	if !sawReplay {
		t.Fatalf("slashResume() messages = %#v, want replayed gateway history", msgs)
	}
}

func TestExecuteLineViaDriverStreamsGatewayEventsDirectly(t *testing.T) {
	turn := &bridgeTestTurn{
		events: make(chan appgateway.EventEnvelope, 1),
	}
	turn.events <- appgateway.EventEnvelope{
		Event: appgateway.Event{
			Kind:       appgateway.EventKindAssistantMessage,
			SessionRef: sdksession.SessionRef{SessionID: "root-session"},
			Narrative: &appgateway.NarrativePayload{
				Role:  appgateway.NarrativeRoleAssistant,
				Text:  "direct gateway event",
				Final: true,
				Scope: appgateway.EventScopeMain,
			},
		},
	}
	close(turn.events)

	driver := &bridgeSubmitDriver{turn: turn}
	var msgs []tea.Msg
	result := executeLineViaDriver(driver, &ProgramSender{Send: func(msg tea.Msg) { msgs = append(msgs, msg) }}, Submission{Text: "hello"})
	if result.Err != nil {
		t.Fatalf("executeLineViaDriver() err = %v", result.Err)
	}
	if len(msgs) != 1 {
		t.Fatalf("executeLineViaDriver() emitted %d msgs, want 1", len(msgs))
	}
	if _, ok := msgs[0].(appgateway.EventEnvelope); !ok {
		t.Fatalf("first msg = %#v, want appgateway.EventEnvelope", msgs[0])
	}
}

func TestSlashResumeReplaysGatewayEventsDirectly(t *testing.T) {
	driver := &bridgeTestDriver{
		status:         tuiadapterruntime.StatusSnapshot{Model: "gpt-4o", ModeLabel: "default"},
		resumedSession: sdksession.Session{SessionRef: sdksession.SessionRef{SessionID: "resumed-session"}},
		replay: []appgateway.EventEnvelope{{
			Event: appgateway.Event{
				Kind:       appgateway.EventKindAssistantMessage,
				SessionRef: sdksession.SessionRef{SessionID: "root-session"},
				Narrative: &appgateway.NarrativePayload{
					Role:  appgateway.NarrativeRoleAssistant,
					Text:  "history reply",
					Final: true,
					Scope: appgateway.EventScopeMain,
				},
			},
		}},
	}
	var msgs []tea.Msg
	slashResume(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "resumed-session")
	var sawReplay bool
	for _, msg := range msgs {
		if env, ok := msg.(appgateway.EventEnvelope); ok && env.Event.Narrative != nil && env.Event.Narrative.Text == "history reply" {
			sawReplay = true
		}
	}
	if !sawReplay {
		t.Fatalf("slashResume() messages = %#v, want replayed gateway envelope", msgs)
	}
}

func TestSlashConnectCallsDriverAndUpdatesStatus(t *testing.T) {
	driver := &bridgeTestDriver{
		status:        tuiadapterruntime.StatusSnapshot{Model: "minimax/MiniMax-M1", ModeLabel: "default", Workspace: "/tmp/ws"},
		connectStatus: tuiadapterruntime.StatusSnapshot{Model: "minimax/MiniMax-M2", ModeLabel: "default", Workspace: "/tmp/ws"},
	}
	var msgs []tea.Msg
	slashConnect(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "minimax MiniMax-M2 - 60 sk-test 204800 8192 low,medium")
	if driver.connectCalls != 1 {
		t.Fatalf("connectCalls = %d, want 1", driver.connectCalls)
	}
	if got := driver.lastConnect.Provider; got != "minimax" {
		t.Fatalf("lastConnect.Provider = %q, want minimax", got)
	}
	if got := driver.lastConnect.Model; got != "MiniMax-M2" {
		t.Fatalf("lastConnect.Model = %q, want MiniMax-M2", got)
	}
	if got := driver.lastConnect.APIKey; got != "sk-test" {
		t.Fatalf("lastConnect.APIKey = %q, want sk-test", got)
	}
	if got := driver.lastConnect.ContextWindowTokens; got != 204800 {
		t.Fatalf("lastConnect.ContextWindowTokens = %d, want 204800", got)
	}
	if got := driver.lastConnect.MaxOutputTokens; got != 8192 {
		t.Fatalf("lastConnect.MaxOutputTokens = %d, want 8192", got)
	}
	if len(msgs) == 0 {
		t.Fatal("slashConnect() emitted no messages")
	}
}

func TestSlashModelUseCallsDriverAndUpdatesStatus(t *testing.T) {
	driver := &bridgeTestDriver{
		status:         tuiadapterruntime.StatusSnapshot{Model: "minimax/MiniMax-M1", ModeLabel: "default", Workspace: "/tmp/ws"},
		useModelStatus: tuiadapterruntime.StatusSnapshot{Model: "minimax/MiniMax-M2", ModeLabel: "default", Workspace: "/tmp/ws"},
	}
	var msgs []tea.Msg
	slashModel(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "use minimax/MiniMax-M2")
	if driver.useModelCalls != 1 {
		t.Fatalf("useModelCalls = %d, want 1", driver.useModelCalls)
	}
	if got := driver.lastModelAlias; got != "minimax/MiniMax-M2" {
		t.Fatalf("lastModelAlias = %q, want minimax/MiniMax-M2", got)
	}
	if len(msgs) == 0 {
		t.Fatal("slashModel(use) emitted no messages")
	}
}

func TestSlashModelDeleteCallsDriverAndRefreshesStatus(t *testing.T) {
	driver := &bridgeTestDriver{
		status: tuiadapterruntime.StatusSnapshot{Model: "minimax/MiniMax-M1", ModeLabel: "default", Workspace: "/tmp/ws"},
	}
	var msgs []tea.Msg
	slashModel(driver, func(msg tea.Msg) { msgs = append(msgs, msg) }, "del minimax/MiniMax-M1")
	if driver.deleteModelCalls != 1 {
		t.Fatalf("deleteModelCalls = %d, want 1", driver.deleteModelCalls)
	}
	if got := driver.lastDeletedAlias; got != "minimax/MiniMax-M1" {
		t.Fatalf("lastDeletedAlias = %q, want minimax/MiniMax-M1", got)
	}
	if len(msgs) == 0 {
		t.Fatal("slashModel(del) emitted no messages")
	}
}

type bridgeTestDriver struct {
	status           tuiadapterruntime.StatusSnapshot
	connectStatus    tuiadapterruntime.StatusSnapshot
	useModelStatus   tuiadapterruntime.StatusSnapshot
	newSession       sdksession.Session
	resumedSession   sdksession.Session
	replay           []appgateway.EventEnvelope
	connectCalls     int
	useModelCalls    int
	deleteModelCalls int
	lastConnect      tuiadapterruntime.ConnectConfig
	lastModelAlias   string
	lastDeletedAlias string
}

type bridgeTestTurn struct {
	events chan appgateway.EventEnvelope
}

func (t *bridgeTestTurn) HandleID() string { return "handle-1" }
func (t *bridgeTestTurn) RunID() string    { return "run-1" }
func (t *bridgeTestTurn) TurnID() string   { return "turn-1" }
func (t *bridgeTestTurn) SessionRef() sdksession.SessionRef {
	return sdksession.SessionRef{SessionID: "root-session"}
}
func (t *bridgeTestTurn) Events() <-chan appgateway.EventEnvelope { return t.events }
func (t *bridgeTestTurn) Submit(context.Context, appgateway.SubmitRequest) error {
	return nil
}
func (t *bridgeTestTurn) Cancel() bool { return false }
func (t *bridgeTestTurn) Close() error { return nil }

type bridgeSubmitDriver struct {
	turn tuiadapterruntime.Turn
}

func (d *bridgeSubmitDriver) Status(context.Context) (tuiadapterruntime.StatusSnapshot, error) {
	return tuiadapterruntime.StatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) WorkspaceDir() string { return "" }
func (d *bridgeSubmitDriver) Submit(context.Context, tuiadapterruntime.Submission) (tuiadapterruntime.Turn, error) {
	return d.turn, nil
}
func (d *bridgeSubmitDriver) Interrupt(context.Context) error { return nil }
func (d *bridgeSubmitDriver) NewSession(context.Context) (sdksession.Session, error) {
	return sdksession.Session{}, nil
}
func (d *bridgeSubmitDriver) ResumeSession(context.Context, string) (sdksession.Session, error) {
	return sdksession.Session{}, nil
}
func (d *bridgeSubmitDriver) ListSessions(context.Context, int) ([]tuiadapterruntime.ResumeCandidate, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) ReplayEvents(context.Context) ([]appgateway.EventEnvelope, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) Compact(context.Context, string) error { return nil }
func (d *bridgeSubmitDriver) Connect(context.Context, tuiadapterruntime.ConnectConfig) (tuiadapterruntime.StatusSnapshot, error) {
	return tuiadapterruntime.StatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) UseModel(context.Context, string) (tuiadapterruntime.StatusSnapshot, error) {
	return tuiadapterruntime.StatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) DeleteModel(context.Context, string) error { return nil }
func (d *bridgeSubmitDriver) CycleSessionMode(context.Context) (tuiadapterruntime.StatusSnapshot, error) {
	return tuiadapterruntime.StatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) SetSandboxBackend(context.Context, string) (tuiadapterruntime.StatusSnapshot, error) {
	return tuiadapterruntime.StatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) SetSandboxMode(context.Context, string) (tuiadapterruntime.StatusSnapshot, error) {
	return tuiadapterruntime.StatusSnapshot{}, nil
}
func (d *bridgeSubmitDriver) CompleteMention(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) CompleteFile(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) CompleteSkill(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) CompleteResume(context.Context, string, int) ([]tuiadapterruntime.ResumeCandidate, error) {
	return nil, nil
}
func (d *bridgeSubmitDriver) CompleteSlashArg(context.Context, string, string, int) ([]tuiadapterruntime.SlashArgCandidate, error) {
	return nil, nil
}

var _ tuiadapterruntime.Turn = (*bridgeTestTurn)(nil)
var _ tuiadapterruntime.Driver = (*bridgeSubmitDriver)(nil)

var _ = time.Time{}

func (d *bridgeTestDriver) Status(context.Context) (tuiadapterruntime.StatusSnapshot, error) {
	return d.status, nil
}
func (d *bridgeTestDriver) WorkspaceDir() string { return "" }
func (d *bridgeTestDriver) Submit(context.Context, tuiadapterruntime.Submission) (tuiadapterruntime.Turn, error) {
	return nil, nil
}
func (d *bridgeTestDriver) Interrupt(context.Context) error { return nil }
func (d *bridgeTestDriver) NewSession(context.Context) (sdksession.Session, error) {
	return d.newSession, nil
}
func (d *bridgeTestDriver) ResumeSession(context.Context, string) (sdksession.Session, error) {
	return d.resumedSession, nil
}
func (d *bridgeTestDriver) ListSessions(context.Context, int) ([]tuiadapterruntime.ResumeCandidate, error) {
	return nil, nil
}
func (d *bridgeTestDriver) ReplayEvents(context.Context) ([]appgateway.EventEnvelope, error) {
	return d.replay, nil
}
func (d *bridgeTestDriver) Compact(context.Context, string) error { return nil }
func (d *bridgeTestDriver) Connect(_ context.Context, cfg tuiadapterruntime.ConnectConfig) (tuiadapterruntime.StatusSnapshot, error) {
	d.connectCalls++
	d.lastConnect = cfg
	if d.connectStatus.Model != "" || d.connectStatus.Workspace != "" || d.connectStatus.ModeLabel != "" {
		return d.connectStatus, nil
	}
	return d.status, nil
}
func (d *bridgeTestDriver) UseModel(_ context.Context, alias string) (tuiadapterruntime.StatusSnapshot, error) {
	d.useModelCalls++
	d.lastModelAlias = alias
	if d.useModelStatus.Model != "" || d.useModelStatus.Workspace != "" || d.useModelStatus.ModeLabel != "" {
		return d.useModelStatus, nil
	}
	return d.status, nil
}
func (d *bridgeTestDriver) DeleteModel(_ context.Context, alias string) error {
	d.deleteModelCalls++
	d.lastDeletedAlias = alias
	return nil
}
func (d *bridgeTestDriver) CycleSessionMode(context.Context) (tuiadapterruntime.StatusSnapshot, error) {
	return d.status, nil
}
func (d *bridgeTestDriver) SetSandboxBackend(context.Context, string) (tuiadapterruntime.StatusSnapshot, error) {
	return d.status, nil
}
func (d *bridgeTestDriver) SetSandboxMode(context.Context, string) (tuiadapterruntime.StatusSnapshot, error) {
	return d.status, nil
}
func (d *bridgeTestDriver) CompleteMention(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (d *bridgeTestDriver) CompleteFile(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (d *bridgeTestDriver) CompleteSkill(context.Context, string, int) ([]string, error) {
	return nil, nil
}
func (d *bridgeTestDriver) CompleteResume(context.Context, string, int) ([]tuiadapterruntime.ResumeCandidate, error) {
	return nil, nil
}
func (d *bridgeTestDriver) CompleteSlashArg(context.Context, string, string, int) ([]tuiadapterruntime.SlashArgCandidate, error) {
	return nil, nil
}
