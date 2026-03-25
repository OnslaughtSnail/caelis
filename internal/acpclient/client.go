package acpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type Update interface {
	UpdateKind() string
}

func (c ContentChunk) UpdateKind() string            { return c.SessionUpdate }
func (t ToolCall) UpdateKind() string                { return t.SessionUpdate }
func (t ToolCallUpdate) UpdateKind() string          { return t.SessionUpdate }
func (p PlanUpdate) UpdateKind() string              { return p.SessionUpdate }
func (u AvailableCommandsUpdate) UpdateKind() string { return u.SessionUpdate }
func (u CurrentModeUpdate) UpdateKind() string       { return u.SessionUpdate }
func (u ConfigOptionUpdate) UpdateKind() string      { return u.SessionUpdate }
func (u SessionInfoUpdate) UpdateKind() string       { return u.SessionUpdate }

type UpdateEnvelope struct {
	SessionID string
	Update    Update
}

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
	cfg  Config
	conn *Conn
	cmd  *exec.Cmd

	cancel context.CancelFunc
	done   chan error

	terminalMu sync.Mutex
	terminals  map[string]clientTerminal
	stderrMu   sync.Mutex
	stderrBuf  bytes.Buffer
}

type clientTerminal struct {
	sessionID       string
	outputByteLimit int
}

func Start(ctx context.Context, cfg Config) (*Client, error) {
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
	serveCtx, cancel := context.WithCancel(context.Background())
	client := &Client{
		cfg:       cfg,
		conn:      NewConn(stdout, stdin),
		cmd:       cmd,
		cancel:    cancel,
		done:      make(chan error, 1),
		terminals: map[string]clientTerminal{},
	}
	go func() {
		err := client.conn.Serve(serveCtx, client.handleRequest, client.handleNotification)
		client.done <- err
	}()
	go func() {
		_, _ = io.Copy(stderrBufferWriter{client: client}, stderr)
	}()
	return client, nil
}

func (c *Client) Initialize(ctx context.Context) (InitializeResponse, error) {
	var resp InitializeResponse
	err := c.conn.Call(ctx, MethodInitialize, InitializeRequest{
		ProtocolVersion: 1,
		ClientCapabilities: ClientCapabilities{
			FS:       FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
			Terminal: true,
		},
		ClientInfo: c.cfg.ClientInfo,
	}, &resp)
	if err != nil {
		return InitializeResponse{}, err
	}
	for _, method := range resp.AuthMethods {
		credential := lookupAuthCredential(method.ID)
		if strings.TrimSpace(credential) == "" {
			continue
		}
		if err := c.conn.Call(ctx, MethodAuthenticate, AuthenticateRequest{MethodID: method.ID}, &AuthenticateResponse{}); err != nil {
			return InitializeResponse{}, err
		}
		break
	}
	return resp, nil
}

func (c *Client) NewSession(ctx context.Context, cwd string, meta map[string]any) (NewSessionResponse, error) {
	var resp NewSessionResponse
	err := c.conn.Call(ctx, MethodSessionNew, NewSessionRequest{
		CWD:        cwd,
		MCPServers: c.mcpServers(),
		Meta:       meta,
	}, &resp)
	return resp, err
}

func (c *Client) LoadSession(ctx context.Context, sessionID string, cwd string, meta map[string]any) (LoadSessionResponse, error) {
	var resp LoadSessionResponse
	err := c.conn.Call(ctx, MethodSessionLoad, LoadSessionRequest{
		SessionID:  sessionID,
		CWD:        cwd,
		MCPServers: c.mcpServers(),
		Meta:       meta,
	}, &resp)
	return resp, err
}

func (c *Client) Prompt(ctx context.Context, sessionID string, text string, meta map[string]any) (PromptResponse, error) {
	var resp PromptResponse
	err := c.conn.Call(ctx, MethodSessionPrompt, PromptRequest{
		SessionID: sessionID,
		Prompt: []json.RawMessage{
			mustMarshalRaw(TextContent{Type: "text", Text: text}),
		},
		Meta: meta,
	}, &resp)
	return resp, err
}

func (c *Client) Cancel(ctx context.Context, sessionID string) error {
	return c.conn.Call(ctx, MethodSessionCancel, CancelRequest{SessionID: sessionID}, &CancelResponse{})
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
	if c == nil || limit <= 0 {
		return ""
	}
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	data := c.stderrBuf.Bytes()
	if len(data) == 0 {
		return ""
	}
	if len(data) > limit {
		data = data[len(data)-limit:]
	}
	return strings.TrimSpace(string(data))
}

func (c *Client) handleNotification(ctx context.Context, msg Message) {
	if c == nil || msg.Method != MethodSessionUpdate || c.cfg.OnUpdate == nil {
		return
	}
	var note SessionNotification
	if err := decodeParams(msg.Params, &note); err != nil {
		return
	}
	update, err := decodeUpdate(note.Update)
	if err != nil {
		return
	}
	if update == nil {
		return
	}
	c.cfg.OnUpdate(UpdateEnvelope{
		SessionID: strings.TrimSpace(note.SessionID),
		Update:    update,
	})
	_ = ctx
}

