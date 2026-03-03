package gopls

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/OnslaughtSnail/caelis/internal/cli/lspbroker"
	"github.com/OnslaughtSnail/caelis/internal/cli/lspclient"
	"github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

const (
	ToolDiagnostics = "LSP_DIAGNOSTICS"
	ToolDefinition  = "LSP_DEFINITION"
	ToolReferences  = "LSP_REFERENCES"
	ToolSymbols     = "LSP_SYMBOLS"
)

type rpcClient interface {
	Call(context.Context, string, any, any) error
	Notify(context.Context, string, any) error
	IsClosed() bool
	Close() error
	ServerCapabilities() map[string]any
}

type clientStarter func(context.Context, lspclient.Config) (rpcClient, error)

type docState struct {
	Version int
	Hash    uint64
}

type managedClient struct {
	workspace string
	rootURI   string
	client    rpcClient

	docMu sync.Mutex
	docs  map[string]docState
}

// Adapter exposes gopls-backed LSP tools.
type Adapter struct {
	language    string
	languageID  string
	command     string
	args        []string
	initTimeout time.Duration
	startClient clientStarter

	mu      sync.Mutex
	clients map[string]*managedClient
}

// Config configures gopls adapter.
type Config struct {
	// Runtime is kept for backward compatibility and is not used by stdio LSP mode.
	Runtime execenv.Runtime

	Language    string
	LanguageID  string
	Command     string
	Args        []string
	InitTimeout time.Duration
}

func New(cfg Config) (*Adapter, error) {
	_ = cfg.Runtime
	language := strings.ToLower(strings.TrimSpace(cfg.Language))
	if language == "" {
		language = "go"
	}
	languageID := strings.TrimSpace(cfg.LanguageID)
	if languageID == "" {
		languageID = language
	}
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		command = "gopls"
	}
	args := append([]string(nil), cfg.Args...)
	if len(args) == 0 && (command == "gopls" || language == "go") {
		args = []string{"serve"}
	}
	initTimeout := cfg.InitTimeout
	if initTimeout <= 0 {
		initTimeout = 15 * time.Second
	}
	return &Adapter{
		language:    language,
		languageID:  languageID,
		command:     command,
		args:        args,
		initTimeout: initTimeout,
		startClient: func(ctx context.Context, cfg lspclient.Config) (rpcClient, error) {
			return lspclient.Start(ctx, cfg)
		},
		clients: map[string]*managedClient{},
	}, nil
}

func (a *Adapter) Language() string {
	if a == nil || strings.TrimSpace(a.language) == "" {
		return "go"
	}
	return a.language
}

func (a *Adapter) BuildToolSet(ctx context.Context, req lspbroker.ActivateRequest) (*lspbroker.ToolSet, error) {
	workspace, err := normalizeWorkspace(req.Workspace)
	if err != nil {
		return nil, err
	}

	diagnosticsTool, err := a.newDiagnosticsTool(workspace)
	if err != nil {
		return nil, err
	}

	// Probe whether the language server supports workspace/symbol.
	symbolsSupported := a.probeSymbolCapability(ctx, workspace)

	tools := []tool.Tool{diagnosticsTool}

	if symbolsSupported {
		// Symbol-driven mode: DEFINITION and REFERENCES accept a symbol name.
		symbolsTool, err := a.newSymbolsTool(workspace)
		if err != nil {
			return nil, err
		}
		definitionTool, err := a.newSymbolDefinitionTool(workspace)
		if err != nil {
			return nil, err
		}
		referencesTool, err := a.newSymbolReferencesTool(workspace)
		if err != nil {
			return nil, err
		}
		tools = append(tools, symbolsTool, definitionTool, referencesTool)
	} else {
		// Fallback: position-driven mode (original behavior, without RENAME_PREVIEW).
		definitionTool, err := a.newDefinitionTool(workspace)
		if err != nil {
			return nil, err
		}
		referencesTool, err := a.newReferencesTool(workspace)
		if err != nil {
			return nil, err
		}
		tools = append(tools, definitionTool, referencesTool)
	}

	return &lspbroker.ToolSet{
		ID:       "lsp:" + a.Language(),
		Language: a.Language(),
		Tools:    tools,
	}, nil
}

