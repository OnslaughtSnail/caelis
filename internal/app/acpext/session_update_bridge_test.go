package acpext

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

func TestACPSessionUpdateBridge_EmitsAssistantStream(t *testing.T) {
	bridge := newACPSessionUpdateBridge(runtime.DelegationMetadata{
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		ParentToolCall:  "call-spawn-1",
		ParentToolName:  tool.SpawnToolName,
		DelegationID:    "dlg-1",
	}, "self", "child", "/workspace", newRemoteSubagentTracker(), nil, nil)
	var (
		mu      sync.Mutex
		updates []sessionstream.Update
	)
	ctx := sessionstream.WithStreamer(context.Background(), sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
	}))

	bridge.Emit(ctx, acpclient.UpdateEnvelope{
		SessionID: "child",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalRaw(acpclient.TextChunk{Type: "text", Text: "hello"}),
		},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	ev := updates[0].Event
	if ev == nil || strings.TrimSpace(ev.Message.TextContent()) != "hello" {
		t.Fatalf("expected bridged assistant text, got %+v", ev)
	}
	meta, ok := runtime.DelegationMetadataFromEvent(ev)
	if !ok || meta.ParentToolName != tool.SpawnToolName {
		t.Fatalf("expected delegated SPAWN metadata, got %+v", ev.Meta)
	}
}

func TestACPSessionUpdateBridge_EmitsDeltaChunks(t *testing.T) {
	bridge := newACPSessionUpdateBridge(runtime.DelegationMetadata{
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		ParentToolCall:  "call-spawn-1",
		ParentToolName:  tool.SpawnToolName,
		DelegationID:    "dlg-1",
	}, "self", "child", "/workspace", newRemoteSubagentTracker(), nil, nil)
	var (
		mu      sync.Mutex
		updates []sessionstream.Update
	)
	ctx := sessionstream.WithStreamer(context.Background(), sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
	}))

	for _, chunk := range []string{"alpha", " beta"} {
		bridge.Emit(ctx, acpclient.UpdateEnvelope{
			SessionID: "child",
			Update: acpclient.ContentChunk{
				SessionUpdate: acpclient.UpdateAgentMessage,
				Content:       mustMarshalRaw(acpclient.TextChunk{Type: "text", Text: chunk}),
			},
		})
	}

	mu.Lock()
	defer mu.Unlock()
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}
	if got := updates[0].Event.Message.TextContent(); got != "alpha" {
		t.Fatalf("expected first delta chunk, got %q", got)
	}
	if got := updates[1].Event.Message.TextContent(); got != " beta" {
		t.Fatalf("expected second delta chunk, got %q", got)
	}
}

func TestACPSessionUpdateBridge_EmitsToolLifecycle(t *testing.T) {
	bridge := newACPSessionUpdateBridge(runtime.DelegationMetadata{
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		ParentToolCall:  "call-spawn-1",
		ParentToolName:  tool.SpawnToolName,
		DelegationID:    "dlg-1",
	}, "self", "child", "/workspace", newRemoteSubagentTracker(), nil, nil)
	var (
		mu      sync.Mutex
		updates []sessionstream.Update
	)
	ctx := sessionstream.WithStreamer(context.Background(), sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
	}))

	bridge.Emit(ctx, acpclient.UpdateEnvelope{
		SessionID: "child",
		Update: acpclient.ToolCall{
			SessionUpdate: acpclient.UpdateToolCall,
			ToolCallID:    "tc-1",
			Title:         "READ /tmp/demo.txt",
			Kind:          "read",
			RawInput: map[string]any{
				"path": "/tmp/demo.txt",
			},
		},
	})
	bridge.Emit(ctx, acpclient.UpdateEnvelope{
		SessionID: "child",
		Update: acpclient.ToolCallUpdate{
			SessionUpdate: acpclient.UpdateToolCallState,
			ToolCallID:    "tc-1",
			Status:        strPtr("completed"),
			RawOutput: map[string]any{
				"content": "demo",
			},
		},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}
	callEv := updates[0].Event
	if callEv == nil {
		t.Fatalf("expected bridged tool call, got nil event")
		return
	}
	calls := callEv.Message.ToolCalls()
	if len(calls) != 1 || calls[0].Name != "READ" {
		t.Fatalf("expected bridged tool call, got %+v", callEv)
	}
	if !strings.Contains(calls[0].Args, `"READ /tmp/demo.txt"`) {
		t.Fatalf("expected ACP title metadata preserved in tool args, got %q", calls[0].Args)
	}
	respEv := updates[1].Event
	if respEv == nil {
		t.Fatalf("expected bridged tool response, got nil event")
		return
	}
	resp := respEv.Message.ToolResponse()
	if resp == nil || resp.Name != "READ" {
		t.Fatalf("expected bridged tool response, got %+v", respEv)
	}
	raw, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(raw), "demo") {
		t.Fatalf("expected bridged tool output, got %s", raw)
	}
}

