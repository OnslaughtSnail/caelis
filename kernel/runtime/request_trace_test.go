package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/filestore"
	taskinmemory "github.com/OnslaughtSnail/caelis/kernel/task/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type traceEchoTool struct{}

func (t traceEchoTool) Name() string        { return "TRACE_ECHO" }
func (t traceEchoTool) Description() string { return "echoes one snippet" }
func (t traceEchoTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{Name: t.Name(), Parameters: map[string]any{"type": "object"}}
}
func (t traceEchoTool) Run(context.Context, map[string]any) (map[string]any, error) {
	return map[string]any{"ok": true, "snippet": "tool result"}, nil
}

type traceAsyncRunner struct {
	mu          sync.Mutex
	started     bool
	statusCalls int
}

type traceSeqResult struct {
	resp *model.Response
	err  error
}

type traceStreamingLLM struct {
	name string
	run  func(*model.Request) []traceSeqResult
}

func (l *traceStreamingLLM) Name() string { return l.name }

func (l *traceStreamingLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.StreamEvent, error] {
	_ = ctx
	return func(yield func(*model.StreamEvent, error) bool) {
		for _, item := range l.run(req) {
			if item.err != nil {
				if !yield(nil, item.err) {
					return
				}
				continue
			}
			if item.resp != nil && !item.resp.TurnComplete {
				// Emit non-final responses as PartDelta events
				text := item.resp.Message.TextContent()
				if text != "" {
					evt := &model.StreamEvent{
						Type: model.StreamEventPartDelta,
						PartDelta: &model.PartDelta{
							Kind:      model.PartKindText,
							TextDelta: text,
						},
					}
					if !yield(evt, nil) {
						return
					}
					continue
				}
				reasoning := item.resp.Message.ReasoningText()
				if reasoning != "" {
					evt := &model.StreamEvent{
						Type: model.StreamEventPartDelta,
						PartDelta: &model.PartDelta{
							Kind:      model.PartKindReasoning,
							TextDelta: reasoning,
						},
					}
					if !yield(evt, nil) {
						return
					}
					continue
				}
			}
			if item.resp != nil {
				if item.resp.Model == "" {
					item.resp.Model = l.name
				}
				if item.resp.Provider == "" {
					item.resp.Provider = "test-provider"
				}
			}
			if !yield(model.StreamEventFromResponse(item.resp), item.err) {
				return
			}
		}
	}
}

func (r *traceAsyncRunner) Run(context.Context, toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}

func (r *traceAsyncRunner) StartAsync(context.Context, toolexec.CommandRequest) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = true
	return "proc-1", nil
}

func (r *traceAsyncRunner) WriteInput(string, []byte) error { return nil }

func (r *traceAsyncRunner) ReadOutput(string, int64, int64) ([]byte, []byte, int64, int64, error) {
	return []byte("done\n"), nil, int64(len("done\n")), 0, nil
}

func (r *traceAsyncRunner) GetSessionStatus(string) (toolexec.SessionStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statusCalls++
	if r.statusCalls == 1 {
		return toolexec.SessionStatus{State: toolexec.SessionStateRunning}, nil
	}
	return toolexec.SessionStatus{State: toolexec.SessionStateCompleted, ExitCode: 0}, nil
}

func (r *traceAsyncRunner) WaitSession(context.Context, string, time.Duration) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{Stdout: "done\n", ExitCode: 0}, nil
}

func (r *traceAsyncRunner) TerminateSession(string) error        { return nil }
func (r *traceAsyncRunner) ListSessions() []toolexec.SessionInfo { return nil }

type traceTaskRuntime struct {
	host toolexec.AsyncCommandRunner
}

func (r traceTaskRuntime) PermissionMode() toolexec.PermissionMode {
	return toolexec.PermissionModeDefault
}

func (r traceTaskRuntime) SandboxType() string                   { return "" }
func (r traceTaskRuntime) SandboxPolicy() toolexec.SandboxPolicy { return toolexec.SandboxPolicy{} }
func (r traceTaskRuntime) FallbackToHost() bool                  { return false }
func (r traceTaskRuntime) FallbackReason() string                { return "" }
func (r traceTaskRuntime) FileSystem() toolexec.FileSystem       { return nil }
func (r traceTaskRuntime) HostRunner() toolexec.CommandRunner    { return r.host }
func (r traceTaskRuntime) SandboxRunner() toolexec.CommandRunner { return nil }
func (r traceTaskRuntime) DecideRoute(string, toolexec.SandboxPermission) toolexec.CommandDecision {
	return toolexec.CommandDecision{Route: toolexec.ExecutionRouteHost}
}

