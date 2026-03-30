package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func TestSearchTool_ReportsTruncationAndFileStats(t *testing.T) {
	tmpDir := t.TempDir()
	aPath := filepath.Join(tmpDir, "a.txt")
	bPath := filepath.Join(tmpDir, "b.txt")
	if err := os.WriteFile(aPath, []byte("hello one\nhello two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte("hello three\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool, err := NewSearchWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{
		"path":  tmpDir,
		"query": "hello",
		"limit": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != 2 {
		t.Fatalf("expected count=2, got %v", out["count"])
	}
	if out["truncated"] != true {
		t.Fatalf("expected truncated=true, got %v", out["truncated"])
	}
	fileCount, ok := out["file_count"].(int)
	if !ok || fileCount <= 0 {
		t.Fatalf("expected file_count>0, got %v", out["file_count"])
	}
}

func TestSearchTool_ReturnsColumn(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "a.txt")
	if err := os.WriteFile(path, []byte("xxHELLOyy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool, err := NewSearchWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{
		"path":           path,
		"query":          "hello",
		"case_sensitive": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	hits, ok := out["hits"].([]map[string]any)
	if !ok {
		t.Fatalf("expected hits array, got %T", out["hits"])
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	column, ok := hits[0]["column"].(int)
	if !ok || column != 3 {
		t.Fatalf("expected column=3, got %v", hits[0]["column"])
	}
}

func TestSearchTool_RespectsGitignore(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte("ignored.txt\nignored/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "visible.txt"), []byte("hello visible\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "ignored.txt"), []byte("hello hidden\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tmpDir, "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "ignored", "nested.txt"), []byte("hello nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool, err := NewSearchWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{
		"path":  tmpDir,
		"query": "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != 1 {
		t.Fatalf("expected only visible match, got %v", out["count"])
	}
	hits, ok := out["hits"].([]map[string]any)
	if !ok || len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %v", out["hits"])
	}
	if hits[0]["path"] != filepath.Join(tmpDir, "visible.txt") {
		t.Fatalf("expected visible file only, got %v", hits[0]["path"])
	}
}

func TestSearchTool_ExcludeFiltersRelativePaths(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "keep.txt"), []byte("hello keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(tmpDir, "skip"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "skip", "hidden.txt"), []byte("hello hidden\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool, err := NewSearchWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{
		"path":    tmpDir,
		"query":   "hello",
		"exclude": []any{"skip", "missing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != 1 {
		t.Fatalf("expected exclude to keep only one hit, got %#v", out)
	}
	hits, ok := out["hits"].([]map[string]any)
	if !ok || len(hits) != 1 {
		t.Fatalf("expected one hit after exclude, got %#v", out)
	}
	if hits[0]["path"] != filepath.Join(tmpDir, "keep.txt") {
		t.Fatalf("expected keep.txt to remain visible, got %#v", hits[0]["path"])
	}
}

func TestSearchTool_DefaultWorkspaceRuntime_AllowsSearchOutsideWorkspace(t *testing.T) {
	rt, _ := newDefaultWorkspaceRuntime(t)
	tool, err := NewSearchWithRuntime(rt)
	if err != nil {
		t.Fatal(err)
	}

	path := newOutsideScratchFilePath(t, "outside.txt")
	if err := os.WriteFile(path, []byte("needle\nhay\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := tool.Run(context.Background(), map[string]any{
		"path":  path,
		"query": "needle",
	})
	if err != nil {
		t.Fatalf("expected outside-workspace search to stay allowed, got %v", err)
	}
	if got := out["count"]; got != 1 {
		t.Fatalf("expected one hit, got %v", got)
	}
}

func TestSearchTool_RestrictedReadableRootsRejectOutsideWorkspace(t *testing.T) {
	rt, _ := newWorkspaceRuntimeWithPolicy(t, toolexec.SandboxPolicy{
		Type:          toolexec.SandboxPolicyWorkspaceWrite,
		ReadableRoots: []string{"."},
	})
	tool, err := NewSearchWithRuntime(rt)
	if err != nil {
		t.Fatal(err)
	}

	path := newOutsideScratchFilePath(t, "outside.txt")
	if err := os.WriteFile(path, []byte("needle\nhay\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = tool.Run(context.Background(), map[string]any{
		"path":  path,
		"query": "needle",
	})
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected permission error for outside-workspace search, got %v", err)
	}
}
