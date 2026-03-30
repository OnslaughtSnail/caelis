package filesystem

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func newTestRuntime(t *testing.T) toolexec.Runtime {
	t.Helper()
	sandboxType := "landlock"
	if runtime.GOOS == "darwin" {
		sandboxType = "seatbelt"
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		SandboxType:    sandboxType,
	})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = toolexec.Close(rt)
	})
	return rt
}

type testFS struct {
	cwd string
}

func (f testFS) Getwd() (string, error)                     { return f.cwd, nil }
func (f testFS) UserHomeDir() (string, error)               { return os.UserHomeDir() }
func (f testFS) Open(name string) (*os.File, error)         { return os.Open(name) }
func (f testFS) ReadDir(name string) ([]os.DirEntry, error) { return os.ReadDir(name) }
func (f testFS) Stat(name string) (os.FileInfo, error)      { return os.Stat(name) }
func (f testFS) ReadFile(name string) ([]byte, error)       { return os.ReadFile(name) }
func (f testFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}
func (f testFS) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }
func (f testFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(root, fn)
}

type noopSandboxRunner struct{}

func (noopSandboxRunner) Run(context.Context, toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return toolexec.CommandResult{}, nil
}

func newDefaultWorkspaceRuntime(t *testing.T) (toolexec.Runtime, string) {
	return newWorkspaceRuntimeWithPolicy(t, toolexec.SandboxPolicy{})
}

func newWorkspaceRuntimeWithPolicy(t *testing.T, policy toolexec.SandboxPolicy) (toolexec.Runtime, string) {
	t.Helper()
	ws := t.TempDir()
	sandboxType := "landlock"
	if runtime.GOOS == "darwin" {
		sandboxType = "seatbelt"
	}
	rt, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    sandboxType,
		SandboxRunner:  noopSandboxRunner{},
		SandboxPolicy:  policy,
		FileSystem:     testFS{cwd: ws},
	})
	if err != nil {
		t.Fatalf("create default workspace runtime: %v", err)
	}
	t.Cleanup(func() {
		_ = toolexec.Close(rt)
	})
	return rt, ws
}

func newOutsideScratchFilePath(t *testing.T, name string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home dir: %v", err)
	}
	dir := filepath.Join(home, ".caelis-test-artifacts", sanitizeTestName(t.Name()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create outside-scratch dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return filepath.Join(dir, name)
}

func sanitizeTestName(name string) string {
	replacer := strings.NewReplacer("/", "_", " ", "_", ":", "_")
	name = replacer.Replace(strings.TrimSpace(name))
	if name == "" {
		return "test"
	}
	return fmt.Sprintf("%s_%d", name, os.Getpid())
}