type traceSpawnRunner struct {
	runtime *Runtime
	appName string
	userID  string
}

func (r *traceSpawnRunner) RunSubagent(ctx context.Context, req agent.SubagentRunRequest) (agent.SubagentRunResult, error) {
	child := &session.Session{AppName: r.appName, UserID: r.userID, ID: "child-1"}
	if _, err := r.runtime.store.GetOrCreate(ctx, child); err != nil {
		return agent.SubagentRunResult{}, err
	}
	if err := r.runtime.store.AppendEvent(ctx, child, lifecycleEvent(child, RunLifecycleStatusRunning, "run", nil)); err != nil {
		return agent.SubagentRunResult{}, err
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = r.runtime.store.AppendEvent(context.Background(), child, &session.Event{
			ID:      "child-assistant",
			Time:    time.Now(),
			Message: model.NewTextMessage(model.RoleAssistant, "child final output"),
		})
		_ = r.runtime.store.AppendEvent(context.Background(), child, lifecycleEvent(child, RunLifecycleStatusCompleted, "run", nil))
	}()
	return agent.SubagentRunResult{
		SessionID:    child.ID,
		DelegationID: "deleg-1",
		Agent:        "self",
		State:        "running",
		Running:      true,
		Yielded:      true,
		Timeout:      time.Minute,
	}, nil
}

func (r *traceSpawnRunner) InspectSubagent(ctx context.Context, sessionID string) (agent.SubagentRunResult, error) {
	return r.runtimeSubagentInspect(ctx, sessionID)
}

func (r *traceSpawnRunner) runtimeSubagentInspect(ctx context.Context, sessionID string) (agent.SubagentRunResult, error) {
	events, err := r.runtime.SessionEvents(ctx, SessionEventsRequest{
		AppName:          r.appName,
		UserID:           r.userID,
		SessionID:        sessionID,
		IncludeLifecycle: false,
	})
	if err != nil {
		return agent.SubagentRunResult{}, err
	}
	return agent.SubagentRunResult{
		SessionID:   sessionID,
		Agent:       "self",
		State:       string(RunLifecycleStatusCompleted),
		Running:     false,
		Assistant:   FinalAssistantText(events),
		LogSnapshot: subagentPreviewFromEvents(events),
	}, nil
}

func TestRuntimeRequestTraceMatchesPersistedToolContext(t *testing.T) {
	t.Setenv(model.RequestTraceEnvVar, "1")
	store := newTraceFileStore(t)
	rt, err := New(Config{Store: store, TaskStore: taskinmemory.New()})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := llmagent.New(llmagent.Config{Name: "trace-agent"})
	if err != nil {
		t.Fatal(err)
	}
	execRT := newTraceExecRuntime(t)
	var calls int
	llm := &scriptedRuntimeLLM{
		name: "trace-runtime-llm",
		run: func(req *model.Request) (*model.Response, error) {
			calls++
			switch calls {
			case 1:
				args, _ := json.Marshal(map[string]any{"text": "payload"})
				return &model.Response{
					Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
						ID:   "call-echo",
						Name: "TRACE_ECHO",
						Args: string(args),
					}}, ""),
					TurnComplete: true,
				}, nil
			case 2:
				return &model.Response{
					Message:      model.NewTextMessage(model.RoleAssistant, "done"),
					TurnComplete: true,
				}, nil
			default:
				return nil, fmt.Errorf("unexpected request %d", calls)
			}
		},
	}
	for _, runErr := range runEvents(t, rt, context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "trace-tool",
		Input:     "do it",
		Agent:     ag,
		Model:     llm,
		Tools:     []tool.Tool{traceEchoTool{}},
		CoreTools: tool.CoreToolsConfig{Runtime: execRT},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	records := readTraceRecords(t, store, &session.Session{AppName: "app", UserID: "u", ID: "trace-tool"})
	if len(records) != 2 {
		t.Fatalf("expected 2 request trace records, got %d", len(records))
	}
	events, err := store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "trace-tool"})
	if err != nil {
		t.Fatal(err)
	}
	idx := indexOfToolResponse(events, "TRACE_ECHO")
	if idx < 0 {
		t.Fatalf("expected persisted TRACE_ECHO tool response, got %+v", events)
	}
	expected := session.Messages(session.AgentVisibleView(events[:idx+1]), "", traceSanitizeToolResult)
	if diff := diffTraceMessages(expected, records[1].Messages); diff != "" {
		t.Fatalf("unexpected second outbound request: %s\nexpected=%#v\ngot=%#v", diff, expected, records[1].Messages)
	}
	assertNoEmptyUserMessages(t, records[1].Messages)
}

