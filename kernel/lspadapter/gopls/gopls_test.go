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

func TestFlattenWorkspaceEdit(t *testing.T) {
	edit := lspWorkspaceEdit{
		Changes: map[string][]lspTextEdit{
			"file:///tmp/a.go": {{
				Range:   lspRange{Start: lspPosition{Line: 0, Character: 0}, End: lspPosition{Line: 0, Character: 1}},
				NewText: "A",
			}},
		},
		DocumentChanges: []lspTextDocumentEditHolder{{
			TextDocument: &lspVersionedTextDocumentIdentifier{URI: "file:///tmp/b.go"},
			Edits: []lspTextEdit{{
				Range:   lspRange{Start: lspPosition{Line: 1, Character: 0}, End: lspPosition{Line: 1, Character: 1}},
				NewText: "B",
			}},
		}},
	}
	out := flattenWorkspaceEdit(edit)
	if len(out) != 2 {
		t.Fatalf("expected 2 files, got %d", len(out))
	}
	if len(out[filepath.Clean("/tmp/a.go")]) != 1 {
		t.Fatalf("expected one edit for /tmp/a.go")
	}
	if len(out[filepath.Clean("/tmp/b.go")]) != 1 {
		t.Fatalf("expected one edit for /tmp/b.go")
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
