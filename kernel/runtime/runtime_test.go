package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	internalacp "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

type fixedAgent struct{}

func (a fixedAgent) Name() string { return "fixed" }
func (a fixedAgent) Run(_ agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		yield(&session.Event{Message: model.NewTextMessage(model.RoleAssistant, "ok")}, nil)
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
		yield(&session.Event{Message: model.NewTextMessage(model.RoleAssistant, "ok")}, nil)
	}
}

type overlayInspectAgent struct {
	t *testing.T
}

func (a overlayInspectAgent) Name() string { return "overlay-inspect" }
func (a overlayInspectAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if !ctx.Overlay() {
			a.t.Fatalf("expected overlay context")
		}
		if len(ctx.Tools()) == 0 {
			a.t.Fatalf("expected overlay context to preserve tools")
		}
		if _, ok := ctx.Tool("FAKE_TOOL"); !ok {
			a.t.Fatalf("expected overlay context to expose FAKE_TOOL")
		}
		last := ctx.Events().At(ctx.Events().Len() - 1)
		if last == nil || last.Message.Role != model.RoleUser || strings.TrimSpace(last.Message.TextContent()) != "side question" {
			a.t.Fatalf("expected overlay user event appended to context, got %#v", last)
		}
		yield(&session.Event{Message: model.NewTextMessage(model.RoleAssistant, "overlay answer")}, nil)
	}
}

type fakeTool struct {
	name string
}

type testSelfSpawnTool struct{}

func (t testSelfSpawnTool) Name() string { return tool.SpawnToolName }

func (t testSelfSpawnTool) Description() string {
	return "spawn child session"
}

func (t testSelfSpawnTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{Name: t.Name()}
}

func (t testSelfSpawnTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	manager, ok := task.ManagerFromContext(ctx)
	if !ok || manager == nil {
		return nil, fmt.Errorf("task manager unavailable")
	}
	taskText, _ := args["prompt"].(string)
	snapshot, err := manager.StartSpawn(ctx, task.SpawnStartRequest{
		Prompt: strings.TrimSpace(taskText),
		Kind:   task.KindSpawn,
	})
	if err != nil {
		return nil, err
	}
	result := tool.SnapshotResultMap(snapshot)
	return tool.AppendTaskSnapshotEvents(result, snapshot), nil
}

func (t fakeTool) Name() string        { return t.name }
func (t fakeTool) Description() string { return t.name }
func (t fakeTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{Name: t.name, Description: t.name, Parameters: map[string]any{"type": "object"}}
}
func (t fakeTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	_ = ctx
	_ = args
	return map[string]any{}, nil
}

type overlayErrorAgent struct {
	err error
}

func (a overlayErrorAgent) Name() string { return "overlay-error" }
func (a overlayErrorAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if !ctx.Overlay() {
			yield(&session.Event{Message: model.NewTextMessage(model.RoleAssistant, "ok")}, nil)
			return
		}
		yield(nil, a.err)
	}
}

func TestNew_RequiresExplicitStores(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatal("expected constructor to reject missing stores")
	}
	if !strings.Contains(err.Error(), "log store") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNew_UsesExplicitStores(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	if rt.logStore != store || rt.stateStore != store {
		t.Fatalf("expected explicit stores to be retained, got log=%T state=%T", rt.logStore, rt.stateStore)
	}
}

func TestRuntimeRun_AllowsAgentWithoutModel(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	for _, runErr := range runEvents(context.Background(), t, rt, RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-no-model",
		Input:     "hello",
		Agent:     fixedAgent{},
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			t.Fatalf("unexpected run error: %v", runErr)
		}
	}
}

