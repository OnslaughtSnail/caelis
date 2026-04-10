package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/epochhandoff"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	coreacpmeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

func TestHandleAgentAddAndRemove(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	uiOut := &bytes.Buffer{}
	console := &cliConsole{
		configStore:   store,
		agentRegistry: appagents.NewRegistry(),
		out:           uiOut,
		ui:            newUI(uiOut, true, false),
	}

	if _, err := handleAgent(console, []string{"add", "codex"}); err != nil {
		t.Fatalf("add preset: %v", err)
	}
	if _, ok := console.configStore.data.Agents["codex"]; !ok {
		t.Fatal("expected codex in config after add")
	}
	if _, ok := console.agentRegistry.Lookup("codex"); !ok {
		t.Fatal("expected codex in runtime registry after add")
	}

	if _, err := handleAgent(console, []string{"rm", "codex"}); err != nil {
		t.Fatalf("remove preset: %v", err)
	}
	if _, ok := console.configStore.data.Agents["codex"]; ok {
		t.Fatal("expected codex removed from config")
	}
}

func TestHandleAgentUseBuiltinAddsAndSwitchesMainAgent(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	client := &stubMainACPClient{
		modes: &acpclient.SessionModeState{
			CurrentModeID: "default",
			AvailableModes: []acpclient.SessionMode{
				{ID: "default", Name: "Default"},
			},
		},
		configOptions: []acpclient.SessionConfigOption{
			{ID: acpConfigModel, Category: "model", CurrentValue: "gpt-5-mini"},
		},
	}
	prevHook := startMainACPClientHook
	startMainACPClientHook = func(context.Context, acpclient.Config) (mainACPClient, error) {
		return client, nil
	}
	defer func() { startMainACPClientHook = prevHook }()
	uiOut := &bytes.Buffer{}
	console := &cliConsole{
		baseCtx:       context.Background(),
		configStore:   store,
		agentRegistry: appagents.NewRegistry(),
		out:           uiOut,
		ui:            newUI(uiOut, true, false),
	}

	if _, err := handleAgent(console, []string{"use", "codex"}); err != nil {
		t.Fatalf("switch builtin main agent: %v", err)
	}
	if got := console.configStore.MainAgent(); got != "codex" {
		t.Fatalf("expected main agent codex, got %q", got)
	}
	if _, ok := console.configStore.data.Agents["codex"]; !ok {
		t.Fatal("expected codex to be added to config when switching")
	}
	if _, ok := console.agentRegistry.Lookup("codex"); !ok {
		t.Fatal("expected codex to be present in runtime registry after switch")
	}

	if _, err := handleAgent(console, []string{"use", "self"}); err != nil {
		t.Fatalf("switch back to self: %v", err)
	}
	if got := console.configStore.MainAgent(); got != "self" {
		t.Fatalf("expected main agent self, got %q", got)
	}
}

func TestHandleAgentUseRefreshesBTWAvailabilityInTUICommands(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	client := &stubMainACPClient{
		modes: &acpclient.SessionModeState{
			CurrentModeID: "default",
			AvailableModes: []acpclient.SessionMode{
				{ID: "default", Name: "Default"},
			},
		},
		configOptions: []acpclient.SessionConfigOption{
			{ID: acpConfigModel, Category: "model", CurrentValue: "gpt-5-mini"},
		},
	}
	prevHook := startMainACPClientHook
	startMainACPClientHook = func(context.Context, acpclient.Config) (mainACPClient, error) {
		return client, nil
	}
	defer func() { startMainACPClientHook = prevHook }()
	sender := &testSender{}
	uiOut := &bytes.Buffer{}
	console := &cliConsole{
		baseCtx:       context.Background(),
		configStore:   store,
		agentRegistry: appagents.NewRegistry(),
		tuiSender:     sender,
		out:           uiOut,
		ui:            newUI(uiOut, true, false),
		commands: map[string]slashCommand{
			"btw":    {Usage: "/btw <question>"},
			"help":   {Usage: "/help"},
			"status": {Usage: "/status"},
		},
	}

	if _, err := handleAgent(console, []string{"use", "codex"}); err != nil {
		t.Fatalf("switch builtin main agent: %v", err)
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
		t.Fatal("expected command refresh after switching main agent")
	}
	if slices.Contains(latest.Commands, "btw") {
		t.Fatalf("did not expect /btw after switching to ACP main agent, got %v", latest.Commands)
	}
}

