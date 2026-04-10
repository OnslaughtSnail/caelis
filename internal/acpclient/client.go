package acpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type Config struct {
	Command             string
	Args                []string
	Env                 map[string]string
	WorkDir             string
	MCPServers          []MCPServer
	Runtime             toolexec.Runtime
	Workspace           string
	ClientInfo          *Implementation
	OnUpdate            func(UpdateEnvelope)
	OnPermissionRequest func(context.Context, RequestPermissionRequest) (RequestPermissionResponse, error)
}

type Client struct {
	core  *CoreClient
	local *LocalClient
	conn  *Conn
	cmd   *exec.Cmd

	cancel context.CancelFunc
	done   chan error
}

func Start(ctx context.Context, cfg Config) (*Client, error) {
	if ctx == nil {
		return nil, fmt.Errorf("acpclient: context is required")
	}
	command := strings.TrimSpace(cfg.Command)
	if command == "" {
		return nil, fmt.Errorf("acpclient: command is required")
	}
	workDir := strings.TrimSpace(cfg.WorkDir)
	if workDir == "" {
		workDir = strings.TrimSpace(cfg.Workspace)
	}
	if workDir != "" && !filepath.IsAbs(workDir) {
		abs, err := filepath.Abs(workDir)
		if err != nil {
			return nil, err
		}
		workDir = abs
	}
	cmd := exec.CommandContext(ctx, command, cfg.Args...)
	cmd.Dir = workDir
	cmd.Env = mergedEnv(cfg.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return NewProcessClient(ctx, cfg, cmd, stdout, stdin, stderr), nil
}

func NewProcessClient(ctx context.Context, cfg Config, cmd *exec.Cmd, reader io.Reader, writer io.Writer, stderr io.Reader) *Client {
	serveCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	conn := NewConn(reader, writer)
	coreCfg := CoreClientConfig{
		MCPServers:          cfg.MCPServers,
		ClientInfo:          cfg.ClientInfo,
		OnUpdate:            cfg.OnUpdate,
		OnPermissionRequest: cfg.OnPermissionRequest,
	}
	local := NewLocalClient(conn, coreCfg, LocalClientConfig{Runtime: cfg.Runtime})
	client := &Client{
		core:   local.core,
		local:  local,
		conn:   conn,
		cmd:    cmd,
		cancel: cancel,
		done:   make(chan error, 1),
	}
	go func() {
		client.done <- conn.Serve(serveCtx, local.handleRequest, local.handleNotification)
	}()
	if stderr != nil {
		go func() {
			_, _ = io.Copy(stderrBufferWriter{local: local}, stderr)
		}()
	}
	return client
}

func DefaultClientInfo(version string) *Implementation {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "dev"
	}
	return &Implementation{
		Name:    "caelis",
		Title:   "Caelis",
		Version: version,
	}
}

func (c *Client) Initialize(ctx context.Context) (InitializeResponse, error) {
	if c == nil || c.core == nil {
		return InitializeResponse{}, fmt.Errorf("acpclient: client is unavailable")
	}
	return c.core.Initialize(ctx)
}

func (c *Client) NewSession(ctx context.Context, cwd string, meta map[string]any) (NewSessionResponse, error) {
	if c == nil || c.core == nil {
		return NewSessionResponse{}, fmt.Errorf("acpclient: client is unavailable")
	}
	return c.core.NewSession(ctx, cwd, meta)
}

func (c *Client) LoadSession(ctx context.Context, sessionID string, cwd string, meta map[string]any) (LoadSessionResponse, error) {
	if c == nil || c.core == nil {
		return LoadSessionResponse{}, fmt.Errorf("acpclient: client is unavailable")
	}
	return c.core.LoadSession(ctx, sessionID, cwd, meta)
}

