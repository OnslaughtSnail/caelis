package acpext

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
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
	}, "child")
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
	}, "child")
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
	if callEv == nil || len(callEv.Message.ToolCalls) != 1 || callEv.Message.ToolCalls[0].Name != "READ" {
		t.Fatalf("expected bridged tool call, got %+v", callEv)
	}
	respEv := updates[1].Event
	if respEv == nil || respEv.Message.ToolResponse == nil || respEv.Message.ToolResponse.Name != "READ" {
		t.Fatalf("expected bridged tool response, got %+v", respEv)
	}
	raw, _ := json.Marshal(respEv.Message.ToolResponse.Result)
	if !strings.Contains(string(raw), "demo") {
		t.Fatalf("expected bridged tool output, got %s", raw)
	}
}

func strPtr(value string) *string { return &value }
