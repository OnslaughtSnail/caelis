package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type patchTestContext struct {
	context.Context
	events []*session.Event
}

func (c patchTestContext) History() []*session.Event {
	out := make([]*session.Event, 0, len(c.events))
	for _, ev := range c.events {
		if ev == nil {
			continue
		}
		cp := *ev
		out = append(out, &cp)
	}
	return out
}

func TestPatchTool_RequiresPriorRead(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "a.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewPatch()
	_, err := tool.Run(context.Background(), map[string]any{
		"path": path,
		"old":  "hello",
		"new":  "world",
	})
	if err == nil {
		t.Fatal("expected permission denied without prior READ")
	}
	if !strings.Contains(err.Error(), "requires prior READ") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPatchTool_AppliesAfterReadEvidence(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "b.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	normalized, err := normalizePath(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := patchTestContext{
		Context: context.Background(),
		events: []*session.Event{
			{
				ID:   "read_1",
				Time: time.Now(),
				Message: model.Message{
					Role: model.RoleTool,
					ToolResponse: &model.ToolResponse{
						ID:   "call_read_1",
						Name: ReadToolName,
						Result: map[string]any{
							"path": normalized,
						},
					},
				},
			},
		},
	}

	tool := NewPatch()
	out, err := tool.Run(ctx, map[string]any{
		"path": path,
		"old":  "hello",
		"new":  "world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["replaced"] != 1 {
		t.Fatalf("expected replaced=1, got %v", out["replaced"])
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "world\n" {
		t.Fatalf("unexpected patched content: %q", string(content))
	}
}
