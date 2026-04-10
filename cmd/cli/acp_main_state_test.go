package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	coreacpmeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

func TestEnsureMainACPControlSession_CreatesMissingRootSession(t *testing.T) {
	store := inmemory.New()
	client := &stubMainACPClient{
		modes: &acpclient.SessionModeState{
			CurrentModeID: "default",
			AvailableModes: []acpclient.SessionMode{
				{ID: "default", Name: "Default"},
			},
		},
		configOptions: []acpclient.SessionConfigOption{
			{ID: acpConfigModel, Category: "model", CurrentValue: "copilot/gpt-5"},
		},
	}
	prevHook := startMainACPClientHook
	startMainACPClientHook = func(context.Context, acpclient.Config) (mainACPClient, error) {
		return client, nil
	}
	defer func() { startMainACPClientHook = prevHook }()

	console := &cliConsole{
		baseCtx:      t.Context(),
		appName:      "app",
		userID:       "u",
		sessionID:    "sess-missing",
		sessionStore: store,
		configStore: &appConfigStore{
			path: filepath.Join(t.TempDir(), "config.json"),
			data: appConfig{
				MainAgent: "copilot",
				Agents: map[string]agentRecord{
					"copilot": {Command: "copilot", Args: []string{"--acp", "--stdio"}},
				},
			},
		},
	}

	sessionID, ok, err := console.currentSessionACPRemoteSession(t.Context(), "copilot")
	if err != nil {
		t.Fatalf("unexpected lookup error before init: %v", err)
	}
	if ok || sessionID != "" {
		t.Fatalf("expected no ACP session before init, got %q ok=%v", sessionID, ok)
	}

	clientConn, remoteSessionID, err := console.ensureMainACPControlSession(t.Context())
	if err != nil {
		t.Fatalf("ensure ACP control session: %v", err)
	}
	if clientConn == nil {
		t.Fatal("expected ACP client")
	}
	if remoteSessionID != "remote-main-1" {
		t.Fatalf("unexpected remote session id %q", remoteSessionID)
	}

	if _, err := store.SnapshotState(t.Context(), &session.Session{AppName: "app", UserID: "u", ID: "sess-missing"}); err != nil {
		t.Fatalf("expected root session to be created, got %v", err)
	}

	sessionID, ok, err = console.currentSessionACPRemoteSession(t.Context(), "copilot")
	if err != nil {
		t.Fatalf("lookup after init: %v", err)
	}
	if !ok || sessionID != "remote-main-1" {
		t.Fatalf("expected ACP session after init, got %q ok=%v", sessionID, ok)
	}
}

