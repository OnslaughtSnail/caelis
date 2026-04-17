package acp

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type BridgeStrategy string

const (
	BridgeStrategyBaseOnly         BridgeStrategy = "base_only"
	BridgeStrategyTerminalTakeover BridgeStrategy = "terminal_takeover"
	BridgeStrategyFullAccessBypass BridgeStrategy = "full_access_bypass"
)

func NewRuntime(base toolexec.Runtime, conn *Conn, sessionID, workspaceRoot, sessionCWD string, caps ClientCapabilities, modeResolver func() string) toolexec.Runtime {
	if base == nil {
		return nil
	}
	workspaceRoot = filepath.Clean(strings.TrimSpace(workspaceRoot))
	sessionCWD = normalizeSessionDir(sessionCWD)
	if sessionCWD == "" {
		sessionCWD = normalizeSessionDir(workspaceRoot)
	}
	baseFS := base.FileSystem()
	bridge := &runtimeBridge{
		base:          base,
		workspaceRoot: workspaceRoot,
		sessionCWD:    sessionCWD,
		modeResolver:  modeResolver,
		strategy:      BridgeStrategyBaseOnly,
	}
	fileSystem := &clientFileSystem{
		base:      baseFS,
		conn:      conn,
		sessionID: strings.TrimSpace(sessionID),
		cwd:       sessionCWD,
		caps:      caps,
		owner:     bridge,
	}
	var terminalBackend toolexec.Backend
	if caps.Terminal && conn != nil && base.PermissionMode() != toolexec.PermissionModeFullControl {
		terminalRunner := wrapSessionAsyncCommandRunner(NewAsyncCommandRunner(conn, sessionID), sessionCWD)
		terminalBackend = newRuntimeBridgeBackend("acp_terminal", toolexec.BackendKindHost, terminalRunner)
		bridge.strategy = BridgeStrategyTerminalTakeover
	}
	bridge.fileSystem = fileSystem
	bridge.terminalBackend = terminalBackend
	if bridge.PermissionMode() == toolexec.PermissionModeFullControl {
		bridge.strategy = BridgeStrategyFullAccessBypass
	}
	return bridge
}

type runtimeBridge struct {
	base            toolexec.Runtime
	workspaceRoot   string
	sessionCWD      string
	fileSystem      toolexec.FileSystem
	terminalBackend toolexec.Backend
	modeResolver    func() string
	strategy        BridgeStrategy
	mu              sync.Mutex
	tempFiles       []string
}

func (r *runtimeBridge) PermissionMode() toolexec.PermissionMode {
	if r == nil {
		return toolexec.PermissionModeDefault
	}
	if r.base != nil && r.base.PermissionMode() == toolexec.PermissionModeFullControl {
		return toolexec.PermissionModeFullControl
	}
	if r.modeResolver != nil && sessionmode.PermissionMode(r.modeResolver()) == toolexec.PermissionModeFullControl {
		return toolexec.PermissionModeFullControl
	}
	return r.base.PermissionMode()
}
func (r *runtimeBridge) SandboxType() string { return r.base.SandboxType() }
func (r *runtimeBridge) SandboxPolicy() toolexec.SandboxPolicy {
	if r.PermissionMode() == toolexec.PermissionModeFullControl {
		return toolexec.SandboxPolicy{
			Type:          toolexec.SandboxPolicyDangerFull,
			NetworkAccess: true,
		}
	}
	return r.base.SandboxPolicy()
}
func (r *runtimeBridge) FallbackToHost() bool {
	if r.PermissionMode() == toolexec.PermissionModeFullControl {
		return false
	}
	return r.base.FallbackToHost()
}
func (r *runtimeBridge) FallbackReason() string {
	if r.PermissionMode() == toolexec.PermissionModeFullControl {
		return ""
	}
	return r.base.FallbackReason()
}
func (r *runtimeBridge) Diagnostics() toolexec.SandboxDiagnostics {
	if r.PermissionMode() == toolexec.PermissionModeFullControl {
		return toolexec.SandboxDiagnostics{}
	}
	return r.base.Diagnostics()
}

