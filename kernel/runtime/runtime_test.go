package runtime

import (
	"context"
	"encoding/json"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	taskinmemory "github.com/OnslaughtSnail/caelis/kernel/task/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type fixedAgent struct{}

func (a fixedAgent) Name() string { return "fixed" }
func (a fixedAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		yield(&session.Event{Message: model.Message{Role: model.RoleAssistant, Text: "ok"}}, nil)
	}
}

type approvalRequiredAgent struct{}

func (a approvalRequiredAgent) Name() string { return "approval-required" }
func (a approvalRequiredAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		_ = ctx
		_ = yield
		yield(nil, &toolexec.ApprovalRequiredError{Reason: "host escalation required"})
	}
}

type approvalAbortedAgent struct{}

func (a approvalAbortedAgent) Name() string { return "approval-aborted" }
func (a approvalAbortedAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		_ = ctx
		_ = yield
		yield(nil, &toolexec.ApprovalAbortedError{Reason: "denied"})
	}
}

type assertReadAgent struct {
	t *testing.T
}

func (a assertReadAgent) Name() string { return "assert-read" }
func (a assertReadAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		foundRead := false
		for _, t := range ctx.Tools() {
			if t != nil && t.Name() == "READ" {
				foundRead = true
				break
			}
		}
		if !foundRead {
			a.t.Fatalf("expected runtime to inject READ tool")
		}
		yield(&session.Event{Message: model.Message{Role: model.RoleAssistant, Text: "ok"}}, nil)
	}
}

type blockingAgent struct {
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
}

type backgroundBashAgent struct {
	taskID string
}

type panicAgent struct{}

func (a panicAgent) Name() string { return "panic-agent" }
func (a panicAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		panic("boom")
	}
}

type scriptedRuntimeLLM struct {
	name string
	run  func(*model.Request) (*model.Response, error)
}

func (a *blockingAgent) Name() string { return "blocking" }
func (a *blockingAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		_ = ctx
		a.once.Do(func() {
			close(a.started)
		})
		<-a.release
		yield(&session.Event{Message: model.Message{Role: model.RoleAssistant, Text: "done"}}, nil)
	}
}

func (a *backgroundBashAgent) Name() string { return "background-bash" }
func (a *backgroundBashAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		manager, ok := task.ManagerFromContext(ctx)
		if !ok || manager == nil {
			yield(nil, context.Canceled)
			return
		}
		snapshot, err := manager.StartBash(ctx, task.BashStartRequest{
			Command: "sleep 30",
			Yield:   10 * time.Millisecond,
			Timeout: time.Minute,
			Route:   string(toolexec.ExecutionRouteHost),
		})
		if err != nil {
			yield(nil, err)
			return
		}
		a.taskID = snapshot.TaskID
		yield(&session.Event{Message: model.Message{Role: model.RoleAssistant, Text: "scheduled"}}, nil)
	}
}

func (l *scriptedRuntimeLLM) Name() string { return l.name }
func (l *scriptedRuntimeLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.Response, error] {
	_ = ctx
	return func(yield func(*model.Response, error) bool) {
		resp, err := l.run(req)
		yield(resp, err)
	}
}

func TestRuntime_Run(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	var events []*session.Event
	for ev, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s",
		Input:     "hello",
		Agent:     fixedAgent{},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		events = append(events, ev)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events (running,user,assistant,completed), got %d", len(events))
	}
	listed, err := store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected persisted 2 events (user,assistant), got %d", len(listed))
	}
	for _, ev := range listed {
		if isLifecycleEvent(ev) {
			t.Fatalf("did not expect lifecycle event to be persisted: %+v", ev)
		}
	}
	statuses := lifecycleStatuses(events)
	if len(statuses) != 2 || statuses[0] != string(RunLifecycleStatusRunning) || statuses[1] != string(RunLifecycleStatusCompleted) {
		t.Fatalf("unexpected lifecycle statuses: %v", statuses)
	}
}

