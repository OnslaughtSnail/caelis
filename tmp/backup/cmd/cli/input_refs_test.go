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
	if got, want := result.DisplayText, "请按 $atlas 执行"; got != want {
		t.Fatalf("expected visible skill trigger preserved in user input, got %q want %q", got, want)
	}
	if !strings.Contains(result.Text, inputReferenceOpenTag) || !strings.Contains(result.Text, "Load atlas Skills.") {
		t.Fatalf("expected hidden skill hint injected into model text, got %q", result.Text)
	}
	if got := stripHiddenInputReferenceHints(result.Text); got != result.DisplayText {
		t.Fatalf("expected hidden hint stripping to recover visible text, got %q want %q", got, result.DisplayText)
	}
	if len(result.ResolvedPaths) != 0 {
		t.Fatalf("did not expect skill trigger to register referenced files, got %v", result.ResolvedPaths)
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
	result, err := resolver.RewriteInput("看一下 #schema.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Notes) != 0 {
		t.Fatalf("did not expect rewrite notes, got %v", result.Notes)
	}
	want := "请阅读文件: " + filepath.ToSlash(filepath.Join(workspace, "kernel", "tool", "schema.go"))
	if !strings.Contains(result.Text, want) {
		t.Fatalf("expected fuzzy mention resolved to absolute read prompt %q, got %q", want, result.Text)
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
	result, err := resolver.RewriteInput("看这个截图 #screenshot.png 和代码 #main.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Notes) != 0 {
		t.Fatalf("did not expect rewrite notes, got %v", result.Notes)
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
	input := []rune("请检查 #kernel/to")
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
	mustWriteFile(t, filepath.Join(workspace, "internal", "app", "skills", "meta.go"), "package skills\n")

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
		if one == "internal/app/skills/meta.go" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected internal/app/skills/meta.go in candidates: %v", candidates)
	}
}

func TestInputReferenceResolver_CompleteFiles_RespectsGitignore(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(workspace, ".gitignore"), "ignored/\nsecret.txt\n")
	mustWriteFile(t, filepath.Join(workspace, "visible.txt"), "visible\n")
	mustWriteFile(t, filepath.Join(workspace, "secret.txt"), "secret\n")
	mustWriteFile(t, filepath.Join(workspace, "ignored", "hidden.go"), "package ignored\n")

	resolver, _, err := newInputReferenceResolver(workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := resolver.CompleteFiles("", 20)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(candidates, ",")
	if strings.Contains(joined, "secret.txt") || strings.Contains(joined, "ignored/hidden.go") {
		t.Fatalf("expected gitignored files excluded, got %v", candidates)
	}
	if !strings.Contains(joined, "visible.txt") {
		t.Fatalf("expected visible file present, got %v", candidates)
	}
}

func TestFormatResolvedMentionPrompt_UsesAbsolutePath(t *testing.T) {
	got := formatResolvedMentionPrompt("/tmp/workspace/build.sh")
	if got != "请阅读文件: /tmp/workspace/build.sh" {
		t.Fatalf("unexpected read prompt: %q", got)
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