func (c *Client) SetMode(ctx context.Context, sessionID string, modeID string) error {
	if c == nil || c.core == nil {
		return fmt.Errorf("acpclient: client is unavailable")
	}
	return c.core.SetMode(ctx, sessionID, modeID)
}

func (c *Client) SetConfigOption(ctx context.Context, sessionID string, configID string, value string) (SetSessionConfigOptionResponse, error) {
	if c == nil || c.core == nil {
		return SetSessionConfigOptionResponse{}, fmt.Errorf("acpclient: client is unavailable")
	}
	return c.core.SetConfigOption(ctx, sessionID, configID, value)
}

func (c *Client) Prompt(ctx context.Context, sessionID string, text string, meta map[string]any) (PromptResponse, error) {
	if c == nil || c.core == nil {
		return PromptResponse{}, fmt.Errorf("acpclient: client is unavailable")
	}
	return c.core.Prompt(ctx, sessionID, text, meta)
}

func (c *Client) PromptParts(ctx context.Context, sessionID string, prompt []json.RawMessage, meta map[string]any) (PromptResponse, error) {
	if c == nil || c.core == nil {
		return PromptResponse{}, fmt.Errorf("acpclient: client is unavailable")
	}
	return c.core.PromptParts(ctx, sessionID, prompt, meta)
}

func (c *Client) Cancel(ctx context.Context, sessionID string) error {
	if c == nil || c.core == nil {
		return fmt.Errorf("acpclient: client is unavailable")
	}
	return c.core.Cancel(ctx, sessionID)
}

func (c *Client) TerminalOutput(ctx context.Context, sessionID, terminalID string) (TerminalOutputResponse, error) {
	if c == nil || c.core == nil {
		return TerminalOutputResponse{}, fmt.Errorf("acpclient: client is unavailable")
	}
	return c.core.TerminalOutput(ctx, sessionID, terminalID)
}

func (c *Client) TerminalRelease(ctx context.Context, sessionID, terminalID string) error {
	if c == nil || c.core == nil {
		return fmt.Errorf("acpclient: client is unavailable")
	}
	return c.core.TerminalRelease(ctx, sessionID, terminalID)
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	if c.cancel != nil {
		c.cancel()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	select {
	case <-time.After(100 * time.Millisecond):
	case <-c.done:
	}
	if c.cmd != nil {
		return c.cmd.Wait()
	}
	return nil
}

func (c *Client) StderrTail(limit int) string {
	if c == nil || c.local == nil || limit <= 0 {
		return ""
	}
	c.local.stderrMu.Lock()
	defer c.local.stderrMu.Unlock()
	data := c.local.stderrBuf.Bytes()
	if len(data) == 0 {
		return ""
	}
	if len(data) > limit {
		data = data[len(data)-limit:]
	}
	return strings.TrimSpace(string(data))
}

func buildShellCommand(command string, args []string) string {
	parts := make([]string, 0, 1+len(args))
	if strings.TrimSpace(command) != "" {
		parts = append(parts, shellQuote(command))
	}
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func mergedEnv(overrides map[string]string) []string {
	base := os.Environ()
	if len(overrides) == 0 {
		return base
	}
	values := map[string]string{}
	for _, item := range base {
		name, value, ok := strings.Cut(item, "=")
		if ok {
			values[name] = value
		}
	}
	for key, value := range overrides {
		name := strings.TrimSpace(key)
		if name == "" {
			continue
		}
		values[name] = value
	}
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}

func lookupAuthCredential(methodID string) string {
	for _, key := range authEnvKeys(methodID) {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return ""
}

func authEnvKeys(methodID string) []string {
	methodID = strings.TrimSpace(methodID)
	if methodID == "" {
		return nil
	}
	normalized := strings.ToUpper(methodID)
	normalized = strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, normalized)
	normalized = strings.Trim(normalized, "_")
	keys := []string{methodID}
	if normalized != "" {
		keys = append(keys, normalized, "ACPX_AUTH_"+normalized)
	}
	return keys
}
