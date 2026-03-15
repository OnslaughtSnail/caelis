package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

// --- Unit tests for loopDetector and turnSignature ---

func TestLoopDetector_NoLoop_DifferentTextEachTurn(t *testing.T) {
	var d loopDetector
	for i := 0; i < 10; i++ {
		events := []*session.Event{
			{Message: model.Message{Role: model.RoleAssistant, Text: fmt.Sprintf("response %d", i)}},
		}
		if d.observeTurn(events) {
			t.Fatalf("false positive: detected loop on turn %d with different text", i)
		}
	}
}

func TestLoopDetector_DetectsIdenticalTextLoop(t *testing.T) {
	var d loopDetector
	events := []*session.Event{
		{Message: model.Message{Role: model.RoleAssistant, Text: "I'm stuck"}},
	}
	for i := 0; i < loopDetectorThreshold; i++ {
		detected := d.observeTurn(events)
		if i < loopDetectorThreshold-1 && detected {
			t.Fatalf("premature loop detection on turn %d", i)
		}
		if i == loopDetectorThreshold-1 && !detected {
			t.Fatal("expected loop detection on final turn")
		}
	}
}

func TestLoopDetector_DetectsIdenticalToolCallLoop(t *testing.T) {
	var d loopDetector
	args, _ := json.Marshal(map[string]any{"command": "ls -la"})
	events := []*session.Event{
		{Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "BASH", Args: string(args)},
			},
		}},
		{Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID: "call_1", Name: "BASH",
				Result: map[string]any{"output": "file1.txt\nfile2.txt"},
			},
		}},
	}
	for i := 0; i < loopDetectorThreshold; i++ {
		detected := d.observeTurn(events)
		if i == loopDetectorThreshold-1 && !detected {
			t.Fatal("expected loop detection for identical tool calls")
		}
	}
}

func TestLoopDetector_NoFalsePositive_SameToolDifferentArgs(t *testing.T) {
	// Reading the same file but different line ranges is legitimate
	var d loopDetector
	for i := 0; i < 10; i++ {
		args, _ := json.Marshal(map[string]any{
			"file":       "main.go",
			"start_line": i * 50,
			"end_line":   (i + 1) * 50,
		})
		events := []*session.Event{
			{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: fmt.Sprintf("call_%d", i), Name: "READ", Args: string(args)},
				},
			}},
			{Message: model.Message{
				Role: model.RoleTool,
				ToolResponse: &model.ToolResponse{
					ID: fmt.Sprintf("call_%d", i), Name: "READ",
					Result: map[string]any{"content": fmt.Sprintf("lines %d-%d", i*50, (i+1)*50)},
				},
			}},
		}
		if d.observeTurn(events) {
			t.Fatalf("false positive on turn %d: same tool but different args should not trigger", i)
		}
	}
}

func TestLoopDetector_NoFalsePositive_DifferentToolResults(t *testing.T) {
	// Same tool call args but different results (e.g. polling a changing status)
	var d loopDetector
	args, _ := json.Marshal(map[string]any{"task_id": "t-123"})
	for i := 0; i < 10; i++ {
		events := []*session.Event{
			{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: "call_1", Name: "TASK", Args: string(args)},
				},
			}},
			{Message: model.Message{
				Role: model.RoleTool,
				ToolResponse: &model.ToolResponse{
					ID: "call_1", Name: "TASK",
					Result: map[string]any{"status": fmt.Sprintf("progress_%d", i)},
				},
			}},
		}
		if d.observeTurn(events) {
			t.Fatalf("false positive on turn %d: same args but different results should not trigger", i)
		}
	}
}

func TestLoopDetector_IgnoresStableTaskWaitPolling(t *testing.T) {
	var d loopDetector
	args, _ := json.Marshal(map[string]any{"action": "wait", "task_id": "t-123"})
	events := []*session.Event{
		{Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "call_1", Name: "TASK", Args: string(args)},
			},
		}},
		{Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID: "call_1", Name: "TASK",
				Result: map[string]any{"task_id": "t-123", "running": true, "state": "running"},
			},
		}},
	}
	for i := 0; i < loopDetectorThreshold+2; i++ {
		if d.observeTurn(events) {
			t.Fatalf("false positive on poll %d: running TASK wait should not trigger loop detection", i)
		}
	}
}

