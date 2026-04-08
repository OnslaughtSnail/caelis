package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

func TestAvailableCommandNames_IncludesConfiguredExternalAgents(t *testing.T) {
	console := &cliConsole{
		commands: map[string]slashCommand{
			"help":   {Usage: "/help"},
			"status": {Usage: "/status"},
		},
		agentRegistry: appagents.NewRegistry(
			appagents.Descriptor{ID: "gemini", Transport: appagents.TransportACP, Command: "gemini"},
			appagents.Descriptor{ID: "help", Transport: appagents.TransportACP, Command: "echo"},
			appagents.Descriptor{ID: "bad/id", Transport: appagents.TransportACP, Command: "echo"},
		),
	}

	got := console.availableCommandNames()
	if !slices.Contains(got, "gemini") {
		t.Fatalf("expected gemini dynamic slash command, got %v", got)
	}
	if slices.Contains(got, "bad/id") {
		t.Fatalf("did not expect invalid slash token, got %v", got)
	}
	if count := slices.Index(got, "help"); count < 0 {
		t.Fatalf("expected builtin help command, got %v", got)
	}
}

func TestDynamicSlashAgents_NilConsoleReturnsNil(t *testing.T) {
	var console *cliConsole
	if got := console.dynamicSlashAgents(); got != nil {
		t.Fatalf("expected nil dynamic slash agents for nil console, got %#v", got)
	}
}

func TestRunExternalAgentSlash_RequiresIdleMainSession(t *testing.T) {
	console := &cliConsole{}
	console.setActiveRunCancel(func() {})

	err := console.runExternalAgentSlashContext(t.Context(), appagents.Descriptor{
		ID:        "gemini",
		Transport: appagents.TransportACP,
		Command:   "gemini",
	}, "today weather")
	if err == nil || err.Error() != "/gemini is only available while the main session is idle" {
		t.Fatalf("expected main-session busy error, got %v", err)
	}
}

func TestExternalACPStartupContextUsesTimeoutForOpenClaw(t *testing.T) {
	ctx, cancel := externalACPStartupContext(context.Background(), "openclaw")
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected openclaw startup context to have a deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > externalACPStartupTimeout+time.Second {
		t.Fatalf("unexpected openclaw startup timeout %v", remaining)
	}
}

func TestExternalACPStartupContextLeavesOtherAgentsUnbounded(t *testing.T) {
	ctx, cancel := externalACPStartupContext(context.Background(), "codex")
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("did not expect non-openclaw startup context deadline")
	}
}

func TestRunPromptAndBTWRejectDuringExternalAgentRun(t *testing.T) {
	console := &cliConsole{}
	console.setActiveExternalRun(func() {})

	if err := console.runPromptWithAttachments("hello", nil); !errors.Is(err, errExternalAgentRunBusy) {
		t.Fatalf("expected prompt rejection, got %v", err)
	}
	if err := console.runBTW("status?", nil); !errors.Is(err, errExternalAgentRunBusy) {
		t.Fatalf("expected /btw rejection, got %v", err)
	}
}

func TestHandleAgentAddAndRemoveRefreshesCommandList(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	uiOut := &bytes.Buffer{}
	sender := &testSender{}
	console := &cliConsole{
		configStore:   store,
		agentRegistry: appagents.NewRegistry(),
		out:           uiOut,
		ui:            newUI(uiOut, true, false),
		tuiSender:     sender,
		commands: map[string]slashCommand{
			"help": {Usage: "/help"},
		},
	}

	if _, err := handleAgent(console, []string{"add", "gemini"}); err != nil {
		t.Fatalf("add preset: %v", err)
	}
	msgs := sender.Snapshot()
	if len(msgs) == 0 {
		t.Fatal("expected command refresh message after add")
	}
	added, ok := msgs[len(msgs)-1].(tuievents.SetCommandsMsg)
	if !ok {
		t.Fatalf("expected SetCommandsMsg after add, got %T", msgs[len(msgs)-1])
	}
	if !slices.Contains(added.Commands, "gemini") {
		t.Fatalf("expected gemini in refreshed commands, got %v", added.Commands)
	}

	if _, err := handleAgent(console, []string{"rm", "gemini"}); err != nil {
		t.Fatalf("remove preset: %v", err)
	}
	msgs = sender.Snapshot()
	removed, ok := msgs[len(msgs)-1].(tuievents.SetCommandsMsg)
	if !ok {
		t.Fatalf("expected SetCommandsMsg after remove, got %T", msgs[len(msgs)-1])
	}
	if slices.Contains(removed.Commands, "gemini") {
		t.Fatalf("did not expect gemini after removal, got %v", removed.Commands)
	}
}

