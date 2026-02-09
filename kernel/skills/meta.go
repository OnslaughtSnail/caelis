package skills

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Meta describes one discovered skill metadata entry.
type Meta struct {
	Name        string
	Description string
	Tags        []string
	Version     string
	Path        string
}

// DiscoverResult includes discovered skills and non-fatal warnings.
type DiscoverResult struct {
	Metas    []Meta
	Warnings []error
}

// DiscoverMeta scans skill directories and returns all valid SKILL.md metadata.
func DiscoverMeta(dirs []string) DiscoverResult {
	out := DiscoverResult{
		Metas:    []Meta{},
		Warnings: []error{},
	}
	seen := make(map[string]struct{})

	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		resolvedDir, err := resolveDir(dir)
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Errorf("skills: resolve %q: %w", dir, err))
			continue
		}
		info, err := os.Stat(resolvedDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			out.Warnings = append(out.Warnings, fmt.Errorf("skills: stat %q: %w", resolvedDir, err))
			continue
		}
		if !info.IsDir() {
			out.Warnings = append(out.Warnings, fmt.Errorf("skills: %q is not a directory", resolvedDir))
			continue
		}

		_ = filepath.WalkDir(resolvedDir, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				out.Warnings = append(out.Warnings, fmt.Errorf("skills: walk %q: %w", path, walkErr))
				return nil
			}
			if d == nil || d.IsDir() || strings.ToUpper(d.Name()) != "SKILL.MD" {
				return nil
			}

			normalized := filepath.Clean(path)
			if _, exists := seen[normalized]; exists {
				return nil
			}
			meta, err := parseSkillMeta(normalized)
			if err != nil {
				out.Warnings = append(out.Warnings, err)
				return nil
			}
			seen[normalized] = struct{}{}
			out.Metas = append(out.Metas, meta)
			return nil
		})
	}

	sort.Slice(out.Metas, func(i, j int) bool {
		return out.Metas[i].Path < out.Metas[j].Path
	})
	return out
}

func resolveDir(dir string) (string, error) {
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
	}
	if !filepath.IsAbs(dir) {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(wd, dir)
	}
	return filepath.Clean(dir), nil
}

func parseSkillMeta(path string) (Meta, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Meta{}, fmt.Errorf("skills: read %q: %w", path, err)
	}
	content := normalizeText(string(raw))
	if strings.TrimSpace(content) == "" {
		return Meta{}, fmt.Errorf("skills: empty SKILL.md: %q", path)
	}

	fm, body := parseFrontMatter(content)
	name := firstNonEmpty(
		fm["name"],
		firstHeading(body),
		filepath.Base(filepath.Dir(path)),
	)
	description := firstNonEmpty(
		fm["description"],
		firstParagraph(body),
	)
	if name == "" || description == "" {
		return Meta{}, fmt.Errorf("skills: invalid skill format %q (name/description is required)", path)
	}

	return Meta{
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		Tags:        parseTags(fm["tags"]),
		Version:     strings.TrimSpace(fm["version"]),
		Path:        path,
	}, nil
}

func parseFrontMatter(content string) (map[string]string, string) {
	trimmed := strings.TrimLeft(content, "\n\r\t ")
	if !strings.HasPrefix(trimmed, "---\n") {
		return map[string]string{}, content
	}
	rest := strings.TrimPrefix(trimmed, "---\n")
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return map[string]string{}, content
	}
	front := rest[:idx]
	body := rest[idx+len("\n---\n"):]

	result := map[string]string{}
	lines := strings.Split(front, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(parts[0]))
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, `"'`)
		result[key] = value
	}
	return result, body
}

func firstHeading(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func firstParagraph(content string) string {
	lines := strings.Split(content, "\n")
	paragraph := make([]string, 0, 4)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			continue
		}
		paragraph = append(paragraph, trimmed)
		if len(paragraph) >= 2 {
			break
		}
	}
	return strings.Join(paragraph, " ")
}

func parseTags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		tag := strings.TrimSpace(strings.Trim(p, `"'`))
		if tag == "" {
			continue
		}
		out = append(out, tag)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func normalizeText(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	return strings.TrimSpace(input)
}

// BuildMetaPrompt renders skills metadata for system prompt injection.
func BuildMetaPrompt(metas []Meta) string {
	if len(metas) == 0 {
		return ""
	}
	var b bytes.Buffer
	b.WriteString("Skills Metadata (auto-loaded, all active):\n")
	for _, m := range metas {
		line := fmt.Sprintf("- name=%q; description=%q; tags=%q; version=%q; path=%q\n",
			m.Name,
			m.Description,
			strings.Join(m.Tags, ","),
			m.Version,
			m.Path,
		)
		b.WriteString(line)
	}
	return strings.TrimSpace(b.String())
}
