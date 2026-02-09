package filesystem

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

var (
	defaultRuntimeOnce sync.Once
	defaultRuntimeInst toolexec.Runtime
	defaultRuntimeErr  error
)

func runtimeOrDefault(runtime toolexec.Runtime) (toolexec.Runtime, error) {
	if runtime != nil {
		return runtime, nil
	}
	defaultRuntimeOnce.Do(func() {
		defaultRuntimeInst, defaultRuntimeErr = toolexec.New(toolexec.Config{
			Mode: toolexec.ModeNoSandbox,
		})
	})
	if defaultRuntimeErr != nil {
		return nil, defaultRuntimeErr
	}
	return defaultRuntimeInst, nil
}

func normalizePathWithFS(fsys toolexec.FileSystem, path string) (string, error) {
	if fsys == nil {
		return "", fmt.Errorf("tool: filesystem runtime is nil")
	}
	if path == "" {
		return "", fmt.Errorf("tool: empty path")
	}
	if strings.HasPrefix(path, "~/") {
		homeDir, err := fsys.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(homeDir, path[2:])
	}
	if !filepath.IsAbs(path) {
		wd, err := fsys.Getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(wd, path)
	}
	return filepath.Clean(path), nil
}

func walkDir(fsys toolexec.FileSystem, root string, fn fs.WalkDirFunc) error {
	if fsys == nil {
		return fmt.Errorf("tool: filesystem runtime is nil")
	}
	return fsys.WalkDir(root, fn)
}