func (c *Client) mcpServers() []MCPServer {
	if c == nil || len(c.cfg.MCPServers) == 0 {
		return []MCPServer{}
	}
	return append([]MCPServer(nil), c.cfg.MCPServers...)
}

type stderrBufferWriter struct {
	client *Client
}

func (w stderrBufferWriter) Write(p []byte) (int, error) {
	if w.client == nil || len(p) == 0 {
		return len(p), nil
	}
	w.client.stderrMu.Lock()
	defer w.client.stderrMu.Unlock()
	const limit = 32 * 1024
	if w.client.stderrBuf.Len()+len(p) > limit {
		trim := w.client.stderrBuf.Len() + len(p) - limit
		if trim >= w.client.stderrBuf.Len() {
			w.client.stderrBuf.Reset()
		} else if trim > 0 {
			rest := append([]byte(nil), w.client.stderrBuf.Bytes()[trim:]...)
			w.client.stderrBuf.Reset()
			_, _ = w.client.stderrBuf.Write(rest)
		}
	}
	_, err := w.client.stderrBuf.Write(p)
	return len(p), err
}

func (c *Client) handleRequest(ctx context.Context, msg Message) (any, *RPCError) {
	switch msg.Method {
	case MethodSessionReqPermission:
		return c.handlePermissionRequest(ctx, msg)
	case MethodReadTextFile:
		return c.handleReadTextFile(msg)
	case MethodWriteTextFile:
		return c.handleWriteTextFile(msg)
	case MethodTerminalCreate:
		return c.handleTerminalCreate(ctx, msg)
	case MethodTerminalOutput:
		return c.handleTerminalOutput(msg)
	case MethodTerminalWaitForExit:
		return c.handleTerminalWait(ctx, msg)
	case MethodTerminalKill:
		return c.handleTerminalKill(msg)
	case MethodTerminalRelease:
		return c.handleTerminalRelease(msg)
	default:
		return nil, &RPCError{Code: -32601, Message: "method not found"}
	}
}

func (c *Client) handlePermissionRequest(ctx context.Context, msg Message) (any, *RPCError) {
	var req RequestPermissionRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	if c.cfg.OnPermissionRequest != nil {
		resp, err := c.cfg.OnPermissionRequest(ctx, req)
		if err != nil {
			return nil, &RPCError{Code: -32000, Message: err.Error()}
		}
		return resp, nil
	}
	outcome := map[string]any{
		"outcome":  "selected",
		"optionId": "allow_once",
	}
	return RequestPermissionResponse{Outcome: mustMarshalRaw(outcome)}, nil
}

func (c *Client) handleReadTextFile(msg Message) (any, *RPCError) {
	var req ReadTextFileRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	if c.cfg.Runtime == nil || c.cfg.Runtime.FileSystem() == nil {
		return nil, &RPCError{Code: -32000, Message: "filesystem unavailable"}
	}
	data, err := c.cfg.Runtime.FileSystem().ReadFile(req.Path)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	return ReadTextFileResponse{Content: sliceTextByLines(string(data), req.Line, req.Limit)}, nil
}

func (c *Client) handleWriteTextFile(msg Message) (any, *RPCError) {
	var req WriteTextFileRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	if c.cfg.Runtime == nil || c.cfg.Runtime.FileSystem() == nil {
		return nil, &RPCError{Code: -32000, Message: "filesystem unavailable"}
	}
	if err := c.cfg.Runtime.FileSystem().WriteFile(req.Path, []byte(req.Content), 0o644); err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	return WriteTextFileResponse{}, nil
}

func (c *Client) handleTerminalCreate(ctx context.Context, msg Message) (any, *RPCError) {
	var req CreateTerminalRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	runner, err := asyncRunner(c.cfg.Runtime)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	sessionID, err := runner.StartAsync(ctx, toolexec.CommandRequest{
		Command:      buildShellCommand(req.Command, req.Args),
		Dir:          strings.TrimSpace(req.CWD),
		TTY:          false,
		EnvOverrides: envSliceToMap(req.Env),
	})
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	terminalID := "term-" + sessionID
	c.terminalMu.Lock()
	limit := 0
	if req.OutputByteLimit != nil && *req.OutputByteLimit > 0 {
		limit = *req.OutputByteLimit
	}
	c.terminals[terminalID] = clientTerminal{
		sessionID:       sessionID,
		outputByteLimit: limit,
	}
	c.terminalMu.Unlock()
	return CreateTerminalResponse{TerminalID: terminalID}, nil
}

func (c *Client) handleTerminalOutput(msg Message) (any, *RPCError) {
	var req TerminalOutputRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	runner, err := asyncRunner(c.cfg.Runtime)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	terminal, ok := c.lookupTerminal(req.TerminalID)
	if !ok {
		return nil, &RPCError{Code: -32000, Message: "unknown terminal"}
	}
	stdout, stderr, _, _, err := runner.ReadOutput(terminal.sessionID, 0, 0)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	status, _ := runner.GetSessionStatus(terminal.sessionID)
	output := string(stdout)
	if len(stderr) > 0 {
		output += string(stderr)
	}
	truncated := false
	if terminal.outputByteLimit > 0 {
		output, truncated = truncateFromStartAtRuneBoundary(output, terminal.outputByteLimit)
	}
	return TerminalOutputResponse{
		Output:     output,
		Truncated:  truncated,
		ExitStatus: exitStatusForSession(status),
	}, nil
}

