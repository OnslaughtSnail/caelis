package filesystem

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func TestListTool_DefaultWorkspaceRuntime_AllowsListOutsideWorkspace(t *testing.T) {
	rt, _ := newDefaultWorkspaceRuntime(t)
	tool, err := NewListWithRuntime(rt)
	if err != nil {
		t.Fatal(err)
	}

	outsideDir := filepath.Dir(newOutsideScratchFilePath(t, "outside.txt"))
	if err := os.WriteFile(filepath.Join(outsideDir, "outside.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := tool.Run(context.Background(), map[string]any{"path": outsideDir})
	if err != nil {
		t.Fatalf("expected outside-workspace list to stay allowed, got %v", err)
	}
	if got := out["count"]; got != 1 {
		t.Fatalf("expected one entry, got %v", got)
	}
}

func TestListTool_RestrictedReadableRootsRejectOutsideWorkspace(t *testing.T) {
	rt, _ := newWorkspaceRuntimeWithPolicy(t, toolexec.SandboxPolicy{
		Type:          toolexec.SandboxPolicyWorkspaceWrite,
		ReadableRoots: []string{"."},
	})
	tool, err := NewListWithRuntime(rt)
	if err != nil {
		t.Fatal(err)
	}

	outsideDir := filepath.Dir(newOutsideScratchFilePath(t, "outside.txt"))
	if err := os.WriteFile(filepath.Join(outsideDir, "outside.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = tool.Run(context.Background(), map[string]any{"path": outsideDir})
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected permission error for outside-workspace list, got %v", err)
	}
}