// probeSymbolCapability checks if the language server supports workspace/symbol.
func (a *Adapter) probeSymbolCapability(ctx context.Context, workspace string) bool {
	mc, err := a.getClient(ctx, workspace, false)
	if err != nil {
		return false
	}
	caps := mc.client.ServerCapabilities()
	if caps == nil {
		return false
	}
	v, ok := caps["workspaceSymbolProvider"]
	if !ok {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case map[string]any:
		return true // non-nil object means supported
	default:
		return false
	}
}

// symbolHit is an internal type for resolved symbol information.
type symbolHit struct {
	Name          string
	Kind          string
	Path          string
	Line          int
	Column        int
	ContainerName string
}

// lspSymbolInformation represents one workspace/symbol result.
type lspSymbolInformation struct {
	Name          string      `json:"name"`
	Kind          int         `json:"kind"`
	Location      lspLocation `json:"location"`
	ContainerName string      `json:"containerName"`
}

// resolveSymbol queries workspace/symbol and returns matched hits sorted by relevance.
func (a *Adapter) resolveSymbol(ctx context.Context, workspace, query string) ([]symbolHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("tool: arg %q is required", "symbol")
	}

	var raw []lspSymbolInformation
	err := withManagedClient(a, ctx, workspace, func(mc *managedClient) error {
		rpcCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return mc.client.Call(rpcCtx, "workspace/symbol", map[string]any{
			"query": query,
		}, &raw)
	})
	if err != nil {
		return nil, fmt.Errorf("tool: lsp workspace/symbol failed: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}

	lineCache := map[string][]string{}
	hits := make([]symbolHit, 0, len(raw))
	for _, s := range raw {
		path := uriToPath(s.Location.URI)
		if path == "" {
			continue
		}
		lines := readLinesCached(lineCache, path)
		line, col := lspPositionToUser(lines, s.Location.Range.Start)
		hits = append(hits, symbolHit{
			Name:          s.Name,
			Kind:          symbolKindToString(s.Kind),
			Path:          path,
			Line:          line,
			Column:        col,
			ContainerName: s.ContainerName,
		})
	}

	// Sort: exact match > prefix match > contains match, then by path+line.
	queryLower := strings.ToLower(query)
	sort.SliceStable(hits, func(i, j int) bool {
		si := matchScore(hits[i].Name, queryLower)
		sj := matchScore(hits[j].Name, queryLower)
		if si != sj {
			return si > sj
		}
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Line < hits[j].Line
	})
	return hits, nil
}

// matchScore returns 3 for exact match, 2 for prefix, 1 for contains, 0 for no match.
func matchScore(name, queryLower string) int {
	nameLower := strings.ToLower(name)
	if nameLower == queryLower {
		return 3
	}
	if strings.HasPrefix(nameLower, queryLower) {
		return 2
	}
	if strings.Contains(nameLower, queryLower) {
		return 1
	}
	return 0
}

// symbolKindToString converts LSP SymbolKind integer to human-readable string.
func symbolKindToString(kind int) string {
	switch kind {
	case 1:
		return "file"
	case 2:
		return "module"
	case 3:
		return "namespace"
	case 4:
		return "package"
	case 5:
		return "class"
	case 6:
		return "method"
	case 7:
		return "property"
	case 8:
		return "field"
	case 9:
		return "constructor"
	case 10:
		return "enum"
	case 11:
		return "interface"
	case 12:
		return "function"
	case 13:
		return "variable"
	case 14:
		return "constant"
	case 15:
		return "string"
	case 16:
		return "number"
	case 17:
		return "boolean"
	case 18:
		return "array"
	case 19:
		return "object"
	case 20:
		return "key"
	case 21:
		return "null"
	case 22:
		return "enum_member"
	case 23:
		return "struct"
	case 24:
		return "event"
	case 25:
		return "operator"
	case 26:
		return "type_parameter"
	default:
		return "unknown"
	}
}

// ---------- LSP_SYMBOLS tool ----------

func (a *Adapter) newSymbolsTool(workspace string) (tool.Tool, error) {
	type args struct {
		Query string `json:"query"`
	}
	type symbolItem struct {
		Name          string `json:"name"`
		Kind          string `json:"kind"`
		Path          string `json:"path"`
		Line          int    `json:"line"`
		ContainerName string `json:"container_name,omitempty"`
	}
	type result struct {
		Query   string       `json:"query"`
		Symbols []symbolItem `json:"symbols"`
	}
	return tool.NewFunction[args, result](
		ToolSymbols,
		"Search for symbols (functions, types, variables) by name across the workspace. Returns matching symbol names, kinds, and locations. Use this to discover symbols before calling LSP_DEFINITION or LSP_REFERENCES.",
		func(ctx context.Context, in args) (result, error) {
			hits, err := a.resolveSymbol(ctx, workspace, in.Query)
			if err != nil {
				return result{Query: in.Query}, err
			}
			const maxResults = 30
			symbols := make([]symbolItem, 0, len(hits))
			for i, h := range hits {
				if i >= maxResults {
					break
				}
				symbols = append(symbols, symbolItem{
					Name:          h.Name,
					Kind:          h.Kind,
					Path:          h.Path,
					Line:          h.Line,
					ContainerName: h.ContainerName,
				})
			}
			return result{Query: in.Query, Symbols: symbols}, nil
		},
	)
}

// ---------- Symbol-driven LSP_DEFINITION ----------

func (a *Adapter) newSymbolDefinitionTool(workspace string) (tool.Tool, error) {
	type args struct {
		Symbol string `json:"symbol"`
	}
	type location struct {
		Path   string `json:"path"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	}
	type result struct {
		Symbol    string     `json:"symbol"`
		Ambiguous bool       `json:"ambiguous,omitempty"`
		Note      string     `json:"note,omitempty"`
		Error     string     `json:"error,omitempty"`
		Hint      string     `json:"hint,omitempty"`
		Locations []location `json:"locations"`
	}
	return tool.NewFunction[args, result](
		ToolDefinition,
		"Find where a symbol is defined. Provide the symbol name (function, type, variable, etc.). Returns the definition location(s). If the symbol is ambiguous (multiple matches), returns all candidates — call LSP_SYMBOLS first to disambiguate.",
		func(ctx context.Context, in args) (result, error) {
			hits, err := a.resolveSymbol(ctx, workspace, in.Symbol)
			if err != nil {
				return result{Symbol: in.Symbol}, err
			}
			if len(hits) == 0 {
				return result{
					Symbol: in.Symbol,
					Error:  "symbol not found",
					Hint:   "try SEARCH tool with the symbol name as query",
				}, nil
			}

			// Limit candidates to avoid too many RPC calls.
			candidates := hits
			if len(candidates) > 5 {
				candidates = candidates[:5]
			}

			ambiguous := len(hits) > 1
			allLocations := make([]location, 0)
			seen := map[string]bool{}

			for _, hit := range candidates {
				absPath, _, lines, syncErr := a.ensureDocumentSynced(ctx, workspace, hit.Path)
				if syncErr != nil {
					continue
				}
				pos, posErr := userPositionToLSP(lines, hit.Line, hit.Column)
				if posErr != nil {
					continue
				}

				var raw json.RawMessage
				callErr := withManagedClient(a, ctx, workspace, func(mc *managedClient) error {
					rpcCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
					defer cancel()
					return mc.client.Call(rpcCtx, "textDocument/definition", map[string]any{
						"textDocument": map[string]any{"uri": mustPathToURI(absPath)},
						"position":     pos,
					}, &raw)
				})
				if callErr != nil {
					continue
				}
				locs, decErr := decodeLocations(raw)
				if decErr != nil {
					continue
				}
				resolved := convertLocations(locs)
				for _, one := range resolved {
					key := fmt.Sprintf("%s:%d:%d", one.Path, one.Line, one.Column)
					if seen[key] {
						continue
					}
					seen[key] = true
					allLocations = append(allLocations, location{
						Path:   one.Path,
						Line:   one.Line,
						Column: one.Column,
					})
				}
			}

			out := result{
				Symbol:    in.Symbol,
				Ambiguous: ambiguous,
				Locations: allLocations,
			}
			if ambiguous {
				out.Note = fmt.Sprintf("%d symbols matched '%s', results combined. Call LSP_SYMBOLS to see all candidates.", len(hits), in.Symbol)
			}
			if len(allLocations) == 0 {
				out.Error = "no definitions found"
				out.Hint = "try SEARCH tool with the symbol name as query"
			}
			return out, nil
		},
	)
}

// ---------- Symbol-driven LSP_REFERENCES ----------

func (a *Adapter) newSymbolReferencesTool(workspace string) (tool.Tool, error) {
	type args struct {
		Symbol string `json:"symbol"`
	}
	type refLocation struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	}
	type result struct {
		Symbol     string        `json:"symbol"`
		TotalCount int           `json:"total_count"`
		Note       string        `json:"note,omitempty"`
		Error      string        `json:"error,omitempty"`
		Hint       string        `json:"hint,omitempty"`
		References []refLocation `json:"references"`
	}
	return tool.NewFunction[args, result](
		ToolReferences,
		"Find all references (usages) of a symbol across the workspace. Returns file paths and line numbers where the symbol is used. If the symbol name is ambiguous, returns references for the best match and notes the ambiguity.",
		func(ctx context.Context, in args) (result, error) {
			hits, err := a.resolveSymbol(ctx, workspace, in.Symbol)
			if err != nil {
				return result{Symbol: in.Symbol}, err
			}
			if len(hits) == 0 {
				return result{
					Symbol: in.Symbol,
					Error:  "symbol not found",
					Hint:   "try SEARCH tool with the symbol name as query",
				}, nil
			}

			// Use the best match (Top1).
			best := hits[0]
			absPath, _, lines, syncErr := a.ensureDocumentSynced(ctx, workspace, best.Path)
			if syncErr != nil {
				return result{Symbol: in.Symbol}, syncErr
			}
			pos, posErr := userPositionToLSP(lines, best.Line, best.Column)
			if posErr != nil {
				return result{Symbol: in.Symbol}, posErr
			}

			var raw []lspLocation
			callErr := withManagedClient(a, ctx, workspace, func(mc *managedClient) error {
				rpcCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				defer cancel()
				return mc.client.Call(rpcCtx, "textDocument/references", map[string]any{
					"textDocument": map[string]any{"uri": mustPathToURI(absPath)},
					"position":     pos,
					"context":      map[string]any{"includeDeclaration": true},
				}, &raw)
			})
			if callErr != nil {
				return result{Symbol: in.Symbol}, fmt.Errorf("tool: lsp references failed: %w", callErr)
			}

			resolved := convertLocations(raw)
			refs := make([]refLocation, 0, len(resolved))
			for _, one := range resolved {
				refs = append(refs, refLocation{
					Path: one.Path,
					Line: one.Line,
				})
			}

			out := result{
				Symbol:     in.Symbol,
				TotalCount: len(refs),
				References: refs,
			}
			if len(hits) > 1 {
				out.Note = fmt.Sprintf("%d symbols matched '%s', showing references for %s at %s:%d. Call LSP_SYMBOLS to see all candidates.",
					len(hits), in.Symbol, best.Name, best.Path, best.Line)
			}
			return out, nil
		},
	)
}

func (a *Adapter) newDiagnosticsTool(workspace string) (tool.Tool, error) {
	type args struct {
		Path string `json:"path"`
	}
	type diagnosticItem struct {
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Column    int    `json:"column"`
		EndLine   int    `json:"end_line"`
		EndColumn int    `json:"end_column"`
		Severity  int    `json:"severity"`
		Code      string `json:"code,omitempty"`
		Source    string `json:"source,omitempty"`
		Message   string `json:"message"`
	}
	type result struct {
		Path        string           `json:"path"`
		Diagnostics []diagnosticItem `json:"diagnostics"`
	}
	return tool.NewFunction[args, result](ToolDiagnostics, "Get diagnostics for one source file.", func(ctx context.Context, in args) (result, error) {
		absPath, _, _, err := a.ensureDocumentSynced(ctx, workspace, strings.TrimSpace(in.Path))
		if err != nil {
			return result{}, err
		}

		var report lspDocumentDiagnosticReport
		err = withManagedClient(a, ctx, workspace, func(mc *managedClient) error {
			rpcCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			return mc.client.Call(rpcCtx, "textDocument/diagnostic", map[string]any{
				"textDocument": map[string]any{"uri": mustPathToURI(absPath)},
			}, &report)
		})
		if err != nil {
			return result{}, fmt.Errorf("tool: lsp diagnostics failed: %w", err)
		}

		items := make([]diagnosticItem, 0, len(report.Items))
		lineCache := map[string][]string{}
		for _, one := range report.Items {
			line := one.Range.Start.Line + 1
			column := one.Range.Start.Character + 1
			endLine := one.Range.End.Line + 1
			endColumn := one.Range.End.Character + 1
			if lines := readLinesCached(lineCache, absPath); len(lines) > 0 {
				line, column = lspPositionToUser(lines, one.Range.Start)
				endLine, endColumn = lspPositionToUser(lines, one.Range.End)
			}
			code := ""
			switch v := any(one.Code).(type) {
			case string:
				code = v
			case float64:
				code = fmt.Sprintf("%.0f", v)
			}
			items = append(items, diagnosticItem{
				Path:      absPath,
				Line:      line,
				Column:    column,
				EndLine:   endLine,
				EndColumn: endColumn,
				Severity:  one.Severity,
				Code:      code,
				Source:    one.Source,
				Message:   one.Message,
			})
		}
		return result{Path: absPath, Diagnostics: items}, nil
	})
}

func (a *Adapter) newDefinitionTool(workspace string) (tool.Tool, error) {
	type args struct {
		Path   string `json:"path"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	}
	type location struct {
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Column    int    `json:"column"`
		EndLine   int    `json:"end_line"`
		EndColumn int    `json:"end_column"`
	}
	type result struct {
		Query      string     `json:"query"`
		Definition string     `json:"definition,omitempty"`
		Locations  []location `json:"locations"`
	}
	return tool.NewFunction[args, result](ToolDefinition, "Find symbol definitions by file position.", func(ctx context.Context, in args) (result, error) {
		absPath, _, lines, err := a.ensureDocumentSynced(ctx, workspace, strings.TrimSpace(in.Path))
		if err != nil {
			return result{}, err
		}
		pos, err := userPositionToLSP(lines, in.Line, in.Column)
		if err != nil {
			return result{}, err
		}
		query := fmt.Sprintf("%s:%d:%d", absPath, in.Line, in.Column)

		var raw json.RawMessage
		err = withManagedClient(a, ctx, workspace, func(mc *managedClient) error {
			rpcCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			return mc.client.Call(rpcCtx, "textDocument/definition", map[string]any{
				"textDocument": map[string]any{"uri": mustPathToURI(absPath)},
				"position":     pos,
			}, &raw)
		})
		if err != nil {
			return result{}, fmt.Errorf("tool: lsp definition failed: %w", err)
		}

		locations, err := decodeLocations(raw)
		if err != nil {
			return result{}, err
		}
		resolved := convertLocations(locations)
		out := result{Query: query, Locations: make([]location, 0, len(resolved))}
		for _, one := range resolved {
			out.Locations = append(out.Locations, location(one))
		}
		if len(out.Locations) > 0 {
			first := out.Locations[0]
			out.Definition = fmt.Sprintf("%s:%d:%d", first.Path, first.Line, first.Column)
		}
		return out, nil
	})
}

func (a *Adapter) newReferencesTool(workspace string) (tool.Tool, error) {
	type args struct {
		Path               string `json:"path"`
		Line               int    `json:"line"`
		Column             int    `json:"column"`
		IncludeDeclaration *bool  `json:"include_declaration,omitempty"`
	}
	type location struct {
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Column    int    `json:"column"`
		EndLine   int    `json:"end_line"`
		EndColumn int    `json:"end_column"`
	}
	type result struct {
		Query      string     `json:"query"`
		References []location `json:"references"`
	}
	return tool.NewFunction[args, result](ToolReferences, "Find references by file position.", func(ctx context.Context, in args) (result, error) {
		absPath, _, lines, err := a.ensureDocumentSynced(ctx, workspace, strings.TrimSpace(in.Path))
		if err != nil {
			return result{}, err
		}
		pos, err := userPositionToLSP(lines, in.Line, in.Column)
		if err != nil {
			return result{}, err
		}
		includeDeclaration := true
		if in.IncludeDeclaration != nil {
			includeDeclaration = *in.IncludeDeclaration
		}
		query := fmt.Sprintf("%s:%d:%d", absPath, in.Line, in.Column)

		var raw []lspLocation
		err = withManagedClient(a, ctx, workspace, func(mc *managedClient) error {
			rpcCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			return mc.client.Call(rpcCtx, "textDocument/references", map[string]any{
				"textDocument": map[string]any{"uri": mustPathToURI(absPath)},
				"position":     pos,
				"context":      map[string]any{"includeDeclaration": includeDeclaration},
			}, &raw)
		})
		if err != nil {
			return result{}, fmt.Errorf("tool: lsp references failed: %w", err)
		}
		resolved := convertLocations(raw)
		out := result{Query: query, References: make([]location, 0, len(resolved))}
		for _, one := range resolved {
			out.References = append(out.References, location(one))
		}
		return out, nil
	})
}

func (a *Adapter) ensureDocumentSynced(ctx context.Context, workspace, path string) (string, string, []string, error) {
	absPath, err := resolvePath(workspace, path)
	if err != nil {
		return "", "", nil, err
	}
	content, err := os.ReadFile(absPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("tool: read file %q: %w", absPath, err)
	}
	uri := mustPathToURI(absPath)
	hash := hashBytes(content)

	err = withManagedClient(a, ctx, workspace, func(mc *managedClient) error {
		mc.docMu.Lock()
		state, exists := mc.docs[absPath]
		mc.docMu.Unlock()

		rpcCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()

		if !exists {
			if notifyErr := mc.client.Notify(rpcCtx, "textDocument/didOpen", map[string]any{
				"textDocument": map[string]any{
					"uri":        uri,
					"languageId": a.languageID,
					"version":    1,
					"text":       string(content),
				},
			}); notifyErr != nil {
				return notifyErr
			}
			mc.docMu.Lock()
			mc.docs[absPath] = docState{Version: 1, Hash: hash}
			mc.docMu.Unlock()
			return nil
		}
		if state.Hash == hash {
			return nil
		}
		nextVersion := state.Version + 1
		if notifyErr := mc.client.Notify(rpcCtx, "textDocument/didChange", map[string]any{
			"textDocument": map[string]any{
				"uri":     uri,
				"version": nextVersion,
			},
			"contentChanges": []map[string]any{{"text": string(content)}},
		}); notifyErr != nil {
			return notifyErr
		}
		mc.docMu.Lock()
		mc.docs[absPath] = docState{Version: nextVersion, Hash: hash}
		mc.docMu.Unlock()
		return nil
	})
	if err != nil {
		return "", "", nil, fmt.Errorf("tool: sync document for LSP failed: %w", err)
	}
	return absPath, uri, splitLines(string(content)), nil
}

func withManagedClient(a *Adapter, ctx context.Context, workspace string, fn func(*managedClient) error) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		forceRecreate := attempt > 0
		mc, err := a.getClient(ctx, workspace, forceRecreate)
		if err != nil {
			return err
		}
		if err := fn(mc); err != nil {
			lastErr = err
			if !shouldReconnect(err) {
				return err
			}
			continue
		}
		return nil
	}
	if lastErr == nil {
		return fmt.Errorf("tool: unavailable LSP client")
	}
	return lastErr
}