func (c *Client) handleTerminalWait(ctx context.Context, msg Message) (any, *RPCError) {
	var req WaitForTerminalExitRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	runner, err := asyncRunner(c.cfg.Runtime)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	terminal, ok := c.lookupTerminal(req.TerminalID)
	if !ok {
		return nil, &RPCError{Code: -32000, Message: "unknown terminal"}
	}
	result, err := runner.WaitSession(ctx, terminal.sessionID, 0)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	exitCode := result.ExitCode
	return WaitForTerminalExitResponse{ExitCode: &exitCode}, nil
}

func (c *Client) handleTerminalKill(msg Message) (any, *RPCError) {
	var req KillTerminalRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	runner, err := asyncRunner(c.cfg.Runtime)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	terminal, ok := c.lookupTerminal(req.TerminalID)
	if !ok {
		return map[string]any{}, nil
	}
	if err := runner.TerminateSession(terminal.sessionID); err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	return map[string]any{}, nil
}

func (c *Client) handleTerminalRelease(msg Message) (any, *RPCError) {
	var req ReleaseTerminalRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	c.terminalMu.Lock()
	delete(c.terminals, req.TerminalID)
	c.terminalMu.Unlock()
	return map[string]any{}, nil
}

func (c *Client) lookupTerminal(id string) (clientTerminal, bool) {
	c.terminalMu.Lock()
	defer c.terminalMu.Unlock()
	terminal, ok := c.terminals[strings.TrimSpace(id)]
	return terminal, ok
}

func decodeParams(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func decodeUpdate(raw json.RawMessage) (Update, error) {
	var probe struct {
		SessionUpdate string `json:"sessionUpdate"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	switch probe.SessionUpdate {
	case UpdateUserMessage, UpdateAgentMessage, UpdateAgentThought:
		var chunk ContentChunk
		if err := json.Unmarshal(raw, &chunk); err != nil {
			return nil, err
		}
		return chunk, nil
	case UpdateToolCall:
		var call ToolCall
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		return call, nil
	case UpdateToolCallState:
		var call ToolCallUpdate
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		return call, nil
	case UpdatePlan:
		var plan PlanUpdate
		if err := json.Unmarshal(raw, &plan); err != nil {
			return nil, err
		}
		return plan, nil
	case UpdateAvailableCmds:
		var update AvailableCommandsUpdate
		if err := json.Unmarshal(raw, &update); err != nil {
			return nil, err
		}
		return update, nil
	case UpdateCurrentMode:
		var update CurrentModeUpdate
		if err := json.Unmarshal(raw, &update); err != nil {
			return nil, err
		}
		return update, nil
	case UpdateConfigOption:
		var update ConfigOptionUpdate
		if err := json.Unmarshal(raw, &update); err != nil {
			return nil, err
		}
		return update, nil
	case UpdateSessionInfo:
		var update SessionInfoUpdate
		if err := json.Unmarshal(raw, &update); err != nil {
			return nil, err
		}
		return update, nil
	default:
		return nil, nil
	}
}

func sliceTextByLines(content string, line *int, limit *int) string {
	lines := strings.Split(content, "\n")
	start := 0
	if line != nil && *line > 1 {
		start = *line - 1
		if start > len(lines) {
			start = len(lines)
		}
	}
	end := len(lines)
	if limit != nil && *limit >= 0 {
		if max := start + *limit; max < end {
			end = max
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func truncateFromStartAtRuneBoundary(text string, limit int) (string, bool) {
	if limit <= 0 || len(text) <= limit {
		return text, false
	}
	start := len(text) - limit
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	if start >= len(text) {
		return "", true
	}
	return text[start:], true
}

func envSliceToMap(items []EnvVariable) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		out[name] = item.Value
	}
	return out
}

func asyncRunner(rt toolexec.Runtime) (toolexec.AsyncCommandRunner, error) {
	if rt == nil {
		return nil, fmt.Errorf("runtime unavailable")
	}
	if runner, ok := rt.SandboxRunner().(toolexec.AsyncCommandRunner); ok && runner != nil {
		return runner, nil
	}
	if runner, ok := rt.HostRunner().(toolexec.AsyncCommandRunner); ok && runner != nil {
		return runner, nil
	}
	return nil, fmt.Errorf("async command runner unavailable")
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

func exitStatusForSession(status toolexec.SessionStatus) *TerminalExitStatus {
	switch status.State {
	case toolexec.SessionStateCompleted, toolexec.SessionStateError, toolexec.SessionStateTerminated:
		code := status.ExitCode
		return &TerminalExitStatus{ExitCode: &code}
	default:
		return nil
	}
}
