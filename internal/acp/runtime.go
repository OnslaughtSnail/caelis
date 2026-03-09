package acp

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func NewRuntime(base toolexec.Runtime, conn *Conn, sessionID, workspaceDir string, caps ClientCapabilities) toolexec.Runtime {
	if base == nil {
		return nil
	}
	workspaceDir = filepath.Clean(strings.TrimSpace(workspaceDir))
	fileSystem := &clientFileSystem{
		base:      base.FileSystem(),
		conn:      conn,
		sessionID: strings.TrimSpace(sessionID),
		caps:      caps,
	}
	hostRunner := base.HostRunner()
	sandboxRunner := base.SandboxRunner()
	if caps.Terminal {
		asyncRunner := NewAsyncCommandRunner(conn, sessionID)
		hostRunner = asyncRunner
		sandboxRunner = asyncRunner
	}
	return &runtimeBridge{
		base:          base,
		fileSystem:    fileSystem,
		hostRunner:    hostRunner,
		sandboxRunner: sandboxRunner,
	}
}

type runtimeBridge struct {
	base          toolexec.Runtime
	fileSystem    toolexec.FileSystem
	hostRunner    toolexec.CommandRunner
	sandboxRunner toolexec.CommandRunner
}

func (r *runtimeBridge) PermissionMode() toolexec.PermissionMode { return r.base.PermissionMode() }
func (r *runtimeBridge) SandboxType() string                     { return r.base.SandboxType() }
func (r *runtimeBridge) SandboxPolicy() toolexec.SandboxPolicy   { return r.base.SandboxPolicy() }
func (r *runtimeBridge) FallbackToHost() bool                    { return r.base.FallbackToHost() }
func (r *runtimeBridge) FallbackReason() string                  { return r.base.FallbackReason() }
func (r *runtimeBridge) FileSystem() toolexec.FileSystem         { return r.fileSystem }
func (r *runtimeBridge) HostRunner() toolexec.CommandRunner      { return r.hostRunner }
func (r *runtimeBridge) SandboxRunner() toolexec.CommandRunner   { return r.sandboxRunner }

func (r *runtimeBridge) DecideRoute(command string, sandboxPermission toolexec.SandboxPermission) toolexec.CommandDecision {
	if sandboxPermission == toolexec.SandboxPermissionRequireEscalated {
		return r.base.DecideRoute(command, sandboxPermission)
	}
	if r.sandboxRunner != nil {
		return toolexec.CommandDecision{Route: toolexec.ExecutionRouteSandbox}
	}
	return r.base.DecideRoute(command, sandboxPermission)
}

type clientFileSystem struct {
	base      toolexec.FileSystem
	conn      *Conn
	sessionID string
	caps      ClientCapabilities
}

func (f *clientFileSystem) Getwd() (string, error)                     { return f.base.Getwd() }
func (f *clientFileSystem) UserHomeDir() (string, error)               { return f.base.UserHomeDir() }
func (f *clientFileSystem) ReadDir(path string) ([]os.DirEntry, error) { return f.base.ReadDir(path) }
func (f *clientFileSystem) Stat(path string) (os.FileInfo, error)      { return f.base.Stat(path) }
func (f *clientFileSystem) Glob(pattern string) ([]string, error)      { return f.base.Glob(pattern) }
func (f *clientFileSystem) WalkDir(root string, fn fs.WalkDirFunc) error {
	return f.base.WalkDir(root, fn)
}

func (f *clientFileSystem) Open(path string) (*os.File, error) {
	if !f.caps.FS.ReadTextFile {
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
		file.Close()
		return nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		file.Close()
		return nil, err
	}
	if runtime.GOOS != "windows" {
		_ = os.Remove(file.Name())
	}
	return file, nil
}

func (f *clientFileSystem) ReadFile(path string) ([]byte, error) {
	if !f.caps.FS.ReadTextFile {
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
	if !f.caps.FS.WriteTextFile {
		return f.base.WriteFile(path, data, perm)
	}
	return f.conn.Call(context.Background(), MethodWriteTextFile, WriteTextFileRequest{
		SessionID: f.sessionID,
		Path:      path,
		Content:   string(data),
	}, nil)
}

type clientCommandRunner struct {
	conn      *Conn
	sessionID string
}

func (r *clientCommandRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
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
		return toolexec.CommandResult{}, err
	}
	defer func() {
		_ = r.conn.Call(context.Background(), MethodTerminalRelease, ReleaseTerminalRequest{
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
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/C", input}
	}
	return "sh", []string{"-lc", input}
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

func (r *clientAsyncCommandRunner) WriteInput(sessionID string, input []byte) error {
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
	err := r.conn.Call(context.Background(), MethodTerminalRelease, ReleaseTerminalRequest{
		SessionID:  r.sessionID,
		TerminalID: sessionID,
	}, nil)
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

func sessionTimeValue(info *commandSession) time.Time {
	if info == nil || info.startTime.IsZero() {
		return time.Now()
	}
	return info.startTime
}