func TestLoopDetector_NoFalsePositive_SameToolDifferentFiles(t *testing.T) {
	// Reading different files is legitimate
	var d loopDetector
	files := []string{"main.go", "util.go", "handler.go", "config.go", "test.go"}
	for i, file := range files {
		args, _ := json.Marshal(map[string]any{"file": file})
		events := []*session.Event{
			{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: fmt.Sprintf("call_%d", i), Name: "READ", Args: string(args)},
				},
			}},
			{Message: model.Message{
				Role: model.RoleTool,
				ToolResponse: &model.ToolResponse{
					ID: fmt.Sprintf("call_%d", i), Name: "READ",
					Result: map[string]any{"content": "package main"},
				},
			}},
		}
		if d.observeTurn(events) {
			t.Fatalf("false positive on turn %d: different file reads should not trigger", i)
		}
	}
}

func TestLoopDetector_ResetOnDifferentTurn(t *testing.T) {
	var d loopDetector
	events := []*session.Event{
		{Message: model.Message{Role: model.RoleAssistant, Text: "same"}},
	}
	// Observe twice (almost at threshold)
	d.observeTurn(events)
	d.observeTurn(events)
	// Different turn resets the counter
	different := []*session.Event{
		{Message: model.Message{Role: model.RoleAssistant, Text: "different"}},
	}
	d.observeTurn(different)
	// Now repeat the first events — should NOT trigger because counter was reset
	d.observeTurn(events)
	if d.observeTurn(events) {
		t.Fatal("expected no loop detection after counter reset")
	}
}

func TestLoopDetector_ResetMethod(t *testing.T) {
	var d loopDetector
	events := []*session.Event{
		{Message: model.Message{Role: model.RoleAssistant, Text: "same"}},
	}
	d.observeTurn(events)
	d.observeTurn(events)
	d.reset()
	// After reset, counter restarts — need full threshold again
	d.observeTurn(events)
	if d.observeTurn(events) {
		t.Fatal("expected no loop detection: only 2 turns after reset, below threshold")
	}
	// Third turn after reset reaches threshold
	if !d.observeTurn(events) {
		t.Fatal("expected loop detection on third turn after reset")
	}
}

func TestLoopDetector_EmptyTurnIgnored(t *testing.T) {
	var d loopDetector
	if d.observeTurn(nil) {
		t.Fatal("expected no loop for nil events")
	}
	if d.observeTurn([]*session.Event{}) {
		t.Fatal("expected no loop for empty events")
	}
}

func TestLoopDetector_ConsidersReasoning(t *testing.T) {
	var d loopDetector
	// Same text but different reasoning is not a loop
	for i := 0; i < 10; i++ {
		events := []*session.Event{
			{Message: model.Message{
				Role:      model.RoleAssistant,
				Text:      "same text",
				Reasoning: fmt.Sprintf("reasoning %d", i),
			}},
		}
		if d.observeTurn(events) {
			t.Fatalf("false positive on turn %d: different reasoning should produce different signature", i)
		}
	}
}

func TestTurnSignature_ArgOrderIndependent(t *testing.T) {
	args1, _ := json.Marshal(map[string]any{"a": 1, "b": 2})
	args2, _ := json.Marshal(map[string]any{"b": 2, "a": 1})
	events1 := []*session.Event{
		{Message: model.Message{
			Role:      model.RoleAssistant,
			ToolCalls: []model.ToolCall{{Name: "TOOL", Args: string(args1)}},
		}},
	}
	events2 := []*session.Event{
		{Message: model.Message{
			Role:      model.RoleAssistant,
			ToolCalls: []model.ToolCall{{Name: "TOOL", Args: string(args2)}},
		}},
	}
	sig1 := turnSignature(events1)
	sig2 := turnSignature(events2)
	if sig1 != sig2 {
		t.Fatalf("expected same signature for different JSON key order, got %q vs %q", sig1, sig2)
	}
}