func (r *runtimeBridge) State() toolexec.RuntimeState {
	state := r.base.State()
	state.Mode = r.PermissionMode()
	return state
}

func (r *runtimeBridge) FileSystem() toolexec.FileSystem { return r.fileSystem }

func (r *runtimeBridge) Execute(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	if backend := r.takeoverBackend(); backend != nil {
		req.BackendName = backend.Name()
		return backend.Execute(ctx, sessionCommandRequest(req, r.sessionCWD))
	}
	if r.PermissionMode() == toolexec.PermissionModeFullControl {
		req.RouteHint = toolexec.ExecutionRouteHost
		req.BackendName = "host"
	}
	return r.base.Execute(ctx, sessionCommandRequest(req, r.sessionCWD))
}

func (r *runtimeBridge) Start(ctx context.Context, req toolexec.CommandRequest) (toolexec.Session, error) {
	if backend := r.takeoverBackend(); backend != nil {
		req.BackendName = backend.Name()
		return backend.Start(ctx, sessionCommandRequest(req, r.sessionCWD))
	}
	if r.PermissionMode() == toolexec.PermissionModeFullControl {
		req.RouteHint = toolexec.ExecutionRouteHost
		req.BackendName = "host"
	}
	return r.base.Start(ctx, sessionCommandRequest(req, r.sessionCWD))
}

func (r *runtimeBridge) OpenSession(ref toolexec.CommandSessionRef) (toolexec.Session, error) {
	if backend := r.takeoverBackend(); backend != nil && strings.EqualFold(strings.TrimSpace(ref.Backend), backend.Name()) {
		return backend.OpenSession(ref.SessionID)
	}
	return r.base.OpenSession(ref)
}

func (r *runtimeBridge) Decide(ctx context.Context, req toolexec.RouteRequest) (toolexec.CommandDecision, error) {
	_ = ctx
	if r.PermissionMode() == toolexec.PermissionModeFullControl {
		return toolexec.CommandDecision{Route: toolexec.ExecutionRouteHost, Backend: "host"}, nil
	}
	decision := r.base.DecideRoute(req.Command, req.SandboxPermission)
	if backend := r.takeoverBackend(); backend != nil {
		decision.Backend = backend.Name()
	}
	return decision, nil
}

func (r *runtimeBridge) DecideRoute(command string, sandboxPermission toolexec.SandboxPermission) toolexec.CommandDecision {
	decision, _ := r.Decide(context.Background(), toolexec.RouteRequest{
		Command:           command,
		SandboxPermission: sandboxPermission,
	})
	return decision
}

func (r *runtimeBridge) BridgeStrategy() BridgeStrategy {
	if r == nil {
		return BridgeStrategyBaseOnly
	}
	return r.strategy
}

func (r *runtimeBridge) trackTempFile(path string) {
	if r == nil || strings.TrimSpace(path) == "" {
		return
	}
	r.mu.Lock()
	r.tempFiles = append(r.tempFiles, path)
	r.mu.Unlock()
}

