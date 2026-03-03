package policy

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

// stubRuntime implements toolexec.Runtime for workspace boundary tests.
type stubRuntime struct {
	policy     toolexec.SandboxPolicy
	permission toolexec.PermissionMode
	fs         toolexec.FileSystem
}

func (r *stubRuntime) PermissionMode() toolexec.PermissionMode { return r.permission }
func (r *stubRuntime) SandboxType() string                     { return "stub" }
func (r *stubRuntime) SandboxPolicy() toolexec.SandboxPolicy   { return r.policy }
func (r *stubRuntime) FallbackToHost() bool                    { return false }
func (r *stubRuntime) FallbackReason() string                  { return "" }
func (r *stubRuntime) FileSystem() toolexec.FileSystem         { return r.fs }
func (r *stubRuntime) HostRunner() toolexec.CommandRunner      { return nil }
func (r *stubRuntime) SandboxRunner() toolexec.CommandRunner   { return nil }
func (r *stubRuntime) DecideRoute(string, toolexec.SandboxPermission) toolexec.CommandDecision {
	return toolexec.CommandDecision{}
}

// stubFS implements toolexec.FileSystem for testing path resolution.
type stubFS struct {
	cwd  string
	home string
}

func (f *stubFS) Getwd() (string, error)                      { return f.cwd, nil }
func (f *stubFS) UserHomeDir() (string, error)                { return f.home, nil }
func (f *stubFS) Open(string) (*os.File, error)               { return nil, errors.New("stub") }
func (f *stubFS) ReadDir(string) ([]os.DirEntry, error)       { return nil, errors.New("stub") }
func (f *stubFS) Stat(string) (os.FileInfo, error)            { return nil, errors.New("stub") }
func (f *stubFS) ReadFile(string) ([]byte, error)             { return nil, errors.New("stub") }
func (f *stubFS) WriteFile(string, []byte, os.FileMode) error { return errors.New("stub") }
func (f *stubFS) Glob(string) ([]string, error)               { return nil, errors.New("stub") }
func (f *stubFS) WalkDir(string, fs.WalkDirFunc) error {
	return errors.New("stub")
}

func writeToolInput(path string) ToolInput {
	return ToolInput{
		Call: model.ToolCall{Name: "WRITE"},
		Args: map[string]any{"path": path, "content": "test"},
		Capability: toolcap.Capability{
			Operations: []toolcap.Operation{toolcap.OperationFileWrite},
			Risk:       toolcap.RiskMedium,
		},
	}
}

func TestWorkspaceBoundary_AllowsPathWithinWorkspace(t *testing.T) {
	ws := t.TempDir()
	rt := &stubRuntime{
		policy: toolexec.SandboxPolicy{
			Type:          toolexec.SandboxPolicyWorkspaceWrite,
			WritableRoots: []string{ws},
		},
		permission: toolexec.PermissionModeDefault,
		fs:         &stubFS{cwd: ws, home: "/home/user"},
	}
	hook := WorkspaceBoundary(WorkspaceBoundaryConfig{Runtime: rt})

	in := writeToolInput(filepath.Join(ws, "src", "main.go"))
	out, err := hook.BeforeTool(context.Background(), in)
	if err != nil {
		t.Fatalf("expected allow for workspace path, got %v", err)
	}
	if out.Call.Name != "WRITE" {
		t.Fatalf("unexpected tool: %q", out.Call.Name)
	}
}

func TestWorkspaceBoundary_RequiresApprovalOutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	rt := &stubRuntime{
		policy: toolexec.SandboxPolicy{
			Type:          toolexec.SandboxPolicyWorkspaceWrite,
			WritableRoots: []string{ws},
		},
		permission: toolexec.PermissionModeDefault,
		fs:         &stubFS{cwd: ws, home: "/home/user"},
	}
	hook := WorkspaceBoundary(WorkspaceBoundaryConfig{Runtime: rt})

	// Path outside workspace should require approval.
	in := writeToolInput("/etc/config.json")
	_, err := hook.BeforeTool(context.Background(), in)
	if err == nil {
		t.Fatal("expected approval error for path outside workspace")
	}
	var approvalErr *toolexec.ApprovalRequiredError
	if !errors.As(err, &approvalErr) {
		t.Fatalf("expected ApprovalRequiredError, got %T: %v", err, err)
	}
}

