package acpext

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/app/acpadapter"
	"github.com/OnslaughtSnail/caelis/internal/app/sessionsvc"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolfs "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/filesystem"
)

func newTestACPAdapterFactory(rt *runtime.Runtime, store session.Store, execRT toolexec.Runtime, workspaceRoot string, ag agent.Agent, llm model.LLM, extraTools []tool.Tool) AdapterFactory {
	return func(conn *internalacp.Conn) (internalacp.Adapter, error) {
		return acpadapter.New(acpadapter.Config{
			Runtime:           rt,
			Store:             store,
			Model:             llm,
			AppName:           "app",
			UserID:            "u",
			WorkspaceRoot:     workspaceRoot,
			BuildSystemPrompt: func(string) (string, error) { return "test self acp prompt", nil },
			NewAgent: func(bool, string, string, internalacp.AgentSessionConfig) (agent.Agent, error) {
				return ag, nil
			},
			NewSessionResources: func(_ context.Context, sessionID string, sessionCWD string, caps internalacp.ClientCapabilities, modeResolver func() string) (*internalacp.SessionResources, error) {
				execRuntimeACP := internalacp.NewRuntime(execRT, conn, sessionID, workspaceRoot, sessionCWD, caps, modeResolver)
				tools, err := tool.RebindRuntime(extraTools, execRuntimeACP)
				if err != nil {
					return nil, err
				}
				return &internalacp.SessionResources{
					Runtime: execRuntimeACP,
					Tools:   tools,
				}, nil
			},
			EnablePlan:      true,
			EnableSelfSpawn: true,
		})
	}
}

