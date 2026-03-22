package session

import (
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

func TestNewEvents_DeepCopyIsolation(t *testing.T) {
	meta := map[string]any{
		"key":    "original",
		"nested": map[string]any{"inner": "v1"},
	}
	src := &Event{
		ID:   "ev-1",
		Time: time.Now(),
		Message: model.Message{
			Role: model.RoleAssistant,
			Text: "hello",
			ToolCalls: []model.ToolCall{
				{ID: "tc-1", Name: "READ", Args: `{"path":"a.txt"}`},
			},
		},
		Meta: meta,
	}
	view := NewEvents([]*Event{src})

	// Mutate source Meta — view must not see changes.
	src.Meta["key"] = "mutated"
	src.Meta["nested"].(map[string]any)["inner"] = "mutated"
	src.Message.ToolCalls[0].Name = "WRITE"

	got := view.At(0)
	if got == nil {
		t.Fatal("At(0) returned nil")
	}
	if got.Meta["key"] != "original" {
		t.Fatalf("view.Meta['key'] = %v, want 'original'", got.Meta["key"])
	}
	nested, ok := got.Meta["nested"].(map[string]any)
	if !ok || nested["inner"] != "v1" {
		t.Fatalf("view.Meta['nested']['inner'] = %v, want 'v1'", nested["inner"])
	}
	if got.Message.ToolCalls[0].Name != "READ" {
		t.Fatalf("view.ToolCalls[0].Name = %v, want 'READ'", got.Message.ToolCalls[0].Name)
	}
}

func TestNewEvents_AtReturnsIndependentCopies(t *testing.T) {
	src := &Event{
		ID:   "ev-1",
		Time: time.Now(),
		Message: model.Message{
			Role: model.RoleAssistant,
			Text: "hello",
		},
		Meta: map[string]any{"key": "original"},
	}
	view := NewEvents([]*Event{src})

	// Two calls to At() should return independent objects.
	a := view.At(0)
	b := view.At(0)
	a.Meta["key"] = "mutated-a"
	if b.Meta["key"] != "original" {
		t.Fatalf("second At(0).Meta['key'] = %v, want 'original'", b.Meta["key"])
	}
}

func TestNewEvents_MutateViewDoesNotAffectSource(t *testing.T) {
	src := &Event{
		ID:   "ev-1",
		Time: time.Now(),
		Message: model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:     "call-1",
				Name:   "READ",
				Result: map[string]any{"output": "data", "nested": map[string]any{"a": "b"}},
			},
		},
		Meta: map[string]any{"m": "v"},
	}
	view := NewEvents([]*Event{src})

	got := view.At(0)
	got.Meta["m"] = "mutated"
	got.Message.ToolResponse.Result["output"] = "mutated"

	if src.Meta["m"] != "v" {
		t.Fatalf("source.Meta['m'] = %v, want 'v'", src.Meta["m"])
	}
	if src.Message.ToolResponse.Result["output"] != "data" {
		t.Fatalf("source.ToolResponse.Result['output'] = %v, want 'data'", src.Message.ToolResponse.Result["output"])
	}
}

func TestNewReadonlyState_DeepCopyIsolation(t *testing.T) {
	values := map[string]any{
		"flat":   "hello",
		"nested": map[string]any{"inner": "v1"},
		"list":   []any{"a", "b"},
	}
	state := NewReadonlyState(values)

	// Mutate source — state must not see changes.
	values["flat"] = "mutated"
	values["nested"].(map[string]any)["inner"] = "mutated"

	flat, ok := state.Get("flat")
	if !ok || flat != "hello" {
		t.Fatalf("state.Get('flat') = %v, want 'hello'", flat)
	}
	nested, ok := state.Get("nested")
	if !ok {
		t.Fatal("state.Get('nested') returned !ok")
	}
	m, ok := nested.(map[string]any)
	if !ok || m["inner"] != "v1" {
		t.Fatalf("state.Get('nested')['inner'] = %v, want 'v1'", m["inner"])
	}
}
