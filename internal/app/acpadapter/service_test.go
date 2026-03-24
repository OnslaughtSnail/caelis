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
)

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
	created, err := svc.NewSession(ctx, internalacp.NewSessionRequest{
		CWD: "/workspace/project",
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}
	if created.CWD != "/workspace/project" {
		t.Fatalf("expected cwd to persist, got %q", created.CWD)
	}

	updated, err := svc.SetMode(ctx, internalacp.SetSessionModeRequest{
		SessionID: created.SessionID,
		ModeID:    "plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Modes == nil || updated.Modes.CurrentModeID != "plan" {
		t.Fatalf("expected plan mode, got %+v", updated.Modes)
	}

	updated, err = svc.SetConfigOption(ctx, internalacp.SetSessionConfigOptionRequest{
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

	loaded, err := svc.LoadSession(ctx, internalacp.LoadSessionRequest{
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
	created, err := svc.NewSession(ctx, internalacp.NewSessionRequest{
		CWD: "/workspace/project-a",
	}, internalacp.ClientCapabilities{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.LoadSession(ctx, internalacp.LoadSessionRequest{
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
	created, err := svc.NewSession(ctx, internalacp.NewSessionRequest{
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
	created, err := svc.NewSession(ctx, internalacp.NewSessionRequest{
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
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeFullControl})
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

	created, err := svc.NewSession(context.Background(), internalacp.NewSessionRequest{CWD: "/workspace/project"}, internalacp.ClientCapabilities{})
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

type testServiceConfig struct {
	llm          model.LLM
	listSessions internalacp.SessionListFactory
}

func newTestService(t *testing.T, cfg testServiceConfig) (*Service, func()) {
	t.Helper()

	store := inmemory.New()
	rt, err := runtime.New(runtime.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	execRT, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
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
		DefaultModeID: "default",
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
