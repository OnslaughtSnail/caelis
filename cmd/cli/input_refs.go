package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/OnslaughtSnail/caelis/kernel/skills"
)

var skillTriggerPattern = regexp.MustCompile(`\$([A-Za-z0-9][A-Za-z0-9._-]*)`)

type inputReferenceResolver struct {
	workspaceRoot string

	skillByName map[string]skills.Meta

	fileOnce sync.Once
	fileErr  error
	files    []string // workspace-relative, slash-separated
}

func newInputReferenceResolver(workspaceRoot string, skillDirs []string) (*inputReferenceResolver, []error, error) {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return nil, nil, fmt.Errorf("empty workspace root")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	resolver := &inputReferenceResolver{
		workspaceRoot: filepath.Clean(absRoot),
		skillByName:   map[string]skills.Meta{},
	}

	discovered := skills.DiscoverMeta(skillDirs)
	sort.Slice(discovered.Metas, func(i, j int) bool {
		return discovered.Metas[i].Path < discovered.Metas[j].Path
	})
	for _, one := range discovered.Metas {
		key := normalizeSkillName(one.Name)
		if key == "" {
			continue
		}
		if _, exists := resolver.skillByName[key]; exists {
			continue
		}
		resolver.skillByName[key] = one
	}
	return resolver, discovered.Warnings, nil
}

func normalizeSkillName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func normalizeSlashPath(pathText string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimSpace(pathText)))
}

// RewriteResult carries the output of RewriteInput.
type RewriteResult struct {
	Text          string   // rewritten input text with resolved mentions
	Notes         []string // informational notes (e.g. unresolved mentions)
	ResolvedPaths []string // workspace-relative paths of all resolved file references
}

func (r *inputReferenceResolver) RewriteInput(input string) (RewriteResult, error) {
	if r == nil {
		return RewriteResult{Text: input}, nil
	}
	text := strings.TrimSpace(input)
	if text == "" {
		return RewriteResult{Text: input}, nil
	}

	notes := make([]string, 0, 4)
	referenced := make([]string, 0, 8)
	referencedSet := map[string]struct{}{}

	addReference := func(pathText string) {
		cleaned := strings.TrimSpace(pathText)
		if cleaned == "" {
			return
		}
		if _, exists := referencedSet[cleaned]; exists {
			return
		}
		referencedSet[cleaned] = struct{}{}
		referenced = append(referenced, cleaned)
	}

	text = skillTriggerPattern.ReplaceAllStringFunc(text, func(token string) string {
		name := normalizeSkillName(strings.TrimPrefix(token, "$"))
		meta, ok := r.skillByName[name]
		if !ok {
			return token
		}
		pathText := r.displayPath(meta.Path)
		addReference(pathText)
		return "@" + pathText
	})

	rewritten, resolved, unresolved, err := r.rewriteFileMentions(text)
	if err != nil {
		return RewriteResult{Text: input}, err
	}
	for _, one := range resolved {
		addReference(one)
	}
	if len(unresolved) > 0 {
		for _, one := range unresolved {
			notes = append(notes, fmt.Sprintf("unresolved mention @%s", one))
		}
	}

	return RewriteResult{
		Text:          strings.TrimSpace(rewritten),
		Notes:         notes,
		ResolvedPaths: referenced,
	}, nil
}

func (r *inputReferenceResolver) rewriteFileMentions(text string) (string, []string, []string, error) {
	runes := []rune(text)
	var b strings.Builder
	resolved := make([]string, 0, 8)
	resolvedSet := map[string]struct{}{}
	unresolved := make([]string, 0, 2)
	unresolvedSet := map[string]struct{}{}

	addResolved := func(pathText string) {
		if _, exists := resolvedSet[pathText]; exists {
			return
		}
		resolvedSet[pathText] = struct{}{}
		resolved = append(resolved, pathText)
	}
	addUnresolved := func(query string) {
		if _, exists := unresolvedSet[query]; exists {
			return
		}
		unresolvedSet[query] = struct{}{}
		unresolved = append(unresolved, query)
	}

	for i := 0; i < len(runes); {
		if runes[i] == '@' && isMentionBoundary(runes, i) {
			j := i + 1
			for j < len(runes) && isMentionQueryRune(runes[j]) {
				j++
			}
			if j > i+1 {
				query := string(runes[i+1 : j])
				if resolvedPath, ok, err := r.ResolveMention(query); err != nil {
					return "", nil, nil, err
				} else if ok {
					b.WriteString(formatResolvedMentionPrompt(r.AbsPath(resolvedPath)))
					addResolved(resolvedPath)
				} else {
					b.WriteString(string(runes[i:j]))
					addUnresolved(query)
				}
				i = j
				continue
			}
		}
		b.WriteRune(runes[i])
		i++
	}
	return b.String(), resolved, unresolved, nil
}

func formatResolvedMentionPrompt(absPath string) string {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(absPath)))
	if cleaned == "" {
		return ""
	}
	return "请阅读文件: " + cleaned
}

