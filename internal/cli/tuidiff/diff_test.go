package tuidiff

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuikit"
)

func TestBuildModel_ModifiedAndContext(t *testing.T) {
	m := BuildModel(Payload{
		Tool: "PATCH",
		Path: "a.txt",
		Old:  "line1\nold\nline3",
		New:  "line1\nnew\nline3",
	})
	if len(m.Rows) != 3 {
		t.Fatalf("expected changed row with nearby context, got %d", len(m.Rows))
	}
	foundModified := false
	foundContext := false
	for _, row := range m.Rows {
		if row.Kind == RowModified {
			foundModified = true
			if row.OldLineNo == 0 || row.NewLineNo == 0 {
				t.Fatalf("expected non-zero line numbers for modified row: %#v", row)
			}
			if len(row.OldSpans) == 0 || len(row.NewSpans) == 0 {
				t.Fatalf("expected inline spans for modified row: %#v", row)
			}
		}
		if row.Kind == RowContext {
			foundContext = true
		}
	}
	if !foundModified {
		t.Fatalf("expected modified row in %#v", m.Rows)
	}
	if !foundContext {
		t.Fatalf("expected context rows near modified hunk in %#v", m.Rows)
	}
}

func TestBuildModel_UnicodeInlineDiff(t *testing.T) {
	m := BuildModel(Payload{
		Tool: "PATCH",
		Path: "a.txt",
		Old:  "你好🙂abc",
		New:  "你好🙂axc",
	})
	if len(m.Rows) == 0 {
		t.Fatal("expected rows")
	}
	if m.Rows[0].Kind != RowModified {
		t.Fatalf("expected modified row, got %v", m.Rows[0].Kind)
	}
	if len(m.Rows[0].OldSpans) == 0 || len(m.Rows[0].NewSpans) == 0 {
		t.Fatal("expected inline spans for unicode diff")
	}
}

func TestRender_AdaptiveLayout(t *testing.T) {
	model := BuildModel(Payload{
		Tool: "PATCH",
		Path: "a.txt",
		Old:  "line1\nold",
		New:  "line1\nnew",
		Hunk: "@@ -1,2 +1,2 @@",
	})
	theme := tuikit.DefaultTheme()

	unified := Render(model, 100, theme)
	split := Render(model, 140, theme)
	unifiedText := ansi.Strip(strings.Join(unified, "\n"))
	splitText := ansi.Strip(strings.Join(split, "\n"))
	if !strings.Contains(unifiedText, "PATCH") || !strings.Contains(splitText, "PATCH") {
		t.Fatalf("expected patch header in both layouts")
	}
	if strings.Contains(unifiedText, " │ ") {
		t.Fatalf("unexpected split separator in unified layout: %q", unifiedText)
	}
	if !strings.Contains(splitText, " │ ") {
		t.Fatalf("expected split separator in split layout: %q", splitText)
	}
}

func TestRender_AddOnlyModelUsesSingleColumnEvenWhenWide(t *testing.T) {
	model := BuildModel(Payload{
		Tool:    "WRITE",
		Path:    "a.txt",
		Created: true,
		Old:     "",
		New:     "new line",
	})
	lines := Render(model, 160, tuikit.DefaultTheme())
	text := ansi.Strip(strings.Join(lines, "\n"))
	if strings.Contains(text, " │ ") {
		t.Fatalf("did not expect split separator for add-only model: %q", text)
	}
	if !strings.Contains(text, "+ new line") && !strings.Contains(text, "+ new line") {
		t.Fatalf("expected added line in output, got %q", text)
	}
}

func TestRender_TruncatedHasNoExtraNoteLine(t *testing.T) {
	model := BuildModel(Payload{
		Tool:      "PATCH",
		Path:      "a.txt",
		Old:       "old",
		New:       "new",
		Truncated: true,
	})
	lines := Render(model, 100, tuikit.DefaultTheme())
	text := ansi.Strip(strings.Join(lines, "\n"))
	if strings.Contains(text, "preview truncated") {
		t.Fatalf("did not expect truncated note line, got %q", text)
	}
}

func TestBuildModel_FoldsSeparatedHunks(t *testing.T) {
	m := BuildModel(Payload{
		Tool: "PATCH",
		Path: "a.txt",
		Old:  "a1\na2\na3\na4\na5\na6\na7\na8\na9\na10",
		New:  "a1\nx2\na3\na4\na5\na6\na7\na8\nx9\na10",
	})
	foundFold := false
	for _, row := range m.Rows {
		if row.Kind == RowFold {
			foundFold = true
			foldText := foldNote(row)
			if !strings.Contains(foldText, "@@ -") || !strings.Contains(foldText, "unchanged lines") {
				t.Fatalf("unexpected fold note: %#v", row)
			}
		}
	}
	if !foundFold {
		t.Fatalf("expected folded separator between hunks, got %#v", m.Rows)
	}
}

func TestRender_FoldedHunksShowSeparator(t *testing.T) {
	model := BuildModel(Payload{
		Tool: "PATCH",
		Path: "a.txt",
		Old:  "a1\na2\na3\na4\na5\na6\na7\na8\na9\na10",
		New:  "a1\nx2\na3\na4\na5\na6\na7\na8\nx9\na10",
	})
	lines := Render(model, 140, tuikit.DefaultTheme())
	text := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(text, "@@ -") || !strings.Contains(text, "unchanged lines") {
		t.Fatalf("expected folded separator in rendered diff, got %q", text)
	}
}
