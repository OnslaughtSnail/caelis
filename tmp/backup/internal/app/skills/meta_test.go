package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverMeta(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "skills", "echo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(dir, "SKILL.md")
	content := `---
name: echo_skill
description: Echo helper skill.
tags: [tool, local]
version: v1
---
# Echo Skill

Echo helper skill description.
`
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result := DiscoverMeta([]string{filepath.Join(root, "skills")})
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %d", len(result.Warnings))
	}
	if len(result.Metas) != 1 {
		t.Fatalf("expected 1 skill meta, got %d", len(result.Metas))
	}
	meta := result.Metas[0]
	if meta.Name != "echo_skill" {
		t.Fatalf("unexpected name: %q", meta.Name)
	}
	if meta.Description == "" {
		t.Fatalf("description should not be empty")
	}
	if len(meta.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(meta.Tags))
	}
}

func TestBuildMetaPrompt(t *testing.T) {
	text := BuildMetaPrompt([]Meta{
		{Name: "a", Description: "desc", Tags: []string{"x"}, Version: "v1", Path: "/tmp/a/SKILL.md"},
	})
	if !strings.Contains(text, "## Skills") {
		t.Fatalf("missing header in prompt: %q", text)
	}
	if !strings.Contains(text, "- a: desc (file: /tmp/a/SKILL.md)") {
		t.Fatalf("missing skill name: %q", text)
	}
	if !strings.Contains(text, "Use a skill only when its description clearly matches the task.") {
		t.Fatalf("missing compact usage rule: %q", text)
	}
	if strings.Contains(text, "### How to use skills") {
		t.Fatalf("expected shorter skill guidance, got %q", text)
	}
	if strings.Contains(text, "must not redefine who you are") {
		t.Fatalf("expected shorter skill guidance, got %q", text)
	}
}

func TestDiscoverMeta_InvalidFormat(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "skills", "bad")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	result := DiscoverMeta([]string{filepath.Join(root, "skills")})
	if len(result.Metas) != 0 {
		t.Fatalf("expected no valid skill meta")
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected warnings for invalid skill")
	}
}

func TestDiscoverMeta_OnlyLoadsFirstLevelSkillDirs(t *testing.T) {
	root := t.TempDir()
	skillsRoot := filepath.Join(root, "skills")
	topLevelDir := filepath.Join(skillsRoot, "top")
	nestedDir := filepath.Join(skillsRoot, ".system", "nested")
	if err := os.MkdirAll(topLevelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	topSkill := `---
name: top_skill
description: Top level skill.
---
# Top Skill
`
	nestedSkill := `---
name: nested_skill
description: Nested skill.
---
# Nested Skill
`
	if err := os.WriteFile(filepath.Join(topLevelDir, "SKILL.md"), []byte(topSkill), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "SKILL.md"), []byte(nestedSkill), 0o644); err != nil {
		t.Fatal(err)
	}

	result := DiscoverMeta([]string{skillsRoot})
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", result.Warnings)
	}
	if len(result.Metas) != 1 {
		t.Fatalf("expected only first-level skill to load, got %#v", result.Metas)
	}
	if result.Metas[0].Name != "top_skill" {
		t.Fatalf("unexpected loaded skill: %#v", result.Metas[0])
	}
}
