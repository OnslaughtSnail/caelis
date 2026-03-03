package main

import (
	"strings"
	"testing"
)

func TestBuildPatchDiffBlockMsg_FromCallArgs(t *testing.T) {
	msg, tooLarge, ok := buildPatchDiffBlockMsg("PATCH", map[string]any{
		"path":    "/tmp/work/a.txt",
		"created": false,
		"metadata": map[string]any{
			"patch": map[string]any{"hunk": "@@ -1,1 +1,1 @@"},
		},
	}, map[string]any{
		"path": "/tmp/work/a.txt",
		"old":  "old line",
		"new":  "new line",
	})
	if !ok || tooLarge {
		t.Fatalf("expected ok rich diff payload, ok=%v tooLarge=%v", ok, tooLarge)
	}
	if msg.Path != "a.txt" {
		t.Fatalf("expected basename path, got %q", msg.Path)
	}
	if msg.Old != "old line" || msg.New != "new line" {
		t.Fatalf("unexpected old/new: %#v", msg)
	}
	if msg.Hunk != "@@ -1,1 +1,1 @@" {
		t.Fatalf("unexpected hunk: %q", msg.Hunk)
	}
}

func TestBuildPatchDiffBlockMsg_FromPreviewFallback(t *testing.T) {
	msg, tooLarge, ok := buildPatchDiffBlockMsg("PATCH", map[string]any{
		"path": "a.txt",
		"metadata": map[string]any{
			"patch": map[string]any{
				"preview": "--- old\n+++ new\n-old\n+new\n... (preview truncated)",
			},
		},
	}, nil)
	if !ok || tooLarge {
		t.Fatalf("expected parsed preview payload, ok=%v tooLarge=%v", ok, tooLarge)
	}
	if msg.Old != "old" || msg.New != "new" {
		t.Fatalf("expected parsed +/- lines, got old=%q new=%q", msg.Old, msg.New)
	}
	if !msg.Truncated {
		t.Fatal("expected truncated=true from preview marker")
	}
}

func TestBuildPatchDiffBlockMsg_TooLarge(t *testing.T) {
	oldLines := strings.Repeat("a\n", 500)
	newLines := strings.Repeat("b\n", 500)
	_, tooLarge, ok := buildPatchDiffBlockMsg("PATCH", map[string]any{"path": "a.txt"}, map[string]any{
		"old": oldLines,
		"new": newLines,
	})
	if !ok {
		t.Fatal("expected PATCH payload to be recognized")
	}
	if !tooLarge {
		t.Fatal("expected tooLarge=true for >800 lines")
	}
}

func TestParsePatchPreview_RejectsNonDiffPreview(t *testing.T) {
	_, _, _, ok := parsePatchPreview("this is not a diff")
	if ok {
		t.Fatal("expected parse failure for non-diff preview")
	}
}
