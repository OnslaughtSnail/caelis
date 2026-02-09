package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTool_OffsetAndLimit(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "a.txt")
	content := "line1\nline2\nline3\nline4\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	readTool, err := NewRead(DefaultReadConfig())
	if err != nil {
		t.Fatal(err)
	}
	out, err := readTool.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 1,
		"limit":  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out["line_count"]; got != 2 {
		t.Fatalf("unexpected line_count: %v", got)
	}
	text, _ := out["content"].(string)
	if !strings.Contains(text, "2: line2") || !strings.Contains(text, "3: line3") {
		t.Fatalf("unexpected content: %q", text)
	}
}

func TestReadTool_TokenLimit(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "b.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("abcdefgh", 400)), 0o644); err != nil {
		t.Fatal(err)
	}

	readTool, err := NewRead(ReadConfig{
		DefaultLimit:     10,
		MaxLimit:         10,
		DefaultMaxTokens: 10,
		MaxTokens:        10,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := readTool.Run(context.Background(), map[string]any{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	reason, _ := out["truncated_reason"].(string)
	if reason != "token_limit" {
		t.Fatalf("expected token_limit, got %q", reason)
	}
}