func (a *Adapter) getClient(ctx context.Context, workspace string, forceRecreate bool) (*managedClient, error) {
	if a == nil {
		return nil, fmt.Errorf("tool: lsp adapter is nil")
	}
	ws, err := normalizeWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	rootURI := mustPathToURI(ws)

	a.mu.Lock()
	existing := a.clients[ws]
	if !forceRecreate && existing != nil && existing.client != nil && !existing.client.IsClosed() {
		a.mu.Unlock()
		return existing, nil
	}
	if existing != nil && existing.client != nil {
		_ = existing.client.Close()
		delete(a.clients, ws)
	}
	a.mu.Unlock()

	client, err := a.startClient(ctx, lspclient.Config{
		Command:     a.command,
		Args:        append([]string(nil), a.args...),
		WorkDir:     ws,
		RootURI:     rootURI,
		InitTimeout: a.initTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("tool: start LSP client failed: %w", err)
	}
	managed := &managedClient{
		workspace: ws,
		rootURI:   rootURI,
		client:    client,
		docs:      map[string]docState{},
	}
	a.mu.Lock()
	a.clients[ws] = managed
	a.mu.Unlock()
	return managed, nil
}

func shouldReconnect(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, lspclient.ErrClientClosed) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "eof")
}

func normalizeWorkspace(workspace string) (string, error) {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("tool: get cwd failed: %w", err)
		}
		ws = cwd
	}
	abs, err := filepath.Abs(ws)
	if err != nil {
		return "", fmt.Errorf("tool: resolve workspace path %q: %w", ws, err)
	}
	return filepath.Clean(abs), nil
}

