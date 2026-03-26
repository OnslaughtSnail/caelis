package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTool_CreateFileWithoutReadEvidence(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "new.txt")

	tool, err := NewWriteWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
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
	previousEmpty, _ := out["previous_empty"].(bool)
	if !previousEmpty {
		t.Fatalf("expected previous_empty=true, got %v", out["previous_empty"])
	}
	if out["added_lines"] != 1 || out["removed_lines"] != 0 {
		t.Fatalf("expected +1 -0 stats, got +%v -%v", out["added_lines"], out["removed_lines"])
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("unexpected content: %q", string(content))
	}
}

func TestWriteTool_OverwriteExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "existing.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool, err := NewWriteWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{
		"path":    path,
		"content": "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, _ := out["created"].(bool)
	if created {
		t.Fatalf("expected created=false, got %v", out["created"])
	}
	previousEmpty, _ := out["previous_empty"].(bool)
	if previousEmpty {
		t.Fatalf("expected previous_empty=false, got %v", out["previous_empty"])
	}
	if out["added_lines"] != 1 || out["removed_lines"] != 1 {
		t.Fatalf("expected +1 -1 stats, got +%v -%v", out["added_lines"], out["removed_lines"])
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new" {
		t.Fatalf("expected content 'new', got %q", string(content))
	}
}
