package task

import (
	"context"
	"testing"
)

func TestWithManager_NilContextReturnsBackground(t *testing.T) {
	var nilCtx context.Context
	ctx := WithManager(nilCtx, nil)
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}
	if ctx != context.Background() {
		t.Fatalf("expected background context, got %#v", ctx)
	}
}

func TestCloneEntry_DeepCopiesNestedMapsAndSlices(t *testing.T) {
	entry := &Entry{
		Spec: map[string]any{
			"nested": map[string]any{"value": "original"},
			"list":   []any{"a", map[string]any{"inner": "v1"}},
		},
		Result: map[string]any{
			"nested": map[string]any{"value": "result"},
		},
	}

	cloned := CloneEntry(entry)
	entry.Spec["nested"].(map[string]any)["value"] = "mutated"
	entry.Spec["list"].([]any)[1].(map[string]any)["inner"] = "mutated"
	entry.Result["nested"].(map[string]any)["value"] = "mutated"

	if got := cloned.Spec["nested"].(map[string]any)["value"]; got != "original" {
		t.Fatalf("cloned nested spec = %v, want original", got)
	}
	if got := cloned.Spec["list"].([]any)[1].(map[string]any)["inner"]; got != "v1" {
		t.Fatalf("cloned nested list item = %v, want v1", got)
	}
	if got := cloned.Result["nested"].(map[string]any)["value"]; got != "result" {
		t.Fatalf("cloned nested result = %v, want result", got)
	}
}

func TestRecordSnapshot_DeepCopiesNestedResult(t *testing.T) {
	record := &Record{
		Result: map[string]any{
			"nested": map[string]any{"value": "original"},
			"list":   []any{map[string]any{"inner": "v1"}},
		},
	}

	snapshot := record.Snapshot(Output{})
	record.Result["nested"].(map[string]any)["value"] = "mutated"
	record.Result["list"].([]any)[0].(map[string]any)["inner"] = "mutated"

	if got := snapshot.Result["nested"].(map[string]any)["value"]; got != "original" {
		t.Fatalf("snapshot nested result = %v, want original", got)
	}
	if got := snapshot.Result["list"].([]any)[0].(map[string]any)["inner"]; got != "v1" {
		t.Fatalf("snapshot nested list item = %v, want v1", got)
	}
}
