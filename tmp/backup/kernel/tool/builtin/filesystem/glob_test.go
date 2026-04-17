package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func TestGlobTool_SupportsRecursiveDoubleStar(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "root.md"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "docs", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "docs", "nested", "guide.md"), []byte("guide"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "docs", "nested", "note.txt"), []byte("note"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool, err := NewGlobWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{
		"pattern": filepath.Join(tmpDir, "**", "*.md"),
	})
	if err != nil {
		t.Fatal(err)
	}
	matches, ok := out["matches"].([]string)
	if !ok {
		t.Fatalf("expected string matches, got %#v", out)
	}
	if len(matches) != 2 {
		t.Fatalf("expected two recursive markdown matches, got %#v", out)
	}
}

func TestGlobTool_ExcludeFiltersRelativePaths(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "docs", "skip"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "docs", "keep.md"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "docs", "skip", "drop.md"), []byte("drop"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool, err := NewGlobWithRuntime(newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := tool.Run(context.Background(), map[string]any{
		"pattern": filepath.Join(tmpDir, "docs", "**", "*.md"),
		"exclude": []any{"skip", "**/drop.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	matches, ok := out["matches"].([]string)
	if !ok {
		t.Fatalf("expected string matches, got %#v", out)
	}
	if len(matches) != 1 || matches[0] != filepath.Join(tmpDir, "docs", "keep.md") {
		t.Fatalf("expected exclude to keep only keep.md, got %#v", matches)
	}
}

func TestGlobTool_DefaultWorkspaceRuntime_AllowsGlobOutsideWorkspace(t *testing.T) {
	rt, _ := newDefaultWorkspaceRuntime(t)
	tool, err := NewGlobWithRuntime(rt)
	if err != nil {
		t.Fatal(err)
	}

	outsideDir := filepath.Dir(newOutsideScratchFilePath(t, "a.txt"))
	if err := os.WriteFile(filepath.Join(outsideDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := tool.Run(context.Background(), map[string]any{
		"pattern": filepath.Join(outsideDir, "*.txt"),
	})
	if err != nil {
		t.Fatalf("expected outside-workspace glob to stay allowed, got %v", err)
	}
	if got := out["count"]; got != 2 {
		t.Fatalf("expected two matches, got %v", got)
	}
}

func TestGlobTool_RestrictedReadableRootsRejectOutsideWorkspace(t *testing.T) {
	rt, _ := newWorkspaceRuntimeWithPolicy(t, toolexec.SandboxPolicy{
		Type:          toolexec.SandboxPolicyWorkspaceWrite,
		ReadableRoots: []string{"."},
	})
	tool, err := NewGlobWithRuntime(rt)
	if err != nil {
		t.Fatal(err)
	}

	outsideDir := filepath.Dir(newOutsideScratchFilePath(t, "a.txt"))
	if err := os.WriteFile(filepath.Join(outsideDir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = tool.Run(context.Background(), map[string]any{
		"pattern": filepath.Join(outsideDir, "*.txt"),
	})
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected permission error for outside-workspace glob, got %v", err)
	}
}