func TestACPSessionUpdateBridge_DoesNotPersistChildHistory(t *testing.T) {
	store := inmemory.New()
	tracker := newRemoteSubagentTracker()
	bridge := newACPSessionUpdateBridge(runtime.DelegationMetadata{
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		ParentToolCall:  "call-spawn-1",
		ParentToolName:  tool.SpawnToolName,
		DelegationID:    "dlg-1",
	}, "self", "child", "/workspace", tracker, nil, nil)

	ctx := context.Background()
	bridge.Emit(ctx, acpclient.UpdateEnvelope{
		SessionID: "child",
		Update: acpclient.ToolCall{
			SessionUpdate: acpclient.UpdateToolCall,
			ToolCallID:    "tc-1",
			Title:         "READ /tmp/demo.txt",
			Kind:          "read",
			RawInput:      map[string]any{"path": "/tmp/demo.txt"},
		},
	})
	bridge.Emit(ctx, acpclient.UpdateEnvelope{
		SessionID: "child",
		Update: acpclient.ToolCallUpdate{
			SessionUpdate: acpclient.UpdateToolCallState,
			ToolCallID:    "tc-1",
			Status:        strPtr("completed"),
			RawOutput:     map[string]any{"content": "demo"},
		},
	})
	bridge.Emit(ctx, acpclient.UpdateEnvelope{
		SessionID: "child",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalRaw(acpclient.TextChunk{Type: "text", Text: "hello"}),
		},
	})
	bridge.FlushAssistant(ctx)

	events, err := store.ListEvents(ctx, &session.Session{AppName: "app", UserID: "u", ID: "child"})
	if err != nil && !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no locally persisted child events, got %d", len(events))
	}
	state, ok := tracker.inspect("self", "child")
	if !ok {
		t.Fatal("expected in-memory tracker state")
	}
	if got := strings.TrimSpace(state.Assistant); got != "hello" {
		t.Fatalf("expected tracked assistant text, got %q", got)
	}
}

