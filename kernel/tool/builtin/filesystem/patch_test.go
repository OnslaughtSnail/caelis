package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchTool_AppliesWithoutPriorRead(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "a.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewPatch()
	out, err := tool.Run(context.Background(), map[string]any{
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
}

func TestPatchTool_AppliesSingleReplacement(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "b.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewPatch()
	out, err := tool.Run(context.Background(), map[string]any{
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
	preview := patchPreviewFromResult(out)
	if !strings.Contains(preview, "--- old") || !strings.Contains(preview, "+++ new") {
		t.Fatalf("expected patch preview header, got %q", preview)
	}
	if !strings.Contains(preview, "-hello") || !strings.Contains(preview, "+world") {
		t.Fatalf("expected patch preview body, got %q", preview)
	}
	hunk := patchHunkFromResult(out)
	if hunk != "@@ -1,1 +1,1 @@" {
		t.Fatalf("expected hunk metadata, got %q", hunk)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "world\n" {
		t.Fatalf("unexpected patched content: %q", string(content))
	}
}

func TestPatchTool_AllowsEmptyOldForEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "empty.txt")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewPatch()
	out, err := tool.Run(context.Background(), map[string]any{
		"path": path,
		"old":  "",
		"new":  "hello",
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
	if string(content) != "hello" {
		t.Fatalf("unexpected content: %q", string(content))
	}
}

func TestPatchTool_CreateMissingFileWithEmptyOld(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "new.txt")

	tool := NewPatch()
	out, err := tool.Run(context.Background(), map[string]any{
		"path": path,
		"old":  "",
		"new":  "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, _ := out["created"].(bool)
	if !created {
		t.Fatalf("expected created=true, got %v", out["created"])
	}
	preview := patchPreviewFromResult(out)
	if !strings.Contains(preview, "+hello") {
		t.Fatalf("expected create preview to include new content, got %q", preview)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("unexpected content: %q", string(content))
	}
}

func patchPreviewFromResult(result map[string]any) string {
	metadata, _ := result["metadata"].(map[string]any)
	patch, _ := metadata["patch"].(map[string]any)
	preview, _ := patch["preview"].(string)
	return preview
}

func patchHunkFromResult(result map[string]any) string {
	metadata, _ := result["metadata"].(map[string]any)
	patch, _ := metadata["patch"].(map[string]any)
	hunk, _ := patch["hunk"].(string)
	return hunk
}

func TestPatchTool_MissingFileRequiresEmptyOld(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "missing.txt")
	tool := NewPatch()
	_, err := tool.Run(context.Background(), map[string]any{
		"path": path,
		"old":  "x",
		"new":  "y",
	})
	if err == nil {
		t.Fatal("expected error for missing file with non-empty old")
	}
	if !strings.Contains(err.Error(), "set \"old\" to empty string") {
		t.Fatalf("unexpected error: %v", err)
	}
}
