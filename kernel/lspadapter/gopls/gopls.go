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

	"github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/lspbroker"
	"github.com/OnslaughtSnail/caelis/kernel/lspclient"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

const (
	ToolDiagnostics   = "LSP_DIAGNOSTICS"
	ToolDefinition    = "LSP_DEFINITION"
	ToolReferences    = "LSP_REFERENCES"
	ToolRenamePreview = "LSP_RENAME_PREVIEW"
)

type rpcClient interface {
	Call(context.Context, string, any, any) error
	Notify(context.Context, string, any) error
	IsClosed() bool
	Close() error
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

	Command     string
	Args        []string
	InitTimeout time.Duration
}

func New(cfg Config) (*Adapter, error) {
	_ = cfg.Runtime
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		command = "gopls"
	}
	args := append([]string(nil), cfg.Args...)
	if len(args) == 0 {
		args = []string{"serve"}
	}
	initTimeout := cfg.InitTimeout
	if initTimeout <= 0 {
		initTimeout = 15 * time.Second
	}
	return &Adapter{
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
	return "go"
}

func (a *Adapter) BuildToolSet(ctx context.Context, req lspbroker.ActivateRequest) (*lspbroker.ToolSet, error) {
	workspace, err := normalizeWorkspace(req.Workspace)
	if err != nil {
		return nil, err
	}
	_ = ctx

	diagnosticsTool, err := a.newDiagnosticsTool(workspace)
	if err != nil {
		return nil, err
	}
	definitionTool, err := a.newDefinitionTool(workspace)
	if err != nil {
		return nil, err
	}
	referencesTool, err := a.newReferencesTool(workspace)
	if err != nil {
		return nil, err
	}
	renamePreviewTool, err := a.newRenamePreviewTool(workspace)
	if err != nil {
		return nil, err
	}
	return &lspbroker.ToolSet{
		ID:       "lsp:go",
		Language: "go",
		Tools: []tool.Tool{
			diagnosticsTool,
			definitionTool,
			referencesTool,
			renamePreviewTool,
		},
	}, nil
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
	return tool.NewFunction[args, result](ToolDiagnostics, "Get diagnostics for one Go source file.", func(ctx context.Context, in args) (result, error) {
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
			return result{}, fmt.Errorf("tool: gopls diagnostics failed: %w", err)
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
			return result{}, fmt.Errorf("tool: gopls definition failed: %w", err)
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
			return result{}, fmt.Errorf("tool: gopls references failed: %w", err)
		}
		resolved := convertLocations(raw)
		out := result{Query: query, References: make([]location, 0, len(resolved))}
		for _, one := range resolved {
			out.References = append(out.References, location(one))
		}
		return out, nil
	})
}

func (a *Adapter) newRenamePreviewTool(workspace string) (tool.Tool, error) {
	type args struct {
		Path    string `json:"path"`
		Line    int    `json:"line"`
		Column  int    `json:"column"`
		NewName string `json:"new_name"`
	}
	type editItem struct {
		StartLine   int    `json:"start_line"`
		StartColumn int    `json:"start_column"`
		EndLine     int    `json:"end_line"`
		EndColumn   int    `json:"end_column"`
		NewText     string `json:"new_text"`
	}
	type fileChange struct {
		Path      string     `json:"path"`
		EditCount int        `json:"edit_count"`
		Edits     []editItem `json:"edits"`
	}
	type result struct {
		Query        string       `json:"query"`
		NewName      string       `json:"new_name"`
		ChangedFiles int          `json:"changed_files"`
		TotalEdits   int          `json:"total_edits"`
		Files        []fileChange `json:"files"`
	}
	return tool.NewFunction[args, result](ToolRenamePreview, "Preview rename edits by file position and new symbol name.", func(ctx context.Context, in args) (result, error) {
		newName := strings.TrimSpace(in.NewName)
		if newName == "" {
			return result{}, fmt.Errorf("tool: arg %q is required", "new_name")
		}
		absPath, _, lines, err := a.ensureDocumentSynced(ctx, workspace, strings.TrimSpace(in.Path))
		if err != nil {
			return result{}, err
		}
		pos, err := userPositionToLSP(lines, in.Line, in.Column)
		if err != nil {
			return result{}, err
		}
		query := fmt.Sprintf("%s:%d:%d", absPath, in.Line, in.Column)

		var wsEdit lspWorkspaceEdit
		err = withManagedClient(a, ctx, workspace, func(mc *managedClient) error {
			rpcCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			var prepare any
			if callErr := mc.client.Call(rpcCtx, "textDocument/prepareRename", map[string]any{
				"textDocument": map[string]any{"uri": mustPathToURI(absPath)},
				"position":     pos,
			}, &prepare); callErr != nil {
				return callErr
			}
			return mc.client.Call(rpcCtx, "textDocument/rename", map[string]any{
				"textDocument": map[string]any{"uri": mustPathToURI(absPath)},
				"position":     pos,
				"newName":      newName,
			}, &wsEdit)
		})
		if err != nil {
			return result{}, fmt.Errorf("tool: gopls rename preview failed: %w", err)
		}

		changes := flattenWorkspaceEdit(wsEdit)
		files := make([]fileChange, 0, len(changes))
		lineCache := map[string][]string{}
		totalEdits := 0
		for path, edits := range changes {
			lines := readLinesCached(lineCache, path)
			fc := fileChange{
				Path:      path,
				EditCount: len(edits),
				Edits:     make([]editItem, 0, len(edits)),
			}
			for _, one := range edits {
				startLine, startCol := lspPositionToUser(lines, one.Range.Start)
				endLine, endCol := lspPositionToUser(lines, one.Range.End)
				fc.Edits = append(fc.Edits, editItem{
					StartLine:   startLine,
					StartColumn: startCol,
					EndLine:     endLine,
					EndColumn:   endCol,
					NewText:     one.NewText,
				})
			}
			totalEdits += len(edits)
			files = append(files, fc)
		}
		sort.Slice(files, func(i, j int) bool {
			return files[i].Path < files[j].Path
		})
		return result{
			Query:        query,
			NewName:      newName,
			ChangedFiles: len(files),
			TotalEdits:   totalEdits,
			Files:        files,
		}, nil
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
					"languageId": "go",
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
		return nil, fmt.Errorf("tool: gopls adapter is nil")
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
		return nil, fmt.Errorf("tool: start gopls LSP client failed: %w", err)
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

type lspTextEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

type lspWorkspaceEdit struct {
	Changes         map[string][]lspTextEdit    `json:"changes"`
	DocumentChanges []lspTextDocumentEditHolder `json:"documentChanges"`
}

type lspTextDocumentEditHolder struct {
	TextDocument *lspVersionedTextDocumentIdentifier `json:"textDocument,omitempty"`
	Edits        []lspTextEdit                       `json:"edits,omitempty"`
}

type lspVersionedTextDocumentIdentifier struct {
	URI string `json:"uri"`
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

func flattenWorkspaceEdit(edit lspWorkspaceEdit) map[string][]lspTextEdit {
	out := map[string][]lspTextEdit{}
	for uri, edits := range edit.Changes {
		path := uriToPath(uri)
		if path == "" {
			continue
		}
		out[path] = append(out[path], edits...)
	}
	for _, dc := range edit.DocumentChanges {
		if dc.TextDocument == nil || strings.TrimSpace(dc.TextDocument.URI) == "" {
			continue
		}
		path := uriToPath(dc.TextDocument.URI)
		if path == "" {
			continue
		}
		out[path] = append(out[path], dc.Edits...)
	}
	return out
}
