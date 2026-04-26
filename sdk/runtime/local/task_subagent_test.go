package local

import (
	"context"
	"strings"
	"testing"

	sdkdelegation "github.com/OnslaughtSnail/caelis/sdk/delegation"
	"github.com/OnslaughtSnail/caelis/sdk/runtime/agents/chat"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	"github.com/OnslaughtSnail/caelis/sdk/session/inmemory"
	sdksubagent "github.com/OnslaughtSnail/caelis/sdk/subagent"
	sdktask "github.com/OnslaughtSnail/caelis/sdk/task"
)

func TestTaskWriteContinuesCompletedSpawnChild(t *testing.T) {
	ctx := context.Background()
	runner := &recordingSubagentRunner{
		spawnResult: sdkdelegation.Result{State: sdkdelegation.StateCompleted, Result: "first done"},
		continueResult: sdkdelegation.Result{
			State:  sdkdelegation.StateCompleted,
			Result: "follow-up done",
		},
	}
	runtime, session := newSubagentTaskTestRuntime(t, runner)

	started, err := runtime.tasks.StartSubagent(ctx, session, session.SessionRef, runner, sdktask.SubagentStartRequest{
		Agent:       "helper",
		Prompt:      "first",
		YieldTimeMS: 1,
	})
	if err != nil {
		t.Fatalf("StartSubagent() error = %v", err)
	}
	if started.State != sdktask.StateCompleted {
		t.Fatalf("started state = %q, want completed", started.State)
	}

	continued, err := runtime.tasks.Write(ctx, session.SessionRef, sdktask.ControlRequest{
		TaskID: started.Ref.TaskID,
		Input:  "next prompt",
	})
	if err != nil {
		t.Fatalf("Write(completed spawn) error = %v", err)
	}
	if got, _ := continued.Result["result"].(string); got != "follow-up done" {
		t.Fatalf("continued result = %q, want follow-up done", got)
	}
	if runner.continuePrompt != "next prompt" {
		t.Fatalf("continue prompt = %q, want next prompt", runner.continuePrompt)
	}
	if runner.continueAnchor.TaskID != started.Ref.TaskID {
		t.Fatalf("continue anchor task id = %q, want %q", runner.continueAnchor.TaskID, started.Ref.TaskID)
	}
}

func TestTaskWriteRejectsRunningSpawnChildWithWaitHint(t *testing.T) {
	ctx := context.Background()
	runner := &recordingSubagentRunner{
		spawnResult: sdkdelegation.Result{State: sdkdelegation.StateRunning, OutputPreview: "still running", Running: true},
	}
	runtime, session := newSubagentTaskTestRuntime(t, runner)

	started, err := runtime.tasks.StartSubagent(ctx, session, session.SessionRef, runner, sdktask.SubagentStartRequest{
		Agent:       "helper",
		Prompt:      "first",
		YieldTimeMS: 1,
	})
	if err != nil {
		t.Fatalf("StartSubagent() error = %v", err)
	}

	_, err = runtime.tasks.Write(ctx, session.SessionRef, sdktask.ControlRequest{
		TaskID: started.Ref.TaskID,
		Input:  "too soon",
	})
	if err == nil {
		t.Fatal("Write(running spawn) error = nil, want wait hint")
	}
	if !strings.Contains(err.Error(), "TASK wait") {
		t.Fatalf("Write(running spawn) error = %v, want TASK wait hint", err)
	}
	if runner.continuePrompt != "" {
		t.Fatalf("Continue was called for running task with prompt %q", runner.continuePrompt)
	}
}

func newSubagentTaskTestRuntime(t *testing.T, runner sdksubagent.Runner) (*Runtime, sdksession.Session) {
	t.Helper()
	sessions := inmemory.NewService(inmemory.NewStore(inmemory.Config{}))
	session, err := sessions.StartSession(context.Background(), sdksession.StartSessionRequest{
		AppName: "caelis",
		UserID:  "task-test",
		Workspace: sdksession.WorkspaceRef{
			Key: "task-ws",
			CWD: t.TempDir(),
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	runtime, err := New(Config{
		Sessions:     sessions,
		AgentFactory: chat.Factory{},
		Subagents:    runner,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return runtime, session
}

type recordingSubagentRunner struct {
	spawnResult    sdkdelegation.Result
	waitResult     sdkdelegation.Result
	continueResult sdkdelegation.Result
	continueAnchor sdkdelegation.Anchor
	continuePrompt string
}

func (r *recordingSubagentRunner) Spawn(context.Context, sdksubagent.SpawnContext, sdkdelegation.Request) (sdkdelegation.Anchor, sdkdelegation.Result, error) {
	return sdkdelegation.Anchor{SessionID: "child-1", Agent: "helper", AgentID: "helper-1"}, sdkdelegation.CloneResult(r.spawnResult), nil
}

func (r *recordingSubagentRunner) Continue(_ context.Context, anchor sdkdelegation.Anchor, req sdkdelegation.Request) (sdkdelegation.Result, error) {
	r.continueAnchor = sdkdelegation.CloneAnchor(anchor)
	r.continuePrompt = strings.TrimSpace(req.Prompt)
	return sdkdelegation.CloneResult(r.continueResult), nil
}

func (r *recordingSubagentRunner) Wait(context.Context, sdkdelegation.Anchor, int) (sdkdelegation.Result, error) {
	return sdkdelegation.CloneResult(r.waitResult), nil
}

func (r *recordingSubagentRunner) Cancel(context.Context, sdkdelegation.Anchor) error {
	return nil
}