func TestFormatExternalToolResult_DoesNotDuplicateToolName(t *testing.T) {
	got := formatExternalToolResult("SEARCHING", nil, map[string]any{}, "completed", false)
	if got != "completed" {
		t.Fatalf("expected compact completed summary, got %q", got)
	}
}

func TestFormatExternalToolStart_UsesACPQuerySummary(t *testing.T) {
	got := formatExternalToolStart("SEARCHING", map[string]any{
		"_acp_kind": "search",
		"query":     "Shanghai weather",
	})
	if got != `for "Shanghai weather"` {
		t.Fatalf("expected ACP query summary, got %q", got)
	}
}

func TestFormatExternalToolResult_ExtractsACPTextContent(t *testing.T) {
	got := formatExternalToolResult("VIEWING", nil, map[string]any{
		"content":         `{"type":"text","text":{"value":"hello from shared formatter"}}`,
		"detailedContent": `{"type":"text","text":{"value":"hello from shared formatter"}}`,
	}, "completed", false)
	if got != "hello from shared formatter" {
		t.Fatalf("expected shared ACP text summary, got %q", got)
	}
}

func TestShouldPersistExternalProjectionEvent_SkipsPartialNarrative(t *testing.T) {
	if shouldPersistExternalProjectionEvent(acpprojector.Projection{
		Event: &session.Event{
			Message: model.NewTextMessage(model.RoleAssistant, "partial"),
			Meta:    map[string]any{"partial": true},
		},
	}) {
		t.Fatal("expected partial narrative projection to skip persistence")
	}
	if !shouldPersistExternalProjectionEvent(acpprojector.Projection{
		Event: &session.Event{
			Message: model.NewTextMessage(model.RoleAssistant, "final"),
		},
	}) {
		t.Fatal("expected non-partial projection to persist")
	}
}

func TestMergeExternalNarrativeChunk_DeduplicatesCumulativeReplay(t *testing.T) {
	prefix := "我是 Gemini CLI，专注于软件工程任务的交互式 AI"
	full := prefix + " 代理。我以高级软件工程师的身份协助你进行代码分析。"
	if got := mergeExternalNarrativeChunk(prefix, full); got != full {
		t.Fatalf("expected cumulative replay to replace previous snapshot, got %q", got)
	}
}