func TestRuntime_RunState_UsesInMemoryLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	for _, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-state",
		Input:     "hello",
		Agent:     fixedAgent{},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	state, err := rt.RunState(context.Background(), RunStateRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-state",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasLifecycle {
		t.Fatalf("expected in-memory lifecycle state, got %+v", state)
	}
	if state.Status != RunLifecycleStatusCompleted {
		t.Fatalf("expected completed status, got %+v", state)
	}
}

func TestRuntime_Run_ApprovalRequiredLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	var events []*session.Event
	var gotErr error
	for ev, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-approval-required",
		Input:     "hello",
		Agent:     approvalRequiredAgent{},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			gotErr = runErr
			break
		}
		events = append(events, ev)
	}
	if gotErr == nil {
		t.Fatal("expected approval required error")
	}
	if !toolexec.IsErrorCode(gotErr, toolexec.ErrorCodeApprovalRequired) {
		t.Fatalf("expected approval required code, got %q (%v)", toolexec.ErrorCodeOf(gotErr), gotErr)
	}
	statuses := lifecycleStatuses(events)
	if len(statuses) != 2 || statuses[0] != string(RunLifecycleStatusRunning) || statuses[1] != string(RunLifecycleStatusWaitingApproval) {
		t.Fatalf("unexpected lifecycle statuses: %v", statuses)
	}
}

func TestRuntime_Run_ApprovalAbortedLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	var events []*session.Event
	var gotErr error
	for ev, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-approval-aborted",
		Input:     "hello",
		Agent:     approvalAbortedAgent{},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			gotErr = runErr
			break
		}
		events = append(events, ev)
	}
	if gotErr == nil {
		t.Fatal("expected approval aborted error")
	}
	if !toolexec.IsErrorCode(gotErr, toolexec.ErrorCodeApprovalAborted) {
		t.Fatalf("expected approval aborted code, got %q (%v)", toolexec.ErrorCodeOf(gotErr), gotErr)
	}
	statuses := lifecycleStatuses(events)
	if len(statuses) != 2 || statuses[0] != string(RunLifecycleStatusRunning) || statuses[1] != string(RunLifecycleStatusInterrupted) {
		t.Fatalf("unexpected lifecycle statuses: %v", statuses)
	}
}

