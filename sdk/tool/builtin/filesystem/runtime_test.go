package filesystem

import (
	"context"
	"io/fs"
	"os"
	"testing"

	sdksandbox "github.com/OnslaughtSnail/caelis/sdk/sandbox"
)

func TestFileSystemFromRuntimeUsesConstraintAwareSelector(t *testing.T) {
	defaultFS := fakeFileSystem{cwd: "/sandbox"}
	hostFS := fakeFileSystem{cwd: "/host"}
	runtime := fakeRuntime{
		defaultFS: defaultFS,
		hostFS:    hostFS,
	}

	got := fileSystemFromRuntime(runtime, map[string]any{
		"sandbox_constraints": sdksandbox.Constraints{
			Route:      sdksandbox.RouteHost,
			Permission: sdksandbox.PermissionFullAccess,
		},
	})
	if got == nil {
		t.Fatal("fileSystemFromRuntime() = nil")
	}
	cwd, err := got.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if cwd != "/host" {
		t.Fatalf("Getwd() = %q, want /host", cwd)
	}
}

func TestFileSystemFromRuntimeFallsBackToDefaultRuntimeFS(t *testing.T) {
	defaultFS := fakeFileSystem{cwd: "/sandbox"}
	runtime := fakeRuntime{defaultFS: defaultFS}

	got := fileSystemFromRuntime(runtime, nil)
	if got == nil {
		t.Fatal("fileSystemFromRuntime() = nil")
	}
	cwd, err := got.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if cwd != "/sandbox" {
		t.Fatalf("Getwd() = %q, want /sandbox", cwd)
	}
}

type fakeRuntime struct {
	defaultFS sdksandbox.FileSystem
	hostFS    sdksandbox.FileSystem
}

func (f fakeRuntime) Describe() sdksandbox.Descriptor { return sdksandbox.Descriptor{} }
func (f fakeRuntime) FileSystem() sdksandbox.FileSystem { return f.defaultFS }
func (f fakeRuntime) FileSystemFor(constraints sdksandbox.Constraints) sdksandbox.FileSystem {
	if constraints.Route == sdksandbox.RouteHost || constraints.Permission == sdksandbox.PermissionFullAccess {
		return f.hostFS
	}
	return f.defaultFS
}
func (f fakeRuntime) Run(context.Context, sdksandbox.CommandRequest) (sdksandbox.CommandResult, error) {
	return sdksandbox.CommandResult{}, nil
}
func (f fakeRuntime) Start(context.Context, sdksandbox.CommandRequest) (sdksandbox.Session, error) {
	return nil, nil
}
func (f fakeRuntime) OpenSession(string) (sdksandbox.Session, error) { return nil, nil }
func (f fakeRuntime) OpenSessionRef(sdksandbox.SessionRef) (sdksandbox.Session, error) {
	return nil, nil
}
func (f fakeRuntime) SupportedBackends() []sdksandbox.Backend { return []sdksandbox.Backend{sdksandbox.BackendHost} }
func (f fakeRuntime) Status() sdksandbox.Status {
	return sdksandbox.Status{
		RequestedBackend: sdksandbox.BackendHost,
		ResolvedBackend:  sdksandbox.BackendHost,
	}
}
func (f fakeRuntime) Close() error { return nil }

type fakeFileSystem struct {
	cwd string
}

func (f fakeFileSystem) Getwd() (string, error)                    { return f.cwd, nil }
func (f fakeFileSystem) UserHomeDir() (string, error)              { return "/home/test", nil }
func (f fakeFileSystem) Open(string) (*os.File, error)             { return nil, fs.ErrNotExist }
func (f fakeFileSystem) ReadDir(string) ([]os.DirEntry, error)     { return nil, fs.ErrNotExist }
func (f fakeFileSystem) Stat(string) (os.FileInfo, error)          { return nil, fs.ErrNotExist }
func (f fakeFileSystem) ReadFile(string) ([]byte, error)           { return nil, fs.ErrNotExist }
func (f fakeFileSystem) WriteFile(string, []byte, os.FileMode) error { return nil }
func (f fakeFileSystem) Glob(string) ([]string, error)             { return nil, nil }
func (f fakeFileSystem) WalkDir(string, fs.WalkDirFunc) error      { return nil }
