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

	readTool, err := NewReadWithRuntime(DefaultReadConfig(), newTestRuntime(t))
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
	if got := out["start_line"]; got != 2 {
		t.Fatalf("unexpected start_line: %v", got)
	}
	if got := out["end_line"]; got != 3 {
		t.Fatalf("unexpected end_line: %v", got)
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

	readTool, err := NewReadWithRuntime(ReadConfig{
		DefaultLimit:     10,
		MaxLimit:         10,
		DefaultMaxTokens: 10,
		MaxTokens:        10,
	}, newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := readTool.Run(context.Background(), map[string]any{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	if out["has_more"] != true {
		t.Fatalf("expected has_more=true, got %#v", out)
	}
}

func TestReadTool_OffsetPastEOFReturnsExhausted(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "c.txt")
	if err := os.WriteFile(path, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	readTool, err := NewReadWithRuntime(DefaultReadConfig(), newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := readTool.Run(context.Background(), map[string]any{
		"path":   path,
		"offset": 99,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["exhausted"] != true {
		t.Fatalf("expected exhausted=true, got %#v", out)
	}
	if out["content"] != "" {
		t.Fatalf("expected empty content past EOF, got %#v", out)
	}
	if out["start_line"] != 0 || out["end_line"] != 0 {
		t.Fatalf("expected zero line range past EOF, got %#v", out)
	}
	if out["next_offset"] != 2 {
		t.Fatalf("expected next_offset to clamp to file length, got %#v", out)
	}
}

func TestReadToolDeclaration_ExplainsLineThenTokenBudget(t *testing.T) {
	readTool, err := NewReadWithRuntime(DefaultReadConfig(), newTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := readTool.Description(); !strings.Contains(got, "first slices by lines") {
		t.Fatalf("expected updated READ description, got %q", got)
	}
}
