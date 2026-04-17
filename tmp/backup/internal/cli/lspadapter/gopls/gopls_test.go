package gopls

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestUserPositionToLSP(t *testing.T) {
	lines := []string{"hello😀", "world"}
	pos, err := userPositionToLSP(lines, 1, 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pos.Line != 0 || pos.Character != 7 {
		t.Fatalf("unexpected position: %+v", pos)
	}

	line, col := lspPositionToUser(lines, lspPosition{Line: 0, Character: 7})
	if line != 1 || col != 7 {
		t.Fatalf("unexpected reverse position: line=%d col=%d", line, col)
	}
}

func TestDecodeLocations(t *testing.T) {
	raw := json.RawMessage(`[{
		"targetUri":"file:///tmp/a.go",
		"targetRange":{"start":{"line":1,"character":2},"end":{"line":1,"character":4}}
	}]`)
	items, err := decodeLocations(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 location, got %d", len(items))
	}
	if items[0].URI != "file:///tmp/a.go" {
		t.Fatalf("unexpected uri: %q", items[0].URI)
	}
}

func TestMatchScore(t *testing.T) {
	tests := []struct {
		name       string
		queryLower string
		want       int
	}{
		{"HandleRequest", "handlerequest", 3}, // exact (case-insensitive)
		{"HandleRequest", "handle", 2},        // prefix
		{"MyHandleRequest", "handle", 1},      // contains
		{"Foo", "handle", 0},                  // no match
	}
	for _, tt := range tests {
		got := matchScore(tt.name, tt.queryLower)
		if got != tt.want {
			t.Errorf("matchScore(%q, %q) = %d, want %d", tt.name, tt.queryLower, got, tt.want)
		}
	}
}

func TestSymbolKindToString(t *testing.T) {
	tests := []struct {
		kind int
		want string
	}{
		{6, "method"},
		{12, "function"},
		{5, "class"},
		{23, "struct"},
		{11, "interface"},
		{13, "variable"},
		{14, "constant"},
		{99, "unknown"},
	}
	for _, tt := range tests {
		got := symbolKindToString(tt.kind)
		if got != tt.want {
			t.Errorf("symbolKindToString(%d) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestConvertLocations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	uri := mustPathToURI(path)
	items := convertLocations([]lspLocation{{
		URI: uri,
		Range: lspRange{
			Start: lspPosition{Line: 1, Character: 5},
			End:   lspPosition{Line: 1, Character: 9},
		},
	}})
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Path != filepath.Clean(path) {
		t.Fatalf("unexpected path %q", items[0].Path)
	}
	if items[0].Line != 2 || items[0].Column != 6 {
		t.Fatalf("unexpected start position: %+v", items[0])
	}
}
