package mcptoolset

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

// TransportType is MCP transport type.
type TransportType string

const (
	TransportStdio      TransportType = "stdio"
	TransportSSE        TransportType = "sse"
	TransportStreamable TransportType = "streamable"
)

// ServerConfig configures one MCP server endpoint.
type ServerConfig struct {
	Name string
	// Prefix is used to namespace exposed tool names. If empty, Name is used.
	Prefix string

	Transport TransportType

	// Stdio transport.
	Command string
	Args    []string
	Env     map[string]string
	WorkDir string

	// HTTP transport (sse/streamable).
	URL string

	// Optional allowlist for original MCP tool names.
	IncludeTools []string

	// CallTimeout controls per-tool call timeout.
	CallTimeout time.Duration
}

// Config configures one MCP tool manager.
type Config struct {
	Servers []ServerConfig
	// CacheTTL controls tool list cache ttl. <=0 means no ttl expiration.
	CacheTTL time.Duration
}

// Manager maintains MCP sessions and exposes MCP tools as kernel tools.
type Manager struct {
	mu sync.Mutex

	servers []*server

	cacheAt time.Time
	cache   []tool.Tool
	cacheTT time.Duration
}

type server struct {
	name    string
	prefix  string
	cfg     ServerConfig
	allow   map[string]struct{}
	client  *mcp.Client
	session *mcp.ClientSession
}

// NewManager creates a manager from config.
func NewManager(cfg Config) (*Manager, error) {
	servers := make([]*server, 0, len(cfg.Servers))
	for i, one := range cfg.Servers {
		s, err := newServer(one, i)
		if err != nil {
			return nil, err
		}
		servers = append(servers, s)
	}
	return &Manager{
		servers: servers,
		cacheTT: cfg.CacheTTL,
	}, nil
}

func newServer(cfg ServerConfig, idx int) (*server, error) {
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return nil, fmt.Errorf("mcptoolset: server[%d] name is required", idx)
	}
	prefix := strings.TrimSpace(cfg.Prefix)
	if prefix == "" {
		prefix = name
	}
	if cfg.Transport == "" {
		cfg.Transport = TransportStdio
	}
	switch cfg.Transport {
	case TransportStdio:
		if strings.TrimSpace(cfg.Command) == "" {
			return nil, fmt.Errorf("mcptoolset: server[%s] command is required for stdio transport", name)
		}
	case TransportSSE, TransportStreamable:
		if strings.TrimSpace(cfg.URL) == "" {
			return nil, fmt.Errorf("mcptoolset: server[%s] url is required for %s transport", name, cfg.Transport)
		}
	default:
		return nil, fmt.Errorf("mcptoolset: server[%s] unsupported transport %q", name, cfg.Transport)
	}
	allow := map[string]struct{}{}
	for _, item := range cfg.IncludeTools {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		allow[item] = struct{}{}
	}
	return &server{
		name:   name,
		prefix: prefix,
		cfg:    cfg,
		allow:  allow,
		client: mcp.NewClient(&mcp.Implementation{
			Name:    "caelis",
			Version: "0.1.0",
		}, nil),
	}, nil
}

// Close closes all open MCP sessions.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []string
	for _, srv := range m.servers {
		if srv == nil || srv.session == nil {
			continue
		}
		if err := srv.session.Close(); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", srv.name, err))
		}
		srv.session = nil
	}
	m.cache = nil
	m.cacheAt = time.Time{}
	if len(errs) > 0 {
		return fmt.Errorf("mcptoolset: close sessions: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Tools returns MCP tools converted to kernel tools.
func (m *Manager) Tools(ctx context.Context) ([]tool.Tool, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.cacheExpiredLocked() {
		return append([]tool.Tool(nil), m.cache...), nil
	}

	toolsByName := map[string]tool.Tool{}
	for _, srv := range m.servers {
		if srv == nil {
			continue
		}
		session, err := m.getSessionLocked(ctx, srv)
		if err != nil {
			return nil, err
		}
		for mt, iterErr := range session.Tools(ctx, nil) {
			if iterErr != nil {
				return nil, fmt.Errorf("mcptoolset: list tools from %s: %w", srv.name, iterErr)
			}
			if mt == nil || strings.TrimSpace(mt.Name) == "" {
				continue
			}
			originalName := strings.TrimSpace(mt.Name)
			if len(srv.allow) > 0 {
				if _, ok := srv.allow[originalName]; !ok {
					continue
				}
			}
			name := exposedToolName(srv.prefix, originalName)
			if _, exists := toolsByName[name]; exists {
				return nil, fmt.Errorf("mcptoolset: duplicate exposed tool name %q", name)
			}
			toolsByName[name] = &mcpTool{
				name:         name,
				originalName: originalName,
				serverName:   srv.name,
				description:  toolDescription(mt.Description, srv.name, originalName),
				parameters:   normalizeSchema(mt.InputSchema),
				callTimeout:  srv.cfg.CallTimeout,
				getSession: func(ctx context.Context) (*mcp.ClientSession, error) {
					m.mu.Lock()
					defer m.mu.Unlock()
					return m.getSessionLocked(ctx, srv)
				},
			}
		}
	}

	out := make([]tool.Tool, 0, len(toolsByName))
	names := make([]string, 0, len(toolsByName))
	for name := range toolsByName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, toolsByName[name])
	}
	m.cache = append([]tool.Tool(nil), out...)
	m.cacheAt = time.Now()
	return out, nil
}