func TestWorkspaceBoundary_ApprovedExternalWriteAllowed(t *testing.T) {
	ws := t.TempDir()
	rt := &stubRuntime{
		policy: toolexec.SandboxPolicy{
			Type:          toolexec.SandboxPolicyWorkspaceWrite,
			WritableRoots: []string{ws},
		},
		permission: toolexec.PermissionModeDefault,
		fs:         &stubFS{cwd: ws, home: "/home/user"},
	}
	hook := WorkspaceBoundary(WorkspaceBoundaryConfig{Runtime: rt})

	authorizer := &stubToolAuthorizer{allow: true}
	ctx := WithToolAuthorizer(context.Background(), authorizer)

	in := writeToolInput("/home/user/other/file.txt")
	out, err := hook.BeforeTool(ctx, in)
	if err != nil {
		t.Fatalf("expected approved write to succeed, got %v", err)
	}
	if out.Call.Name != "WRITE" {
		t.Fatalf("unexpected tool: %q", out.Call.Name)
	}
	if authorizer.calls != 1 {
		t.Fatalf("expected 1 authorization call, got %d", authorizer.calls)
	}
}

func TestWorkspaceBoundary_DeniedExternalWriteBlocked(t *testing.T) {
	ws := t.TempDir()
	rt := &stubRuntime{
		policy: toolexec.SandboxPolicy{
			Type:          toolexec.SandboxPolicyWorkspaceWrite,
			WritableRoots: []string{ws},
		},
		permission: toolexec.PermissionModeDefault,
		fs:         &stubFS{cwd: ws, home: "/home/user"},
	}
	hook := WorkspaceBoundary(WorkspaceBoundaryConfig{Runtime: rt})

	authorizer := &stubToolAuthorizer{allow: false}
	ctx := WithToolAuthorizer(context.Background(), authorizer)

	in := writeToolInput("/home/user/other/file.txt")
	_, err := hook.BeforeTool(ctx, in)
	if err == nil {
		t.Fatal("expected denied write to fail")
	}
	var abortedErr *toolexec.ApprovalAbortedError
	if !errors.As(err, &abortedErr) {
		t.Fatalf("expected ApprovalAbortedError, got %T: %v", err, err)
	}
}

func TestWorkspaceBoundary_SkippedForDangerFullAccess(t *testing.T) {
	rt := &stubRuntime{
		policy: toolexec.SandboxPolicy{
			Type: toolexec.SandboxPolicyDangerFull,
		},
		permission: toolexec.PermissionModeDefault,
		fs:         &stubFS{cwd: "/workspace", home: "/home/user"},
	}
	hook := WorkspaceBoundary(WorkspaceBoundaryConfig{Runtime: rt})

	in := writeToolInput("/etc/config.json")
	_, err := hook.BeforeTool(context.Background(), in)
	if err != nil {
		t.Fatalf("expected danger_full to skip boundary check, got %v", err)
	}
}

func TestWorkspaceBoundary_SkippedForFullControlMode(t *testing.T) {
	ws := t.TempDir()
	rt := &stubRuntime{
		policy: toolexec.SandboxPolicy{
			Type:          toolexec.SandboxPolicyWorkspaceWrite,
			WritableRoots: []string{ws},
		},
		permission: toolexec.PermissionModeFullControl,
		fs:         &stubFS{cwd: ws, home: "/home/user"},
	}
	hook := WorkspaceBoundary(WorkspaceBoundaryConfig{Runtime: rt})

	in := writeToolInput("/etc/config.json")
	_, err := hook.BeforeTool(context.Background(), in)
	if err != nil {
		t.Fatalf("expected full_control to skip boundary check, got %v", err)
	}
}

