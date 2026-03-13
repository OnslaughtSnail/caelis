package filesystem

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/gitignorefilter"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func runtimeOrDefault(runtime toolexec.Runtime) (toolexec.Runtime, error) {
	if runtime != nil {
		return runtime, nil
	}
	return nil, fmt.Errorf("tool: runtime is required")
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

type toolFileSystemAdapter struct {
	fsys toolexec.FileSystem
}

func (a toolFileSystemAdapter) ReadFile(path string) ([]byte, error) {
	return a.fsys.ReadFile(path)
}

func (a toolFileSystemAdapter) Stat(path string) (fs.FileInfo, error) {
	return a.fsys.Stat(path)
}

func newGitignoreMatcher(fsys toolexec.FileSystem, target string) (*gitignorefilter.Matcher, error) {
	if fsys == nil {
		return nil, nil
	}
	return gitignorefilter.NewForPath(toolFileSystemAdapter{fsys: fsys}, target)
}