func TestExternalParticipantUpdatesOnlyPersistRenderCache(t *testing.T) {
	store := inmemory.New()
	root := &session.Session{AppName: "app", UserID: "u", ID: "root"}
	if _, err := store.GetOrCreate(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	console := &cliConsole{
		baseCtx:      context.Background(),
		appName:      "app",
		userID:       "u",
		sessionID:    root.ID,
		sessionStore: store,
		tuiSender:    &testSender{},
	}
	turn := &externalAgentTurn{
		callID: "call-1",
		participant: externalParticipant{
			Alias:          "cole",
			AgentID:        "copilot",
			ChildSessionID: "child-1",
			DisplayLabel:   "cole(copilot)",
		},
		routeText: "/copilot 介绍一下你自己",
		routeKind: "slash_create",
		projector: acpprojector.NewLiveProjector(),
	}
	if err := console.initializeExternalParticipantProjectionTurn(context.Background(), turn); err != nil {
		t.Fatal(err)
	}
	if err := console.persistExternalParticipantRoute(context.Background(), turn); err != nil {
		t.Fatal(err)
	}

	console.forwardExternalAgentUpdate(context.Background(), turn, acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       json.RawMessage(`{"type":"text","text":"hello from copilot"}`),
		},
	})
	if err := console.updateExternalParticipantProjectionStatus(context.Background(), turn, "completed"); err != nil {
		t.Fatal(err)
	}

	events, err := store.ListEvents(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected only root route event in session log, got %d events", len(events))
	}
	if got := strings.TrimSpace(events[0].Message.TextContent()); got != turn.routeText {
		t.Fatalf("expected persisted route text %q, got %q", turn.routeText, got)
	}

	projections, err := console.acpProjectionStore().LoadEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projections) != 3 {
		t.Fatalf("expected projection log to contain turn start, assistant event and status, got %d", len(projections))
	}
	if projections[0].Kind != "turn_start" || projections[0].CallID != "call-1" {
		t.Fatalf("unexpected first projection event: %#v", projections[0])
	}
	if projections[1].Kind != "projection" || projections[1].DeltaText != "hello from copilot" {
		t.Fatalf("expected assistant projection event in log, got %#v", projections[1])
	}
	if projections[2].Kind != "status" || projections[2].Status != "completed" {
		t.Fatalf("expected completed status event in log, got %#v", projections[2])
	}
}

func TestRouteExternalParticipantContext_LoadsExistingSessionIntoNewTurn(t *testing.T) {
	store := inmemory.New()
	root := &session.Session{AppName: "app", UserID: "u", ID: "root"}
	if _, err := store.GetOrCreate(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	console := &cliConsole{
		baseCtx:      context.Background(),
		appName:      "app",
		userID:       "u",
		sessionID:    root.ID,
		sessionStore: store,
		workspace:    workspaceContext{Key: "wk", CWD: "/workspace"},
		tuiSender:    sender,
		agentRegistry: appagents.NewRegistry(
			appagents.Descriptor{ID: "copilot", Transport: appagents.TransportACP, Command: "copilot"},
		),
	}
	if err := console.registerExternalParticipant(context.Background(), externalParticipant{
		Alias:          "cole",
		AgentID:        "copilot",
		ChildSessionID: "child-1",
		DisplayLabel:   "cole(copilot)",
		Status:         "completed",
	}); err != nil {
		t.Fatal(err)
	}

	client := &stubExternalSlashACPClient{
		promptDone: make(chan struct{}),
	}
	prevStarter := startExternalSlashACPClientHook
	t.Cleanup(func() {
		startExternalSlashACPClientHook = prevStarter
	})
	startExternalSlashACPClientHook = func(_ *cliConsole, _ context.Context, runState *activeExternalAgentRun, desc appagents.Descriptor, turn *externalAgentTurn) (externalSlashACPClient, func(), error) {
		if got, want := strings.TrimSpace(desc.ID), "copilot"; got != want {
			t.Fatalf("unexpected descriptor %q want %q", got, want)
		}
		if turn == nil || turn.mode != externalAgentTurnLoad {
			t.Fatalf("expected load turn, got %+v", turn)
		}
		if got, want := strings.TrimSpace(turn.participant.ChildSessionID), "child-1"; got != want {
			t.Fatalf("unexpected child session %q want %q", got, want)
		}
		runState.setSessionID(turn.participant.ChildSessionID)
		return client, func() {}, nil
	}

	if err := console.routeExternalParticipantContext(context.Background(), "cole", "继续分析"); err != nil {
		t.Fatal(err)
	}
	waitForExternalPromptDone(t, client.promptDone)

	if client.newSessionCalls != 0 {
		t.Fatalf("did not expect new session call, got %d", client.newSessionCalls)
	}
	if client.loadSessionCalls != 1 || client.loadedSessionID != "child-1" {
		t.Fatalf("expected one load for child-1, got calls=%d session=%q", client.loadSessionCalls, client.loadedSessionID)
	}
	if client.promptCalls != 1 || client.promptSessionID != "child-1" || client.promptText != "继续分析" {
		t.Fatalf("unexpected prompt invocation calls=%d session=%q text=%q", client.promptCalls, client.promptSessionID, client.promptText)
	}

	events, err := store.ListEvents(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one route mirror event, got %d", len(events))
	}
	if got := strings.TrimSpace(events[0].Message.TextContent()); got != "@cole 继续分析" {
		t.Fatalf("unexpected route text %q", got)
	}

	projections, err := console.acpProjectionStore().LoadEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projections) < 3 {
		t.Fatalf("expected at least turn_start + prompting + completed status, got %#v", projections)
	}
	callID := strings.TrimSpace(projections[0].CallID)
	if projections[0].Kind != "turn_start" || callID == "" {
		t.Fatalf("expected first projection to open a new turn, got %#v", projections[0])
	}
	if projections[1].Kind != "status" || projections[1].Status != "prompting" || strings.TrimSpace(projections[1].CallID) != callID {
		t.Fatalf("expected prompting status for same turn, got %#v", projections[1])
	}
	if projections[len(projections)-1].Kind != "status" || projections[len(projections)-1].Status != "completed" || strings.TrimSpace(projections[len(projections)-1].CallID) != callID {
		t.Fatalf("expected completed status for same turn, got %#v", projections[len(projections)-1])
	}

	var (
		foundTurnStart bool
		foundPrompting bool
		foundCompleted bool
	)
	for _, raw := range sender.Snapshot() {
		switch msg := raw.(type) {
		case tuievents.ParticipantTurnStartMsg:
			if msg.SessionID == "child-1" {
				foundTurnStart = true
			}
		case tuievents.ParticipantStatusMsg:
			if msg.SessionID != "child-1" {
				continue
			}
			switch msg.State {
			case "prompting":
				foundPrompting = true
			case "completed":
				foundCompleted = true
			}
		}
	}
	if !foundTurnStart || !foundPrompting || !foundCompleted {
		t.Fatalf("expected new participant turn and terminal status messages, got %#v", sender.Snapshot())
	}
}

