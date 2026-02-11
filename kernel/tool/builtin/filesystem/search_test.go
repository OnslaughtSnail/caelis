package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchTool_ReportsTruncationAndFileStats(t *testing.T) {
	tmpDir := t.TempDir()
	aPath := filepath.Join(tmpDir, "a.txt")
	bPath := filepath.Join(tmpDir, "b.txt")
	if err := os.WriteFile(aPath, []byte("hello one\nhello two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte("hello three\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewSearch()
	out, err := tool.Run(context.Background(), map[string]any{
		"path":  tmpDir,
		"query": "hello",
		"limit": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["count"] != 2 {
		t.Fatalf("expected count=2, got %v", out["count"])
	}
	if out["truncated"] != true {
		t.Fatalf("expected truncated=true, got %v", out["truncated"])
	}
	fileCount, ok := out["file_count"].(int)
	if !ok || fileCount <= 0 {
		t.Fatalf("expected file_count>0, got %v", out["file_count"])
	}
	scanned, ok := out["scanned_files"].(int)
	if !ok || scanned <= 0 {
		t.Fatalf("expected scanned_files>0, got %v", out["scanned_files"])
	}
}

func TestSearchTool_ReturnsColumn(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "a.txt")
	if err := os.WriteFile(path, []byte("xxHELLOyy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewSearch()
	out, err := tool.Run(context.Background(), map[string]any{
		"path":           path,
		"query":          "hello",
		"case_sensitive": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	hits, ok := out["hits"].([]map[string]any)
	if !ok {
		t.Fatalf("expected hits array, got %T", out["hits"])
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	column, ok := hits[0]["column"].(int)
	if !ok || column != 3 {
		t.Fatalf("expected column=3, got %v", hits[0]["column"])
	}
}