func TestRuntimeRequestTraceExcludesPartialEventsFromNextRequest(t *testing.T) {
	t.Setenv(model.RequestTraceEnvVar, "1")
	store := newTraceFileStore(t)
	rt, err := New(Config{Store: store, TaskStore: taskinmemory.New()})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := llmagent.New(llmagent.Config{Name: "trace-agent", EmitPartialEvents: true})
	if err != nil {
		t.Fatal(err)
	}
	execRT := newTraceExecRuntime(t)
	var calls int
	llm := &traceStreamingLLM{
		name: "trace-streaming-llm",
		run: func(req *model.Request) []traceSeqResult {
			calls++
			switch calls {
			case 1:
				args, _ := json.Marshal(map[string]any{"text": "payload"})
				return []traceSeqResult{
					{resp: &model.Response{
						Message:      model.NewReasoningMessage(model.RoleAssistant, "thinking", model.ReasoningVisibilityVisible),
						TurnComplete: false,
					}},
					{resp: &model.Response{
						Message:      model.NewTextMessage(model.RoleAssistant, "partial answer"),
						TurnComplete: false,
					}},
					{resp: &model.Response{
						Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
							ID:   "call-echo-stream",
							Name: "TRACE_ECHO",
							Args: string(args),
						}}, ""),
						TurnComplete: true,
					}},
				}
			case 2:
				for _, msg := range req.Messages {
					if strings.Contains(msg.TextContent(), "partial answer") || strings.Contains(msg.ReasoningText(), "thinking") {
						t.Fatalf("unexpected partial content leaked into second request: %+v", req.Messages)
					}
				}
				return []traceSeqResult{{
					resp: &model.Response{
						Message:      model.NewTextMessage(model.RoleAssistant, "done"),
						TurnComplete: true,
					},
				}}
			default:
				return []traceSeqResult{{err: fmt.Errorf("unexpected request %d", calls)}}
			}
		},
	}
	for _, runErr := range runEvents(t, rt, context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "trace-partials",
		Input:     "do it",
		Agent:     ag,
		Model:     llm,
		Tools:     []tool.Tool{traceEchoTool{}},
		CoreTools: tool.CoreToolsConfig{Runtime: execRT},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	records := readTraceRecords(t, store, &session.Session{AppName: "app", UserID: "u", ID: "trace-partials"})
	if len(records) != 2 {
		t.Fatalf("expected 2 request trace records, got %d", len(records))
	}
	events, err := store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "trace-partials"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev != nil && session.IsPartial(ev) {
			t.Fatalf("partial event must not be persisted: %+v", events)
		}
	}
	idx := indexOfToolResponse(events, "TRACE_ECHO")
	if idx < 0 {
		t.Fatalf("expected persisted TRACE_ECHO tool response, got %+v", events)
	}
	expected := session.Messages(session.AgentVisibleView(events[:idx+1]), "", traceSanitizeToolResult)
	if diff := diffTraceMessages(expected, records[1].Messages); diff != "" {
		t.Fatalf("unexpected second outbound request after partial stream: %s\nexpected=%#v\ngot=%#v", diff, expected, records[1].Messages)
	}
}