func TestFinalizeExternalTurnStreams_SkipsDuplicateFinalWhenStreamAlreadyObserved(t *testing.T) {
	sender := &testSender{}
	console := &cliConsole{tuiSender: sender}
	turn := &externalAgentTurn{
		participant: externalParticipant{
			Alias:          "evan",
			AgentID:        "copilot",
			ChildSessionID: "child-1",
		},
		projector: acpprojector.NewLiveProjector(),
	}
	turn.sawAssistantStream.Store(true)

	console.finalizeExternalTurnStreams(context.Background(), turn, false)

	for _, raw := range sender.Snapshot() {
		if msg, ok := raw.(tuievents.RawDeltaMsg); ok && msg.ScopeID == "child-1" && msg.Stream == "answer" {
			t.Fatalf("did not expect duplicate final answer after streamed participant output, got %#v", msg)
		}
	}
}

func TestHandleExternalPermissionRequest_FullAccessAutoSelectsAllowOnceWithoutPrompt(t *testing.T) {
	prompter := &stubLineEditor{lines: []string{"n"}}
	console := &cliConsole{
		sessionMode: sessionmode.FullMode,
		approver:    newTerminalApprover(prompter, io.Discard, newUI(io.Discard, true, false)),
	}
	console.approver.modeResolver = func() string { return console.sessionMode }

	resp, err := console.handleExternalPermissionRequest(context.Background(), acpclient.RequestPermissionRequest{
		SessionID: "child-copilot",
		Options: []acpclient.PermissionOption{
			{OptionID: "allow_always", Name: "Always allow", Kind: "allow_always"},
			{OptionID: "allow_once", Name: "Allow once", Kind: "allow_once"},
			{OptionID: "reject_once", Name: "Reject once", Kind: "reject_once"},
		},
	}, "copilot", &activeExternalAgentRun{sessionID: "child-copilot"})
	if err != nil {
		t.Fatalf("permission request: %v", err)
	}
	if got := selectedPermissionOptionID(t, resp); got != "allow_once" {
		t.Fatalf("expected allow_once, got %q", got)
	}
	if prompter.reads != 0 {
		t.Fatalf("expected no interactive prompt, got %d reads", prompter.reads)
	}
}