func TestWorkspaceBoundary_AllowsTempDir(t *testing.T) {
	ws := t.TempDir()
	rt := &stubRuntime{
		policy: toolexec.SandboxPolicy{
			Type:          toolexec.SandboxPolicyWorkspaceWrite,
			WritableRoots: []string{ws},
		},
		permission: toolexec.PermissionModeDefault,
		fs:         &stubFS{cwd: ws, home: "/home/user"},
	}
	hook := WorkspaceBoundary(WorkspaceBoundaryConfig{Runtime: rt})

	tmpPath := filepath.Join(os.TempDir(), "caelis_test.txt")
	in := writeToolInput(tmpPath)
	_, err := hook.BeforeTool(context.Background(), in)
	if err != nil {
		t.Fatalf("expected temp dir write to be allowed, got %v", err)
	}
}

func TestWorkspaceBoundary_RelativeWritableRoot(t *testing.T) {
	ws := t.TempDir()
	rt := &stubRuntime{
		policy: toolexec.SandboxPolicy{
			Type:          toolexec.SandboxPolicyWorkspaceWrite,
			WritableRoots: []string{"."},
		},
		permission: toolexec.PermissionModeDefault,
		fs:         &stubFS{cwd: ws, home: "/home/user"},
	}
	hook := WorkspaceBoundary(WorkspaceBoundaryConfig{Runtime: rt})

	// File within cwd should be allowed.
	in := writeToolInput(filepath.Join(ws, "file.txt"))
	_, err := hook.BeforeTool(context.Background(), in)
	if err != nil {
		t.Fatalf("expected relative root to allow workspace file, got %v", err)
	}

	// File outside cwd should require approval.
	in2 := writeToolInput("/opt/other/file.txt")
	_, err2 := hook.BeforeTool(context.Background(), in2)
	if err2 == nil {
		t.Fatal("expected path outside relative root to require approval")
	}
}

func TestWorkspaceBoundary_SkipsNonWriteOps(t *testing.T) {
	ws := t.TempDir()
	rt := &stubRuntime{
		policy: toolexec.SandboxPolicy{
			Type:          toolexec.SandboxPolicyWorkspaceWrite,
			WritableRoots: []string{ws},
		},
		permission: toolexec.PermissionModeDefault,
		fs:         &stubFS{cwd: ws, home: "/home/user"},
	}
	hook := WorkspaceBoundary(WorkspaceBoundaryConfig{Runtime: rt})

	// READ tool should pass through even for paths outside workspace.
	in := ToolInput{
		Call: model.ToolCall{Name: "READ"},
		Args: map[string]any{"path": "/etc/passwd"},
		Capability: toolcap.Capability{
			Operations: []toolcap.Operation{toolcap.OperationFileRead},
			Risk:       toolcap.RiskLow,
		},
	}
	_, err := hook.BeforeTool(context.Background(), in)
	if err != nil {
		t.Fatalf("expected READ to pass through, got %v", err)
	}
}

func TestWorkspaceBoundary_SymlinkEscapeRequiresApproval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior differs on windows and may require elevated privilege")
	}
	ws := t.TempDir()
	linkPath := filepath.Join(ws, "outside_link")
	if err := os.Symlink("/etc", linkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	rt := &stubRuntime{
		policy: toolexec.SandboxPolicy{
			Type:          toolexec.SandboxPolicyWorkspaceWrite,
			WritableRoots: []string{ws},
		},
		permission: toolexec.PermissionModeDefault,
		fs:         &stubFS{cwd: ws, home: "/home/user"},
	}
	hook := WorkspaceBoundary(WorkspaceBoundaryConfig{Runtime: rt})

	in := writeToolInput(filepath.Join(linkPath, "escape.txt"))
	_, err := hook.BeforeTool(context.Background(), in)
	if err == nil {
		t.Fatal("expected approval error for symlink escape path")
	}
	var approvalErr *toolexec.ApprovalRequiredError
	if !errors.As(err, &approvalErr) {
		t.Fatalf("expected ApprovalRequiredError, got %T: %v", err, err)
	}
}