func TestRuntimeRequestTraceBashYieldUsesLatestPersistedTaskResult(t *testing.T) {
	t.Setenv(model.RequestTraceEnvVar, "1")
	store := newTraceFileStore(t)
	rt, err := New(Config{Store: store, TaskStore: taskinmemory.New()})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := llmagent.New(llmagent.Config{Name: "trace-agent"})
	if err != nil {
		t.Fatal(err)
	}
	asyncRunner := &traceAsyncRunner{}
	var calls int
	llm := &scriptedRuntimeLLM{
		name: "trace-bash-llm",
		run: func(req *model.Request) (*model.Response, error) {
			calls++
			switch calls {
			case 1:
				args, _ := json.Marshal(map[string]any{
					"command":       "echo done",
					"yield_time_ms": 10,
				})
				return &model.Response{
					Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
						ID:   "call-bash",
						Name: "BASH",
						Args: string(args),
					}}, ""),
					TurnComplete: true,
				}, nil
			case 2:
				if resultStringFromMessages(req.Messages, "BASH", "task_id") == "" {
					t.Fatalf("expected yielded BASH task_id in second request context, got %+v", req.Messages)
				}
				return &model.Response{
					Message:      model.NewTextMessage(model.RoleAssistant, "bash complete"),
					TurnComplete: true,
				}, nil
			default:
				return nil, fmt.Errorf("unexpected request %d", calls)
			}
		},
	}
	for _, runErr := range runEvents(t, rt, context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "trace-bash",
		Input:     "run bash",
		Agent:     ag,
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{
			Runtime: traceTaskRuntime{host: asyncRunner},
		},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	records := readTraceRecords(t, store, &session.Session{AppName: "app", UserID: "u", ID: "trace-bash"})
	if len(records) != 2 {
		t.Fatalf("expected 2 request trace records, got %d", len(records))
	}
	events, err := store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "trace-bash"})
	if err != nil {
		t.Fatal(err)
	}
	bashIdx := indexOfToolResponse(events, "BASH")
	if bashIdx < 0 {
		t.Fatalf("expected persisted BASH tool response, got %+v", events)
	}
	expected := session.Messages(session.AgentVisibleView(events[:bashIdx+1]), "", traceSanitizeToolResult)
	if diff := diffTraceMessages(expected, records[1].Messages); diff != "" {
		t.Fatalf("unexpected second outbound request after BASH yield: %s\nexpected=%#v\ngot=%#v", diff, expected, records[1].Messages)
	}
	last := lastToolResult(records[1].Messages)
	if resultStringFromMessages(records[1].Messages, "BASH", "task_id") == "" {
		t.Fatalf("expected yielded BASH tool result with task_id, got %+v", last)
	}
	assertNoEmptyUserMessages(t, records[1].Messages)
}

func TestRuntimeRequestTraceSpawnYieldUsesLatestPersistedTaskResult(t *testing.T) {
	t.Setenv(model.RequestTraceEnvVar, "1")
	store := newTraceFileStore(t)
	rt, err := New(Config{Store: store, TaskStore: taskinmemory.New()})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := llmagent.New(llmagent.Config{Name: "trace-agent"})
	if err != nil {
		t.Fatal(err)
	}
	execRT := newTraceExecRuntime(t)
	var calls int
	llm := &scriptedRuntimeLLM{
		name: "trace-spawn-llm",
		run: func(req *model.Request) (*model.Response, error) {
			calls++
			switch calls {
			case 1:
				args, _ := json.Marshal(map[string]any{
					"prompt":        "child task",
					"yield_seconds": 1,
				})
				return &model.Response{
					Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
						ID:   "call-spawn",
						Name: tool.SpawnToolName,
						Args: string(args),
					}}, ""),
					TurnComplete: true,
				}, nil
			case 2:
				if resultStringFromMessages(req.Messages, tool.SpawnToolName, "task_id") == "" {
					t.Fatalf("expected yielded SPAWN task_id in second request context, got %+v", req.Messages)
				}
				return &model.Response{
					Message:      model.NewTextMessage(model.RoleAssistant, "spawn complete"),
					TurnComplete: true,
				}, nil
			default:
				return nil, fmt.Errorf("unexpected request %d", calls)
			}
		},
	}
	for _, runErr := range runEvents(t, rt, context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "trace-spawn",
		Input:     "run spawn",
		Agent:     ag,
		Model:     llm,
		Tools:     []tool.Tool{testSelfSpawnTool{}},
		CoreTools: tool.CoreToolsConfig{Runtime: execRT},
		SubagentRunnerFactory: func(rt *Runtime, _ *session.Session, req RunRequest) agent.SubagentRunner {
			return &traceSpawnRunner{runtime: rt, appName: req.AppName, userID: req.UserID}
		},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	records := readTraceRecords(t, store, &session.Session{AppName: "app", UserID: "u", ID: "trace-spawn"})
	if len(records) != 2 {
		t.Fatalf("expected 2 request trace records, got %d", len(records))
	}
	events, err := store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "trace-spawn"})
	if err != nil {
		t.Fatal(err)
	}
	spawnIdx := indexOfToolResponse(events, tool.SpawnToolName)
	if spawnIdx < 0 {
		t.Fatalf("expected persisted SPAWN tool response, got %+v", events)
	}
	expected := session.Messages(session.AgentVisibleView(events[:spawnIdx+1]), "", traceSanitizeToolResult)
	if diff := diffTraceMessages(expected, records[1].Messages); diff != "" {
		t.Fatalf("unexpected second outbound request after SPAWN yield: %s\nexpected=%#v\ngot=%#v", diff, expected, records[1].Messages)
	}
	last := lastToolResult(records[1].Messages)
	if resultStringFromMessages(records[1].Messages, tool.SpawnToolName, "task_id") == "" {
		t.Fatalf("expected yielded SPAWN tool result with task_id, got %+v", last)
	}
	assertNoEmptyUserMessages(t, records[1].Messages)
}

