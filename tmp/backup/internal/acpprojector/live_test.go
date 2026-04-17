package acpprojector

import (
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
)

func TestLiveProjector_DeduplicatesCumulativeNarrativeReplay(t *testing.T) {
	projector := NewLiveProjector()
	prefix := "先列出仓库结构，然后继续说明。"
	full := prefix + "最后给出总结。"

	first := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalReplayText(prefix),
		},
	})
	if len(first) != 1 || first[0].DeltaText != prefix || first[0].FullText != prefix {
		t.Fatalf("expected first delta to pass through, got %#v", first)
	}

	second := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalReplayText(full),
		},
	})
	if len(second) != 1 || second[0].DeltaText != "最后给出总结。" || second[0].FullText != full {
		t.Fatalf("expected cumulative replay to emit only incremental suffix, got %#v", second)
	}

	third := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "child-1",
		Update: acpclient.ContentChunk{
			SessionUpdate: acpclient.UpdateAgentMessage,
			Content:       mustMarshalReplayText(full),
		},
	})
	if len(third) != 0 {
		t.Fatalf("expected identical replay to be suppressed, got %#v", third)
	}
}

func TestLiveProjector_MultiChunkStreamingAppends(t *testing.T) {
	projector := NewLiveProjector()
	chunks := []string{"我", "是 ", "caelis", "，你好世界！"}
	cumulative := ""
	for i, chunk := range chunks {
		result := projector.Project(acpclient.UpdateEnvelope{
			SessionID: "s1",
			Update: acpclient.ContentChunk{
				SessionUpdate: acpclient.UpdateAgentMessage,
				Content:       mustMarshalReplayText(chunk),
			},
		})
		cumulative += chunk
		if len(result) != 1 {
			t.Fatalf("chunk %d: expected 1 projection, got %d", i, len(result))
		}
		if result[0].DeltaText != chunk {
			t.Fatalf("chunk %d: expected delta=%q, got %q", i, chunk, result[0].DeltaText)
		}
		if result[0].FullText != cumulative {
			t.Fatalf("chunk %d: expected full=%q, got %q", i, cumulative, result[0].FullText)
		}
	}
}

func TestLiveProjector_ToolCallAndResult(t *testing.T) {
	projector := NewLiveProjector()

	// Tool call arrives
	callResult := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "s1",
		Update: acpclient.ToolCall{
			ToolCallID: "call-1",
			Title:      "READ main.go",
			Kind:       "read",
			RawInput:   map[string]any{"path": "main.go"},
		},
	})
	if len(callResult) != 1 {
		t.Fatalf("expected 1 tool call projection, got %d", len(callResult))
	}
	if callResult[0].ToolCallID != "call-1" || callResult[0].ToolName != "READ" {
		t.Fatalf("unexpected tool call: %+v", callResult[0])
	}

	// Tool result arrives
	doneStatus := "completed"
	resultResult := projector.Project(acpclient.UpdateEnvelope{
		SessionID: "s1",
		Update: acpclient.ToolCallUpdate{
			ToolCallID: "call-1",
			Status:     &doneStatus,
			RawOutput:  map[string]any{"content": "package main"},
		},
	})
	if len(resultResult) != 1 {
		t.Fatalf("expected 1 tool result projection, got %d", len(resultResult))
	}
	if resultResult[0].ToolStatus != "completed" || resultResult[0].ToolCallID != "call-1" {
		t.Fatalf("unexpected tool result: %+v", resultResult[0])
	}
}

func TestLiveProjector_AppendNarrativeChunkPrefixDedup(t *testing.T) {
	tests := []struct {
		name        string
		existing    string
		incoming    string
		wantNext    string
		wantDelta   string
		wantChanged bool
	}{
		{"empty to first chunk", "", "hello", "hello", "hello", true},
		{"cumulative extension", "hello", "hello world", "hello world", " world", true},
		{"identical suppressed", "hello", "hello", "hello", "", false},
		{"stale shorter suppressed", "hello world", "hello", "hello world", "", false},
		{"disjoint appends", "hello", " world", "hello world", " world", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, delta, changed := appendNarrativeChunk(tt.existing, tt.incoming)
			if next != tt.wantNext || delta != tt.wantDelta || changed != tt.wantChanged {
				t.Errorf("appendNarrativeChunk(%q, %q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.existing, tt.incoming, next, delta, changed, tt.wantNext, tt.wantDelta, tt.wantChanged)
			}
		})
	}
}
