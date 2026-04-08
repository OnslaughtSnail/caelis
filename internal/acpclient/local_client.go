package acpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type LocalClientConfig struct {
	Runtime toolexec.Runtime
}

type LocalClient struct {
	core *CoreClient
	cfg  LocalClientConfig

	terminalMu sync.Mutex
	terminals  map[string]clientTerminal
	stderrMu   sync.Mutex
	stderrBuf  bytes.Buffer
}

type clientTerminal struct {
	backendName     string
	sessionID       string
	outputByteLimit int
}

func NewLocalClient(conn *Conn, coreCfg CoreClientConfig, localCfg LocalClientConfig) *LocalClient {
	return &LocalClient{
		core:      NewCoreClient(conn, coreCfg),
		cfg:       localCfg,
		terminals: map[string]clientTerminal{},
	}
}

func (c *LocalClient) handleRequest(ctx context.Context, msg Message) (any, *RPCError) {
	switch msg.Method {
	case MethodSessionReqPermission:
		return c.core.handlePermissionRequest(ctx, msg)
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

func (c *LocalClient) handleNotification(ctx context.Context, msg Message) {
	c.core.handleNotification(ctx, msg)
}

func (c *LocalClient) handleReadTextFile(msg Message) (any, *RPCError) {
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

func (c *LocalClient) handleWriteTextFile(msg Message) (any, *RPCError) {
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

func (c *LocalClient) handleTerminalCreate(ctx context.Context, msg Message) (any, *RPCError) {
	var req CreateTerminalRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	sessionRef, err := c.cfg.Runtime.Start(ctx, toolexec.CommandRequest{
		Command:      buildShellCommand(req.Command, req.Args),
		Dir:          strings.TrimSpace(req.CWD),
		TTY:          false,
		EnvOverrides: envSliceToMap(req.Env),
	})
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	ref := sessionRef.Ref()
	sessionID := ref.SessionID
	terminalID := "term-" + sessionID
	c.terminalMu.Lock()
	limit := 0
	if req.OutputByteLimit != nil && *req.OutputByteLimit > 0 {
		limit = *req.OutputByteLimit
	}
	c.terminals[terminalID] = clientTerminal{
		backendName:     ref.Backend,
		sessionID:       sessionID,
		outputByteLimit: limit,
	}
	c.terminalMu.Unlock()
	return CreateTerminalResponse{TerminalID: terminalID}, nil
}

func (c *LocalClient) handleTerminalOutput(msg Message) (any, *RPCError) {
	var req TerminalOutputRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	terminal, ok := c.lookupTerminal(req.TerminalID)
	if !ok {
		return nil, &RPCError{Code: -32000, Message: "unknown terminal"}
	}
	sessionRef, err := c.cfg.Runtime.OpenSession(toolexec.CommandSessionRef{
		Backend:   terminal.backendName,
		SessionID: terminal.sessionID,
	})
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	stdout, stderr, _, _, err := sessionRef.ReadOutput(0, 0)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	status, _ := sessionRef.Status()
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

func (c *LocalClient) handleTerminalWait(ctx context.Context, msg Message) (any, *RPCError) {
	var req WaitForTerminalExitRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	terminal, ok := c.lookupTerminal(req.TerminalID)
	if !ok {
		return nil, &RPCError{Code: -32000, Message: "unknown terminal"}
	}
	sessionRef, err := c.cfg.Runtime.OpenSession(toolexec.CommandSessionRef{
		Backend:   terminal.backendName,
		SessionID: terminal.sessionID,
	})
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	result, err := sessionRef.Wait(ctx, 0)
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	exitCode := result.ExitCode
	return WaitForTerminalExitResponse{ExitCode: &exitCode}, nil
}

func (c *LocalClient) handleTerminalKill(msg Message) (any, *RPCError) {
	var req KillTerminalRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	terminal, ok := c.lookupTerminal(req.TerminalID)
	if !ok {
		return map[string]any{}, nil
	}
	sessionRef, err := c.cfg.Runtime.OpenSession(toolexec.CommandSessionRef{
		Backend:   terminal.backendName,
		SessionID: terminal.sessionID,
	})
	if err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	if err := sessionRef.Terminate(); err != nil {
		return nil, &RPCError{Code: -32000, Message: err.Error()}
	}
	return map[string]any{}, nil
}

func (c *LocalClient) handleTerminalRelease(msg Message) (any, *RPCError) {
	var req ReleaseTerminalRequest
	if err := decodeParams(msg.Params, &req); err != nil {
		return nil, &RPCError{Code: -32602, Message: err.Error()}
	}
	c.terminalMu.Lock()
	delete(c.terminals, req.TerminalID)
	c.terminalMu.Unlock()
	return map[string]any{}, nil
}

func (c *LocalClient) lookupTerminal(id string) (clientTerminal, bool) {
	c.terminalMu.Lock()
	defer c.terminalMu.Unlock()
	terminal, ok := c.terminals[strings.TrimSpace(id)]
	return terminal, ok
}

type stderrBufferWriter struct {
	local *LocalClient
}

func (w stderrBufferWriter) Write(p []byte) (int, error) {
	if w.local == nil || len(p) == 0 {
		return len(p), nil
	}
	w.local.stderrMu.Lock()
	defer w.local.stderrMu.Unlock()
	const limit = 32 * 1024
	if w.local.stderrBuf.Len()+len(p) > limit {
		trim := w.local.stderrBuf.Len() + len(p) - limit
		if trim >= w.local.stderrBuf.Len() {
			w.local.stderrBuf.Reset()
		} else if trim > 0 {
			rest := append([]byte(nil), w.local.stderrBuf.Bytes()[trim:]...)
			w.local.stderrBuf.Reset()
			_, _ = w.local.stderrBuf.Write(rest)
		}
	}
	_, err := w.local.stderrBuf.Write(p)
	return len(p), err
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
		return nil, fmt.Errorf("%w: %s", errUnknownSessionUpdate, probe.SessionUpdate)
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
		if limitEnd := start + *limit; limitEnd < end {
			end = limitEnd
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

func exitStatusForSession(status toolexec.SessionStatus) *TerminalExitStatus {
	switch status.State {
	case toolexec.SessionStateCompleted, toolexec.SessionStateError, toolexec.SessionStateTerminated:
		code := status.ExitCode
		return &TerminalExitStatus{ExitCode: &code}
	default:
		return nil
	}
}
