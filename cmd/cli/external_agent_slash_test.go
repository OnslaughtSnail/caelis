package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
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

func TestHandleExternalPermissionRequest_FullAccessAutoAllowsOnceWithoutPrompt(t *testing.T) {
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

func TestHandleExternalPermissionRequest_FullAccessFallbackPromptsUser(t *testing.T) {
	prompter := &stubLineEditor{lines: []string{"y"}}
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
		t.Fatalf("expected prompted approval to choose approve, got %q", got)
	}
	if prompter.reads != 1 {
		t.Fatalf("expected one interactive prompt, got %d reads", prompter.reads)
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
