package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type previewTestRuntime struct {
	cwd string
}

func (r previewTestRuntime) PermissionMode() toolexec.PermissionMode {
	return toolexec.PermissionModeDefault
}
func (r previewTestRuntime) SandboxType() string                   { return "test" }
func (r previewTestRuntime) SandboxPolicy() toolexec.SandboxPolicy { return toolexec.SandboxPolicy{} }
func (r previewTestRuntime) FallbackToHost() bool                  { return false }
func (r previewTestRuntime) FallbackReason() string                { return "" }
func (r previewTestRuntime) FileSystem() toolexec.FileSystem {
	return previewTestFS{cwd: r.cwd}
}
func (r previewTestRuntime) HostRunner() toolexec.CommandRunner    { return nil }
func (r previewTestRuntime) SandboxRunner() toolexec.CommandRunner { return nil }
func (r previewTestRuntime) DecideRoute(string, toolexec.SandboxPermission) toolexec.CommandDecision {
	return toolexec.CommandDecision{}
}

type previewTestFS struct {
	cwd string
}

func (f previewTestFS) Getwd() (string, error)             { return f.cwd, nil }
func (f previewTestFS) UserHomeDir() (string, error)       { return os.UserHomeDir() }
func (f previewTestFS) Open(name string) (*os.File, error) { return os.Open(name) }
func (f previewTestFS) ReadDir(name string) ([]os.DirEntry, error) {
	return os.ReadDir(name)
}
func (f previewTestFS) Stat(name string) (os.FileInfo, error) { return os.Stat(name) }
func (f previewTestFS) ReadFile(name string) ([]byte, error)  { return os.ReadFile(name) }
func (f previewTestFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (f previewTestFS) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }
func (f previewTestFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(root, fn)
}

func TestBuildToolCallDiffBlockMsg_PatchUsesLiveDiskSnapshot(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("line1\nold\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, tooLarge, ok := buildToolCallDiffBlockMsg(previewTestRuntime{cwd: ws}, "PATCH", map[string]any{
		"path": path,
		"old":  "old",
		"new":  "new",
	})
	if !ok || tooLarge {
		t.Fatalf("expected ok rich diff payload, ok=%v tooLarge=%v", ok, tooLarge)
	}
	if msg.Path != "a.txt" {
		t.Fatalf("expected basename path, got %q", msg.Path)
	}
	if msg.Old != "line1\nold\n" || msg.New != "line1\nnew\n" {
		t.Fatalf("unexpected old/new: %#v", msg)
	}
	if msg.Hunk != "@@ -2,1 +2,1 @@" {
		t.Fatalf("unexpected hunk: %q", msg.Hunk)
	}
}

func TestBuildToolCallDiffBlockMsg_WriteUsesLiveDiskSnapshot(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, tooLarge, ok := buildToolCallDiffBlockMsg(previewTestRuntime{cwd: ws}, "WRITE", map[string]any{
		"path":    path,
		"content": "new\n",
	})
	if !ok || tooLarge {
		t.Fatalf("expected ok rich diff payload, ok=%v tooLarge=%v", ok, tooLarge)
	}
	if msg.Tool != "WRITE" {
		t.Fatalf("expected WRITE tool label, got %q", msg.Tool)
	}
	if msg.Old != "old\n" || msg.New != "new\n" {
		t.Fatalf("unexpected old/new: %#v", msg)
	}
}

func TestBuildToolCallDiffBlockMsg_WriteSkipsNoOpPreview(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	content := "same\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, tooLarge, ok := buildToolCallDiffBlockMsg(previewTestRuntime{cwd: ws}, "WRITE", map[string]any{
		"path":    path,
		"content": content,
	})
	if tooLarge {
		t.Fatal("expected no-op preview to be skipped without size fallback")
	}
	if ok {
		t.Fatal("expected no-op write preview to be suppressed")
	}
}

func TestBuildToolCallDiffBlockMsg_TooLarge(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("a\n", 500)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, tooLarge, ok := buildToolCallDiffBlockMsg(previewTestRuntime{cwd: ws}, "WRITE", map[string]any{
		"path":    path,
		"content": strings.Repeat("b\n", 500),
	})
	if !ok {
		t.Fatal("expected WRITE payload to be recognized")
	}
	if !tooLarge {
		t.Fatal("expected tooLarge=true for >800 lines")
	}
}

func TestBuildToolCallDiffBlockMsg_RejectsMismatchedPatch(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(path, []byte("line1\nactual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, ok := buildToolCallDiffBlockMsg(previewTestRuntime{cwd: ws}, "PATCH", map[string]any{
		"path": path,
		"old":  "expected",
		"new":  "new",
	})
	if ok {
		t.Fatal("expected preview build to fail when disk content does not match patch old text")
	}
}

func TestPreviewTestRuntimeUsesCwd(t *testing.T) {
	ws := t.TempDir()
	rt := previewTestRuntime{cwd: ws}
	got, err := rt.FileSystem().Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got != ws {
		t.Fatalf("expected cwd %q, got %q", ws, got)
	}
	if _, err := rt.FileSystem().Stat(filepath.Join(ws, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}