func TestTurnSignature_DifferentArgsProduceDifferentSig(t *testing.T) {
	args1, _ := json.Marshal(map[string]any{"file": "a.go", "line": 1})
	args2, _ := json.Marshal(map[string]any{"file": "a.go", "line": 100})
	sig1 := turnSignature([]*session.Event{
		{Message: model.Message{
			Role:      model.RoleAssistant,
			ToolCalls: []model.ToolCall{{Name: "READ", Args: string(args1)}},
		}},
	})
	sig2 := turnSignature([]*session.Event{
		{Message: model.Message{
			Role:      model.RoleAssistant,
			ToolCalls: []model.ToolCall{{Name: "READ", Args: string(args2)}},
		}},
	})
	if sig1 == sig2 {
		t.Fatal("expected different signatures for different tool args")
	}
}

// --- Integration test: loop detection terminates a stuck runner ---

type loopingAgent struct {
	turnCount int
}

func (a *loopingAgent) Name() string { return "looping-agent" }
func (a *loopingAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		// Infinite loop: always produces the same assistant → tool → tool-response cycle
		args, _ := json.Marshal(map[string]any{"command": "echo hello"})
		for {
			a.turnCount++
			// Assistant with tool call
			if !yield(&session.Event{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: "call_1", Name: "BASH", Args: string(args)},
				},
			}}, nil) {
				return
			}
			// Tool response
			if !yield(&session.Event{Message: model.Message{
				Role: model.RoleTool,
				ToolResponse: &model.ToolResponse{
					ID: "call_1", Name: "BASH",
					Result: map[string]any{"output": "hello"},
				},
			}}, nil) {
				return
			}
		}
	}
}

func TestRuntime_LoopDetection_TerminatesStuckAgent(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	ag := &loopingAgent{}
	var gotLoopErr bool
	for _, runErr := range runEvents(t, rt, context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-loop",
		Input:     "hello",
		Agent:     ag,
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			if errors.Is(runErr, errLoopDetected) {
				gotLoopErr = true
				break
			}
			t.Fatalf("unexpected error: %v", runErr)
		}
	}
	if !gotLoopErr {
		t.Fatal("expected loop detection to terminate the stuck agent")
	}
	// The agent should have run exactly loopDetectorThreshold turns
	if ag.turnCount != loopDetectorThreshold {
		t.Fatalf("expected %d turns before loop detection, got %d", loopDetectorThreshold, ag.turnCount)
	}
}

// varyingToolArgsAgent calls the same tool with different args each turn
type varyingToolArgsAgent struct {
	turn int
}

func (a *varyingToolArgsAgent) Name() string { return "varying-args-agent" }
func (a *varyingToolArgsAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		for a.turn < loopDetectorThreshold+2 {
			a.turn++
			args, _ := json.Marshal(map[string]any{
				"file":       "main.go",
				"start_line": a.turn * 10,
				"end_line":   a.turn*10 + 10,
			})
			if !yield(&session.Event{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{
					{ID: fmt.Sprintf("call_%d", a.turn), Name: "READ", Args: string(args)},
				},
			}}, nil) {
				return
			}
			if !yield(&session.Event{Message: model.Message{
				Role: model.RoleTool,
				ToolResponse: &model.ToolResponse{
					ID: fmt.Sprintf("call_%d", a.turn), Name: "READ",
					Result: map[string]any{"content": fmt.Sprintf("lines %d-%d", a.turn*10, a.turn*10+10)},
				},
			}}, nil) {
				return
			}
		}
		// Final response after reading
		yield(&session.Event{Message: model.Message{
			Role: model.RoleAssistant, Text: "done reading",
		}}, nil)
	}
}

func TestRuntime_LoopDetection_NoFalsePositiveForVaryingArgs(t *testing.T) {
	store := inmemory.New()
	rt, err := New(Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	ag := &varyingToolArgsAgent{}
	var gotLoopErr bool
	for _, runErr := range runEvents(t, rt, context.Background(), RunRequest{
		AppName:   "app",
		UserID:    "u",
		SessionID: "s-no-false-pos",
		Input:     "read the file",
		Agent:     ag,
		Model:     llm,
		CoreTools: tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
	}) {
		if runErr != nil {
			if errors.Is(runErr, errLoopDetected) {
				gotLoopErr = true
			}
			break
		}
	}
	if gotLoopErr {
		t.Fatal("false positive: loop detection fired for agent with varying tool args")
	}
}