func (m *Manager) cacheExpiredLocked() bool {
	if len(m.cache) == 0 {
		return true
	}
	if m.cacheTT <= 0 {
		return false
	}
	return time.Since(m.cacheAt) > m.cacheTT
}

func (m *Manager) getSessionLocked(ctx context.Context, srv *server) (*mcp.ClientSession, error) {
	if srv.session != nil {
		return srv.session, nil
	}
	transport, err := buildTransport(srv.cfg)
	if err != nil {
		return nil, err
	}
	session, err := srv.client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcptoolset: connect %s: %w", srv.name, err)
	}
	srv.session = session
	return session, nil
}

func buildTransport(cfg ServerConfig) (mcp.Transport, error) {
	switch cfg.Transport {
	case TransportStdio:
		cmd := exec.Command(strings.TrimSpace(cfg.Command), cfg.Args...)
		if strings.TrimSpace(cfg.WorkDir) != "" {
			cmd.Dir = strings.TrimSpace(cfg.WorkDir)
		}
		if len(cfg.Env) > 0 {
			env := os.Environ()
			for k, v := range cfg.Env {
				k = strings.TrimSpace(k)
				if k == "" {
					continue
				}
				env = append(env, k+"="+v)
			}
			cmd.Env = env
		}
		return &mcp.CommandTransport{Command: cmd}, nil
	case TransportSSE:
		return &mcp.SSEClientTransport{
			Endpoint: strings.TrimSpace(cfg.URL),
		}, nil
	case TransportStreamable:
		return &mcp.StreamableClientTransport{
			Endpoint: strings.TrimSpace(cfg.URL),
		}, nil
	default:
		return nil, fmt.Errorf("mcptoolset: unsupported transport %q", cfg.Transport)
	}
}

func toolDescription(desc, serverName, originalName string) string {
	desc = strings.TrimSpace(desc)
	prefix := fmt.Sprintf("[MCP:%s/%s]", serverName, originalName)
	if desc == "" {
		return prefix
	}
	return prefix + " " + desc
}

func normalizeSchema(schema any) map[string]any {
	if m, ok := schema.(map[string]any); ok && len(m) > 0 {
		return m
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{"type": "object"}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || len(out) == 0 {
		return map[string]any{"type": "object"}
	}
	return out
}

func exposedToolName(prefix, original string) string {
	prefix = sanitizeName(prefix)
	original = sanitizeName(original)
	if prefix == "" {
		prefix = "mcp"
	}
	if original == "" {
		original = "tool"
	}
	name := prefix + "__" + original
	if len(name) <= 64 {
		return name
	}
	sum := sha1.Sum([]byte(name))
	suffix := hex.EncodeToString(sum[:4])
	maxPrefix := 64 - 2 - len(suffix)
	if maxPrefix < 1 {
		maxPrefix = 1
	}
	if len(name) > maxPrefix {
		name = name[:maxPrefix]
	}
	name = strings.Trim(name, "_")
	if name == "" {
		name = "mcp"
	}
	return name + "__" + suffix
}

func sanitizeName(input string) string {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return ""
	}
	var b strings.Builder
	prevUnderscore := false
	for _, r := range input {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		if ok {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return ""
	}
	return out
}

type mcpTool struct {
	name         string
	originalName string
	serverName   string
	description  string
	parameters   map[string]any
	callTimeout  time.Duration
	getSession   func(context.Context) (*mcp.ClientSession, error)
}

func (t *mcpTool) Name() string {
	return t.name
}

func (t *mcpTool) Description() string {
	return t.description
}

func (t *mcpTool) Declaration() model.ToolDefinition {
	return model.ToolDefinition{
		Name:        t.name,
		Description: t.description,
		Parameters:  t.parameters,
	}
}

func (t *mcpTool) Run(ctx context.Context, args map[string]any) (map[string]any, error) {
	if t.getSession == nil {
		return nil, fmt.Errorf("mcptoolset: session getter is nil")
	}
	session, err := t.getSession(ctx)
	if err != nil {
		return nil, err
	}
	callCtx := ctx
	cancel := func() {}
	if t.callTimeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, t.callTimeout)
	}
	defer cancel()

	res, err := session.CallTool(callCtx, &mcp.CallToolParams{
		Name:      t.originalName,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("mcptoolset: call %s/%s: %w", t.serverName, t.originalName, err)
	}
	if res == nil {
		return map[string]any{"ok": true}, nil
	}

	texts := extractText(res.Content)
	if res.IsError {
		if strings.TrimSpace(texts) == "" {
			texts = "mcp tool returned isError=true"
		}
		return nil, fmt.Errorf("%s", texts)
	}

	output := map[string]any{}
	if res.StructuredContent != nil {
		if m, ok := res.StructuredContent.(map[string]any); ok {
			for k, v := range m {
				output[k] = v
			}
		} else {
			output["structured_output"] = res.StructuredContent
		}
	}
	if strings.TrimSpace(texts) != "" {
		if _, exists := output["output"]; !exists {
			output["output"] = texts
		} else {
			output["content"] = texts
		}
	}
	if len(output) == 0 {
		output["ok"] = true
	}
	return output, nil
}

func extractText(content []mcp.Content) string {
	if len(content) == 0 {
		return ""
	}
	parts := make([]string, 0, len(content))
	for _, c := range content {
		switch value := c.(type) {
		case *mcp.TextContent:
			text := strings.TrimSpace(value.Text)
			if text != "" {
				parts = append(parts, text)
			}
		default:
			raw, err := json.Marshal(value)
			if err == nil {
				text := strings.TrimSpace(string(raw))
				if text != "" && text != "{}" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}