func (r *runtimeBridge) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	tempFiles := append([]string(nil), r.tempFiles...)
	r.tempFiles = nil
	r.mu.Unlock()
	var firstErr error
	for _, path := range tempFiles {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	if err := toolexec.Close(r.base); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (r *runtimeBridge) takeoverBackend() toolexec.Backend {
	if r == nil || r.PermissionMode() == toolexec.PermissionModeFullControl {
		return nil
	}
	return r.terminalBackend
}

type clientFileSystem struct {
	base      toolexec.FileSystem
	conn      *Conn
	sessionID string
	cwd       string
	caps      ClientCapabilities
	owner     *runtimeBridge
}

type runtimeBridgeBackend struct {
	name   string
	kind   toolexec.BackendKind
	runner toolexec.AsyncCommandRunner
}

func newRuntimeBridgeBackend(name string, kind toolexec.BackendKind, runner toolexec.AsyncCommandRunner) *runtimeBridgeBackend {
	if runner == nil {
		return nil
	}
	return &runtimeBridgeBackend{name: strings.TrimSpace(name), kind: kind, runner: runner}
}

func (b *runtimeBridgeBackend) Name() string               { return b.name }
func (b *runtimeBridgeBackend) Kind() toolexec.BackendKind { return b.kind }
func (b *runtimeBridgeBackend) Capabilities() toolexec.BackendCapabilities {
	return toolexec.BackendCapabilities{Async: b.runner != nil}
}
func (b *runtimeBridgeBackend) Health(context.Context) toolexec.BackendHealth {
	return toolexec.BackendHealth{Ready: b.runner != nil}
}
func (b *runtimeBridgeBackend) Execute(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return b.runner.Run(ctx, req)
}
func (b *runtimeBridgeBackend) Start(ctx context.Context, req toolexec.CommandRequest) (toolexec.Session, error) {
	sessionID, err := b.runner.StartAsync(ctx, req)
	if err != nil {
		return nil, err
	}
	return &runtimeBridgeSession{
		ref:    toolexec.CommandSessionRef{Backend: b.name, SessionID: sessionID},
		runner: b.runner,
	}, nil
}
func (b *runtimeBridgeBackend) OpenSession(sessionID string) (toolexec.Session, error) {
	return &runtimeBridgeSession{
		ref:    toolexec.CommandSessionRef{Backend: b.name, SessionID: strings.TrimSpace(sessionID)},
		runner: b.runner,
	}, nil
}

type runtimeBridgeSession struct {
	ref    toolexec.CommandSessionRef
	runner toolexec.AsyncCommandRunner
}

func (s *runtimeBridgeSession) Ref() toolexec.CommandSessionRef { return s.ref }
func (s *runtimeBridgeSession) WriteInput(input []byte) error {
	return s.runner.WriteInput(s.ref.SessionID, input)
}
func (s *runtimeBridgeSession) ReadOutput(stdoutMarker, stderrMarker int64) ([]byte, []byte, int64, int64, error) {
	return s.runner.ReadOutput(s.ref.SessionID, stdoutMarker, stderrMarker)
}
func (s *runtimeBridgeSession) Status() (toolexec.SessionStatus, error) {
	return s.runner.GetSessionStatus(s.ref.SessionID)
}
func (s *runtimeBridgeSession) Wait(ctx context.Context, timeout time.Duration) (toolexec.CommandResult, error) {
	return s.runner.WaitSession(ctx, s.ref.SessionID, timeout)
}
func (s *runtimeBridgeSession) Terminate() error {
	return s.runner.TerminateSession(s.ref.SessionID)
}

func (f *clientFileSystem) Getwd() (string, error) {
	if cwd := normalizeSessionDir(f.cwd); cwd != "" {
		return cwd, nil
	}
	return f.base.Getwd()
}
func (f *clientFileSystem) UserHomeDir() (string, error)               { return f.base.UserHomeDir() }
func (f *clientFileSystem) ReadDir(path string) ([]os.DirEntry, error) { return f.base.ReadDir(path) }
func (f *clientFileSystem) Stat(path string) (os.FileInfo, error)      { return f.base.Stat(path) }
func (f *clientFileSystem) Glob(pattern string) ([]string, error)      { return f.base.Glob(pattern) }
func (f *clientFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	return f.base.WalkDir(root, fn)
}

func (f *clientFileSystem) Open(path string) (*os.File, error) {
	if !f.useClientReadFS() {
		return f.base.Open(path)
	}
	data, err := f.ReadFile(path)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp("", "caelis-acp-read-*")
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(data); err != nil {
		if stdruntime.GOOS == "windows" {
			_ = os.Remove(file.Name())
		}
		file.Close()
		return nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		if stdruntime.GOOS == "windows" {
			_ = os.Remove(file.Name())
		}
		file.Close()
		return nil, err
	}
	if stdruntime.GOOS != "windows" {
		_ = os.Remove(file.Name())
	} else if f.owner != nil {
		f.owner.trackTempFile(file.Name())
	}
	return file, nil
}

func (f *clientFileSystem) ReadFile(path string) ([]byte, error) {
	if !f.useClientReadFS() {
		return f.base.ReadFile(path)
	}
	var resp ReadTextFileResponse
	if err := f.conn.Call(context.Background(), MethodReadTextFile, ReadTextFileRequest{
		SessionID: f.sessionID,
		Path:      path,
	}, &resp); err != nil {
		return nil, err
	}
	return []byte(resp.Content), nil
}

func (f *clientFileSystem) WriteFile(path string, data []byte, perm os.FileMode) error {
	_ = perm
	if !f.useClientWriteFS() {
		return f.base.WriteFile(path, data, perm)
	}
	return f.conn.Call(context.Background(), MethodWriteTextFile, WriteTextFileRequest{
		SessionID: f.sessionID,
		Path:      path,
		Content:   string(data),
	}, nil)
}

func (f *clientFileSystem) useClientReadFS() bool {
	return f != nil && f.conn != nil && f.caps.FS.ReadTextFile
}

func (f *clientFileSystem) useClientWriteFS() bool {
	return f != nil && f.conn != nil && f.caps.FS.WriteTextFile
}

type clientCommandRunner struct {
	conn      *Conn
	sessionID string
}

func (r *clientCommandRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	command, args := shellCommand(req.Command)
	outputLimit := 256 * 1024
	var created CreateTerminalResponse
	releaseCtx := context.WithoutCancel(ctx)
	if err := r.conn.Call(ctx, MethodTerminalCreate, CreateTerminalRequest{
		SessionID:       r.sessionID,
		Command:         command,
		Args:            args,
		CWD:             strings.TrimSpace(req.Dir),
		OutputByteLimit: &outputLimit,
	}, &created); err != nil {
		return toolexec.CommandResult{}, err
	}
	defer func() {
		_ = r.conn.Call(releaseCtx, MethodTerminalRelease, ReleaseTerminalRequest{
			SessionID:  r.sessionID,
			TerminalID: created.TerminalID,
		}, nil)
	}()
	var waitResp WaitForTerminalExitResponse
	if err := r.conn.Call(ctx, MethodTerminalWaitForExit, WaitForTerminalExitRequest{
		SessionID:  r.sessionID,
		TerminalID: created.TerminalID,
	}, &waitResp); err != nil {
		return toolexec.CommandResult{}, err
	}
	var outputResp TerminalOutputResponse
	if err := r.conn.Call(ctx, MethodTerminalOutput, TerminalOutputRequest{
		SessionID:  r.sessionID,
		TerminalID: created.TerminalID,
	}, &outputResp); err != nil {
		return toolexec.CommandResult{}, err
	}
	if req.OnOutput != nil && strings.TrimSpace(outputResp.Output) != "" {
		req.OnOutput(toolexec.CommandOutputChunk{Stream: "stdout", Text: outputResp.Output})
	}
	exitCode := 0
	if waitResp.ExitCode != nil {
		exitCode = *waitResp.ExitCode
	}
	return toolexec.CommandResult{
		Stdout:   outputResp.Output,
		ExitCode: exitCode,
	}, nil
}

func shellCommand(input string) (string, []string) {
	input = strings.TrimSpace(input)
	if stdruntime.GOOS == "windows" {
		return "cmd.exe", []string{"/C", input}
	}
	return "sh", []string{"-lc", input}
}

type sessionAsyncCommandRunner struct {
	toolexec.AsyncCommandRunner
	sessionCWD string
}

func wrapSessionAsyncCommandRunner(base toolexec.AsyncCommandRunner, sessionCWD string) toolexec.AsyncCommandRunner {
	if base == nil {
		return nil
	}
	return &sessionAsyncCommandRunner{
		AsyncCommandRunner: base,
		sessionCWD:         normalizeSessionDir(sessionCWD),
	}
}

func (r *sessionAsyncCommandRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return r.AsyncCommandRunner.Run(ctx, sessionCommandRequest(req, r.sessionCWD))
}

func (r *sessionAsyncCommandRunner) StartAsync(ctx context.Context, req toolexec.CommandRequest) (string, error) {
	return r.AsyncCommandRunner.StartAsync(ctx, sessionCommandRequest(req, r.sessionCWD))
}

func sessionCommandRequest(req toolexec.CommandRequest, sessionCWD string) toolexec.CommandRequest {
	if strings.TrimSpace(req.Dir) == "" && strings.TrimSpace(sessionCWD) != "" {
		req.Dir = sessionCWD
	}
	return req
}

func normalizeSessionDir(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(trimmed)
}

type commandSession struct {
	terminalID string
	command    string
	dir        string
	startTime  time.Time
	state      toolexec.SessionState
	exitCode   int
}

type clientAsyncCommandRunner struct {
	syncRunner *clientCommandRunner
	conn       *Conn
	sessionID  string

	mu       sync.Mutex
	sessions map[string]*commandSession
}

func NewAsyncCommandRunner(conn *Conn, sessionID string) toolexec.AsyncCommandRunner {
	return &clientAsyncCommandRunner{
		syncRunner: &clientCommandRunner{conn: conn, sessionID: sessionID},
		conn:       conn,
		sessionID:  strings.TrimSpace(sessionID),
		sessions:   map[string]*commandSession{},
	}
}

func (r *clientAsyncCommandRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	return r.syncRunner.Run(ctx, req)
}

func (r *clientAsyncCommandRunner) StartAsync(ctx context.Context, req toolexec.CommandRequest) (string, error) {
	command, args := shellCommand(req.Command)
	outputLimit := 256 * 1024
	var created CreateTerminalResponse
	if err := r.conn.Call(ctx, MethodTerminalCreate, CreateTerminalRequest{
		SessionID:       r.sessionID,
		Command:         command,
		Args:            args,
		CWD:             strings.TrimSpace(req.Dir),
		OutputByteLimit: &outputLimit,
	}, &created); err != nil {
		return "", err
	}
	r.mu.Lock()
	r.sessions[created.TerminalID] = &commandSession{
		terminalID: created.TerminalID,
		command:    req.Command,
		dir:        strings.TrimSpace(req.Dir),
		startTime:  time.Now(),
		state:      toolexec.SessionStateRunning,
		exitCode:   -1,
	}
	r.mu.Unlock()
	return created.TerminalID, nil
}

func (r *clientAsyncCommandRunner) WriteInput(_ string, input []byte) error {
	_ = input
	return fmt.Errorf("acp terminal does not support interactive input")
}

func (r *clientAsyncCommandRunner) ReadOutput(sessionID string, stdoutMarker, stderrMarker int64) (stdout, stderr []byte, newStdoutMarker, newStderrMarker int64, err error) {
	_ = stderrMarker
	var outputResp TerminalOutputResponse
	if err = r.conn.Call(context.Background(), MethodTerminalOutput, TerminalOutputRequest{
		SessionID:  r.sessionID,
		TerminalID: sessionID,
	}, &outputResp); err != nil {
		return nil, nil, 0, 0, err
	}
	text := outputResp.Output
	start := int(stdoutMarker)
	if start < 0 || start > len(text) {
		start = 0
	}
	return []byte(text[start:]), nil, int64(len(text)), 0, nil
}

func (r *clientAsyncCommandRunner) GetSessionStatus(sessionID string) (toolexec.SessionStatus, error) {
	var outputResp TerminalOutputResponse
	if err := r.conn.Call(context.Background(), MethodTerminalOutput, TerminalOutputRequest{
		SessionID:  r.sessionID,
		TerminalID: sessionID,
	}, &outputResp); err != nil {
		return toolexec.SessionStatus{}, err
	}
	state := toolexec.SessionStateRunning
	exitCode := -1
	if outputResp.ExitStatus != nil {
		state = toolexec.SessionStateCompleted
		if outputResp.ExitStatus.ExitCode != nil {
			exitCode = *outputResp.ExitStatus.ExitCode
		}
	}
	info := r.sessionInfo(sessionID)
	if info != nil {
		r.mu.Lock()
		info.state = state
		info.exitCode = exitCode
		r.mu.Unlock()
	}
	return toolexec.SessionStatus{
		ID:           sessionID,
		Command:      sessionValue(info, func(s *commandSession) string { return s.command }),
		Dir:          sessionValue(info, func(s *commandSession) string { return s.dir }),
		State:        state,
		StartTime:    sessionTimeValue(info),
		LastActivity: time.Now(),
		ExitCode:     exitCode,
		StdoutBytes:  int64(len(outputResp.Output)),
	}, nil
}

func (r *clientAsyncCommandRunner) WaitSession(ctx context.Context, sessionID string, timeout time.Duration) (toolexec.CommandResult, error) {
	waitCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	var waitResp WaitForTerminalExitResponse
	if err := r.conn.Call(waitCtx, MethodTerminalWaitForExit, WaitForTerminalExitRequest{
		SessionID:  r.sessionID,
		TerminalID: sessionID,
	}, &waitResp); err != nil {
		return toolexec.CommandResult{}, err
	}
	var outputResp TerminalOutputResponse
	if err := r.conn.Call(waitCtx, MethodTerminalOutput, TerminalOutputRequest{
		SessionID:  r.sessionID,
		TerminalID: sessionID,
	}, &outputResp); err != nil {
		return toolexec.CommandResult{}, err
	}
	result := toolexec.CommandResult{Stdout: outputResp.Output}
	if waitResp.ExitCode != nil {
		result.ExitCode = *waitResp.ExitCode
	}
	r.mu.Lock()
	if info, ok := r.sessions[sessionID]; ok {
		info.state = toolexec.SessionStateCompleted
		info.exitCode = result.ExitCode
	}
	r.mu.Unlock()
	return result, nil
}

func (r *clientAsyncCommandRunner) TerminateSession(sessionID string) error {
	err := r.conn.Call(context.Background(), MethodTerminalKill, KillTerminalRequest{
		SessionID:  r.sessionID,
		TerminalID: sessionID,
	}, nil)
	if err != nil && isMethodNotFoundRPC(err) {
		err = r.conn.Call(context.Background(), MethodTerminalRelease, ReleaseTerminalRequest{
			SessionID:  r.sessionID,
			TerminalID: sessionID,
		}, nil)
	}
	r.mu.Lock()
	if info, ok := r.sessions[sessionID]; ok {
		info.state = toolexec.SessionStateTerminated
	}
	r.mu.Unlock()
	return err
}

func (r *clientAsyncCommandRunner) ListSessions() []toolexec.SessionInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]toolexec.SessionInfo, 0, len(r.sessions))
	for id, info := range r.sessions {
		out = append(out, toolexec.SessionInfo{
			ID:           id,
			Command:      info.command,
			State:        info.state,
			StartTime:    info.startTime,
			LastActivity: time.Now(),
			ExitCode:     info.exitCode,
			HasOutput:    true,
		})
	}
	return out
}

func (r *clientAsyncCommandRunner) sessionInfo(sessionID string) *commandSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[sessionID]
}

func sessionValue(info *commandSession, getter func(*commandSession) string) string {
	if info == nil || getter == nil {
		return ""
	}
	return getter(info)
}

func isMethodNotFoundRPC(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "acp rpc error -32601")
}

func sessionTimeValue(info *commandSession) time.Time {
	if info == nil || info.startTime.IsZero() {
		return time.Now()
	}
	return info.startTime
}