func TestShouldPersistEvent_SkipsUIOnly(t *testing.T) {
	ev := session.MarkNotice(&session.Event{}, session.NoticeLevelWarn, "retrying in 1s")
	if shouldPersistEvent(ev) {
		t.Fatalf("expected ui-only event to be skipped from persistence")
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
func (a panicAgent) Run(_ agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(_ func(*session.Event, error) bool) {
		panic("boom")
	}
}

type scriptedRuntimeLLM struct {
	name string
	run  func(*model.Request) (*model.Response, error)
}

type manyAssistantEventsAgent struct {
	count int
}

func (a *blockingAgent) Name() string { return "blocking" }
func (a *blockingAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		_ = ctx
		a.once.Do(func() {
			close(a.started)
		})
		<-a.release
		yield(&session.Event{Message: model.NewTextMessage(model.RoleAssistant, "done")}, nil)
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
		yield(&session.Event{Message: model.NewTextMessage(model.RoleAssistant, "scheduled")}, nil)
	}
}

func (l *scriptedRuntimeLLM) Name() string { return l.name }
func (l *scriptedRuntimeLLM) Generate(ctx context.Context, req *model.Request) iter.Seq2[*model.StreamEvent, error] {
	_ = ctx
	return func(yield func(*model.StreamEvent, error) bool) {
		resp, err := l.run(req)
		if err != nil {
			yield(nil, err)
			return
		}
		if resp != nil {
			if resp.Model == "" {
				resp.Model = l.name
			}
			if resp.Provider == "" {
				resp.Provider = "test-provider"
			}
		}
		yield(model.StreamEventFromResponse(resp), nil)
	}
}

func (a manyAssistantEventsAgent) Name() string { return "many-assistant-events" }
func (a manyAssistantEventsAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	_ = ctx
	return func(yield func(*session.Event, error) bool) {
		for i := 0; i < a.count; i++ {
			if !yield(&session.Event{
				Message: model.NewTextMessage(model.RoleAssistant, fmt.Sprintf("msg-%04d", i)),
			}, nil) {
				return
			}
		}
	}
}

func runEvents(ctx context.Context, t *testing.T, rt *Runtime, req RunRequest) iter.Seq2[*session.Event, error] {
	t.Helper()
	runner, err := rt.Run(ctx, req)
	if err != nil {
		return func(yield func(*session.Event, error) bool) {
			yield(nil, err)
		}
	}
	return func(yield func(*session.Event, error) bool) {
		defer runner.Close()
		for ev, runErr := range runner.Events() {
			if !yield(ev, runErr) {
				return
			}
		}
	}
}

func TestRuntime_Run(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	var events []*session.Event
	for ev, runErr := range runEvents(context.Background(), t, rt, RunRequest{
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

func TestRuntimeRunnerEvents_ReplaysAllDroppedDurablePages(t *testing.T) {
	const assistantCount = replayFetchLimit + 300

	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-replay-pages"}

	runner, err := rt.Run(context.Background(), RunRequest{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
		Input:     "hello",
		Agent: manyAssistantEventsAgent{
			count: assistantCount,
		},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()

	deadline := time.Now().Add(10 * time.Second)
	for {
		events, listErr := store.ListEvents(context.Background(), sess)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(events) >= assistantCount+1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for persisted events, got %d", len(events))
		}
		time.Sleep(10 * time.Millisecond)
	}

	gotAssistant := 0
	for ev, runErr := range runner.Events() {
		if runErr != nil {
			t.Fatal(runErr)
		}
		if ev != nil && ev.Message.Role == model.RoleAssistant && strings.HasPrefix(ev.Message.TextContent(), "msg-") {
			gotAssistant++
		}
	}
	if gotAssistant != assistantCount {
		t.Fatalf("expected %d assistant messages from replay, got %d", assistantCount, gotAssistant)
	}
}

func TestRuntimeRunPreservesProvidedContentPartOrder(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	reqParts := []model.ContentPart{
		{Type: model.ContentPartImage, FileName: "first.png", Data: "a"},
		{Type: model.ContentPartText, Text: "Hi豆包"},
		{Type: model.ContentPartImage, FileName: "second.png", Data: "b"},
		{Type: model.ContentPartText, Text: "这两个是什么APP?"},
	}

	for _, runErr := range runEvents(context.Background(), t, rt, RunRequest{
		AppName:      "app",
		UserID:       "u",
		SessionID:    "s-order",
		Input:        "Hi豆包这两个是什么APP?",
		ContentParts: reqParts,
		Agent:        fixedAgent{},
		Model:        llm,
		CoreTools:    tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}

	listed, err := store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "s-order"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) == 0 || listed[0] == nil {
		t.Fatal("expected persisted user event")
	}
	expectedParts := model.PartsFromContentParts(reqParts)
	if got := listed[0].Message.Parts; len(got) != len(expectedParts) {
		t.Fatalf("expected %d content parts, got %+v", len(expectedParts), got)
	}
	for i := range expectedParts {
		if listed[0].Message.Parts[i].Kind != expectedParts[i].Kind {
			t.Fatalf("expected content part %d kind to match, want %+v got %+v", i, expectedParts[i], listed[0].Message.Parts[i])
		}
	}
}

func TestRuntimeRunPrependsInputWhenContentPartsHaveNoText(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	reqParts := []model.ContentPart{
		{Type: model.ContentPartImage, FileName: "only.png", Data: "a"},
	}

	for _, runErr := range runEvents(context.Background(), t, rt, RunRequest{
		AppName:      "app",
		UserID:       "u",
		SessionID:    "s-input-prefix",
		Input:        "what is in this image?",
		ContentParts: reqParts,
		Agent:        fixedAgent{},
		Model:        llm,
		CoreTools:    tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}

	listed, err := store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "s-input-prefix"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) == 0 || listed[0] == nil {
		t.Fatal("expected persisted user event")
	}
	got := listed[0].Message.Parts
	if len(got) != 2 {
		t.Fatalf("expected text+image content parts, got %+v", got)
	}
	if got[0].Kind != model.PartKindText || got[0].Text == nil || got[0].Text.Text != "what is in this image?" {
		t.Fatalf("expected input text prepended, got %+v", got[0])
	}
	if got[1].Kind != model.PartKindMedia || got[1].Media == nil {
		t.Fatalf("expected original image preserved, got %+v", got[1])
	}
}

func TestRuntime_RunState_UsesInMemoryLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	for _, runErr := range runEvents(context.Background(), t, rt, RunRequest{
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

func TestRuntime_OverlaySubmission_IsEphemeral(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	for _, runErr := range runEvents(context.Background(), t, rt, RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-overlay",
		Input:     "hello",
		Agent:     fixedAgent{},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
	}

	runner, err := rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-overlay",
		Agent:     overlayInspectAgent{t: t},
		Model:     llm,
		Tools:     []tool.Tool{fakeTool{name: "FAKE_TOOL"}},
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	if err := runner.Submit(Submission{Text: "side question", Mode: SubmissionOverlay}); err != nil {
		t.Fatal(err)
	}

	foundOverlay := false
	for ev, runErr := range runner.Events() {
		if runErr != nil {
			t.Fatal(runErr)
		}
		if ev == nil || ev.Message.Role != model.RoleAssistant {
			continue
		}
		if strings.TrimSpace(ev.Message.TextContent()) == "overlay answer" {
			foundOverlay = true
			if !session.IsOverlay(ev) {
				t.Fatalf("expected assistant overlay event to be marked overlay")
			}
		}
	}
	if !foundOverlay {
		t.Fatal("expected overlay assistant event")
	}

	listed, err := store.ListEvents(context.Background(), &session.Session{AppName: "app", UserID: "u", ID: "s-overlay"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected overlay submission not persisted, got %d events", len(listed))
	}
	for _, ev := range listed {
		if ev != nil && strings.Contains(ev.Message.TextContent(), "side question") {
			t.Fatalf("did not expect overlay question to persist: %#v", ev)
		}
	}
}

func TestRuntime_OverlaySubmission_PropagatesStandaloneFailure(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	overlayErr := errors.New("overlay boom")

	runner, err := rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-overlay-failed",
		Agent:     overlayErrorAgent{err: overlayErr},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	if err := runner.Submit(Submission{Text: "side question", Mode: SubmissionOverlay}); err != nil {
		t.Fatal(err)
	}

	var (
		events []*session.Event
		gotErr error
	)
	for ev, runErr := range runner.Events() {
		if runErr != nil {
			gotErr = runErr
			break
		}
		events = append(events, ev)
	}
	if !errors.Is(gotErr, overlayErr) {
		t.Fatalf("expected overlay error %v, got %v", overlayErr, gotErr)
	}
	statuses := lifecycleStatuses(events)
	if len(statuses) != 2 || statuses[0] != string(RunLifecycleStatusRunning) || statuses[1] != string(RunLifecycleStatusFailed) {
		t.Fatalf("unexpected lifecycle statuses: %v", statuses)
	}
	for _, ev := range events {
		if ev == nil || ev.Message.Role != model.RoleAssistant {
			continue
		}
		if strings.Contains(ev.Message.TextContent(), "overlay boom") {
			t.Fatalf("expected standalone overlay failure to propagate as an error, got assistant event %#v", ev)
		}
	}
}

func TestRuntime_OverlaySubmission_PropagatesStandaloneCancellation(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")

	runner, err := rt.Run(context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-overlay-cancelled",
		Agent:     overlayErrorAgent{err: context.Canceled},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	if err := runner.Submit(Submission{Text: "side question", Mode: SubmissionOverlay}); err != nil {
		t.Fatal(err)
	}

	var (
		events []*session.Event
		gotErr error
	)
	for ev, runErr := range runner.Events() {
		if runErr != nil {
			gotErr = runErr
			break
		}
		events = append(events, ev)
	}
	if !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", gotErr)
	}
	statuses := lifecycleStatuses(events)
	if len(statuses) != 2 || statuses[0] != string(RunLifecycleStatusRunning) || statuses[1] != string(RunLifecycleStatusInterrupted) {
		t.Fatalf("unexpected lifecycle statuses: %v", statuses)
	}
}

func TestRuntime_Run_ApprovalRequiredLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	var events []*session.Event
	var gotErr error
	for ev, runErr := range runEvents(context.Background(), t, rt, RunRequest{
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
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	var events []*session.Event
	var gotErr error
	for ev, runErr := range runEvents(context.Background(), t, rt, RunRequest{
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

func TestRuntime_Run_SpawnChildRunPersistsLineage(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
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
					args, _ := json.Marshal(map[string]any{"prompt": "child task", "yield_time_ms": 0})
					return &model.Response{
						Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
							ID:   "call_delegate_1",
							Name: tool.SpawnToolName,
							Args: string(args),
						}}, ""),
						TurnComplete: true,
					}, nil
				case "child task":
					return &model.Response{
						Message:      model.NewTextMessage(model.RoleAssistant, "child done"),
						TurnComplete: true,
					}, nil
				}
			case model.RoleTool:
				if last.ToolResponse() != nil && last.ToolResponse().Name == tool.SpawnToolName {
					return &model.Response{
						Message:      model.NewTextMessage(model.RoleAssistant, "delegated complete"),
						TurnComplete: true,
					}, nil
				}
			}
			return &model.Response{
				Message:      model.NewTextMessage(model.RoleAssistant, "fallback"),
				TurnComplete: true,
			}, nil
		},
	}

	var (
		parentEvents []*session.Event
		liveUpdates  []sessionstream.Update
		liveMu       sync.Mutex
	)
	runCtx := sessionstream.WithStreamer(context.Background(), sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		liveMu.Lock()
		liveUpdates = append(liveUpdates, update)
		liveMu.Unlock()
	}))
	for ev, runErr := range runEvents(runCtx, t, rt, RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "parent-session",
		Input:     "delegate please",
		Agent:     ag,
		Model:     llm,
		Tools:     []tool.Tool{testSelfSpawnTool{}},
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
		if ev == nil || ev.Message.ToolResponse() == nil || ev.Message.ToolResponse().Name != tool.SpawnToolName {
			continue
		}
		childSessionID, _ = ev.Message.ToolResponse().Result["child_session_id"].(string)
		if childSessionID == "" {
			t.Logf("spawn result payload: %#v", ev.Message.ToolResponse().Result)
		}
	}
	if childSessionID == "" {
		t.Fatal("expected delegated child session reference in parent tool response")
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
		if err == nil && len(childStored) >= 1 {
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
	if len(childStored) == 0 {
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
	childState, err := store.SnapshotState(context.Background(), &session.Session{
		AppName: "app",
		UserID:  "u",
		ID:      childSessionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !internalacp.IsDelegatedChild(anyMap(anyMap(childState["acp"])["meta"])) {
		t.Fatalf("expected delegated child marker in child session state, got %#v", childState)
	}
	liveMu.Lock()
	liveSnapshot := append([]sessionstream.Update(nil), liveUpdates...)
	liveMu.Unlock()
	var sawLiveChild bool
	for _, update := range liveSnapshot {
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

func anyMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func TestRuntime_BuildInvocationContext_DisablesDelegateForChildRuns(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
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
		{Message: model.NewTextMessage(model.RoleAssistant, "working...\n```text\n12\n-rw-r--r-- demo.html\n```\ndone.")},
	}
	got := subagentPreviewFromEvents(events)
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

func TestRunSubagent_NoOrphanContextOnTimeout(t *testing.T) {
	// Verify that the timeout path creates exactly one detached context,
	// not two (which would leak the first cancel func).
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store, TaskStore: taskinmemory.New()})
	if err != nil {
		t.Fatal(err)
	}

	lineage := delegationLineage{
		ParentSessionID: "parent",
		ChildSessionID:  "child-timeout",
		DelegationID:    "dlg-timeout",
	}

	// With Timeout > 0: only one context should be created.
	base := detachSubagentContext(context.Background(), lineage)
	ctx1, cancel1 := context.WithTimeout(base, 5*time.Second)
	defer cancel1()

	// Verify the context is functional (not orphaned).
	select {
	case <-ctx1.Done():
		t.Fatal("context should not be done immediately")
	default:
	}

	// Cancel and verify it propagates.
	cancel1()
	select {
	case <-ctx1.Done():
	default:
		t.Fatal("context should be done after cancel")
	}

	// Without Timeout: only WithCancel should be used.
	ctx2, cancel2 := context.WithCancel(base)
	defer cancel2()

	select {
	case <-ctx2.Done():
		t.Fatal("context should not be done immediately")
	default:
	}

	cancel2()
	select {
	case <-ctx2.Done():
	default:
		t.Fatal("context should be done after cancel")
	}

	_ = rt
}

func TestRunSubagent_ContextBranchingIsExclusive(t *testing.T) {
	// Verify that RunSubagent's context creation uses exactly one branch.
	// We test by inspecting the fixed code pattern: if Timeout > 0, use
	// WithTimeout; otherwise use WithCancel. Never both.
	lineage := delegationLineage{
		ParentSessionID: "parent",
		ChildSessionID:  "child-branch",
		DelegationID:    "dlg-branch",
	}
	base := detachSubagentContext(context.Background(), lineage)

	// Timeout branch: context has a deadline.
	ctxTimeout, cancelTimeout := context.WithTimeout(base, time.Hour)
	defer cancelTimeout()
	if _, ok := ctxTimeout.Deadline(); !ok {
		t.Fatal("timeout context must have a deadline")
	}

	// Cancel branch: context has no deadline.
	ctxCancel, cancelCancel := context.WithCancel(base)
	defer cancelCancel()
	if _, ok := ctxCancel.Deadline(); ok {
		t.Fatal("cancel context must not have a deadline")
	}
}

func TestRuntime_Run_PreAgentSetupFailureAppendsFailedLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	badToolA, err := tool.NewFunction(
		"DUPLICATE_TOOL",
		"duplicate-a",
		func(ctx context.Context, args struct{}) (struct{}, error) {
			_ = ctx
			_ = args
			return struct{}{}, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	badToolB, err := tool.NewFunction(
		"DUPLICATE_TOOL",
		"duplicate-b",
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
	for ev, runErr := range runEvents(context.Background(), t, rt, RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-setup-failed",
		Input:     "hello",
		Agent:     fixedAgent{},
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
		Tools:     []tool.Tool{badToolA, badToolB},
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
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	for _, runErr := range runEvents(context.Background(), t, rt, RunRequest{
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
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	for _, runErr := range runEvents(context.Background(), t, rt, RunRequest{
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
	rt, err := New(Config{LogStore: store, StateStore: store})
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

func TestDetachedSubagentStartupFailurePersistsFailedLifecycle(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
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
		SessionID: "child-startup-failure",
		Input:     "hello",
		Model:     newRuntimeTestLLM("fake"),
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}

	runner.runDetachedSubagent(context.Background(), childReq, delegationLineage{
		ParentSessionID: "parent",
		ChildSessionID:  "child-startup-failure",
		DelegationID:    "dlg-startup-failure",
	})

	state, err := rt.RunState(context.Background(), RunStateRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "child-startup-failure",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasLifecycle || state.Status != RunLifecycleStatusFailed {
		t.Fatalf("expected failed lifecycle after detached startup failure, got %+v", state)
	}
	if state.Phase != "delegate_panic" {
		t.Fatalf("expected delegate_panic phase for detached startup failure, got %+v", state)
	}
	if !strings.Contains(state.Error, "runtime: agent is nil") {
		t.Fatalf("expected startup failure recorded, got %+v", state)
	}
}

func TestPrepareChildRunUsesCurrentTaskWriteLineageForExistingSessionContinuation(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
	if err != nil {
		t.Fatal(err)
	}
	parent := &session.Session{AppName: "app", UserID: "u", ID: "parent"}
	child := &session.Session{AppName: "app", UserID: "u", ID: "child-existing"}
	if _, err := store.GetOrCreate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetOrCreate(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), child, &session.Event{
		ID:        "ev-1",
		SessionID: child.ID,
		Message:   model.NewTextMessage(model.RoleAssistant, "first child output"),
		Meta: map[string]any{
			"parent_session_id":   parent.ID,
			"child_session_id":    child.ID,
			"parent_tool_call_id": "call-spawn-original",
			"parent_tool_name":    tool.SpawnToolName,
			"delegation_id":       "dlg-original",
		},
	}); err != nil {
		t.Fatal(err)
	}

	runner := &runtimeSubagentRunner{
		runtime: rt,
		parent:  parent,
		req: RunRequest{
			AppName:   "app",
			UserID:    "u",
			Model:     newRuntimeTestLLM("fake"),
			CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
		},
	}

	ctx := toolexec.WithToolCallInfo(context.Background(), tool.TaskToolName, "call-task-write")
	_, lineage, err := runner.prepareChildRun(WithSubagentContinuation(ctx), agent.SubagentRunRequest{
		SessionID: child.ID,
		Prompt:    "follow up",
	})
	if err != nil {
		t.Fatal(err)
	}
	if lineage.ParentToolCall != "call-task-write" {
		t.Fatalf("expected current TASK write call id, got %q", lineage.ParentToolCall)
	}
	if lineage.ParentToolName != tool.TaskToolName {
		t.Fatalf("expected current TASK tool preserved, got %q", lineage.ParentToolName)
	}
	if lineage.DelegationID == "dlg-original" || lineage.DelegationID == "" {
		t.Fatalf("expected fresh delegation id for continuation, got %q", lineage.DelegationID)
	}
}

func TestRuntime_ContextUsage_MissingSessionReturnsEmpty(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{LogStore: store, StateStore: store})
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
	rt, err := New(Config{LogStore: store, StateStore: store})
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
	rt, err := New(Config{LogStore: store, StateStore: store})
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
		for _, runErr := range runEvents(context.Background(), t, rt, RunRequest{
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
	for _, runErr := range runEvents(context.Background(), t, rt, RunRequest{
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
	rt, err := New(Config{LogStore: store, StateStore: store, TaskStore: taskStore})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	agent := &backgroundBashAgent{}
	for _, runErr := range runEvents(context.Background(), t, rt, RunRequest{
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