func TestHandleAgentUseTUIUsesHintAndACPStatusInsteadOfPrinting(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	sessionStore := inmemory.New()
	root := &session.Session{AppName: "app", UserID: "u", ID: "sess-1"}
	if _, err := sessionStore.GetOrCreate(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	sender := &testSender{}
	uiOut := &bytes.Buffer{}
	client := &stubMainACPClient{
		modes: &acpclient.SessionModeState{
			CurrentModeID: "plan",
			AvailableModes: []acpclient.SessionMode{
				{ID: "default", Name: "Default"},
				{ID: "plan", Name: "Plan"},
			},
		},
		configOptions: []acpclient.SessionConfigOption{
			{
				ID:           acpConfigModel,
				Category:     "model",
				CurrentValue: "copilot/gpt-5",
				Options: []acpclient.SessionConfigSelectOption{
					{Value: "copilot/gpt-5", Name: "copilot/gpt-5"},
				},
			},
			{
				ID:           acpConfigReasoningEffort,
				Category:     "thought_level",
				CurrentValue: "high",
				Options: []acpclient.SessionConfigSelectOption{
					{Value: "high", Name: "High"},
				},
			},
		},
	}
	prevHook := startMainACPClientHook
	startMainACPClientHook = func(context.Context, acpclient.Config) (mainACPClient, error) {
		return client, nil
	}
	defer func() { startMainACPClientHook = prevHook }()

	console := &cliConsole{
		baseCtx:       t.Context(),
		appName:       "app",
		userID:        "u",
		sessionID:     root.ID,
		sessionStore:  sessionStore,
		configStore:   store,
		agentRegistry: appagents.NewRegistry(),
		tuiSender:     sender,
		out:           uiOut,
		ui:            newUI(uiOut, true, false),
		commands: map[string]slashCommand{
			"btw":  {Usage: "/btw <question>"},
			"help": {Usage: "/help"},
		},
	}

	if _, err := handleAgent(console, []string{"use", "codex"}); err != nil {
		t.Fatalf("switch builtin main agent: %v", err)
	}
	if uiOut.Len() != 0 {
		t.Fatalf("expected no direct output in TUI mode, got %q", uiOut.String())
	}

	var hint tuievents.SetHintMsg
	var status tuievents.SetStatusMsg
	var foundHint bool
	var foundStatus bool
	for _, raw := range sender.Snapshot() {
		switch msg := raw.(type) {
		case tuievents.SetHintMsg:
			hint = msg
			foundHint = true
		case tuievents.SetStatusMsg:
			status = msg
			foundStatus = true
		}
	}
	if !foundHint {
		t.Fatal("expected transient hint after switching main agent")
	}
	if hint.Hint != "main agent: codex" {
		t.Fatalf("unexpected hint %q", hint.Hint)
	}
	if hint.ClearAfter <= 0 {
		t.Fatalf("expected auto-clearing hint, got %s", hint.ClearAfter)
	}
	if !foundStatus {
		t.Fatal("expected status refresh after switching main agent")
	}
	if status.Model != "copilot/gpt-5 [high]" {
		t.Fatalf("expected ACP status model, got %q", status.Model)
	}
}

func TestHandleAgentUse_RestoresPersistedACPSettings(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: appConfig{
			MainAgent: "self",
			Agents: map[string]agentRecord{
				"copilot": {
					Command: "copilot",
					Args:    []string{"--acp", "--stdio"},
					ACP: &agentACPRecord{
						Model:           "gpt-5-mini",
						ReasoningEffort: "medium",
					},
				},
			},
		},
	}
	sessionStore := inmemory.New()
	root := &session.Session{AppName: "app", UserID: "u", ID: "sess-restore-pref"}
	if _, err := sessionStore.GetOrCreate(t.Context(), root); err != nil {
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
			{
				ID:           acpConfigModel,
				Category:     "model",
				CurrentValue: "gpt-4.1",
				Options: []acpclient.SessionConfigSelectOption{
					{Value: "gpt-4.1", Name: "GPT-4.1"},
					{Value: "gpt-5-mini", Name: "GPT-5 Mini"},
				},
			},
			{
				ID:           acpConfigReasoningEffort,
				Category:     "thought_level",
				CurrentValue: "low",
				Options: []acpclient.SessionConfigSelectOption{
					{Value: "low", Name: "Low"},
					{Value: "medium", Name: "Medium"},
				},
			},
		},
	}
	prevHook := startMainACPClientHook
	startMainACPClientHook = func(context.Context, acpclient.Config) (mainACPClient, error) {
		return client, nil
	}
	defer func() { startMainACPClientHook = prevHook }()

	out := bytes.NewBuffer(nil)
	console := &cliConsole{
		baseCtx:      t.Context(),
		appName:      "app",
		userID:       "u",
		sessionID:    root.ID,
		sessionStore: sessionStore,
		configStore:  store,
		agentRegistry: appagents.NewRegistry(
			appagents.Descriptor{ID: "copilot", Transport: appagents.TransportACP, Command: "copilot"},
		),
		out: out,
		ui:  newUI(out, true, false),
	}

	if _, err := handleAgent(console, []string{"use", "copilot"}); err != nil {
		t.Fatalf("switch to copilot: %v", err)
	}
	if len(client.setConfigCalls) < 2 {
		t.Fatalf("expected persisted ACP settings to be restored before probing, got %v", client.setConfigCalls)
	}
	if want := []string{"model=gpt-5-mini", "reasoning_effort=medium"}; !slices.Equal(client.setConfigCalls[:2], want) {
		t.Fatalf("expected persisted ACP settings restore prefix %v, got %v", want, client.setConfigCalls)
	}
}

