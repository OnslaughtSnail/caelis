package filesystem

import (
	"fmt"
	"io/fs"
	"path"
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

func parseStringSliceArg(args map[string]any, key string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, nil
	}
	switch typed := raw.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("tool: arg %q must be an array of strings", key)
			}
			text = strings.TrimSpace(text)
			if text != "" {
				out = append(out, text)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("tool: arg %q must be an array of strings", key)
	}
}

func shouldExcludePath(root, candidate string, isDir bool, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	rel := candidate
	if root != "" {
		if computed, err := filepath.Rel(root, candidate); err == nil {
			rel = computed
		}
	}
	rel = normalizeRelativeMatchPath(rel)
	for _, pattern := range patterns {
		pattern = normalizeRelativeMatchPath(pattern)
		if pattern == "" {
			continue
		}
		if pathGlobMatch(pattern, rel) {
			return true
		}
		if isDir && strings.TrimSuffix(pattern, "/") == rel {
			return true
		}
	}
	return false
}

func normalizeRelativeMatchPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = filepath.ToSlash(filepath.Clean(value))
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimPrefix(value, "/")
	if value == "." {
		return ""
	}
	return value
}

func hasPathGlobMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[")
}

func pathGlobMatch(pattern, rel string) bool {
	pattern = normalizeRelativeMatchPath(pattern)
	rel = normalizeRelativeMatchPath(rel)
	if pattern == "" {
		return rel == ""
	}
	return matchPathGlobSegments(splitPathSegments(pattern), splitPathSegments(rel))
}

func splitPathSegments(value string) []string {
	value = normalizeRelativeMatchPath(value)
	if value == "" {
		return nil
	}
	return strings.Split(value, "/")
}

func matchPathGlobSegments(patternParts, pathParts []string) bool {
	if len(patternParts) == 0 {
		return len(pathParts) == 0
	}
	head := patternParts[0]
	if head == "**" {
		if matchPathGlobSegments(patternParts[1:], pathParts) {
			return true
		}
		for i := 0; i < len(pathParts); i++ {
			if matchPathGlobSegments(patternParts[1:], pathParts[i+1:]) {
				return true
			}
		}
		return false
	}
	if len(pathParts) == 0 {
		return false
	}
	matched, err := path.Match(head, pathParts[0])
	if err != nil || !matched {
		return false
	}
	return matchPathGlobSegments(patternParts[1:], pathParts[1:])
}

func splitAbsoluteGlobPattern(pattern string) (string, string) {
	pattern = filepath.Clean(pattern)
	volume := filepath.VolumeName(pattern)
	rest := strings.TrimPrefix(pattern, volume)
	rest = strings.TrimPrefix(rest, string(filepath.Separator))
	segments := strings.FieldsFunc(rest, func(r rune) bool {
		return r == filepath.Separator
	})
	metaIndex := len(segments)
	for i, segment := range segments {
		if hasPathGlobMeta(segment) {
			metaIndex = i
			break
		}
	}
	rootParts := segments[:metaIndex]
	patternParts := segments[metaIndex:]
	root := volume
	if filepath.IsAbs(pattern) {
		if root == "" {
			root = string(filepath.Separator)
		} else if !strings.HasSuffix(root, string(filepath.Separator)) {
			root += string(filepath.Separator)
		}
	}
	if len(rootParts) > 0 {
		pieces := append([]string{root}, rootParts...)
		root = filepath.Join(pieces...)
	}
	if root == "" {
		root = "."
	}
	return filepath.Clean(root), filepath.ToSlash(strings.Join(patternParts, string(filepath.Separator)))
}