func (r *inputReferenceResolver) ResolveMention(query string) (string, bool, error) {
	if r == nil {
		return "", false, nil
	}
	query = normalizeSlashPath(query)
	if query == "" || query == "." {
		return "", false, nil
	}

	if existing, ok := r.resolveExistingPath(query); ok {
		return existing, true, nil
	}

	candidates, err := r.CompleteFiles(query, 1)
	if err != nil {
		return "", false, err
	}
	if len(candidates) == 0 {
		return "", false, nil
	}
	return candidates[0], true, nil
}

func (r *inputReferenceResolver) resolveExistingPath(query string) (string, bool) {
	candidate := query
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(r.workspaceRoot, filepath.FromSlash(query))
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", false
	}
	return r.displayPath(candidate), true
}

// AbsPath resolves a workspace-relative path to an absolute path.
func (r *inputReferenceResolver) AbsPath(relPath string) string {
	if filepath.IsAbs(relPath) {
		return filepath.Clean(relPath)
	}
	return filepath.Join(r.workspaceRoot, filepath.FromSlash(relPath))
}

func (r *inputReferenceResolver) displayPath(pathText string) string {
	abs := pathText
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(r.workspaceRoot, abs)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(r.workspaceRoot, abs)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(filepath.Clean(rel))
	}
	return filepath.ToSlash(abs)
}

func (r *inputReferenceResolver) CompleteFiles(query string, limit int) ([]string, error) {
	if r == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 8
	}
	if err := r.ensureFilesLoaded(); err != nil {
		return nil, err
	}

	query = strings.ToLower(normalizeSlashPath(query))
	if query == "." {
		query = ""
	}

	type match struct {
		path  string
		score int
	}
	matches := make([]match, 0, minInt(limit*4, 64))
	for _, one := range r.files {
		score, ok := mentionMatchScore(query, one)
		if !ok {
			continue
		}
		matches = append(matches, match{path: one, score: score})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		if len(matches[i].path) != len(matches[j].path) {
			return len(matches[i].path) < len(matches[j].path)
		}
		return matches[i].path < matches[j].path
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	out := make([]string, 0, len(matches))
	for _, one := range matches {
		out = append(out, one.path)
	}
	return out, nil
}

func (r *inputReferenceResolver) ensureFilesLoaded() error {
	if r == nil {
		return nil
	}
	r.fileOnce.Do(func() {
		r.files, r.fileErr = collectWorkspaceFiles(r.workspaceRoot)
	})
	return r.fileErr
}

func collectWorkspaceFiles(root string) ([]string, error) {
	out := make([]string, 0, 2048)
	err := filepath.WalkDir(root, func(pathText string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			if skipMentionDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, pathText)
		if err != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(filepath.Clean(rel)))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func skipMentionDir(name string) bool {
	switch name {
	case ".git", ".codex", "node_modules", ".idea", ".vscode":
		return true
	default:
		return false
	}
}

func mentionMatchScore(query string, candidate string) (int, bool) {
	candidate = strings.ToLower(candidate)
	if query == "" {
		return 100 + len(candidate), true
	}
	base := path.Base(candidate)
	switch {
	case candidate == query:
		return 0, true
	case base == query:
		return 1, true
	case strings.HasPrefix(candidate, query):
		return 2, true
	case strings.HasPrefix(base, query):
		return 3, true
	case strings.Contains(candidate, query):
		return 4 + len(candidate) - len(query), true
	case isSubsequence(query, base):
		return 20 + len(base), true
	case isSubsequence(query, candidate):
		return 30 + len(candidate), true
	default:
		return 0, false
	}
}

func isSubsequence(query string, text string) bool {
	if query == "" {
		return true
	}
	qrunes := []rune(query)
	qi := 0
	for _, r := range text {
		if qi >= len(qrunes) {
			return true
		}
		if qrunes[qi] == r {
			qi++
		}
	}
	return qi == len(qrunes)
}

func mentionQueryAtCursor(input []rune, cursor int) (int, int, string, bool) {
	if len(input) == 0 {
		return 0, 0, "", false
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	start := cursor
	for start > 0 && isMentionQueryRune(input[start-1]) {
		start--
	}
	if start == 0 || input[start-1] != '@' {
		return 0, 0, "", false
	}
	at := start - 1
	if !isMentionBoundary(input, at) {
		return 0, 0, "", false
	}
	end := cursor
	for end < len(input) && isMentionQueryRune(input[end]) {
		end++
	}
	query := string(input[start:end])
	return at, end, query, true
}

func isMentionBoundary(input []rune, at int) bool {
	if at <= 0 {
		return true
	}
	prev := input[at-1]
	if unicode.IsSpace(prev) {
		return true
	}
	switch prev {
	case '(', '[', '{', ',', ';', ':', '"', '\'':
		return true
	default:
		return false
	}
}

func isMentionQueryRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' || r == '/' || r == '\\'
}

func replaceRuneSpan(input []rune, start int, end int, replacement string) ([]rune, int) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(input) {
		end = len(input)
	}
	head := append([]rune(nil), input[:start]...)
	replRunes := []rune(replacement)
	head = append(head, replRunes...)
	head = append(head, input[end:]...)
	return head, start + len(replRunes)
}