func TestRuntime_Run_DelegatedChildRunPersistsLineage(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	ag, err := llmagent.New(llmagent.Config{Name: "delegate"})
	if err != nil {
		t.Fatal(err)
	}
	llm := &scriptedRuntimeLLM{
		name: "delegate-llm",
		run: func(req *model.Request) (*model.Response, error) {
			last := req.Messages[len(req.Messages)-1]
			switch last.Role {
			case model.RoleUser:
				switch last.TextContent() {
				case "delegate please":
					args, _ := json.Marshal(map[string]any{"task": "child task", "yield_time_ms": 0})
					return &model.Response{
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID:   "call_delegate_1",
								Name: tool.DelegateTaskToolName,
								Args: string(args),
							}},
						},
						TurnComplete: true,
					}, nil
				case "child task":
					return &model.Response{
						Message:      model.Message{Role: model.RoleAssistant, Text: "child done"},
						TurnComplete: true,
					}, nil
				}
			case model.RoleTool:
				if last.ToolResponse != nil && last.ToolResponse.Name == tool.DelegateTaskToolName {
					return &model.Response{
						Message:      model.Message{Role: model.RoleAssistant, Text: "delegated complete"},
						TurnComplete: true,
					}, nil
				}
			}
			return &model.Response{
				Message:      model.Message{Role: model.RoleAssistant, Text: "fallback"},
				TurnComplete: true,
			}, nil
		},
	}

	var (
		parentEvents []*session.Event
		liveUpdates  []sessionstream.Update
	)
	runCtx := sessionstream.WithStreamer(context.Background(), sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		liveUpdates = append(liveUpdates, update)
	}))
	for ev, runErr := range rt.Run(runCtx, RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "parent-session",
		Input:     "delegate please",
		Agent:     ag,
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		parentEvents = append(parentEvents, ev)
	}
	if len(parentEvents) == 0 {
		t.Fatal("expected parent events")
	}

	parentStored, err := store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "parent-session"})
	if err != nil {
		t.Fatal(err)
	}
	var childSessionID string
	for _, ev := range parentStored {
		if ev == nil || ev.Message.ToolResponse == nil || ev.Message.ToolResponse.Name != tool.DelegateTaskToolName {
			continue
		}
		childSessionID, _ = ev.Message.ToolResponse.Result["child_session_id"].(string)
	}
	if childSessionID == "" {
		t.Fatal("expected delegated child_session_id in parent tool response")
	}
	if !strings.HasPrefix(childSessionID, "s-") {
		t.Fatalf("expected compact child session id, got %q", childSessionID)
	}
	if strings.Contains(childSessionID, "__delegate__") {
		t.Fatalf("expected delegated child session id without embedded parent path, got %q", childSessionID)
	}

	var childStored []*session.Event
	deadline := time.Now().Add(2 * time.Second)
	for {
		childStored, err = store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: childSessionID})
		if err == nil && len(childStored) > 0 {
			break
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(childStored) < 2 {
		t.Fatalf("expected child session events, got %d", len(childStored))
	}
	for _, ev := range childStored {
		if ev == nil {
			continue
		}
		if got := ev.Meta[metaParentSessionID]; got != "parent-session" {
			t.Fatalf("expected parent lineage metadata, got %+v", ev.Meta)
		}
		if got := ev.Meta[metaChildSessionID]; got != childSessionID {
			t.Fatalf("expected child lineage metadata, got %+v", ev.Meta)
		}
		if got := ev.Meta[metaParentToolCall]; got != "call_delegate_1" {
			t.Fatalf("expected parent tool call lineage metadata, got %+v", ev.Meta)
		}
		if strings.TrimSpace(asStringValue(ev.Meta[metaDelegationID])) == "" {
			t.Fatalf("expected delegation_id metadata, got %+v", ev.Meta)
		}
	}
	var sawLiveChild bool
	for _, update := range liveUpdates {
		if update.Event == nil || strings.TrimSpace(update.SessionID) != childSessionID {
			continue
		}
		if got := update.Event.Meta[metaParentToolCall]; got != "call_delegate_1" {
			t.Fatalf("expected raw child update to preserve parent tool call metadata, got %+v", update.Event.Meta)
		}
		sawLiveChild = true
	}
	if !sawLiveChild {
		t.Fatal("expected raw sessionstream updates for delegated child session")
	}
}

func asStringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func TestRuntime_BuildInvocationContext_DisablesDelegateForChildRuns(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{AppName: "app", UserID: "u", ID: "child-session"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	ctx := withDelegationLineage(context.Background(), delegationLineage{
		ParentSessionID: "parent-session",
		ChildSessionID:  "child-session",
		DelegationID:    "dlg-1",
	})
	inv, err := rt.buildInvocationContext(ctx, sess, RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "child-session",
		Model:     newRuntimeTestLLM("fake"),
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := inv.Tool(tool.DelegateTaskToolName); ok {
		t.Fatal("expected child invocation context to hide DELEGATE")
	}
	if _, ok := inv.Tool(tool.TaskToolName); !ok {
		t.Fatal("expected child invocation context to keep TASK")
	}
}

func TestDetachSubagentContext_DoesNotInheritTaskStreamer(t *testing.T) {
	streamer := taskstream.StreamerFunc(func(_ context.Context, ev taskstream.Event) {
		t.Fatalf("did not expect detached delegated context to inherit task streamer: %+v", ev)
	})
	ctx := taskstream.WithStreamer(context.Background(), streamer)
	detached := detachSubagentContext(ctx, delegationLineage{DelegationID: "dlg-1"})

	if _, ok := taskstream.StreamerFromContext(detached); ok {
		t.Fatal("expected detached delegated context to omit task streamer")
	}
}

func TestAttachSubagentContext_DoesNotInheritTaskStreamer(t *testing.T) {
	streamer := taskstream.StreamerFunc(func(_ context.Context, ev taskstream.Event) {
		t.Fatalf("did not expect attached delegated context to inherit task streamer: %+v", ev)
	})
	ctx := taskstream.WithStreamer(context.Background(), streamer)
	attached := attachSubagentContext(ctx, delegationLineage{DelegationID: "dlg-1", TaskID: "t-parent"})

	if _, ok := taskstream.StreamerFromContext(attached); ok {
		t.Fatal("expected attached delegated context to omit task streamer")
	}
}

func TestDelegatePreviewFromEvents_SkipsFencedCodeBlockContent(t *testing.T) {
	events := []*session.Event{
		{Message: model.Message{Role: model.RoleAssistant, Text: "working...\n```text\n12\n-rw-r--r-- demo.html\n```\ndone."}},
	}
	got := delegatePreviewFromEvents(events)
	if strings.Contains(got, "demo.html") || strings.Contains(got, "\n12\n") {
		t.Fatalf("expected fenced block content hidden, got %q", got)
	}
	if !strings.Contains(got, "working...") || !strings.Contains(got, "done.") {
		t.Fatalf("expected prose lines preserved, got %q", got)
	}
}

func TestDetachSubagentContext_DoesNotInheritOutputStreamer(t *testing.T) {
	streamer := toolexec.OutputStreamerFunc(func(_ context.Context, chunk toolexec.OutputChunk) {
		t.Fatalf("did not expect delegated context to inherit output streamer: %+v", chunk)
	})
	ctx := toolexec.WithOutputStreamer(context.Background(), streamer)
	detached := detachSubagentContext(ctx, delegationLineage{DelegationID: "dlg-1"})

	if _, ok := toolexec.OutputStreamerFromContext(detached); ok {
		t.Fatal("expected delegated context to omit output streamer")
	}
}

func TestAttachSubagentContext_DoesNotInheritOutputStreamer(t *testing.T) {
	streamer := toolexec.OutputStreamerFunc(func(_ context.Context, chunk toolexec.OutputChunk) {
		t.Fatalf("did not expect delegated context to inherit output streamer: %+v", chunk)
	})
	ctx := toolexec.WithOutputStreamer(context.Background(), streamer)
	attached := attachSubagentContext(ctx, delegationLineage{DelegationID: "dlg-1"})

	if _, ok := toolexec.OutputStreamerFromContext(attached); ok {
		t.Fatal("expected attached delegated context to omit output streamer")
	}
}

func TestDetachSubagentContext_DoesNotRerouteOutputStreamer(t *testing.T) {
	var seen []taskstream.Event
	streamer := taskstream.StreamerFunc(func(_ context.Context, ev taskstream.Event) {
		seen = append(seen, ev)
	})
	ctx := taskstream.WithStreamer(context.Background(), streamer)
	ctx = toolexec.WithOutputStreamer(ctx, toolexec.OutputStreamerFunc(func(_ context.Context, chunk toolexec.OutputChunk) {
		t.Fatalf("did not expect original output streamer to be used directly: %+v", chunk)
	}))
	detached := detachSubagentContext(ctx, delegationLineage{DelegationID: "dlg-1", TaskID: "t-parent"})

	if _, ok := toolexec.OutputStreamerFromContext(detached); ok {
		t.Fatal("expected delegated context to omit output streamer")
	}
	if len(seen) != 0 {
		t.Fatalf("did not expect delegated output reroute events, got %+v", seen)
	}
}

func TestAttachSubagentContext_DoesNotRerouteOutputStreamer(t *testing.T) {
	var seen []taskstream.Event
	streamer := taskstream.StreamerFunc(func(_ context.Context, ev taskstream.Event) {
		seen = append(seen, ev)
	})
	ctx := taskstream.WithStreamer(context.Background(), streamer)
	ctx = toolexec.WithOutputStreamer(ctx, toolexec.OutputStreamerFunc(func(_ context.Context, chunk toolexec.OutputChunk) {
		t.Fatalf("did not expect original output streamer to be used directly: %+v", chunk)
	}))
	attached := attachSubagentContext(ctx, delegationLineage{DelegationID: "dlg-1", TaskID: "t-parent"})

	if _, ok := toolexec.OutputStreamerFromContext(attached); ok {
		t.Fatal("expected attached delegated context to omit output streamer")
	}
	if len(seen) != 0 {
		t.Fatalf("did not expect delegated output reroute events, got %+v", seen)
	}
}

func TestDetachSubagentContext_InheritsSessionEventStreamer(t *testing.T) {
	var seen []sessionstream.Update
	streamer := sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		seen = append(seen, update)
	})
	ctx := sessionstream.WithStreamer(context.Background(), streamer)
	detached := detachSubagentContext(ctx, delegationLineage{DelegationID: "dlg-1"})

	sessionstream.Emit(detached, "child-session", &session.Event{SessionID: "child-session"})

	if len(seen) != 1 || seen[0].SessionID != "child-session" {
		t.Fatalf("expected detached delegated context to preserve raw sessionstream, got %+v", seen)
	}
}

