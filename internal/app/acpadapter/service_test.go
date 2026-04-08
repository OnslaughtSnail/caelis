package acpadapter

import (
	"context"
	"errors"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type noopExecRunner struct{}

func (noopExecRunner) Run(context.Context, toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}

func TestServiceNewLoadAndRestoreState(t *testing.T) {
	svc, cleanup := newTestService(t, testServiceConfig{
		llm: &scriptedLLM{
			calls: [][]*model.Response{
				{{Message: model.NewTextMessage(model.RoleAssistant, "hello")}},
			},
		},
	})
	defer cleanup()

	ctx := context.Background()
	created, err := svc.NewSession(ctx, internalacp.AdapterNewSessionRequest{
		CWD: "/workspace/project",
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	if created.CWD != "/workspace/project" {
		t.Fatalf("expected cwd to persist, got %q", created.CWD)
	}

	updated, err := svc.SetMode(ctx, internalacp.AdapterSetModeRequest{
		SessionID: created.SessionID,
		ModeID:    "plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Modes == nil || updated.Modes.CurrentModeID != "plan" {
		t.Fatalf("expected plan mode, got %+v", updated.Modes)
	}

	updated, err = svc.SetConfigOption(ctx, internalacp.AdapterSetConfigOptionRequest{
		SessionID: created.SessionID,
		ConfigID:  "model",
		Value:     "gpt-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := currentOptionValue(updated.ConfigOptions, "model"); got != "gpt-b" {
		t.Fatalf("expected model config to persist, got %q", got)
	}

	loaded, err := svc.LoadSession(ctx, internalacp.AdapterLoadSessionRequest{
		SessionID: created.SessionID,
		CWD:       "/workspace/project",
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Session.Modes == nil || loaded.Session.Modes.CurrentModeID != "plan" {
		t.Fatalf("expected restored plan mode, got %+v", loaded.Session.Modes)
	}
	if got := currentOptionValue(loaded.Session.ConfigOptions, "model"); got != "gpt-b" {
		t.Fatalf("expected restored model config, got %q", got)
	}
	if !hasAvailableCommand(loaded.Session.AvailableCommands, "status") {
		t.Fatalf("expected available commands to be restored, got %+v", loaded.Session.AvailableCommands)
	}
}

func TestServiceLoadSessionRejectsPersistedCWDMismatch(t *testing.T) {
	svc, cleanup := newTestService(t, testServiceConfig{})
	defer cleanup()

	ctx := context.Background()
	created, err := svc.NewSession(ctx, internalacp.AdapterNewSessionRequest{
		CWD: "/workspace/project-a",
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.LoadSession(ctx, internalacp.AdapterLoadSessionRequest{
		SessionID: created.SessionID,
		CWD:       "/workspace/project-b",
	}, internalacp.ClientCapabilities{})
	if err == nil || !strings.Contains(err.Error(), "persisted session cwd") {
		t.Fatalf("expected persisted cwd mismatch, got %v", err)
	}
}

func TestServiceListSessionsDelegates(t *testing.T) {
	svc, cleanup := newTestService(t, testServiceConfig{
		listSessions: func(context.Context, internalacp.SessionListRequest) (internalacp.SessionListResponse, error) {
			return internalacp.SessionListResponse{
				Sessions: []internalacp.SessionSummary{{
					SessionID: "sess-1",
					CWD:       "/workspace/project",
					Title:     "One",
				}},
				NextCursor: "next",
			}, nil
		},
	})
	defer cleanup()

	resp, err := svc.ListSessions(context.Background(), internalacp.SessionListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Sessions) != 1 || resp.Sessions[0].SessionID != "sess-1" || resp.NextCursor != "next" {
		t.Fatalf("unexpected list response: %+v", resp)
	}
}

func TestServiceStartPromptEmitsCanonicalEvents(t *testing.T) {
	llm := &scriptedLLM{
		calls: [][]*model.Response{
			{{Message: model.NewTextMessage(model.RoleAssistant, "hello from adapter")}},
		},
	}
	svc, cleanup := newTestService(t, testServiceConfig{llm: llm})
	defer cleanup()

	ctx := context.Background()
	created, err := svc.NewSession(ctx, internalacp.AdapterNewSessionRequest{
		CWD: "/workspace/project",
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.StartPrompt(ctx, internalacp.StartPromptRequest{
		SessionID: created.SessionID,
		InputText: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Handle == nil {
		t.Fatal("expected prompt handle")
	}
	defer func() { _ = result.Handle.Close() }()

	events, errs := drainPromptEvents(result.Handle.Events())
	if len(errs) > 0 {
		t.Fatalf("unexpected prompt errors: %v", errs)
	}
	if !containsAssistantText(events, "hello from adapter") {
		t.Fatalf("expected assistant event, got %+v", events)
	}
	if len(llm.reqs) == 0 {
		t.Fatal("expected model request to be recorded")
	}
}

func TestServiceCancelPromptStopsActiveRun(t *testing.T) {
	blocking := &blockingLLM{started: make(chan struct{})}
	svc, cleanup := newTestService(t, testServiceConfig{llm: blocking})
	defer cleanup()

	ctx := context.Background()
	created, err := svc.NewSession(ctx, internalacp.AdapterNewSessionRequest{
		CWD: "/workspace/project",
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.StartPrompt(ctx, internalacp.StartPromptRequest{
		SessionID: created.SessionID,
		InputText: "wait",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Handle == nil {
		t.Fatal("expected prompt handle")
	}
	defer func() { _ = result.Handle.Close() }()

	done := make(chan []error, 1)
	go func() {
		_, errs := drainPromptEvents(result.Handle.Events())
		done <- errs
	}()

	select {
	case <-blocking.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for blocking llm to start")
	}

	svc.CancelPrompt(created.SessionID)

	select {
	case errs := <-done:
		if len(errs) > 1 {
			t.Fatalf("unexpected cancellation errors: %v", errs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancelled prompt to finish")
	}
	sess, err := svc.session(created.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sess.activeHandle() != nil {
		t.Fatal("expected cancelled prompt to clear active handle")
	}
}

func TestServiceStartPromptFreezesSystemPromptPerLoadedSession(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		SandboxRunner:  noopExecRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = toolexec.Close(execRT) }()

	llm := &scriptedLLM{
		calls: [][]*model.Response{
			{{Message: model.NewTextMessage(model.RoleAssistant, "one")}},
			{{Message: model.NewTextMessage(model.RoleAssistant, "two")}},
		},
	}
	promptText := "prompt-one"
	var captured []string
	svc, err := New(Config{
		Runtime:           rt,
		Store:             store,
		Model:             llm,
		AppName:           "app",
		UserID:            "u",
		WorkspaceRoot:     "/workspace",
		SessionModes:      []internalacp.SessionMode{{ID: "default", Name: "Default"}},
		DefaultModeID:     "default",
		BuildSystemPrompt: func(string) (string, error) { return promptText, nil },
		NewSessionResources: func(context.Context, string, string, internalacp.ClientCapabilities, func() string) (*internalacp.SessionResources, error) {
			return &internalacp.SessionResources{Runtime: execRT}, nil
		},
		NewAgent: func(stream bool, _ string, systemPrompt string, _ internalacp.AgentSessionConfig) (agent.Agent, error) {
			captured = append(captured, systemPrompt)
			return llmagent.New(llmagent.Config{
				Name:              "test-agent",
				SystemPrompt:      systemPrompt,
				StreamModel:       stream,
				EmitPartialEvents: stream,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := svc.NewSession(context.Background(), internalacp.AdapterNewSessionRequest{CWD: "/workspace/project"}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.StartPrompt(context.Background(), internalacp.StartPromptRequest{
		SessionID: created.SessionID,
		InputText: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = drainPromptEvents(first.Handle.Events())
	_ = first.Handle.Close()

	promptText = "prompt-two"
	second, err := svc.StartPrompt(context.Background(), internalacp.StartPromptRequest{
		SessionID: created.SessionID,
		InputText: "second",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = drainPromptEvents(second.Handle.Events())
	_ = second.Handle.Close()

	if len(captured) != 2 || captured[0] != "prompt-one" || captured[1] != "prompt-one" {
		t.Fatalf("expected prompt frozen per loaded session, got %v", captured)
	}
}

func TestServiceSessionMetaRoundTripsThroughNewLoadAndPrompt(t *testing.T) {
	llm := &scriptedLLM{
		calls: [][]*model.Response{
			{{Message: model.NewTextMessage(model.RoleAssistant, "hello from adapter")}},
		},
	}
	svc, cleanup := newTestService(t, testServiceConfig{llm: llm})
	defer cleanup()

	ctx := context.Background()
	created, err := svc.NewSession(ctx, internalacp.AdapterNewSessionRequest{
		CWD: "/workspace/project",
		Meta: map[string]any{
			"caelis": map[string]any{
				"delegatedChild": true,
			},
		},
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	assertMeta := func(wantDelegated bool, wantTrace string, wantRequest string) {
		t.Helper()
		values, err := svc.store.SnapshotState(ctx, svc.sessionRef(created.SessionID))
		if err != nil {
			t.Fatal(err)
		}
		meta := anyMap(anyMap(values["acp"])["meta"])
		if got := internalacp.IsDelegatedChild(meta); got != wantDelegated {
			t.Fatalf("expected delegatedChild=%t, got %#v", wantDelegated, meta)
		}
		caelisMeta := anyMap(meta["caelis"])
		traceValue, _ := caelisMeta["trace"].(string)
		if got := strings.TrimSpace(traceValue); got != wantTrace {
			t.Fatalf("expected caelis.trace %q, got %#v", wantTrace, meta)
		}
		requestValue, _ := meta["request"].(string)
		if got := strings.TrimSpace(requestValue); got != wantRequest {
			t.Fatalf("expected request %q, got %#v", wantRequest, meta)
		}
	}

	assertMeta(true, "", "")

	if _, err := svc.LoadSession(ctx, internalacp.AdapterLoadSessionRequest{
		SessionID: created.SessionID,
		CWD:       "/workspace/project",
		Meta: map[string]any{
			"caelis": map[string]any{
				"trace": "load-1",
			},
		},
	}, internalacp.ClientCapabilities{}); err != nil {
		t.Fatal(err)
	}
	assertMeta(true, "load-1", "")

	result, err := svc.StartPrompt(ctx, internalacp.StartPromptRequest{
		SessionID: created.SessionID,
		InputText: "hi",
		Meta: map[string]any{
			"request": "prompt-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, errs := drainPromptEvents(result.Handle.Events())
	if len(errs) > 0 {
		t.Fatalf("unexpected prompt errors: %v", errs)
	}
	if err := result.Handle.Close(); err != nil {
		t.Fatal(err)
	}

	assertMeta(true, "load-1", "prompt-1")
}

func TestServiceNewSession_SeedsModelFromSessionMeta(t *testing.T) {
	ctx := context.Background()
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		SandboxRunner:  noopExecRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	var gotModel string
	svc, err := New(Config{
		Runtime:       rt,
		Store:         store,
		AppName:       "app",
		UserID:        "u",
		WorkspaceRoot: "/workspace",
		SessionModes: []internalacp.SessionMode{
			{ID: "default", Name: "Default"},
		},
		DefaultModeID: "default",
		SessionConfig: []internalacp.SessionConfigOptionTemplate{
			{
				ID:           "mode",
				Name:         "Mode",
				Category:     "mode",
				DefaultValue: "default",
				Options: []internalacp.SessionConfigSelectOption{
					{Value: "default", Name: "Default"},
				},
			},
			{
				ID:           "model",
				Name:         "Model",
				Category:     "model",
				DefaultValue: "",
				Options: []internalacp.SessionConfigSelectOption{
					{Value: "minimax/minimax-m2.7-highspeed", Name: "Minimax"},
				},
			},
		},
		NewModel: func(cfg internalacp.AgentSessionConfig) (model.LLM, error) {
			gotModel = strings.TrimSpace(cfg.ConfigValues["model"])
			return &scriptedLLM{calls: [][]*model.Response{{{Message: model.NewTextMessage(model.RoleAssistant, "ok")}}}}, nil
		},
		BuildSystemPrompt: func(string) (string, error) { return "test", nil },
		NewSessionResources: func(context.Context, string, string, internalacp.ClientCapabilities, func() string) (*internalacp.SessionResources, error) {
			return &internalacp.SessionResources{Runtime: execRT}, nil
		},
		NewAgent: func(stream bool, _ string, systemPrompt string, _ internalacp.AgentSessionConfig) (agent.Agent, error) {
			return llmagent.New(llmagent.Config{
				Name:              "test-agent",
				SystemPrompt:      systemPrompt,
				StreamModel:       stream,
				EmitPartialEvents: stream,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := svc.NewSession(ctx, internalacp.AdapterNewSessionRequest{
		CWD: "/workspace/project",
		Meta: map[string]any{
			"caelis": map[string]any{
				"modelAlias": "minimax/minimax-m2.7-highspeed",
			},
		},
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	if got := currentOptionValue(created.ConfigOptions, "model"); got != "minimax/minimax-m2.7-highspeed" {
		t.Fatalf("expected session model seeded from metadata, got %q", got)
	}

	result, err := svc.StartPrompt(ctx, internalacp.StartPromptRequest{
		SessionID: created.SessionID,
		InputText: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = result.Handle.Close() }()
	_, errs := drainPromptEvents(result.Handle.Events())
	if len(errs) > 0 {
		t.Fatalf("unexpected prompt errors: %v", errs)
	}
	if gotModel != "minimax/minimax-m2.7-highspeed" {
		t.Fatalf("expected NewModel to see metadata-derived model alias, got %q", gotModel)
	}
}

func TestServiceSessionServiceDisablesSpawnForDelegatedChildSessions(t *testing.T) {
	svc, cleanup := newTestService(t, testServiceConfig{})
	defer cleanup()

	ctx := context.Background()
	rootState, err := svc.NewSession(ctx, internalacp.AdapterNewSessionRequest{
		CWD: "/workspace/project",
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	rootSess, err := svc.session(rootState.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	rootSvc, err := svc.sessionService(rootSess)
	if err != nil {
		t.Fatal(err)
	}
	rootTools, err := rootSvc.VisibleTools()
	if err != nil {
		t.Fatal(err)
	}
	if !toolListContains(rootTools, tool.SpawnToolName) {
		t.Fatalf("expected top-level ACP session to expose %q, got %+v", tool.SpawnToolName, toolNames(rootTools))
	}

	childState, err := svc.NewSession(ctx, internalacp.AdapterNewSessionRequest{
		CWD: "/workspace/project",
		Meta: map[string]any{
			"caelis": map[string]any{
				"delegatedChild": true,
			},
		},
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	childSess, err := svc.session(childState.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	childSvc, err := svc.sessionService(childSess)
	if err != nil {
		t.Fatal(err)
	}
	childTools, err := childSvc.VisibleTools()
	if err != nil {
		t.Fatal(err)
	}
	if toolListContains(childTools, tool.SpawnToolName) {
		t.Fatalf("expected delegated child ACP session to hide %q, got %+v", tool.SpawnToolName, toolNames(childTools))
	}
}

func TestServiceDelegatedChildPromptRemovesSpawnGuidance(t *testing.T) {
	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		SandboxRunner:  noopExecRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = toolexec.Close(execRT) }()

	llm := &scriptedLLM{
		calls: [][]*model.Response{
			{{Message: model.NewTextMessage(model.RoleAssistant, "child done")}},
		},
	}
	var capturedPrompt string
	svc, err := New(Config{
		Runtime:         rt,
		Store:           store,
		Model:           llm,
		AppName:         "app",
		UserID:          "u",
		WorkspaceRoot:   "/workspace",
		DefaultModeID:   "default",
		SessionModes:    []internalacp.SessionMode{{ID: "default", Name: "Default"}},
		EnableSelfSpawn: true,
		BuildSystemPrompt: func(string) (string, error) {
			return strings.Join([]string{
				"## Capability Guidance",
				"",
				"- Tool families: use READ/SEARCH/GLOB/LIST to inspect, WRITE/PATCH for targeted file changes, BASH for shell work, TASK for async follow-up, and SPAWN for delegated child sessions.",
				"- Delegation: keep critical-path decisions in the current session and use child sessions for bounded side work or specialization.",
				"",
				"## Agent Delegation",
				"",
				"- Use SPAWN only for bounded delegated work or specialization.",
			}, "\n"), nil
		},
		NewSessionResources: func(context.Context, string, string, internalacp.ClientCapabilities, func() string) (*internalacp.SessionResources, error) {
			return &internalacp.SessionResources{Runtime: execRT}, nil
		},
		NewAgent: func(stream bool, _ string, systemPrompt string, _ internalacp.AgentSessionConfig) (agent.Agent, error) {
			capturedPrompt = systemPrompt
			return llmagent.New(llmagent.Config{
				Name:              "test-agent",
				SystemPrompt:      systemPrompt,
				StreamModel:       stream,
				EmitPartialEvents: stream,
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := svc.NewSession(context.Background(), internalacp.AdapterNewSessionRequest{
		CWD: "/workspace/project",
		Meta: map[string]any{
			"caelis": map[string]any{
				"delegatedChild": true,
			},
		},
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.StartPrompt(context.Background(), internalacp.StartPromptRequest{
		SessionID: created.SessionID,
		InputText: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = result.Handle.Close() }()
	_, errs := drainPromptEvents(result.Handle.Events())
	if len(errs) > 0 {
		t.Fatalf("unexpected prompt errors: %v", errs)
	}
	if strings.Contains(capturedPrompt, "SPAWN for delegated child sessions") {
		t.Fatalf("expected delegated child prompt to drop SPAWN tool guidance, got %q", capturedPrompt)
	}
	if strings.Contains(capturedPrompt, "## Agent Delegation") {
		t.Fatalf("expected delegated child prompt to drop agent delegation section, got %q", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "cannot call SPAWN") {
		t.Fatalf("expected delegated child prompt to explain SPAWN is unavailable, got %q", capturedPrompt)
	}
}

type testServiceConfig struct {
	llm          model.LLM
	listSessions internalacp.SessionListFactory
}

func toolListContains(tools []tool.Tool, name string) bool {
	for _, one := range tools {
		if one != nil && strings.EqualFold(one.Name(), name) {
			return true
		}
	}
	return false
}

func toolNames(tools []tool.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, one := range tools {
		if one == nil {
			continue
		}
		out = append(out, one.Name())
	}
	return out
}

func newTestService(t *testing.T, cfg testServiceConfig) (*Service, func()) {
	t.Helper()

	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		SandboxRunner:  noopExecRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}

	llm := cfg.llm
	if llm == nil {
		llm = &scriptedLLM{
			calls: [][]*model.Response{
				{{Message: model.NewTextMessage(model.RoleAssistant, "ok")}},
			},
		}
	}

	svc, err := New(Config{
		Runtime:       rt,
		Store:         store,
		Model:         llm,
		AppName:       "app",
		UserID:        "u",
		WorkspaceRoot: "/workspace",
		SessionModes: []internalacp.SessionMode{
			{ID: "default", Name: "Default"},
			{ID: "plan", Name: "Plan"},
		},
		DefaultModeID:   "default",
		EnableSelfSpawn: true,
		SessionConfig: []internalacp.SessionConfigOptionTemplate{
			{
				ID:           "mode",
				Name:         "Mode",
				Category:     "mode",
				DefaultValue: "default",
				Options: []internalacp.SessionConfigSelectOption{
					{Value: "default", Name: "Default"},
					{Value: "plan", Name: "Plan"},
				},
			},
			{
				ID:           "model",
				Name:         "Model",
				Category:     "model",
				DefaultValue: "gpt-a",
				Options: []internalacp.SessionConfigSelectOption{
					{Value: "gpt-a", Name: "GPT A"},
					{Value: "gpt-b", Name: "GPT B"},
				},
			},
		},
		BuildSystemPrompt: func(string) (string, error) { return "test", nil },
		NewSessionResources: func(context.Context, string, string, internalacp.ClientCapabilities, func() string) (*internalacp.SessionResources, error) {
			return &internalacp.SessionResources{Runtime: execRT}, nil
		},
		NewAgent: func(stream bool, _ string, systemPrompt string, _ internalacp.AgentSessionConfig) (agent.Agent, error) {
			if strings.TrimSpace(systemPrompt) == "" {
				systemPrompt = "test"
			}
			return llmagent.New(llmagent.Config{
				Name:              "test-agent",
				SystemPrompt:      systemPrompt,
				StreamModel:       stream,
				EmitPartialEvents: stream,
			})
		},
		AvailableCommands: func(cfg internalacp.AgentSessionConfig) []internalacp.AvailableCommand {
			if cfg.ModeID == "plan" {
				return []internalacp.AvailableCommand{{Name: "status"}}
			}
			return internalacp.DefaultAvailableCommands()
		},
		ListSessions: cfg.listSessions,
	})
	if err != nil {
		_ = toolexec.Close(execRT)
		t.Fatal(err)
	}

	return svc, func() {
		_ = toolexec.Close(execRT)
	}
}

type scriptedLLM struct {
	mu    sync.Mutex
	calls [][]*model.Response
	reqs  []*model.Request
}

func (s *scriptedLLM) Name() string { return "scripted" }

func (s *scriptedLLM) Generate(_ context.Context, req *model.Request) iter.Seq2[*model.StreamEvent, error] {
	s.mu.Lock()
	var batch []*model.Response
	if len(s.calls) > 0 {
		batch = s.calls[0]
		s.calls = s.calls[1:]
	}
	if req != nil {
		clone := *req
		clone.Messages = append([]model.Message(nil), req.Messages...)
		s.reqs = append(s.reqs, &clone)
	}
	s.mu.Unlock()
	return func(yield func(*model.StreamEvent, error) bool) {
		for _, one := range batch {
			if one != nil && !one.TurnComplete {
				clone := *one
				clone.TurnComplete = true
				one = &clone
			}
			if !yield(model.StreamEventFromResponse(one), nil) {
				return
			}
		}
	}
}

type blockingLLM struct {
	started chan struct{}
	once    sync.Once
}

func (b *blockingLLM) Name() string { return "blocking" }

func (b *blockingLLM) Generate(ctx context.Context, _ *model.Request) iter.Seq2[*model.StreamEvent, error] {
	return func(yield func(*model.StreamEvent, error) bool) {
		b.once.Do(func() { close(b.started) })
		<-ctx.Done()
		yield(nil, ctx.Err())
	}
}

func drainPromptEvents(seq iter.Seq2[*session.Event, error]) ([]*session.Event, []error) {
	events := make([]*session.Event, 0)
	errs := make([]error, 0)
	for ev, err := range seq {
		if ev != nil {
			events = append(events, ev)
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			errs = append(errs, err)
		}
		if err != nil && errors.Is(err, context.Canceled) {
			errs = append(errs, err)
		}
	}
	return events, errs
}

func containsAssistantText(events []*session.Event, want string) bool {
	for _, ev := range events {
		if ev != nil && ev.Message.Role == model.RoleAssistant && ev.Message.TextContent() == want {
			return true
		}
	}
	return false
}

func currentOptionValue(options []internalacp.SessionConfigOption, id string) string {
	for _, item := range options {
		if item.ID == id {
			return item.CurrentValue
		}
	}
	return ""
}

func hasAvailableCommand(cmds []internalacp.AvailableCommand, name string) bool {
	for _, item := range cmds {
		if item.Name == name {
			return true
		}
	}
	return false
}
