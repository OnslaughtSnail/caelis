package gitignorefilter

import (
	"os"
	"path/filepath"
	"testing"
)

type osFS struct{}

func (osFS) ReadFile(path string) ([]byte, error)  { return os.ReadFile(path) }
func (osFS) Stat(path string) (os.FileInfo, error) { return os.Stat(path) }

func TestMatcher_RespectsRootAndNestedGitignore(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.txt\nlogs/\nsub/keep.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", ".gitignore"), []byte("*.tmp\n!keep.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	matcher, err := NewForPath(osFS{}, root)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path   string
		isDir  bool
		ignore bool
	}{
		{path: filepath.Join(root, "ignored.txt"), ignore: true},
		{path: filepath.Join(root, "logs"), isDir: true, ignore: true},
		{path: filepath.Join(root, "logs", "app.log"), ignore: true},
		{path: filepath.Join(root, "sub", "a.tmp"), ignore: true},
		{path: filepath.Join(root, "sub", "keep.tmp"), ignore: false},
		{path: filepath.Join(root, "sub", "keep.log"), ignore: true},
		{path: filepath.Join(root, "visible.txt"), ignore: false},
	}
	for _, tc := range cases {
		got, err := matcher.Match(tc.path, tc.isDir)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if got != tc.ignore {
			t.Fatalf("%s: expected ignore=%v, got %v", tc.path, tc.ignore, got)
		}
	}
}

func TestMatcher_FallsBackToNearestGitignoreRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("generated/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	matcher, err := NewForPath(osFS{}, filepath.Join(root, "nested"))
	if err != nil {
		t.Fatal(err)
	}
	if matcher.Root() != root {
		t.Fatalf("expected root %q, got %q", root, matcher.Root())
	}

	got, err := matcher.Match(filepath.Join(root, "generated", "out.txt"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("expected generated file to be ignored")
	}
}
