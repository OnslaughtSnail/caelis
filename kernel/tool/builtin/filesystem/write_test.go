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

func TestWriteTool_CreateFileWithoutReadEvidence(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "new.txt")

	tool := NewWrite()
	out, err := tool.Run(context.Background(), map[string]any{
		"path":    path,
		"content": "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, _ := out["created"].(bool)
	if !created {
		t.Fatalf("expected created=true, got %v", out["created"])
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("unexpected content: %q", string(content))
	}
}

func TestWriteTool_RequiresReadForExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "existing.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewWrite()
	_, err := tool.Run(context.Background(), map[string]any{
		"path":    path,
		"content": "new",
	})
	if err == nil {
		t.Fatal("expected permission denied")
	}
	if !strings.Contains(err.Error(), "requires prior READ") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWriteTool_OverwriteAfterReadEvidence(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "existing.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
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

	tool := NewWrite()
	out, err := tool.Run(ctx, map[string]any{
		"path":    path,
		"content": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, _ := out["created"].(bool)
	if created {
		t.Fatalf("expected created=false, got %v", out["created"])
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "" {
		t.Fatalf("expected empty content, got %q", string(content))
	}
}