func resolvePath(workspace, path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("tool: arg %q is required", "path")
	}
	candidate := trimmed
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(workspace, candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("tool: resolve path %q: %w", path, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("tool: stat path %q: %w", abs, err)
	}
	return filepath.Clean(abs), nil
}

func mustPathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return uri.String()
}

func uriToPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "file" {
		return raw
	}
	if parsed.Path == "" {
		return raw
	}
	if parsed.Host != "" {
		return filepath.Clean("//" + parsed.Host + parsed.Path)
	}
	return filepath.Clean(filepath.FromSlash(parsed.Path))
}

func hashBytes(b []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}

func splitLines(text string) []string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.Split(normalized, "\n")
}

func readLinesCached(cache map[string][]string, path string) []string {
	if cache == nil {
		return nil
	}
	if lines, ok := cache[path]; ok {
		return lines
	}
	content, err := os.ReadFile(path)
	if err != nil {
		cache[path] = nil
		return nil
	}
	lines := splitLines(string(content))
	cache[path] = lines
	return lines
}

func userPositionToLSP(lines []string, line, column int) (lspPosition, error) {
	if line <= 0 {
		return lspPosition{}, fmt.Errorf("tool: arg %q must be > 0", "line")
	}
	if column <= 0 {
		return lspPosition{}, fmt.Errorf("tool: arg %q must be > 0", "column")
	}
	if line > len(lines) {
		return lspPosition{}, fmt.Errorf("tool: line %d out of range (max %d)", line, len(lines))
	}
	runeCount := utf8.RuneCountInString(lines[line-1])
	if column > runeCount+1 {
		return lspPosition{}, fmt.Errorf("tool: column %d out of range for line %d (max %d)", column, line, runeCount+1)
	}
	charUnits := 0
	for _, r := range []rune(lines[line-1])[:column-1] {
		charUnits += utf16Len(r)
	}
	return lspPosition{Line: line - 1, Character: charUnits}, nil
}