func TestRuntimeRequestTraceOverlayDoesNotPersistMainHistory(t *testing.T) {
	t.Setenv(model.RequestTraceEnvVar, "1")
	store := newTraceFileStore(t)
	rt, err := New(Config{Store: store, TaskStore: taskinmemory.New()})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := llmagent.New(llmagent.Config{Name: "trace-agent"})
	if err != nil {
		t.Fatal(err)
	}
	execRT := newTraceExecRuntime(t)
	llm := &scriptedRuntimeLLM{
		name: "trace-overlay-llm",
		run: func(req *model.Request) (*model.Response, error) {
			last := req.Messages[len(req.Messages)-1]
			if last.Role != model.RoleUser || strings.TrimSpace(last.TextContent()) != "side question" {
				t.Fatalf("expected overlay request to end with side question, got %+v", req.Messages)
			}
			return &model.Response{
				Message:      model.NewTextMessage(model.RoleAssistant, "overlay answer"),
				TurnComplete: true,
			}, nil
		},
	}
	runner, err := rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "trace-overlay",
		Agent:     ag,
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: execRT},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Submit(Submission{Text: "side question", Mode: SubmissionOverlay}); err != nil {
		t.Fatal(err)
	}
	for _, runErr := range drainRuntimeErrors(runner.Events()) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if err := runner.Close(); err != nil {
		t.Fatal(err)
	}
	records := readTraceRecords(t, store, &session.Session{AppName: "app", UserID: "u", ID: "trace-overlay"})
	if len(records) != 1 {
		t.Fatalf("expected 1 overlay request trace record, got %d", len(records))
	}
	last := records[0].Messages[len(records[0].Messages)-1]
	if last.Role != model.RoleUser || strings.TrimSpace(last.TextContent()) != "side question" {
		t.Fatalf("unexpected overlay outbound messages: %+v", records[0].Messages)
	}
	events, err := store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "trace-overlay"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev != nil && strings.Contains(ev.Message.TextContent(), "side question") {
			t.Fatalf("overlay message must not persist into main session history: %+v", events)
		}
	}
}

func newTraceFileStore(t *testing.T) *filestore.Store {
	t.Helper()
	store, err := filestore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func newTraceExecRuntime(t *testing.T) toolexec.Runtime {
	t.Helper()
	rt, err := toolexec.New(toolexec.Config{PermissionMode: toolexec.PermissionModeFullControl})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = toolexec.Close(rt)
	})
	return rt
}

func readTraceRecords(t *testing.T, store *filestore.Store, sess *session.Session) []model.RequestTraceRecord {
	t.Helper()
	dir, err := store.SessionDir(sess)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, model.RequestTraceFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var out []model.RequestTraceRecord
	for {
		var record model.RequestTraceRecord
		if err := dec.Decode(&record); err != nil {
			if strings.Contains(err.Error(), "EOF") {
				break
			}
			t.Fatal(err)
		}
		out = append(out, record)
	}
	return out
}