func TestAttachSubagentContext_InheritsSessionEventStreamer(t *testing.T) {
	var seen []sessionstream.Update
	streamer := sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		seen = append(seen, update)
	})
	ctx := sessionstream.WithStreamer(context.Background(), streamer)
	attached := attachSubagentContext(ctx, delegationLineage{DelegationID: "dlg-1", TaskID: "t-parent"})

	sessionstream.Emit(attached, "child-session", &session.Event{SessionID: "child-session"})

	if len(seen) != 1 || seen[0].SessionID != "child-session" {
		t.Fatalf("expected attached delegated context to preserve raw sessionstream, got %+v", seen)
	}
}

func TestRuntime_Run_PreAgentSetupFailureAppendsFailedLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	badTool, err := tool.NewFunction[struct{}, struct{}](
		tool.ReadToolName,
		"reserved",
		func(ctx context.Context, args struct{}) (struct{}, error) {
			_ = ctx
			_ = args
			return struct{}{}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	var events []*session.Event
	var gotErr error
	for ev, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-setup-failed",
		Input:     "hello",
		Agent:     fixedAgent{},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
		Tools:     []tool.Tool{badTool},
	}) {
		if runErr != nil {
			gotErr = runErr
			break
		}
		events = append(events, ev)
	}
	if gotErr == nil {
		t.Fatal("expected setup failure")
	}
	statuses := lifecycleStatuses(events)
	if len(statuses) != 2 || statuses[0] != string(RunLifecycleStatusRunning) || statuses[1] != string(RunLifecycleStatusFailed) {
		t.Fatalf("unexpected lifecycle statuses: %v", statuses)
	}
}

func TestRuntime_InjectsCoreReadTool(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	for _, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-read",
		Input:     "hello",
		Agent:     assertReadAgent{t: t},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
}