func TestHandleExternalPermissionRequest_FullAccessUnknownOptionsAutoSelectWithoutPrompt(t *testing.T) {
	prompter := &stubLineEditor{lines: []string{"Approve"}}
	console := &cliConsole{
		sessionMode: sessionmode.FullMode,
		approver:    newTerminalApprover(prompter, io.Discard, newUI(io.Discard, true, false)),
	}
	console.approver.modeResolver = func() string { return console.sessionMode }

	resp, err := console.handleExternalPermissionRequest(context.Background(), acpclient.RequestPermissionRequest{
		SessionID: "child-unknown",
		Options: []acpclient.PermissionOption{
			{OptionID: "approve", Name: "Approve"},
			{OptionID: "reject", Name: "Reject"},
		},
	}, "unknown-agent", &activeExternalAgentRun{sessionID: "child-unknown"})
	if err != nil {
		t.Fatalf("permission request: %v", err)
	}
	if got := selectedPermissionOptionID(t, resp); got != "approve" {
		t.Fatalf("expected auto-selected first option, got %q", got)
	}
	if prompter.reads != 0 {
		t.Fatalf("expected no interactive prompt, got %d reads", prompter.reads)
	}
}

func TestHandleExternalPermissionRequest_UsesServerProvidedOptionsInChoicePrompt(t *testing.T) {
	prompter := &stubChoiceEditor{response: "reject_custom"}
	console := &cliConsole{
		approver: newTerminalApprover(prompter, io.Discard, newUI(io.Discard, true, false)),
	}

	resp, err := console.handleExternalPermissionRequest(context.Background(), acpclient.RequestPermissionRequest{
		SessionID: "child-server-options",
		ToolCall: acpclient.ToolCallUpdate{
			Title:    strPtr("BASH create"),
			Kind:     strPtr("execute"),
			RawInput: map[string]any{"command": "echo hi"},
		},
		Options: []acpclient.PermissionOption{
			{OptionID: "allow_custom", Name: "Run now", Kind: "allow_once"},
			{OptionID: "reject_custom", Name: "Skip this", Kind: "reject_once"},
		},
	}, "custom-agent", &activeExternalAgentRun{sessionID: "child-server-options"})
	if err != nil {
		t.Fatalf("permission request: %v", err)
	}
	if got := selectedPermissionOptionID(t, resp); got != "reject_custom" {
		t.Fatalf("expected choice prompt selection to pass through server option id, got %q", got)
	}
	if got := prompter.lastDefaultChoice; got != "allow_custom" {
		t.Fatalf("expected default choice to use server allow option, got %q", got)
	}
	if got := prompter.lastChoices; len(got) != 2 {
		t.Fatalf("expected 2 server choices, got %#v", got)
	} else {
		if got[0].Label != "Run now" || got[0].Value != "allow_custom" {
			t.Fatalf("unexpected first choice %#v", got[0])
		}
		if got[1].Label != "Skip this" || got[1].Value != "reject_custom" {
			t.Fatalf("unexpected second choice %#v", got[1])
		}
	}
}