func TestSelfACPSpawnCreatesAttachableChildSession(t *testing.T) {
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
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &testACPSpawnLLM{}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime:         rt,
		Store:           store,
		AppName:         "app",
		UserID:          "u",
		WorkspaceCWD:    "/workspace",
		Execution:       execRT,
		EnableSelfSpawn: true,
		SubagentRunnerFactory: NewACPSubagentRunnerFactory(Config{
			Store:         store,
			WorkspaceCWD:  "/workspace",
			ClientRuntime: execRT,
			NewAdapter:    newTestACPAdapterFactory(rt, store, execRT, "/workspace", ag, llm, nil),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	runResult, err := svc.RunTurn(context.Background(), sessionsvc.RunTurnRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    "parent",
			WorkspaceKey: "wk",
		},
		Input: "delegate please",
		Agent: ag,
		Model: llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, runErr := range drainTurn(runResult.Handle.Events()) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if err := runResult.Handle.Close(); err != nil {
		t.Fatal(err)
	}

	delegations, err := svc.ListDelegations(context.Background(), sessionsvc.SessionRef{
		AppName:      "app",
		UserID:       "u",
		SessionID:    "parent",
		WorkspaceKey: "wk",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delegations) != 1 {
		t.Fatalf("expected 1 delegation, got %d", len(delegations))
	}
	loaded, err := svc.AttachSession(context.Background(), sessionsvc.AttachSessionRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    "parent",
			WorkspaceKey: "wk",
		},
		ChildSessionID: delegations[0].ChildSessionID,
		CWD:            "/workspace",
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SessionID == "parent" || loaded.SessionID == "" {
		t.Fatalf("expected attached child session, got %q", loaded.SessionID)
	}
	found := false
	for _, ev := range loaded.Events {
		if ev != nil && ev.Message.TextContent() == "child done" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected child assistant output in attached session, got %+v", loaded.Events)
	}
}

func TestSelfACPSpawnUsesProvidedAdapterFactory(t *testing.T) {
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
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	sentinel := errors.New("adapter factory invoked")
	var factoryCalled bool
	factory := NewACPSubagentRunnerFactory(Config{
		Store:         store,
		WorkspaceCWD:  "/workspace",
		ClientRuntime: execRT,
		NewAdapter: func(*internalacp.Conn) (internalacp.Adapter, error) {
			factoryCalled = true
			return nil, sentinel
		},
	})
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	runner := factory(rt, parent, runtime.RunRequest{
		AppName: "app",
		UserID:  "u",
		CoreTools: tool.CoreToolsConfig{
			Runtime: execRT,
		},
	})
	if runner == nil {
		t.Fatal("expected self ACP subagent runner")
	}
	_, err = runner.RunSubagent(context.Background(), agent.SubagentRunRequest{
		Agent: "self",
		Task:  "child task",
		Yield: time.Second,
	})
	if !factoryCalled {
		t.Fatal("expected self ACP runner to use provided adapter factory")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected adapter factory error %v, got %v", sentinel, err)
	}
}

func TestSelfACPSpawnBridgesLiveChildSessionUpdates(t *testing.T) {
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
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &testACPSpawnLLM{}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime:         rt,
		Store:           store,
		AppName:         "app",
		UserID:          "u",
		WorkspaceCWD:    "/workspace",
		Execution:       execRT,
		EnableSelfSpawn: true,
		SubagentRunnerFactory: NewACPSubagentRunnerFactory(Config{
			Store:         store,
			WorkspaceCWD:  "/workspace",
			ClientRuntime: execRT,
			NewAdapter:    newTestACPAdapterFactory(rt, store, execRT, "/workspace", ag, llm, nil),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	var (
		mu      sync.Mutex
		updates []sessionstream.Update
	)
	runCtx := sessionstream.WithStreamer(context.Background(), sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
	}))

	runResult, err := svc.RunTurn(runCtx, sessionsvc.RunTurnRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    "parent",
			WorkspaceKey: "wk",
		},
		Input: "delegate please",
		Agent: ag,
		Model: llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, runErr := range drainTurn(runResult.Handle.Events()) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if err := runResult.Handle.Close(); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	live := append([]sessionstream.Update(nil), updates...)
	mu.Unlock()

	var sawChildText bool
	for _, update := range live {
		if update.Event == nil || strings.TrimSpace(update.SessionID) == "" || update.SessionID == "parent" {
			continue
		}
		if strings.TrimSpace(update.Event.Message.TextContent()) != "child done" {
			continue
		}
		meta, ok := runtime.DelegationMetadataFromEvent(update.Event)
		if !ok || strings.TrimSpace(meta.ParentToolName) != tool.SpawnToolName {
			t.Fatalf("expected bridged child update to preserve SPAWN lineage, got %+v", update.Event.Meta)
		}
		sawChildText = true
		break
	}
	if !sawChildText {
		t.Fatalf("expected live bridged child assistant update, got %+v", live)
	}
}

func TestSelfACPSpawn_ListAndGlobUseChildWorkspace(t *testing.T) {
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
	t.Cleanup(func() { _ = toolexec.Close(execRT) })

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "one.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "two.md"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	listTool, err := toolfs.NewListWithRuntime(execRT)
	if err != nil {
		t.Fatal(err)
	}
	globTool, err := toolfs.NewGlobWithRuntime(execRT)
	if err != nil {
		t.Fatal(err)
	}
	ag, err := llmagent.New(llmagent.Config{Name: "test-agent"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &testACPListGlobLLM{}
	svc, err := sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime:         rt,
		Store:           store,
		AppName:         "app",
		UserID:          "u",
		WorkspaceCWD:    workspace,
		Execution:       execRT,
		Tools:           []tool.Tool{listTool, globTool},
		EnableSelfSpawn: true,
		SubagentRunnerFactory: NewACPSubagentRunnerFactory(Config{
			Store:         store,
			WorkspaceCWD:  workspace,
			ClientRuntime: execRT,
			NewAdapter:    newTestACPAdapterFactory(rt, store, execRT, workspace, ag, llm, []tool.Tool{listTool, globTool}),
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	runResult, err := svc.RunTurn(context.Background(), sessionsvc.RunTurnRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    "parent",
			WorkspaceKey: workspace,
		},
		Input: "delegate list glob",
		Agent: ag,
		Model: llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, runErr := range drainTurn(runResult.Handle.Events()) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if err := runResult.Handle.Close(); err != nil {
		t.Fatal(err)
	}

	delegations, err := svc.ListDelegations(context.Background(), sessionsvc.SessionRef{
		AppName:      "app",
		UserID:       "u",
		SessionID:    "parent",
		WorkspaceKey: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delegations) != 1 {
		t.Fatalf("expected 1 delegation, got %d", len(delegations))
	}
	loaded, err := svc.AttachSession(context.Background(), sessionsvc.AttachSessionRequest{
		SessionRef: sessionsvc.SessionRef{
			AppName:      "app",
			UserID:       "u",
			SessionID:    "parent",
			WorkspaceKey: workspace,
		},
		ChildSessionID: delegations[0].ChildSessionID,
		CWD:            workspace,
	})
	if err != nil {
		t.Fatal(err)
	}

	var sawList, sawGlob bool
	for _, ev := range loaded.Events {
		if ev == nil || ev.Message.ToolResponse == nil {
			continue
		}
		resp := ev.Message.ToolResponse
		switch resp.Name {
		case toolfs.ListToolName:
			if got := intValue(resp.Result["count"]); got < 2 {
				t.Fatalf("expected LIST count >= 2, got %#v", resp.Result)
			}
			if filepath.Clean(stringValue(resp.Result["path"])) != workspace {
				t.Fatalf("expected LIST path %q, got %#v", workspace, resp.Result)
			}
			sawList = true
		case toolfs.GlobToolName:
			if got := intValue(resp.Result["count"]); got != 1 {
				t.Fatalf("expected GLOB count 1, got %#v", resp.Result)
			}
			sawGlob = true
		}
	}
	if !sawList || !sawGlob {
		t.Fatalf("expected LIST and GLOB tool responses, sawList=%v sawGlob=%v", sawList, sawGlob)
	}
}

type testACPSpawnLLM struct{}

type testACPListGlobLLM struct{}

func (l *testACPSpawnLLM) Name() string { return "test-acp-spawn" }

func (l *testACPSpawnLLM) Generate(_ context.Context, req *model.Request) iter.Seq2[*model.Response, error] {
	return func(yield func(*model.Response, error) bool) {
		last := req.Messages[len(req.Messages)-1]
		switch last.Role {
		case model.RoleUser:
			switch last.TextContent() {
			case "delegate please":
				args, _ := json.Marshal(map[string]any{"task": "child task", "yield_seconds": 0})
				yield(&model.Response{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "call-spawn-1",
							Name: tool.SpawnToolName,
							Args: string(args),
						}},
					},
					TurnComplete: true,
				}, nil)
				return
			case "child task":
				yield(&model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "child done"},
					TurnComplete: true,
				}, nil)
				return
			}
		case model.RoleTool:
			if last.ToolResponse != nil && last.ToolResponse.Name == tool.SpawnToolName {
				yield(&model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "delegated complete"},
					TurnComplete: true,
				}, nil)
				return
			}
		}
		yield(&model.Response{
			Message:      model.Message{Role: model.RoleAssistant, Text: "fallback"},
			TurnComplete: true,
		}, nil)
	}
}

func (l *testACPListGlobLLM) Name() string { return "test-acp-list-glob" }

func (l *testACPListGlobLLM) Generate(_ context.Context, req *model.Request) iter.Seq2[*model.Response, error] {
	return func(yield func(*model.Response, error) bool) {
		last := req.Messages[len(req.Messages)-1]
		switch last.Role {
		case model.RoleUser:
			switch last.TextContent() {
			case "delegate list glob":
				args, _ := json.Marshal(map[string]any{"task": "child list glob", "yield_seconds": 0})
				yield(&model.Response{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "call-spawn-1",
							Name: tool.SpawnToolName,
							Args: string(args),
						}},
					},
					TurnComplete: true,
				}, nil)
				return
			case "child list glob":
				yield(&model.Response{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "call-list-1",
							Name: toolfs.ListToolName,
							Args: `{"path":"."}`,
						}},
					},
					TurnComplete: true,
				}, nil)
				return
			}
		case model.RoleTool:
			if last.ToolResponse == nil {
				break
			}
			switch last.ToolResponse.Name {
			case tool.SpawnToolName:
				yield(&model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "delegated complete"},
					TurnComplete: true,
				}, nil)
				return
			case toolfs.ListToolName:
				yield(&model.Response{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "call-glob-1",
							Name: toolfs.GlobToolName,
							Args: `{"pattern":"*.txt"}`,
						}},
					},
					TurnComplete: true,
				}, nil)
				return
			case toolfs.GlobToolName:
				yield(&model.Response{
					Message:      model.Message{Role: model.RoleAssistant, Text: "child done"},
					TurnComplete: true,
				}, nil)
				return
			}
		}
		yield(&model.Response{
			Message:      model.Message{Role: model.RoleAssistant, Text: "fallback"},
			TurnComplete: true,
		}, nil)
	}
}

func intValue(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func drainTurn(seq iter.Seq2[*session.Event, error]) []error {
	errs := make([]error, 0)
	for _, err := range seq {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