func lspPositionToUser(lines []string, pos lspPosition) (int, int) {
	line := pos.Line + 1
	if pos.Line < 0 || pos.Line >= len(lines) {
		return line, pos.Character + 1
	}
	runes := []rune(lines[pos.Line])
	needUnits := pos.Character
	if needUnits <= 0 {
		return line, 1
	}
	units := 0
	col := 0
	for col < len(runes) {
		next := units + utf16Len(runes[col])
		if next > needUnits {
			break
		}
		units = next
		col++
	}
	return line, col + 1
}

func utf16Len(r rune) int {
	if r < 0 {
		return 1
	}
	if r <= 0xFFFF {
		return 1
	}
	return 2
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspLocationLink struct {
	TargetURI   string   `json:"targetUri"`
	TargetRange lspRange `json:"targetRange"`
}

type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"`
	Code     any      `json:"code"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

type lspDocumentDiagnosticReport struct {
	Kind  string          `json:"kind"`
	Items []lspDiagnostic `json:"items"`
}

type outputLocation struct {
	Path      string
	Line      int
	Column    int
	EndLine   int
	EndColumn int
}

func decodeLocations(raw json.RawMessage) ([]lspLocation, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var many []lspLocation
	if err := json.Unmarshal(raw, &many); err == nil {
		out := make([]lspLocation, 0, len(many))
		for _, one := range many {
			if strings.TrimSpace(one.URI) == "" {
				continue
			}
			out = append(out, one)
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	var one lspLocation
	if err := json.Unmarshal(raw, &one); err == nil && strings.TrimSpace(one.URI) != "" {
		return []lspLocation{one}, nil
	}
	var links []lspLocationLink
	if err := json.Unmarshal(raw, &links); err == nil {
		out := make([]lspLocation, 0, len(links))
		for _, link := range links {
			if strings.TrimSpace(link.TargetURI) == "" {
				continue
			}
			out = append(out, lspLocation{URI: link.TargetURI, Range: link.TargetRange})
		}
		return out, nil
	}
	return nil, fmt.Errorf("tool: unexpected LSP location payload")
}

func convertLocations(items []lspLocation) []outputLocation {
	if len(items) == 0 {
		return nil
	}
	lineCache := map[string][]string{}
	out := make([]outputLocation, 0, len(items))
	for _, one := range items {
		path := uriToPath(one.URI)
		if path == "" {
			continue
		}
		lines := readLinesCached(lineCache, path)
		startLine, startCol := lspPositionToUser(lines, one.Range.Start)
		endLine, endCol := lspPositionToUser(lines, one.Range.End)
		out = append(out, outputLocation{
			Path:      path,
			Line:      startLine,
			Column:    startCol,
			EndLine:   endLine,
			EndColumn: endCol,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Column < out[j].Column
	})
	return out
}
