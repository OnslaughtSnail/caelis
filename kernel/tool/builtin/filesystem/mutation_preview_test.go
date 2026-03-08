package filesystem

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildMutationPreview_PatchUsesCurrentDiskContent(t *testing.T) {
	rt := newTestRuntime(t)
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := rt.FileSystem().WriteFile(path, []byte("line1\nold\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	preview, err := BuildMutationPreview(rt, PatchToolName, map[string]any{
		"path": path,
		"old":  "old",
		"new":  "new",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Old != "line1\nold\n" || preview.New != "line1\nnew\n" {
		t.Fatalf("unexpected preview old/new: %+v", preview)
	}
	if preview.Hunk != "@@ -2,1 +2,1 @@" {
		t.Fatalf("unexpected hunk %q", preview.Hunk)
	}
}

func TestBuildMutationPreview_WriteUsesCurrentDiskContent(t *testing.T) {
	rt := newTestRuntime(t)
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := rt.FileSystem().WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	preview, err := BuildMutationPreview(rt, WriteToolName, map[string]any{
		"path":    path,
		"content": "new\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Old != "old\n" || preview.New != "new\n" {
		t.Fatalf("unexpected preview old/new: %+v", preview)
	}
}

func TestBuildMutationPreview_PatchFailsWhenExpectedTextMissing(t *testing.T) {
	rt := newTestRuntime(t)
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := rt.FileSystem().WriteFile(path, []byte("actual\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := BuildMutationPreview(rt, PatchToolName, map[string]any{
		"path": path,
		"old":  "expected",
		"new":  "new",
	})
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !strings.Contains(err.Error(), "old content not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}
