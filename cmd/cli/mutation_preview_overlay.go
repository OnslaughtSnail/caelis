package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type mutationPreviewRuntime struct {
	base toolexec.Runtime
	fsys toolexec.FileSystem
}

func (r mutationPreviewRuntime) PermissionMode() toolexec.PermissionMode {
	return r.base.PermissionMode()
}

func (r mutationPreviewRuntime) SandboxType() string {
	return r.base.SandboxType()
}

func (r mutationPreviewRuntime) SandboxPolicy() toolexec.SandboxPolicy {
	return r.base.SandboxPolicy()
}

func (r mutationPreviewRuntime) FallbackToHost() bool {
	return r.base.FallbackToHost()
}

func (r mutationPreviewRuntime) FallbackReason() string {
	return r.base.FallbackReason()
}

func (r mutationPreviewRuntime) FileSystem() toolexec.FileSystem {
	return r.fsys
}

func (r mutationPreviewRuntime) HostRunner() toolexec.CommandRunner {
	return r.base.HostRunner()
}

func (r mutationPreviewRuntime) SandboxRunner() toolexec.CommandRunner {
	return r.base.SandboxRunner()
}

func (r mutationPreviewRuntime) DecideRoute(command string, permission toolexec.SandboxPermission) toolexec.CommandDecision {
	return r.base.DecideRoute(command, permission)
}

type mutationPreviewFS struct {
	base      toolexec.FileSystem
	overrides map[string]mutationPreviewFile
}

type mutationPreviewFile struct {
	data []byte
	mode os.FileMode
}

func newMutationPreviewFS(base toolexec.FileSystem) *mutationPreviewFS {
	if base == nil {
		return nil
	}
	return &mutationPreviewFS{
		base:      base,
		overrides: map[string]mutationPreviewFile{},
	}
}

func (f *mutationPreviewFS) Stage(path string, content string) {
	if f == nil || f.base == nil {
		return
	}
	target := filepath.Clean(path)
	mode := os.FileMode(0o644)
	if info, err := f.base.Stat(target); err == nil {
		mode = info.Mode()
	} else if existing, ok := f.overrides[target]; ok {
		mode = existing.mode
	}
	f.overrides[target] = mutationPreviewFile{data: []byte(content), mode: mode}
}

func (f *mutationPreviewFS) override(path string) (mutationPreviewFile, bool) {
	if f == nil {
		return mutationPreviewFile{}, false
	}
	file, ok := f.overrides[filepath.Clean(path)]
	return file, ok
}

func (f *mutationPreviewFS) Getwd() (string, error) {
	return f.base.Getwd()
}

func (f *mutationPreviewFS) UserHomeDir() (string, error) {
	return f.base.UserHomeDir()
}

func (f *mutationPreviewFS) Open(path string) (*os.File, error) {
	return f.base.Open(path)
}

func (f *mutationPreviewFS) ReadDir(path string) ([]os.DirEntry, error) {
	return f.base.ReadDir(path)
}

func (f *mutationPreviewFS) Stat(path string) (os.FileInfo, error) {
	if file, ok := f.override(path); ok {
		return mutationPreviewFileInfo{
			name: filepath.Base(path),
			size: int64(len(file.data)),
			mode: file.mode,
		}, nil
	}
	return f.base.Stat(path)
}

func (f *mutationPreviewFS) ReadFile(path string) ([]byte, error) {
	if file, ok := f.override(path); ok {
		return append([]byte(nil), file.data...), nil
	}
	return f.base.ReadFile(path)
}

func (f *mutationPreviewFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return f.base.WriteFile(path, data, perm)
}

func (f *mutationPreviewFS) Glob(pattern string) ([]string, error) {
	return f.base.Glob(pattern)
}

func (f *mutationPreviewFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	return f.base.WalkDir(root, fn)
}

type mutationPreviewFileInfo struct {
	name string
	size int64
	mode os.FileMode
}

func (i mutationPreviewFileInfo) Name() string       { return i.name }
func (i mutationPreviewFileInfo) Size() int64        { return i.size }
func (i mutationPreviewFileInfo) Mode() os.FileMode  { return i.mode }
func (i mutationPreviewFileInfo) ModTime() time.Time { return time.Time{} }
func (i mutationPreviewFileInfo) IsDir() bool        { return false }
func (i mutationPreviewFileInfo) Sys() any           { return nil }
