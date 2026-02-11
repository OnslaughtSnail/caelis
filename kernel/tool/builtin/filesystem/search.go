package filesystem

import (
	"bufio"
	"context"
	"errors"
	"io/fs"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
)

const (
	SearchToolName = "SEARCH"
)

var errSearchLimitReached = errors.New("search: limit reached")

type SearchTool struct {
	runtime toolexec.Runtime
}

func NewSearch() *SearchTool {
	tool, _ := NewSearchWithRuntime(nil)
	return tool
}

func NewSearchWithRuntime(runtime toolexec.Runtime) (*SearchTool, error) {
	resolvedRuntime, err := runtimeOrDefault(runtime)
	if err != nil {
		return nil, err
	}
	return &SearchTool{runtime: resolvedRuntime}, nil
}

func (t *SearchTool) Name() string {
	return SearchToolName
}

func (t *SearchTool) Description() string {
	return "Search text in a file or directory recursively."
}

func (t *SearchTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":           map[string]any{"type": "string", "description": "target file or directory path"},
				"query":          map[string]any{"type": "string", "description": "search text"},
				"limit":          map[string]any{"type": "integer", "description": "max results"},
				"case_sensitive": map[string]any{"type": "boolean", "description": "case sensitive search"},
			},
			"required": []string{"path", "query"},
		},
	}
}

func (t *SearchTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	pathArg, err := argparse.String(args, "path", true)
	if err != nil {
		return nil, err
	}
	query, err := argparse.String(args, "query", true)
	if err != nil {
		return nil, err
	}
	limit, err := argparse.Int(args, "limit", 50)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	caseSensitive := false
	if raw, ok := args["case_sensitive"].(bool); ok {
		caseSensitive = raw
	}
	target, err := normalizePathWithFS(t.runtime.FileSystem(), pathArg)
	if err != nil {
		return nil, err
	}
	info, err := t.runtime.FileSystem().Stat(target)
	if err != nil {
		return nil, err
	}

	queryToMatch := query
	if !caseSensitive {
		queryToMatch = strings.ToLower(query)
	}
	results := make([]map[string]any, 0, limit)
	filesWithHits := map[string]struct{}{}
	truncated := false
	appendMatch := func(path string, lineNum, column int, text string) bool {
		filesWithHits[path] = struct{}{}
		results = append(results, map[string]any{
			"path":   path,
			"line":   lineNum,
			"column": column,
			"text":   text,
		})
		if len(results) >= limit {
			truncated = true
		}
		return len(results) >= limit
	}
	scannedFiles := 0

	if info.IsDir() {
		walkErr := walkDir(t.runtime.FileSystem(), target, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d == nil || d.IsDir() {
				return nil
			}
			scannedFiles++
			matched, stop := searchInFile(t.runtime.FileSystem(), path, queryToMatch, caseSensitive, appendMatch)
			_ = matched
			if stop {
				return errSearchLimitReached
			}
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, errSearchLimitReached) {
			return nil, walkErr
		}
	} else {
		scannedFiles = 1
		_, stop := searchInFile(t.runtime.FileSystem(), target, queryToMatch, caseSensitive, appendMatch)
		if stop {
			truncated = true
		}
	}

	return map[string]any{
		"path":          target,
		"query":         query,
		"count":         len(results),
		"file_count":    len(filesWithHits),
		"scanned_files": scannedFiles,
		"limit":         limit,
		"truncated":     truncated,
		"hits":          results,
	}, nil
}

func searchInFile(fsys toolexec.FileSystem, path, query string, caseSensitive bool, appendMatch func(string, int, int, string) bool) (bool, bool) {
	file, err := fsys.Open(path)
	if err != nil {
		return false, false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	matched := false
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		text := scanner.Text()
		candidate := text
		if !caseSensitive {
			candidate = strings.ToLower(candidate)
		}
		if strings.Contains(candidate, query) {
			matched = true
			column := strings.Index(candidate, query) + 1
			if column <= 0 {
				column = 1
			}
			if appendMatch(path, lineNum, column, text) {
				return true, true
			}
		}
	}
	return matched, false
}