func TestACPSessionUpdateBridge_EmitsInProgressToolUpdate(t *testing.T) {
	tracker := newRemoteSubagentTracker()
	bridge := newACPSessionUpdateBridge(runtime.DelegationMetadata{
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		ParentToolCall:  "call-spawn-1",
		ParentToolName:  tool.SpawnToolName,
		DelegationID:    "dlg-1",
	}, "self", "child", "/workspace", tracker, nil, nil)

	var (
		mu      sync.Mutex
		updates []sessionstream.Update
	)
	ctx := sessionstream.WithStreamer(context.Background(), sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
	}))

	bridge.Emit(ctx, acpclient.UpdateEnvelope{
		SessionID: "child",
		Update: acpclient.ToolCall{
			SessionUpdate: acpclient.UpdateToolCall,
			ToolCallID:    "tc-bash-1",
			Title:         "BASH python long_job.py",
			Kind:          "execute",
			RawInput:      map[string]any{"command": "python long_job.py"},
		},
	})
	bridge.Emit(ctx, acpclient.UpdateEnvelope{
		SessionID: "child",
		Update: acpclient.ToolCallUpdate{
			SessionUpdate: acpclient.UpdateToolCallState,
			ToolCallID:    "tc-bash-1",
			Status:        strPtr("in_progress"),
			RawOutput: map[string]any{
				"state":         "running",
				"task_id":       "task-bash-1",
				"latest_output": "[10s] heartbeat 1/6",
			},
		},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(updates) != 2 {
		t.Fatalf("expected call + in-progress response, got %d", len(updates))
	}
	resp := updates[1].Event.Message.ToolResponse()
	if resp == nil || resp.ID != "tc-bash-1" {
		t.Fatalf("expected in-progress tool response, got %#v", updates[1].Event)
	}
	if got := resp.Result["latest_output"]; got != "[10s] heartbeat 1/6" {
		t.Fatalf("expected running output preview preserved, got %#v", resp.Result)
	}
}

func TestACPSessionUpdateBridge_TracksPendingToolCallsAndHooks(t *testing.T) {
	tracker := newRemoteSubagentTracker()
	var pauses, resumes int
	bridge := newACPSessionUpdateBridge(runtime.DelegationMetadata{
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		ParentToolCall:  "call-spawn-1",
		ParentToolName:  tool.SpawnToolName,
		DelegationID:    "dlg-1",
	}, "self", "child", "/workspace", tracker, func() { pauses++ }, func() { resumes++ })

	ctx := context.Background()
	bridge.Emit(ctx, acpclient.UpdateEnvelope{
		SessionID: "child",
		Update: acpclient.ToolCall{
			SessionUpdate: acpclient.UpdateToolCall,
			ToolCallID:    "tc-1",
			Title:         "FIND /workspace",
			Kind:          "search",
		},
	})
	state, ok := tracker.inspect("self", "child")
	if !ok || !state.ToolCallPending {
		t.Fatalf("expected pending tool call state, got %#v", state)
	}
	if pauses != 1 || resumes != 0 {
		t.Fatalf("expected one pause and no resume after tool start, got pauses=%d resumes=%d", pauses, resumes)
	}

	bridge.Emit(ctx, acpclient.UpdateEnvelope{
		SessionID: "child",
		Update: acpclient.ToolCallUpdate{
			SessionUpdate: acpclient.UpdateToolCallState,
			ToolCallID:    "tc-1",
			Status:        strPtr("completed"),
		},
	})
	state, ok = tracker.inspect("self", "child")
	if !ok || state.ToolCallPending {
		t.Fatalf("expected tool call pending flag to clear, got %#v", state)
	}
	if pauses != 1 || resumes != 1 {
		t.Fatalf("expected one resume after last tool completed, got pauses=%d resumes=%d", pauses, resumes)
	}
}

func TestACPSessionUpdateBridge_SuppressesTerminalBackedInProgressUpdate(t *testing.T) {
	tracker := newRemoteSubagentTracker()
	bridge := newACPSessionUpdateBridge(runtime.DelegationMetadata{
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		ParentToolCall:  "call-spawn-1",
		ParentToolName:  tool.SpawnToolName,
		DelegationID:    "dlg-1",
	}, "self", "child", "/workspace", tracker, nil, nil)

	var (
		mu      sync.Mutex
		updates []sessionstream.Update
	)
	ctx := sessionstream.WithStreamer(context.Background(), sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
	}))

	bridge.Emit(ctx, acpclient.UpdateEnvelope{
		SessionID: "child",
		Update: acpclient.ToolCall{
			SessionUpdate: acpclient.UpdateToolCall,
			ToolCallID:    "tc-bash-1",
			Title:         "BASH python long_job.py",
			Kind:          "execute",
			RawInput:      map[string]any{"command": "python long_job.py"},
		},
	})
	bridge.Emit(ctx, acpclient.UpdateEnvelope{
		SessionID: "child",
		Update: acpclient.ToolCallUpdate{
			SessionUpdate: acpclient.UpdateToolCallState,
			ToolCallID:    "tc-bash-1",
			Status:        strPtr("in_progress"),
			Content: []acpclient.ToolCallContent{{
				Type:       "terminal",
				TerminalID: "term-child-1",
			}},
			RawOutput: map[string]any{
				"state":         "running",
				"latest_output": "suppressed preview",
			},
		},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(updates) != 1 {
		t.Fatalf("expected only initial tool call event when terminal bridge will take over, got %d", len(updates))
	}
}

func strPtr(value string) *string { return &value }
