package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	image "github.com/OnslaughtSnail/caelis/internal/cli/imageutil"
)

func TestInputReferenceResolver_RewriteInput_SkillTrigger(t *testing.T) {
	workspace := t.TempDir()
	skillRoot := filepath.Join(t.TempDir(), "skills")
	skillDir := filepath.Join(skillRoot, "atlas")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	skillDoc := `---
name: atlas
description: atlas skill
---
# atlas
`
	if err := os.WriteFile(skillFile, []byte(skillDoc), 0o644); err != nil {
		t.Fatal(err)
	}

	resolver, warnings, err := newInputReferenceResolver(workspace, []string{skillRoot})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %d", len(warnings))
	}
	result, err := resolver.RewriteInput("请按 $atlas 执行")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "@") || !strings.Contains(result.Text, "SKILL.md") {
		t.Fatalf("expected SKILL mention, got %q", result.Text)
	}
	if !strings.Contains(result.Text, "Referenced files:") {
		t.Fatalf("expected referenced files section, got %q", result.Text)
	}
}

func TestInputReferenceResolver_RewriteInput_FileMentionFuzzy(t *testing.T) {
	workspace := t.TempDir()
	mustWriteFile(t, filepath.Join(workspace, "kernel", "tool", "schema.go"), "package tool\n")
	mustWriteFile(t, filepath.Join(workspace, "kernel", "skills", "meta.go"), "package skills\n")

	resolver, _, err := newInputReferenceResolver(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolver.RewriteInput("看一下 @schema.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Notes) != 0 {
		t.Fatalf("expected no unresolved notes, got %v", result.Notes)
	}
	if !strings.Contains(result.Text, "@kernel/tool/schema.go") {
		t.Fatalf("expected fuzzy mention resolved, got %q", result.Text)
	}
}

func TestInputReferenceResolver_RewriteInput_ImageMention(t *testing.T) {
	workspace := t.TempDir()
	mustWriteFile(t, filepath.Join(workspace, "docs", "screenshot.png"), "fake-png-data")
	mustWriteFile(t, filepath.Join(workspace, "src", "main.go"), "package main\n")

	resolver, _, err := newInputReferenceResolver(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolver.RewriteInput("看这个截图 @screenshot.png 和代码 @main.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Notes) != 0 {
		t.Fatalf("expected no unresolved notes, got %v", result.Notes)
	}
	if len(result.ResolvedPaths) != 2 {
		t.Fatalf("expected 2 resolved paths, got %d: %v", len(result.ResolvedPaths), result.ResolvedPaths)
	}
	// Verify image detection on resolved paths.
	imageCount := 0
	for _, p := range result.ResolvedPaths {
		if image.IsImagePath(p) {
			imageCount++
		}
	}
	if imageCount != 1 {
		t.Fatalf("expected 1 image path, got %d (paths: %v)", imageCount, result.ResolvedPaths)
	}
	// Verify AbsPath resolution.
	for _, p := range result.ResolvedPaths {
		abs := resolver.AbsPath(p)
		if !filepath.IsAbs(abs) {
			t.Fatalf("expected absolute path, got %q", abs)
		}
	}
}

func TestMentionQueryAtCursor(t *testing.T) {
	input := []rune("请检查 @kernel/to")
	start, end, query, ok := mentionQueryAtCursor(input, len(input))
	if !ok {
		t.Fatal("expected mention query found")
	}
	if start < 0 || end <= start {
		t.Fatalf("unexpected span: start=%d end=%d", start, end)
	}
	if query != "kernel/to" {
		t.Fatalf("unexpected query: %q", query)
	}
}

func TestInputReferenceResolver_CompleteFiles(t *testing.T) {
	workspace := t.TempDir()
	mustWriteFile(t, filepath.Join(workspace, "cmd", "cli", "console.go"), "package main\n")
	mustWriteFile(t, filepath.Join(workspace, "kernel", "skills", "meta.go"), "package skills\n")

	resolver, _, err := newInputReferenceResolver(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := resolver.CompleteFiles("meta", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected non-empty candidates")
	}
	found := false
	for _, one := range candidates {
		if one == "kernel/skills/meta.go" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected kernel/skills/meta.go in candidates: %v", candidates)
	}
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
