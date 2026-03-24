package gitignorefilter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	gitignore "github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

type FileSystem interface {
	ReadFile(path string) ([]byte, error)
	Stat(path string) (os.FileInfo, error)
}

type Matcher struct {
	fs   FileSystem
	root string

	mu            sync.Mutex
	patternsByDir map[string][]gitignore.Pattern
	matchersByDir map[string]gitignore.Matcher
}

func New(fs FileSystem, root string) (*Matcher, error) {
	cleanRoot := filepath.Clean(strings.TrimSpace(root))
	if fs == nil || cleanRoot == "" {
		return nil, nil
	}
	return &Matcher{
		fs:            fs,
		root:          cleanRoot,
		patternsByDir: map[string][]gitignore.Pattern{},
		matchersByDir: map[string]gitignore.Matcher{},
	}, nil
}

func NewForPath(fs FileSystem, path string) (*Matcher, error) {
	root, err := discoverRoot(fs, path)
	if err != nil {
		return nil, err
	}
	return New(fs, root)
}

func (m *Matcher) Root() string {
	if m == nil {
		return ""
	}
	return m.root
}

func (m *Matcher) Match(path string, isDir bool) (bool, error) {
	if m == nil || strings.TrimSpace(path) == "" {
		return false, nil
	}
	cleanPath := filepath.Clean(path)
	if cleanPath == m.root {
		return false, nil
	}
	if !pathIsUnder(cleanPath, m.root) {
		return false, nil
	}
	parentDir := filepath.Dir(cleanPath)
	matcher, err := m.matcherForDir(parentDir)
	if err != nil || matcher == nil {
		return false, err
	}
	segments, err := relativeSegments(m.root, cleanPath)
	if err != nil || len(segments) == 0 {
		return false, err
	}
	return matcher.Match(segments, isDir), nil
}

func (m *Matcher) matcherForDir(dir string) (gitignore.Matcher, error) {
	cleanDir := filepath.Clean(dir)
	if cleanDir == "" {
		cleanDir = m.root
	}
	if !pathIsUnder(cleanDir, m.root) && cleanDir != m.root {
		cleanDir = m.root
	}

	m.mu.Lock()
	if matcher, ok := m.matchersByDir[cleanDir]; ok {
		m.mu.Unlock()
		return matcher, nil
	}
	m.mu.Unlock()

	patterns, err := m.patternsForDir(cleanDir)
	if err != nil {
		return nil, err
	}
	matcher := gitignore.NewMatcher(patterns)

	m.mu.Lock()
	m.matchersByDir[cleanDir] = matcher
	m.mu.Unlock()
	return matcher, nil
}

func (m *Matcher) patternsForDir(dir string) ([]gitignore.Pattern, error) {
	cleanDir := filepath.Clean(dir)
	if cleanDir == "" {
		cleanDir = m.root
	}
	if !pathIsUnder(cleanDir, m.root) && cleanDir != m.root {
		cleanDir = m.root
	}

	m.mu.Lock()
	if patterns, ok := m.patternsByDir[cleanDir]; ok {
		m.mu.Unlock()
		return patterns, nil
	}
	m.mu.Unlock()

	var patterns []gitignore.Pattern
	if cleanDir == m.root {
		rootPatterns, err := m.loadRootPatterns()
		if err != nil {
			return nil, err
		}
		patterns = rootPatterns
	} else {
		parentPatterns, err := m.patternsForDir(filepath.Dir(cleanDir))
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, parentPatterns...)
		localPatterns, err := m.loadGitignoreFile(filepath.Join(cleanDir, ".gitignore"), cleanDir)
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, localPatterns...)
	}

	m.mu.Lock()
	m.patternsByDir[cleanDir] = patterns
	m.mu.Unlock()
	return patterns, nil
}

func (m *Matcher) loadRootPatterns() ([]gitignore.Pattern, error) {
	patterns := make([]gitignore.Pattern, 0, 16)
	excludePatterns, err := m.loadGitignoreFile(filepath.Join(m.root, ".git", "info", "exclude"), m.root)
	if err != nil {
		return nil, err
	}
	patterns = append(patterns, excludePatterns...)
	rootPatterns, err := m.loadGitignoreFile(filepath.Join(m.root, ".gitignore"), m.root)
	if err != nil {
		return nil, err
	}
	patterns = append(patterns, rootPatterns...)
	return patterns, nil
}

func (m *Matcher) loadGitignoreFile(path string, domainDir string) ([]gitignore.Pattern, error) {
	data, err := m.fs.ReadFile(path)
	if err != nil {
		if isMissingFileError(err) {
			return nil, nil
		}
		return nil, err
	}
	domain, err := relativeSegments(m.root, domainDir)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	patterns := make([]gitignore.Pattern, 0, len(lines))
	for _, line := range lines {
		if strings.HasSuffix(line, "\r") {
			line = strings.TrimSuffix(line, "\r")
		}
		if line == "" {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	return patterns, nil
}

func isMissingFileError(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "no such file or directory") ||
		strings.Contains(text, "file does not exist") ||
		strings.Contains(text, "resource not found")
}

func discoverRoot(fs FileSystem, path string) (string, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "" {
		return "", nil
	}
	dir := cleanPath
	if info, err := fs.Stat(cleanPath); err == nil && !info.IsDir() {
		dir = filepath.Dir(cleanPath)
	} else if err != nil {
		dir = filepath.Dir(cleanPath)
	}
	if dir == "" {
		dir = cleanPath
	}

	fallback := dir
	lastIgnoreRoot := ""
	for {
		if _, err := fs.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		if _, err := fs.Stat(filepath.Join(dir, ".gitignore")); err == nil {
			lastIgnoreRoot = dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if lastIgnoreRoot != "" {
		return lastIgnoreRoot, nil
	}
	return fallback, nil
}

func relativeSegments(root string, path string) ([]string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		return nil, nil
	}
	return strings.Split(filepath.ToSlash(rel), "/"), nil
}

func pathIsUnder(path string, root string) bool {
	if path == root {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(path, prefix)
}
