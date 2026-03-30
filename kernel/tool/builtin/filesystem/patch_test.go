package filesystem

import (
	"context"
	"errors"
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

	tool, err := NewPatchWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
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
	if previousEmpty, _ := out["previous_empty"].(bool); previousEmpty {
		t.Fatalf("expected previous_empty=false, got %v", out["previous_empty"])
	}
}

func TestPatchTool_AppliesSingleReplacement(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "b.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool, err := NewPatchWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
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
	if previousEmpty, _ := out["previous_empty"].(bool); previousEmpty {
		t.Fatalf("expected previous_empty=false, got %v", out["previous_empty"])
	}
	if out["added_lines"] != 1 || out["removed_lines"] != 1 {
		t.Fatalf("expected +1 -1 stats, got +%v -%v", out["added_lines"], out["removed_lines"])
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "world\n" {
		t.Fatalf("unexpected patched content: %q", string(content))
	}
}

func TestPatchTool_ReportsInsertedLinesWithoutPhantomRemovals(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "insert.txt")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool, err := NewPatchWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{
		"path": path,
		"old":  "a\nb",
		"new":  "a\nx\nb",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["added_lines"] != 1 || out["removed_lines"] != 0 {
		t.Fatalf("expected +1 -0 stats, got +%v -%v", out["added_lines"], out["removed_lines"])
	}
}

func TestPatchTool_AllowsEmptyOldForEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "empty.txt")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tool, err := NewPatchWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
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
	if previousEmpty, _ := out["previous_empty"].(bool); !previousEmpty {
		t.Fatalf("expected previous_empty=true, got %v", out["previous_empty"])
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

	tool, err := NewPatchWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
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
	if previousEmpty, _ := out["previous_empty"].(bool); !previousEmpty {
		t.Fatalf("expected previous_empty=true, got %v", out["previous_empty"])
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("unexpected content: %q", string(content))
	}
}

func TestPatchTool_MissingFileRequiresEmptyOld(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "missing.txt")
	tool, err := NewPatchWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Run(context.Background(), map[string]any{
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

func TestPatchTool_MissingExactMatchSuggestsReadAndReplaceAll(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "mismatch.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool, err := NewPatchWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Run(context.Background(), map[string]any{
		"path": path,
		"old":  "missing",
		"new":  "replacement",
	})
	if err == nil {
		t.Fatal("expected PATCH exact-match failure")
	}
	text := err.Error()
	for _, want := range []string{path, "READ", "replace_all=true"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected PATCH error to mention %q, got %v", want, err)
		}
	}
}

func TestPatchTool_DefaultWorkspaceRuntimeRejectsGitWrites(t *testing.T) {
	rt, ws := newDefaultWorkspaceRuntime(t)
	tool, err := NewPatchWithRuntime(rt)
	if err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.Join(ws, ".git", "config")
	if err := os.MkdirAll(filepath.Dir(gitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gitPath, []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = tool.Run(context.Background(), map[string]any{
		"path": gitPath,
		"old":  "[core]",
		"new":  "[user]",
	})
	if err == nil {
		t.Fatal("expected .git patch write to be rejected")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected permission error, got %v", err)
	}
}
