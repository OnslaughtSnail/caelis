package policy

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool/capability"
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

func (f *stubFS) Getwd() (string, error)                     { return f.cwd, nil }
func (f *stubFS) UserHomeDir() (string, error)               { return f.home, nil }
func (f *stubFS) Open(name string) (*os.File, error)         { return os.Open(name) }
func (f *stubFS) ReadDir(name string) ([]os.DirEntry, error) { return os.ReadDir(name) }
func (f *stubFS) Stat(name string) (os.FileInfo, error)      { return os.Stat(name) }
func (f *stubFS) ReadFile(name string) ([]byte, error)       { return os.ReadFile(name) }
func (f *stubFS) WriteFile(name string, data []byte, mode os.FileMode) error {
	return os.WriteFile(name, data, mode)
}
func (f *stubFS) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }
func (f *stubFS) WalkDir(string, fs.WalkDirFunc) error {
	return errors.New("stub")
}

func writeToolInput(path string) ToolInput {
	return ToolInput{
		Call: model.ToolCall{Name: "WRITE"},
		Args: map[string]any{"path": path, "content": "test"},
		Capability: capability.Capability{
			Operations: []capability.Operation{capability.OperationFileWrite},
			Risk:       capability.RiskMedium,
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

func TestWorkspaceBoundary_ApprovalRequestIncludesPathScopeAndPreview(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a unix-style absolute path")
	}
	ws := t.TempDir()
	targetPath := "/etc/hosts"
	original, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target fixture: %v", err)
	}
	oldValue := string(original)
	firstLine := strings.TrimSuffix(strings.SplitN(oldValue, "\n", 2)[0], "\r")
	if strings.TrimSpace(firstLine) == "" {
		t.Fatal("expected non-empty first line in /etc/hosts")
	}
	newValue := strings.Replace(oldValue, firstLine, firstLine+" localdomain", 1)
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

	in := ToolInput{
		Call: model.ToolCall{Name: "PATCH"},
		Args: map[string]any{
			"path": targetPath,
			"old":  oldValue,
			"new":  newValue,
		},
		Capability: capability.Capability{
			Operations: []capability.Operation{capability.OperationFileWrite},
			Risk:       capability.RiskMedium,
		},
	}
	if _, err := hook.BeforeTool(ctx, in); err != nil {
		t.Fatalf("expected approval request to pass with authorizer, got %v", err)
	}
	if authorizer.last.Path != targetPath {
		t.Fatalf("expected absolute target path, got %q", authorizer.last.Path)
	}
	if authorizer.last.ScopeKey != "/etc" {
		t.Fatalf("expected directory scope %q, got %q", "/etc", authorizer.last.ScopeKey)
	}
	if !strings.Contains(authorizer.last.Preview, "--- old") || !strings.Contains(authorizer.last.Preview, "+++ new") {
		t.Fatalf("expected diff preview headers, got %q", authorizer.last.Preview)
	}
	if !strings.Contains(authorizer.last.Preview, "-"+firstLine) || !strings.Contains(authorizer.last.Preview, "+"+firstLine+" localdomain") {
		t.Fatalf("expected diff preview body, got %q", authorizer.last.Preview)
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
		Capability: capability.Capability{
			Operations: []capability.Operation{capability.OperationFileRead},
			Risk:       capability.RiskLow,
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