func TestHandleAgentUseRejectsWhileRunActive(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	uiOut := &bytes.Buffer{}
	console := &cliConsole{
		configStore:   store,
		agentRegistry: appagents.NewRegistry(),
		out:           uiOut,
		ui:            newUI(uiOut, true, false),
		activeRunCancel: func() {
		},
		activeRunKind: runOccupancyMainSession,
	}

	if _, err := handleAgent(console, []string{"use", "self"}); err == nil {
		t.Fatal("expected switching main agent while a run is active to fail")
	}
}

func TestHandleAgentRemoveCurrentMainAgentFallsBackToSelf(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	client := &stubMainACPClient{
		modes: &acpclient.SessionModeState{
			CurrentModeID: "default",
			AvailableModes: []acpclient.SessionMode{
				{ID: "default", Name: "Default"},
			},
		},
		configOptions: []acpclient.SessionConfigOption{
			{ID: acpConfigModel, Category: "model", CurrentValue: "gpt-5-mini"},
		},
	}
	prevHook := startMainACPClientHook
	startMainACPClientHook = func(context.Context, acpclient.Config) (mainACPClient, error) {
		return client, nil
	}
	defer func() { startMainACPClientHook = prevHook }()
	uiOut := &bytes.Buffer{}
	console := &cliConsole{
		baseCtx:       context.Background(),
		configStore:   store,
		agentRegistry: appagents.NewRegistry(),
		out:           uiOut,
		ui:            newUI(uiOut, true, false),
	}

	if _, err := handleAgent(console, []string{"use", "codex"}); err != nil {
		t.Fatalf("switch builtin main agent: %v", err)
	}
	if _, err := handleAgent(console, []string{"rm", "codex"}); err != nil {
		t.Fatalf("remove codex: %v", err)
	}
	if got := console.configStore.MainAgent(); got != "self" {
		t.Fatalf("expected main agent to fall back to self after removal, got %q", got)
	}
}