func TestRuntime_ContextUsage(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	for _, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-usage",
		Input:     "hello",
		Agent:     fixedAgent{},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	usage, err := rt.ContextUsage(context.Background(), UsageRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-usage",
		Model:     llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage.WindowTokens <= 0 {
		t.Fatalf("expected positive window tokens, got %d", usage.WindowTokens)
	}
	if usage.CurrentTokens <= 0 {
		t.Fatalf("expected positive current tokens, got %d", usage.CurrentTokens)
	}
	if usage.Ratio <= 0 {
		t.Fatalf("expected positive ratio, got %f", usage.Ratio)
	}
}

func TestDetachedSubagentPanicPersistsFailedLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	runner := &runtimeSubagentRunner{
		runtime: rt,
		parent:  &session.Session{AppName: "app", UserID: "u", ID: "parent"},
		req: RunRequest{
			AppName:   "app",
			UserID:    "u",
			Model:     newRuntimeTestLLM("fake"),
			CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
		},
	}
	childReq := RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "child-panic",
		Input:     "hello",
		Agent:     panicAgent{},
		Model:     newRuntimeTestLLM("fake"),
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}

	runner.runDetachedSubagent(context.Background(), childReq, delegationLineage{
		ParentSessionID: "parent",
		ChildSessionID:  "child-panic",
		DelegationID:    "dlg-panic",
	})

	state, err := rt.RunState(context.Background(), RunStateRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "child-panic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasLifecycle || state.Status != RunLifecycleStatusFailed {
		t.Fatalf("expected failed lifecycle after detached panic, got %+v", state)
	}
	if state.Phase != "delegate_panic" {
		t.Fatalf("expected delegate_panic phase, got %+v", state)
	}
	if !strings.Contains(state.Error, "subagent panic: boom") {
		t.Fatalf("expected panic error recorded, got %+v", state)
	}
}

func TestRuntime_ContextUsage_MissingSessionReturnsEmpty(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	usage, err := rt.ContextUsage(context.Background(), UsageRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "missing",
		Model:     llm,
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage.CurrentTokens != 0 || usage.EventCount != 0 {
		t.Fatalf("expected empty usage for missing session, got %+v", usage)
	}
}

func TestRuntime_RunState_MissingSessionReturnsNone(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	state, err := rt.RunState(context.Background(), RunStateRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.HasLifecycle {
		t.Fatalf("expected missing session run state to be empty, got %+v", state)
	}
}

func TestRuntime_Run_SessionSingleFlight(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	release := make(chan struct{})
	agent1 := &blockingAgent{
		started: make(chan struct{}),
		release: release,
	}
	firstRunDone := make(chan struct{})
	go func() {
		defer close(firstRunDone)
		for _, runErr := range rt.Run(context.Background(), RunRequest{
			AppName:   "app",
			UserID:    "u",
			SessionID: "s-single-flight",
			Input:     "hello",
			Agent:     agent1,
			Model:     llm,
			CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
		}) {
			if runErr != nil {
				return
			}
		}
	}()
	<-agent1.started

	var gotErr error
	for _, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-single-flight",
		Input:     "hello2",
		Agent:     fixedAgent{},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			gotErr = runErr
			break
		}
	}
	if gotErr == nil {
		t.Fatal("expected session busy error for concurrent run")
	}
	if !IsSessionBusy(gotErr) {
		t.Fatalf("expected session busy error, got %v", gotErr)
	}

	close(release)
	<-firstRunDone
}

func TestRuntime_Run_CleansUpTurnScopedBackgroundTasks(t *testing.T) {
	store := inmemory.New()
	taskStore := taskinmemory.New()
	rt, err := New(Config{Store: store, TaskStore: taskStore})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	agent := &backgroundBashAgent{}
	for _, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-cleanup",
		Input:     "hello",
		Agent:     agent,
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}
	if strings.TrimSpace(agent.taskID) == "" {
		t.Fatal("expected background task id")
	}
	entry, err := taskStore.Get(context.Background(), agent.taskID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.State != task.StateCancelled || entry.Running {
		t.Fatalf("expected turn-scoped task to be cancelled, got state=%q running=%v result=%#v", entry.State, entry.Running, entry.Result)
	}
}

func lifecycleStatuses(events []*session.Event) []string {
	out := make([]string, 0, 2)
	for _, ev := range events {
		if ev == nil || ev.Meta == nil {
			continue
		}
		kind, _ := ev.Meta[metaKind].(string)
		if kind != metaKindLifecycle {
			continue
		}
		payload, _ := ev.Meta[MetaLifecycle].(map[string]any)
		status, _ := payload["status"].(string)
		if status == "" {
			continue
		}
		out = append(out, status)
	}
	return out
}