func TestHandleExternalPermissionRequest_FullAccessAutoSelectSkipsApprovalStatuses(t *testing.T) {
	prompter := &stubLineEditor{lines: []string{"approve", "approve"}}
	sender := &testSender{}
	console := &cliConsole{
		sessionMode: sessionmode.FullMode,
		approver:    newTerminalApprover(prompter, io.Discard, newUI(io.Discard, true, false)),
		tuiSender:   sender,
	}
	console.approver.modeResolver = func() string { return console.sessionMode }

	for _, childSessionID := range []string{"child-gemini-1", "child-gemini-2"} {
		_, err := console.handleExternalPermissionRequest(context.Background(), acpclient.RequestPermissionRequest{
			SessionID: childSessionID,
			ToolCall: acpclient.ToolCallUpdate{
				Title:    strPtr("BASH cat <<EOF"),
				Kind:     strPtr("execute"),
				RawInput: map[string]any{"command": "cat <<EOF\nhello\nEOF"},
			},
			Options: []acpclient.PermissionOption{
				{OptionID: "approve", Name: "Approve"},
				{OptionID: "reject", Name: "Reject"},
			},
		}, "unknown-agent", &activeExternalAgentRun{sessionID: childSessionID})
		if err != nil {
			t.Fatalf("permission request for %s: %v", childSessionID, err)
		}
	}

	var waiting, running []string
	for _, msg := range sender.Snapshot() {
		status, ok := msg.(tuievents.ParticipantStatusMsg)
		if !ok {
			continue
		}
		switch status.State {
		case "waiting_approval":
			waiting = append(waiting, status.SessionID)
		case "running":
			running = append(running, status.SessionID)
		}
	}
	if len(waiting) != 0 {
		t.Fatalf("expected no waiting_approval statuses on auto-select path, got %v", waiting)
	}
	if len(running) != 0 {
		t.Fatalf("expected no running reset statuses on auto-select path, got %v", running)
	}
	if prompter.reads != 0 {
		t.Fatalf("expected no interactive prompt on auto-select path, got %d reads", prompter.reads)
	}
}

func selectedPermissionOptionID(t *testing.T, resp acpclient.RequestPermissionResponse) string {
	t.Helper()
	var selected struct {
		OptionID string `json:"optionId"`
	}
	if err := json.Unmarshal(resp.Outcome, &selected); err != nil {
		t.Fatalf("unmarshal permission outcome: %v", err)
	}
	return selected.OptionID
}

func strPtr(value string) *string { return &value }

type stubExternalSlashACPClient struct {
	newSessionCalls  int
	loadSessionCalls int
	promptCalls      int
	loadedSessionID  string
	promptSessionID  string
	promptText       string
	promptDone       chan struct{}
}

func (c *stubExternalSlashACPClient) Initialize(context.Context) (acpclient.InitializeResponse, error) {
	return acpclient.InitializeResponse{}, nil
}

func (c *stubExternalSlashACPClient) NewSession(context.Context, string, map[string]any) (acpclient.NewSessionResponse, error) {
	c.newSessionCalls++
	return acpclient.NewSessionResponse{SessionID: "new-child"}, nil
}

func (c *stubExternalSlashACPClient) LoadSession(_ context.Context, sessionID string, _ string, _ map[string]any) (acpclient.LoadSessionResponse, error) {
	c.loadSessionCalls++
	c.loadedSessionID = strings.TrimSpace(sessionID)
	return acpclient.LoadSessionResponse{}, nil
}

func (c *stubExternalSlashACPClient) Prompt(_ context.Context, sessionID string, text string, _ map[string]any) (acpclient.PromptResponse, error) {
	c.promptCalls++
	c.promptSessionID = strings.TrimSpace(sessionID)
	c.promptText = strings.TrimSpace(text)
	if c.promptDone != nil {
		select {
		case <-c.promptDone:
		default:
			close(c.promptDone)
		}
	}
	return acpclient.PromptResponse{}, nil
}

func (c *stubExternalSlashACPClient) Cancel(context.Context, string) error { return nil }

func (c *stubExternalSlashACPClient) StderrTail(int) string { return "" }

func (c *stubExternalSlashACPClient) Close() error { return nil }

func waitForExternalPromptDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for external participant turn to finish")
	}
}