func TestHandleAgentUse_ToSelfOnlyUpdatesConfig(t *testing.T) {
	t.Parallel()

	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	_ = store.SetMainAgent("copilot")

	sessionStore := inmemory.New()
	rootSession := &session.Session{AppName: "app", UserID: "u", ID: "sess-1"}
	if _, err := sessionStore.GetOrCreate(t.Context(), rootSession); err != nil {
		t.Fatal(err)
	}
	if err := sessionStore.AppendEvent(t.Context(), rootSession, &session.Event{
		ID:      "ev-1",
		Message: model.NewTextMessage(model.RoleUser, "继续修复切换问题"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := coreacpmeta.UpdateControllerEpoch(t.Context(), sessionStore, rootSession, func(coreacpmeta.ControllerEpoch) coreacpmeta.ControllerEpoch {
		return coreacpmeta.ControllerEpoch{
			EpochID:        "2",
			ControllerKind: coreacpmeta.ControllerKindACP,
			ControllerID:   "copilot",
		}
	}); err != nil {
		t.Fatal(err)
	}

	uiOut := &bytes.Buffer{}
	console := &cliConsole{
		baseCtx:       t.Context(),
		appName:       "app",
		userID:        "u",
		sessionID:     "sess-1",
		configStore:   store,
		agentRegistry: appagents.NewRegistry(),
		out:           uiOut,
		ui:            newUI(uiOut, true, false),
		sessionStore:  sessionStore,
	}

	if _, err := handleAgent(console, []string{"use", "self"}); err != nil {
		t.Fatalf("switch to self: %v", err)
	}

	epoch, err := coreacpmeta.ControllerEpochFromStore(t.Context(), sessionStore, rootSession)
	if err != nil {
		t.Fatal(err)
	}
	if epoch.ControllerKind != coreacpmeta.ControllerKindACP || epoch.ControllerID != "copilot" || epoch.EpochID != "2" {
		t.Fatalf("expected epoch unchanged after config-only switch: %+v", epoch)
	}

	checkpoints, err := epochhandoff.NewHandoffCoordinator(sessionStore).LoadCheckpointState(t.Context(), rootSession)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 0 {
		t.Fatalf("expected no checkpoint persisted during /agent use, got %d", len(checkpoints))
	}
}

func TestPrepareMainControllerTurn_SwitchToSelfClosesEpochAndBuildsPrelude(t *testing.T) {
	t.Parallel()

	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: defaultAppConfig(),
	}
	_ = store.SetMainAgent("self")

	sessionStore := inmemory.New()
	rootSession := &session.Session{AppName: "app", UserID: "u", ID: "sess-1"}
	if _, err := sessionStore.GetOrCreate(t.Context(), rootSession); err != nil {
		t.Fatal(err)
	}
	if err := sessionStore.AppendEvent(t.Context(), rootSession, &session.Event{
		ID:      "ev-1",
		Message: model.NewTextMessage(model.RoleUser, "继续修复切换问题"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := coreacpmeta.UpdateControllerEpoch(t.Context(), sessionStore, rootSession, func(coreacpmeta.ControllerEpoch) coreacpmeta.ControllerEpoch {
		return coreacpmeta.ControllerEpoch{
			EpochID:        "2",
			ControllerKind: coreacpmeta.ControllerKindACP,
			ControllerID:   "copilot",
		}
	}); err != nil {
		t.Fatal(err)
	}

	console := &cliConsole{
		baseCtx:      t.Context(),
		appName:      "app",
		userID:       "u",
		sessionID:    "sess-1",
		configStore:  store,
		sessionStore: sessionStore,
	}

	epochID, prelude, changed, err := console.prepareMainControllerTurn(t.Context(), coreacpmeta.ControllerKindSelf, "self")
	if err != nil {
		t.Fatalf("prepare controller turn: %v", err)
	}
	if !changed {
		t.Fatal("expected controller change to be detected")
	}
	if epochID != "3" {
		t.Fatalf("expected advanced epoch 3, got %q", epochID)
	}
	if len(prelude) != 1 {
		t.Fatalf("expected one invocation prelude, got %d", len(prelude))
	}
	if text := prelude[0].TextContent(); !strings.Contains(text, "[System-generated handoff checkpoint]") {
		t.Fatalf("expected structured handoff prelude, got %q", text)
	}
	checkpoints, err := epochhandoff.NewHandoffCoordinator(sessionStore).LoadCheckpointState(t.Context(), rootSession)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 1 {
		t.Fatalf("expected one persisted checkpoint, got %d", len(checkpoints))
	}
	if checkpoints[0].System.EpochID != "2" || checkpoints[0].System.ControllerID != "copilot" {
		t.Fatalf("unexpected persisted checkpoint: %+v", checkpoints[0].System)
	}
}

func TestPrepareMainControllerTurn_SkipsMissingSessionForFirstTurn(t *testing.T) {
	t.Parallel()

	sessionStore := inmemory.New()
	console := &cliConsole{
		baseCtx:      t.Context(),
		appName:      "app",
		userID:       "u",
		sessionID:    "sess-new",
		sessionStore: sessionStore,
	}

	epochID, prelude, changed, err := console.prepareMainControllerTurn(t.Context(), coreacpmeta.ControllerKindSelf, "self")
	if err != nil {
		t.Fatalf("prepare first controller turn: %v", err)
	}
	if changed {
		t.Fatal("expected missing-session first turn to skip controller transition")
	}
	if epochID != "" {
		t.Fatalf("expected no epoch for missing-session first turn, got %q", epochID)
	}
	if len(prelude) != 0 {
		t.Fatalf("expected no handoff prelude for first turn, got %d messages", len(prelude))
	}

	rootSession := &session.Session{AppName: "app", UserID: "u", ID: "sess-new"}
	epoch, err := coreacpmeta.ControllerEpochFromStore(t.Context(), sessionStore, rootSession)
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("expected session to remain absent before first run, got epoch=%+v err=%v", epoch, err)
	}
}
