package tuiapp

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestLayoutComposerDisplay_WrapsComplexGraphemeAsSingleUnit(t *testing.T) {
	displayValue, displayCursor := composeInputDisplay("A👨‍👩‍👧B", len([]rune("A👨‍👩‍👧")), nil)
	layout := layoutComposerDisplay(displayValue, displayCursor, 3)

	if layout.totalRows != 2 {
		t.Fatalf("expected two wrapped rows, got %+v", layout)
	}
	if layout.rows[0] != "A👨‍👩‍👧" {
		t.Fatalf("expected first row to keep family emoji intact, got %q", layout.rows[0])
	}
	if layout.rows[1] != "B" {
		t.Fatalf("expected trailing text on second row, got %q", layout.rows[1])
	}
	if layout.cursorRow != 0 || layout.cursorCol != 3 {
		t.Fatalf("expected cursor after full grapheme cluster, got row=%d col=%d", layout.cursorRow, layout.cursorCol)
	}
}

func TestLayoutComposerDisplay_SnapsCursorBeforeIncompleteGrapheme(t *testing.T) {
	layout := layoutComposerDisplay("A👨‍👩‍👧B", len([]rune("A👨")), 10)

	if layout.cursorRow != 0 {
		t.Fatalf("expected single-row layout, got row=%d", layout.cursorRow)
	}
	if layout.cursorCol != 1 {
		t.Fatalf("expected cursor to stay before incomplete grapheme cluster, got col=%d", layout.cursorCol)
	}
}

func TestRenderInputBar_VisibleTailPreservesComplexGraphemesAndAttachments(t *testing.T) {
	m := newTestModel()
	_, _ = m.Update(tea.WindowSizeMsg{Width: 28, Height: 16})

	value := strings.Join([]string{
		"line1",
		"family 👨‍👩‍👧 zone",
		"tail 👍🏻 end",
		"flag 🇺🇸 done",
	}, "\n")
	m.textarea.SetValue(value)
	m.textarea.CursorEnd()
	m.setInputAttachments([]inputAttachment{{Name: "clip.png", Offset: len([]rune("line1\n"))}})
	m.syncInputFromTextarea()

	rendered := ansi.Strip(m.renderInputBar())
	if strings.Contains(rendered, "line1") {
		t.Fatalf("expected oldest row clipped from tail window, got %q", rendered)
	}
	for _, want := range []string{"[clip.png]", "👨‍👩‍👧", "👍🏻", "🇺🇸"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected visible tail to preserve %q, got %q", want, rendered)
		}
	}
}
