package envload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFilesIfExists_LoadsExistingAndSkipsMissing(t *testing.T) {
	root := t.TempDir()
	present := filepath.Join(root, ".env")
	missing := filepath.Join(root, "missing.env")
	if err := os.WriteFile(present, []byte("DEMO_ENVLOAD_KEY=demo-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFilesIfExists([]string{missing, present})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0] != present {
		t.Fatalf("unexpected loaded files: %v", loaded)
	}
	if got := os.Getenv("DEMO_ENVLOAD_KEY"); got != "demo-value" {
		t.Fatalf("expected DEMO_ENVLOAD_KEY=demo-value, got %q", got)
	}
}

func TestLoadFileIfExists_DoesNotOverrideExistingEnv(t *testing.T) {
	t.Setenv("DEMO_ENVLOAD_OVERRIDE", "from-env")
	root := t.TempDir()
	path := filepath.Join(root, ".env")
	if err := os.WriteFile(path, []byte("DEMO_ENVLOAD_OVERRIDE=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok, err := LoadFileIfExists(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected file to be loaded")
	}
	if got := os.Getenv("DEMO_ENVLOAD_OVERRIDE"); got != "from-env" {
		t.Fatalf("expected existing env to win, got %q", got)
	}
}