func TestCurrentSessionACPRemoteSession_MatchesSpecificAgent(t *testing.T) {
	store := inmemory.New()
	root := &session.Session{AppName: "app", UserID: "u", ID: "sess-1"}
	if _, err := store.GetOrCreate(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	if err := coreacpmeta.UpdateControllerSession(t.Context(), store, root, func(coreacpmeta.ControllerSession) coreacpmeta.ControllerSession {
		return coreacpmeta.ControllerSession{
			AgentID:   "copilot",
			SessionID: "remote-copilot-1",
		}
	}); err != nil {
		t.Fatal(err)
	}

	console := &cliConsole{
		baseCtx:      t.Context(),
		appName:      "app",
		userID:       "u",
		sessionID:    root.ID,
		sessionStore: store,
	}

	sessionID, ok, err := console.currentSessionACPRemoteSession(t.Context(), "copilot")
	if err != nil {
		t.Fatalf("copilot lookup: %v", err)
	}
	if !ok || sessionID != "remote-copilot-1" {
		t.Fatalf("unexpected copilot session lookup %q ok=%v", sessionID, ok)
	}

	sessionID, ok, err = console.currentSessionACPRemoteSession(t.Context(), "codex")
	if err != nil {
		t.Fatalf("codex lookup: %v", err)
	}
	if ok || sessionID != "" {
		t.Fatalf("expected no codex session, got %q ok=%v", sessionID, ok)
	}
}

func TestEnsureMainACPControlSession_BootstrapAvailableCommandsUpdateRefreshesCommandList(t *testing.T) {
	store := inmemory.New()
	sender := &testSender{}
	client := &stubMainACPClient{
		modes: &acpclient.SessionModeState{
			CurrentModeID: "default",
			AvailableModes: []acpclient.SessionMode{
				{ID: "default", Name: "Default"},
			},
		},
		configOptions: []acpclient.SessionConfigOption{
			{ID: acpConfigModel, Category: "model", CurrentValue: "copilot/gpt-5"},
		},
	}
	var onUpdate func(acpclient.UpdateEnvelope)
	prevHook := startMainACPClientHook
	startMainACPClientHook = func(_ context.Context, cfg acpclient.Config) (mainACPClient, error) {
		onUpdate = cfg.OnUpdate
		return client, nil
	}
	defer func() { startMainACPClientHook = prevHook }()

	console := &cliConsole{
		baseCtx:      t.Context(),
		appName:      "app",
		userID:       "u",
		sessionID:    "sess-bootstrap",
		sessionStore: store,
		tuiSender:    sender,
		commands: map[string]slashCommand{
			"help":    {Usage: "/help"},
			"status":  {Usage: "/status"},
			"new":     {Usage: "/new"},
			"model":   {Usage: "/model"},
			"agent":   {Usage: "/agent"},
			"quit":    {Usage: "/quit"},
			"exit":    {Usage: "/exit"},
			"compact": {Usage: "/compact"},
		},
		configStore: &appConfigStore{
			path: filepath.Join(t.TempDir(), "config.json"),
			data: appConfig{
				MainAgent: "copilot",
				Agents: map[string]agentRecord{
					"copilot": {Command: "copilot", Args: []string{"--acp", "--stdio"}},
				},
			},
		},
	}

	if _, _, err := console.ensureMainACPControlSession(t.Context()); err != nil {
		t.Fatalf("ensure ACP control session: %v", err)
	}
	if onUpdate == nil {
		t.Fatal("expected ACP update callback")
	}

	onUpdate(acpclient.UpdateEnvelope{
		SessionID: "remote-main-1",
		Update: acpclient.AvailableCommandsUpdate{
			AvailableCommands: []map[string]any{
				{"name": "compact", "description": "Compact remotely", "input": map[string]any{"hint": "/compact [note]"}},
			},
		},
	})

	if !slices.Contains(console.availableCommandNames(), "compact") {
		t.Fatalf("expected remote compact command after bootstrap update, got %v", console.availableCommandNames())
	}
	var latest tuievents.SetCommandsMsg
	var found bool
	for _, raw := range sender.Snapshot() {
		msg, ok := raw.(tuievents.SetCommandsMsg)
		if !ok {
			continue
		}
		latest = msg
		found = true
	}
	if !found {
		t.Fatal("expected SetCommandsMsg after ACP available commands update")
	}
	if !slices.Contains(latest.Commands, "compact") {
		t.Fatalf("expected compact in refreshed command list, got %v", latest.Commands)
	}
}

func TestApplyMainAgentSelectionState_ProbesACPModelProfilesFromServerConfig(t *testing.T) {
	store := inmemory.New()
	client := &stubMainACPClient{
		modes: &acpclient.SessionModeState{
			CurrentModeID: "https://agentclientprotocol.com/protocol/session-modes#agent",
			AvailableModes: []acpclient.SessionMode{
				{ID: "https://agentclientprotocol.com/protocol/session-modes#agent", Name: "Agent"},
			},
		},
		configOptions: []acpclient.SessionConfigOption{
			{
				ID:           acpConfigModel,
				Category:     "model",
				CurrentValue: "claude-sonnet-4.6",
				Options: []acpclient.SessionConfigSelectOption{
					{Value: "claude-sonnet-4.6", Name: "Claude Sonnet 4.6"},
					{Value: "gpt-5-mini", Name: "GPT-5 mini"},
					{Value: "gpt-4.1", Name: "GPT-4.1"},
				},
			},
			{
				ID:           acpConfigReasoningEffort,
				Category:     "thought_level",
				CurrentValue: "medium",
				Options: []acpclient.SessionConfigSelectOption{
					{Value: "low", Name: "Low"},
					{Value: "medium", Name: "Medium"},
					{Value: "high", Name: "High"},
				},
			},
		},
	}
	client.onSetConfig = func(configID string, value string, current []acpclient.SessionConfigOption) []acpclient.SessionConfigOption {
		if strings.EqualFold(configID, acpConfigModel) {
			next := []acpclient.SessionConfigOption{
				{
					ID:           acpConfigModel,
					Category:     "model",
					CurrentValue: value,
					Options: []acpclient.SessionConfigSelectOption{
						{Value: "claude-sonnet-4.6", Name: "Claude Sonnet 4.6"},
						{Value: "gpt-5-mini", Name: "GPT-5 mini"},
						{Value: "gpt-4.1", Name: "GPT-4.1"},
					},
				},
			}
			if strings.EqualFold(value, "gpt-5-mini") {
				next = append(next, acpclient.SessionConfigOption{
					ID:           acpConfigReasoningEffort,
					Category:     "thought_level",
					CurrentValue: "medium",
					Options: []acpclient.SessionConfigSelectOption{
						{Value: "low", Name: "Low"},
						{Value: "medium", Name: "Medium"},
						{Value: "high", Name: "High"},
					},
				})
			}
			return next
		}
		for i := range current {
			if strings.EqualFold(current[i].ID, configID) {
				current[i].CurrentValue = value
			}
		}
		return current
	}
	prevHook := startMainACPClientHook
	startMainACPClientHook = func(context.Context, acpclient.Config) (mainACPClient, error) {
		return client, nil
	}
	defer func() { startMainACPClientHook = prevHook }()

	console := &cliConsole{
		baseCtx:      t.Context(),
		appName:      "app",
		userID:       "u",
		sessionID:    "sess-probe-models",
		sessionStore: store,
		configStore: &appConfigStore{
			path: filepath.Join(t.TempDir(), "config.json"),
			data: appConfig{
				MainAgent: "copilot",
				Agents: map[string]agentRecord{
					"copilot": {Command: "copilot", Args: []string{"--acp", "--stdio"}},
				},
			},
		},
	}

	if err := console.applyMainAgentSelectionState(t.Context()); err != nil {
		t.Fatalf("apply main agent selection state: %v", err)
	}

	got := console.completeModelReasoningCandidates("gpt-5-mini", "", 10)
	if len(got) != 3 || got[0].Value != "low" || got[2].Value != "high" {
		t.Fatalf("unexpected probed reasoning candidates: %+v", got)
	}
	got = console.completeModelReasoningCandidates("gpt-4.1", "", 10)
	if len(got) != 0 {
		t.Fatalf("expected no reasoning candidates for gpt-4.1, got %+v", got)
	}
	if got := console.currentSessionModelAlias(); got != "claude-sonnet-4.6" {
		t.Fatalf("expected original ACP model restored after probing, got %q", got)
	}
}

func TestHandleSlashContext_ACPMainRemoteCommandPassesThroughToServer(t *testing.T) {
	store := inmemory.New()
	root := &session.Session{AppName: "app", UserID: "u", ID: "sess-remote-slash"}
	if _, err := store.GetOrCreate(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	client := &stubMainACPClient{
		modes: &acpclient.SessionModeState{
			CurrentModeID: "default",
			AvailableModes: []acpclient.SessionMode{
				{ID: "default", Name: "Default"},
			},
		},
		configOptions: []acpclient.SessionConfigOption{
			{ID: acpConfigModel, Category: "model", CurrentValue: "copilot/gpt-5"},
		},
	}
	console := &cliConsole{
		baseCtx:      t.Context(),
		appName:      "app",
		userID:       "u",
		sessionID:    root.ID,
		sessionStore: store,
		commands: map[string]slashCommand{
			"help":    {Usage: "/help"},
			"status":  {Usage: "/status"},
			"compact": {Usage: "/compact"},
		},
		configStore: &appConfigStore{
			path: filepath.Join(t.TempDir(), "config.json"),
			data: appConfig{
				MainAgent: "copilot",
				Agents: map[string]agentRecord{
					"copilot": {Command: "copilot", Args: []string{"--acp", "--stdio"}},
				},
			},
		},
		persistentMainACP: &persistentMainACPState{
			client:          client,
			agentID:         "copilot",
			remoteSessionID: "remote-main-1",
			modes:           cloneACPModeState(client.modes),
			configOptions:   cloneACPConfigOptions(client.configOptions),
			availableCmds: []acpMainAvailableCommand{
				{Name: "compact", Description: "Compact remotely"},
			},
		},
	}

	if _, err := console.handleSlashContext(t.Context(), "/compact summarize current work"); err != nil {
		t.Fatalf("handle ACP remote slash: %v", err)
	}
	if got := client.promptSessionID; got != "remote-main-1" {
		t.Fatalf("expected remote ACP session prompt, got %q", got)
	}
	if len(client.promptParts) == 0 {
		t.Fatal("expected ACP prompt parts")
	}
	lastPrompt := decodeMainACPTextBlock(t, client.promptParts[len(client.promptParts)-1])
	if lastPrompt != "/compact summarize current work" {
		t.Fatalf("expected remote slash prompt passthrough, got %q", lastPrompt)
	}
}

func TestPreparePromptSubmission_ACPMainSkipsLocalPromptSnapshot(t *testing.T) {
	console := &cliConsole{
		appName:   "app",
		sessionID: "sess-acp-prepare",
		workspace: workspaceContext{CWD: "/workspace"},
		configStore: &appConfigStore{data: appConfig{
			MainAgent: "copilot",
			Agents: map[string]agentRecord{
				"copilot": {Command: "copilot", Args: []string{"--acp", "--stdio"}},
			},
		}},
		promptSnapshots: map[string]string{},
	}

	prepared, err := console.preparePromptSubmission("hello from acp", nil)
	if err != nil {
		t.Fatalf("prepare ACP main submission: %v", err)
	}
	if prepared.mainACP == nil {
		t.Fatal("expected ACP main submission payload")
	}
	if prepared.agent != nil {
		t.Fatal("did not expect local agent for ACP main submission")
	}
	if len(console.promptSnapshots) != 0 {
		t.Fatalf("did not expect local prompt snapshot cache to be populated for ACP main: %+v", console.promptSnapshots)
	}
}
