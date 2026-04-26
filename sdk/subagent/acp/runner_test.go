package acp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	sdkacpclient "github.com/OnslaughtSnail/caelis/acp/client"
	sdkdelegation "github.com/OnslaughtSnail/caelis/sdk/delegation"
	sdkstream "github.com/OnslaughtSnail/caelis/sdk/stream"
	sdksubagent "github.com/OnslaughtSnail/caelis/sdk/subagent"
)

func TestRunnerHandleUpdatePublishesChildStream(t *testing.T) {
	t.Parallel()

	sink := &recordingStreams{}
	run := &childRun{
		anchor: sdkdelegation.Anchor{
			TaskID:    "task-1",
			SessionID: "child-1",
			Agent:     "self",
			AgentID:   "self-1",
		},
		taskID:  "task-1",
		sink:    sink,
		state:   sdkdelegation.StateRunning,
		running: true,
	}
	runner := &Runner{clock: time.Now}
	raw, _ := json.Marshal(sdkacpclient.TextChunk{Type: "text", Text: "child output"})

	runner.handleUpdate(run, sdkacpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: sdkacpclient.ContentChunk{
			SessionUpdate: sdkacpclient.UpdateAgentMessage,
			Content:       raw,
		},
	})

	if len(sink.frames) != 1 {
		t.Fatalf("stream frames = %#v, want one frame", sink.frames)
	}
	got := sink.frames[0]
	if got.Ref.TaskID != "task-1" || got.Ref.SessionID != "child-1" || got.Text != "child output" || !got.Running {
		t.Fatalf("stream frame = %#v", got)
	}
}

func TestRunnerHandleUpdateUsesAgentMessageDeltas(t *testing.T) {
	t.Parallel()

	sink := &recordingStreams{}
	run := &childRun{
		anchor:  sdkdelegation.Anchor{TaskID: "task-1", SessionID: "child-1", Agent: "self", AgentID: "self-1"},
		taskID:  "task-1",
		sink:    sink,
		state:   sdkdelegation.StateRunning,
		running: true,
	}
	runner := &Runner{clock: time.Now}

	runner.handleUpdate(run, contentUpdate(t, sdkacpclient.UpdateUserMessage, "ignored prompt"))
	runner.handleUpdate(run, contentUpdate(t, sdkacpclient.UpdateAgentMessage, "我来按步骤"))
	runner.handleUpdate(run, contentUpdate(t, sdkacpclient.UpdateAgentMessage, "我来按步骤执行"))
	runner.handleUpdate(run, contentUpdate(t, sdkacpclient.UpdateAgentMessage, "我来按步骤执行这个任务。"))

	if got := len(sink.frames); got != 3 {
		t.Fatalf("stream frames = %#v, want three agent delta updates", sink.frames)
	}
	var rendered string
	for _, frame := range sink.frames {
		rendered += frame.Text
	}
	if rendered != "我来按步骤执行这个任务。" {
		t.Fatalf("rendered stream = %q, want deduped final text", rendered)
	}
	run.mu.RLock()
	result := run.result
	run.mu.RUnlock()
	if result != "我来按步骤执行这个任务。" {
		t.Fatalf("run.result = %q, want deduped final text", result)
	}
}

func TestRunnerHandleUpdateAcceptsStringContentChunks(t *testing.T) {
	t.Parallel()

	sink := &recordingStreams{}
	run := &childRun{
		anchor:  sdkdelegation.Anchor{TaskID: "task-1", SessionID: "child-1", Agent: "copilot", AgentID: "copilot-1"},
		taskID:  "task-1",
		sink:    sink,
		state:   sdkdelegation.StateRunning,
		running: true,
	}
	runner := &Runner{clock: time.Now}
	raw, err := json.Marshal("string chunk")
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	runner.handleUpdate(run, sdkacpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: sdkacpclient.ContentChunk{
			SessionUpdate: sdkacpclient.UpdateAgentMessage,
			Content:       raw,
		},
	})

	if got := len(sink.frames); got != 1 {
		t.Fatalf("stream frames = %#v, want one string-content frame", sink.frames)
	}
	if got := sink.frames[0].Text; got != "string chunk" {
		t.Fatalf("stream frame text = %q, want string chunk", got)
	}
	run.mu.RLock()
	result := run.result
	run.mu.RUnlock()
	if result != "string chunk" {
		t.Fatalf("run.result = %q, want string chunk", result)
	}
}

func TestRunnerSpawnChildSurvivesCallerContextCancelAfterYield(t *testing.T) {
	repo := repoRootForRunnerTest(t)
	root := t.TempDir()
	childBin := filepath.Join(t.TempDir(), "e2eagent")
	build := exec.Command("go", "build", "-o", childBin, "./acpbridge/cmd/e2eagent")
	build.Dir = repo
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build e2eagent: %v\n%s", err, string(output))
	}
	registry, err := NewRegistry([]AgentConfig{{
		Name:        "self",
		Description: "self child",
		Command:     childBin,
		WorkDir:     repo,
		Env: map[string]string{
			"SDK_ACP_STUB_REPLY":    "child survived caller cancel",
			"SDK_ACP_STUB_DELAY_MS": "150",
			"SDK_ACP_SESSION_ROOT":  filepath.Join(root, "child-sessions"),
			"SDK_ACP_TASK_ROOT":     filepath.Join(root, "child-tasks"),
		},
	}})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	runner, err := NewRunner(RunnerConfig{Registry: registry})
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	anchor, result, err := runner.Spawn(ctx, sdksubagent.SpawnContext{
		TaskID: "task-cancel",
		CWD:    t.TempDir(),
	}, sdkdelegation.Request{
		Agent:  "self",
		Prompt: "Reply exactly: child survived caller cancel",
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	if !result.Running {
		t.Fatalf("Spawn() result = %+v, want yielded running task", result)
	}
	cancel()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer waitCancel()
	result, err = runner.Wait(waitCtx, anchor, 10_000)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if result.Running || result.State != sdkdelegation.StateCompleted {
		t.Fatalf("Wait() result = %+v, want completed child", result)
	}
	if result.Result != "child survived caller cancel" {
		t.Fatalf("Wait() result text = %q, want child reply", result.Result)
	}
}

func contentUpdate(t *testing.T, kind string, text string) sdkacpclient.UpdateEnvelope {
	t.Helper()
	raw, err := json.Marshal(sdkacpclient.TextChunk{Type: "text", Text: text})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return sdkacpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: sdkacpclient.ContentChunk{
			SessionUpdate: kind,
			Content:       raw,
		},
	}
}

type recordingStreams struct {
	frames []sdkstream.Frame
}

func (s *recordingStreams) PublishStream(frame sdkstream.Frame) {
	s.frames = append(s.frames, sdkstream.CloneFrame(frame))
}

func repoRootForRunnerTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root")
		}
		dir = parent
	}
}
