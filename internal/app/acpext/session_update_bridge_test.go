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
	}, "self", "child", "/workspace", newRemoteSubagentTracker())
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

func TestACPSessionUpdateBridge_EmitsToolLifecycle(t *testing.T) {
	bridge := newACPSessionUpdateBridge(runtime.DelegationMetadata{
		ParentSessionID: "parent",
		ChildSessionID:  "child",
		ParentToolCall:  "call-spawn-1",
		ParentToolName:  tool.SpawnToolName,
		DelegationID:    "dlg-1",
	}, "self", "child", "/workspace", newRemoteSubagentTracker())
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
	}, "self", "child", "/workspace", tracker)

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

func strPtr(value string) *string { return &value }
