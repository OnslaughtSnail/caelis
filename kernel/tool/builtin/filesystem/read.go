package filesystem

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool/builtin/internal/argparse"
	"github.com/OnslaughtSnail/caelis/kernel/tool/capability"
)

const (
	// ReadToolName is the built-in file read tool name.
	ReadToolName = "READ"
)

// ReadConfig configures the built-in READ tool.
type ReadConfig struct {
	DefaultLimit     int
	MaxLimit         int
	DefaultMaxTokens int
	MaxTokens        int
}

// DefaultReadConfig returns safe defaults for the built-in READ tool.
func DefaultReadConfig() ReadConfig {
	return ReadConfig{
		DefaultLimit:     200,
		MaxLimit:         400,
		DefaultMaxTokens: 2000,
		MaxTokens:        4000,
	}
}

// ReadTool is built-in READ implementation.
type ReadTool struct {
	cfg     ReadConfig
	runtime toolexec.Runtime
}

// NewReadWithRuntime creates READ tool with one execution runtime.
func NewReadWithRuntime(cfg ReadConfig, runtime toolexec.Runtime) (*ReadTool, error) {
	if cfg.DefaultLimit <= 0 || cfg.MaxLimit <= 0 || cfg.DefaultMaxTokens <= 0 || cfg.MaxTokens <= 0 {
		cfg = DefaultReadConfig()
	}
	if cfg.DefaultLimit > cfg.MaxLimit {
		cfg.DefaultLimit = cfg.MaxLimit
	}
	if cfg.DefaultMaxTokens > cfg.MaxTokens {
		cfg.DefaultMaxTokens = cfg.MaxTokens
	}
	resolvedRuntime, err := runtimeOrDefault(runtime)
	if err != nil {
		return nil, err
	}
	return &ReadTool{
		cfg:     cfg,
		runtime: resolvedRuntime,
	}, nil
}

func (t *ReadTool) Name() string {
	return ReadToolName
}

func (t *ReadTool) Description() string {
	return "Read part of a text file. READ first slices by lines, then truncates further to fit the token budget."
}

func (t *ReadTool) Capability() capability.Capability {
	return capability.Capability{
		Operations: []capability.Operation{capability.OperationFileRead},
		Risk:       capability.RiskLow,
	}
}

func (t *ReadTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "File path, absolute or relative."},
				"offset":     map[string]any{"type": "integer", "description": "Zero-based starting line offset."},
				"limit":      map[string]any{"type": "integer", "description": "Optional max lines to read before token truncation."},
				"max_tokens": map[string]any{"type": "integer", "description": "Optional token budget applied after line slicing."},
			},
			"required": []string{"path"},
		},
	}
}

func (t *ReadTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	pathArg, err := argparse.String(args, "path", true)
	if err != nil {
		return nil, err
	}
	offset, err := argparse.Int(args, "offset", 0)
	if err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, fmt.Errorf("tool: arg %q must be >= 0", "offset")
	}
	limit, err := argparse.Int(args, "limit", t.cfg.DefaultLimit)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = t.cfg.DefaultLimit
	}
	if limit > t.cfg.MaxLimit {
		limit = t.cfg.MaxLimit
	}
	maxTokens, err := argparse.Int(args, "max_tokens", t.cfg.DefaultMaxTokens)
	if err != nil {
		return nil, err
	}
	if maxTokens <= 0 {
		maxTokens = t.cfg.DefaultMaxTokens
	}
	if maxTokens > t.cfg.MaxTokens {
		maxTokens = t.cfg.MaxTokens
	}

	targetPath, err := normalizePathWithFS(t.runtime.FileSystem(), pathArg)
	if err != nil {
		return nil, err
	}
	file, err := t.runtime.FileSystem().Open(targetPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var (
		lineNo    int
		usedToken int
		lines     []string

		hasMore bool
	)
	for scanner.Scan() {
		lineNo++
		if lineNo <= offset {
			continue
		}
		if len(lines) >= limit {
			hasMore = true
			break
		}
		line := scanner.Text()
		tokens := estimateToken(line)
		usedToken += tokens
		if usedToken > maxTokens {
			if len(lines) == 0 {
				budget := maxTokens - (usedToken - tokens)
				if budget <= 0 {
					budget = 1
				}
				line = truncateByTokenBudget(line, budget)
				lines = append(lines, line)
			}
			hasMore = true
			break
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	var content strings.Builder
	for i, line := range lines {
		if i > 0 {
			content.WriteByte('\n')
		}
		fmt.Fprintf(&content, "%d: %s", offset+i+1, line)
	}

	startLine := 0
	endLine := 0
	if len(lines) > 0 {
		startLine = offset + 1
		endLine = offset + len(lines)
	}
	nextOffset := endLine
	if len(lines) == 0 {
		nextOffset = lineNo
	}
	exhausted := len(lines) == 0 && offset >= lineNo

	return map[string]any{
		"path":        targetPath,
		"start_line":  startLine,
		"end_line":    endLine,
		"next_offset": nextOffset,
		"has_more":    hasMore,
		"exhausted":   exhausted,
		"content":     content.String(),
	}, nil
}

func (t *ReadTool) WithRuntime(runtime toolexec.Runtime) (*ReadTool, error) {
	return NewReadWithRuntime(t.cfg, runtime)
}

func estimateToken(text string) int {
	if text == "" {
		return 0
	}
	runes := utf8.RuneCountInString(text)
	token := runes / 4
	if runes%4 != 0 {
		token++
	}
	if token <= 0 {
		token = 1
	}
	return token
}

func truncateByTokenBudget(text string, budget int) string {
	if budget <= 0 || text == "" {
		return ""
	}
	maxRunes := budget * 4
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	var (
		builder strings.Builder
		count   int
	)
	for _, r := range text {
		if count >= maxRunes {
			break
		}
		builder.WriteRune(r)
		count++
	}
	builder.WriteString(" ...[truncated]")
	return builder.String()
}