func indexOfToolResponse(events []*session.Event, name string) int {
	for i, ev := range events {
		if ev == nil || ev.Message.ToolResponse() == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(ev.Message.ToolResponse().Name), strings.TrimSpace(name)) {
			return i
		}
	}
	return -1
}

func lastToolResult(messages []model.Message) map[string]any {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.ToolResponse() != nil {
			return msg.ToolResponse().Result
		}
	}
	return nil
}

func resultStringFromMessages(messages []model.Message, toolName string, key string) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.ToolResponse() == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(msg.ToolResponse().Name), strings.TrimSpace(toolName)) {
			continue
		}
		return strings.TrimSpace(fmt.Sprint(msg.ToolResponse().Result[key]))
	}
	return ""
}

func assertNoEmptyUserMessages(t *testing.T, messages []model.Message) {
	t.Helper()
	for _, msg := range messages {
		if msg.Role == model.RoleUser && strings.TrimSpace(msg.TextContent()) == "" && len(msg.Parts) == 0 {
			t.Fatalf("unexpected empty user message in outbound request: %+v", messages)
		}
	}
}

func diffTraceMessages(expected, got []model.Message) string {
	if len(expected) != len(got) {
		return fmt.Sprintf("message count mismatch expected=%d got=%d", len(expected), len(got))
	}
	for i := range expected {
		left := expected[i]
		right := got[i]
		if left.Role != right.Role {
			return fmt.Sprintf("message[%d] role mismatch expected=%q got=%q", i, left.Role, right.Role)
		}
		if left.TextContent() != right.TextContent() {
			return fmt.Sprintf("message[%d] text mismatch expected=%q got=%q", i, left.TextContent(), right.TextContent())
		}
		if left.ReasoningText() != right.ReasoningText() {
			return fmt.Sprintf("message[%d] reasoning mismatch expected=%q got=%q", i, left.ReasoningText(), right.ReasoningText())
		}
		if len(left.ToolCalls()) != len(right.ToolCalls()) {
			return fmt.Sprintf("message[%d] tool call count mismatch expected=%d got=%d", i, len(left.ToolCalls()), len(right.ToolCalls()))
		}
		for j := range left.ToolCalls() {
			if left.ToolCalls()[j] != right.ToolCalls()[j] {
				return fmt.Sprintf("message[%d] tool call[%d] mismatch expected=%+v got=%+v", i, j, left.ToolCalls()[j], right.ToolCalls()[j])
			}
		}
		switch {
		case left.ToolResponse() == nil && right.ToolResponse() == nil:
		case left.ToolResponse() == nil || right.ToolResponse() == nil:
			return fmt.Sprintf("message[%d] tool response presence mismatch", i)
		default:
			if left.ToolResponse().ID != right.ToolResponse().ID || left.ToolResponse().Name != right.ToolResponse().Name {
				return fmt.Sprintf("message[%d] tool response id/name mismatch expected=%+v got=%+v", i, left.ToolResponse(), right.ToolResponse())
			}
			if normalizeJSONString(left.ToolResponse().Result) != normalizeJSONString(right.ToolResponse().Result) {
				return fmt.Sprintf("message[%d] tool response result mismatch expected=%s got=%s", i, normalizeJSONString(left.ToolResponse().Result), normalizeJSONString(right.ToolResponse().Result))
			}
		}
	}
	return ""
}

func traceSanitizeToolResult(result map[string]any) map[string]any {
	if len(result) == 0 {
		return result
	}
	out := make(map[string]any, len(result))
	for key, value := range result {
		if strings.HasPrefix(strings.TrimSpace(key), "_ui_") || strings.EqualFold(strings.TrimSpace(key), "metadata") {
			continue
		}
		out[key] = traceSanitizeToolValue(value)
	}
	return out
}

func traceSanitizeToolValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return traceSanitizeToolResult(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, one := range typed {
			out = append(out, traceSanitizeToolValue(one))
		}
		return out
	default:
		return value
	}
}

func normalizeJSONString(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(raw)
}

func drainRuntimeErrors(seq iter.Seq2[*session.Event, error]) []error {
	var errs []error
	for _, err := range seq {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
