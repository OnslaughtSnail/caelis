package acp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func fullTerminalCapabilities() bool {
	return true
}

func TestNewRuntime_TerminalCapabilityUsesACPBridgeForHostAndSandbox(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	clientConn := NewConn(s2cR, c2sW)
	serverConn := NewConn(c2sR, s2cW)

	baseRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxType(),
		HostRunner:     stubRunner{},
		SandboxRunner:  stubRunner{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer func() { _ = toolexec.Close(baseRuntime) }()

	go func() {
		_ = serverConn.Serve(ctx, func(ctx context.Context, msg Message) (any, *RPCError) {
			switch msg.Method {
			case MethodTerminalCreate:
				return CreateTerminalResponse{TerminalID: "term-async-1"}, nil
			case MethodTerminalOutput:
				return TerminalOutputResponse{
					Output:    "chunk-one\nchunk-two\n",
					Truncated: false,
					ExitStatus: &TerminalExitStatus{
						ExitCode: ptr(0),
					},
				}, nil
			case MethodTerminalWaitForExit:
				return WaitForTerminalExitResponse{ExitCode: ptr(0)}, nil
			case MethodTerminalKill:
				return map[string]any{}, nil
			case MethodTerminalRelease:
				return map[string]any{}, nil
			default:
				return nil, &RPCError{Code: -32601, Message: "unexpected method"}
			}
		}, func(context.Context, Message) {})
	}()
	go func() {
		_ = clientConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}, func(context.Context, Message) {})
	}()

	rt := NewRuntime(baseRuntime, clientConn, "session-1", "/workspace", "/workspace", ClientCapabilities{
		Terminal: fullTerminalCapabilities(),
	}, nil)
	asyncRunner, ok := rt.HostRunner().(toolexec.AsyncCommandRunner)
	if !ok {
		t.Fatal("expected host runner to implement AsyncCommandRunner")
	}
	sandboxAsync, ok := rt.SandboxRunner().(toolexec.AsyncCommandRunner)
	if !ok {
		t.Fatal("expected sandbox runner to implement AsyncCommandRunner")
	}
	if _, ok := sandboxAsync.(*sessionAsyncCommandRunner); !ok {
		t.Fatalf("expected sandbox runner to be wrapped ACP async runner, got %T", sandboxAsync)
	}

	sessionID, err := asyncRunner.StartAsync(context.Background(), toolexec.CommandRequest{
		Command: "echo hi",
		Dir:     "/workspace",
	})
	if err != nil {
		t.Fatalf("start async: %v", err)
	}
	if sessionID != "term-async-1" {
		t.Fatalf("unexpected session id %q", sessionID)
	}

	stdout, stderr, stdoutMarker, stderrMarker, err := asyncRunner.ReadOutput(sessionID, 0, 0)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(stdout) != "chunk-one\nchunk-two\n" {
		t.Fatalf("unexpected stdout %q", string(stdout))
	}
	if len(stderr) != 0 || stderrMarker != 0 {
		t.Fatalf("unexpected stderr result %q marker=%d", string(stderr), stderrMarker)
	}
	if stdoutMarker == 0 {
		t.Fatal("expected stdout marker to advance")
	}

	status, err := asyncRunner.GetSessionStatus(sessionID)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status.State != toolexec.SessionStateCompleted {
		t.Fatalf("expected completed status, got %q", status.State)
	}
	if status.Command != "echo hi" || status.Dir != "/workspace" {
		t.Fatalf("unexpected status metadata: %+v", status)
	}

	result, err := asyncRunner.WaitSession(context.Background(), sessionID, time.Second)
	if err != nil {
		t.Fatalf("wait session: %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "chunk-one\nchunk-two\n" {
		t.Fatalf("unexpected wait result: %+v", result)
	}

	if err := asyncRunner.TerminateSession(sessionID); err != nil {
		t.Fatalf("terminate session: %v", err)
	}
	sessions := asyncRunner.ListSessions()
	if len(sessions) != 1 || sessions[0].State != toolexec.SessionStateTerminated {
		t.Fatalf("unexpected sessions list: %+v", sessions)
	}

	cancel()
}

func TestNewRuntime_TerminalCapabilityRoutesSandboxAsyncThroughACPBridge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	clientConn := NewConn(s2cR, c2sW)
	serverConn := NewConn(c2sR, s2cW)

	sandboxRunner := newAsyncSandboxStubRunner()
	baseRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxType(),
		HostRunner:     stubRunner{},
		SandboxRunner:  sandboxRunner,
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer func() { _ = toolexec.Close(baseRuntime) }()

	terminalCreates := 0
	go func() {
		_ = serverConn.Serve(ctx, func(ctx context.Context, msg Message) (any, *RPCError) {
			switch msg.Method {
			case MethodTerminalCreate:
				terminalCreates++
				return CreateTerminalResponse{TerminalID: "term-should-not-be-used"}, nil
			case MethodTerminalOutput:
				return TerminalOutputResponse{
					Output:    "terminal-output\n",
					Truncated: false,
					ExitStatus: &TerminalExitStatus{
						ExitCode: ptr(0),
					},
				}, nil
			case MethodTerminalWaitForExit:
				return WaitForTerminalExitResponse{ExitCode: ptr(0)}, nil
			case MethodTerminalKill:
				return map[string]any{}, nil
			case MethodTerminalRelease:
				return map[string]any{}, nil
			default:
				return nil, &RPCError{Code: -32601, Message: "unexpected method"}
			}
		}, func(context.Context, Message) {})
	}()
	go func() {
		_ = clientConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}, func(context.Context, Message) {})
	}()

	rt := NewRuntime(baseRuntime, clientConn, "session-1", "/workspace", "/workspace/subdir", ClientCapabilities{
		Terminal: fullTerminalCapabilities(),
	}, nil)

	hostRunner, ok := rt.HostRunner().(*sessionAsyncCommandRunner)
	if !ok {
		t.Fatalf("expected async host runner wrapper, got %T", rt.HostRunner())
	}
	if _, ok := hostRunner.AsyncCommandRunner.(*clientAsyncCommandRunner); !ok {
		t.Fatalf("expected host runner to use ACP terminal bridge, got %T", hostRunner.AsyncCommandRunner)
	}

	sandboxAsync, ok := rt.SandboxRunner().(*sessionAsyncCommandRunner)
	if !ok {
		t.Fatalf("expected async sandbox runner wrapper, got %T", rt.SandboxRunner())
	}
	if _, ok := sandboxAsync.AsyncCommandRunner.(*clientAsyncCommandRunner); !ok {
		t.Fatalf("expected sandbox runner to use ACP terminal bridge, got %T", sandboxAsync.AsyncCommandRunner)
	}

	sessionID, err := sandboxAsync.StartAsync(context.Background(), toolexec.CommandRequest{
		Command: "echo from sandbox",
	})
	if err != nil {
		t.Fatalf("start async on sandbox runner: %v", err)
	}
	if sessionID != "term-should-not-be-used" {
		t.Fatalf("unexpected sandbox session id %q", sessionID)
	}
	if terminalCreates != 1 {
		t.Fatalf("expected terminal/create for sandbox async, got %d", terminalCreates)
	}
	if sandboxRunner.startCalls != 0 {
		t.Fatalf("did not expect local sandbox async start, got %d", sandboxRunner.startCalls)
	}
}

func TestNewRuntime_FallsBackWithoutTerminalCapability(t *testing.T) {
	baseHost := stubRunner{}
	baseSandbox := stubRunner{}
	baseRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxType(),
		HostRunner:     baseHost,
		SandboxRunner:  baseSandbox,
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer func() { _ = toolexec.Close(baseRuntime) }()

	rt := NewRuntime(baseRuntime, nil, "session-1", "/workspace", "/workspace", ClientCapabilities{}, nil)
	if _, ok := rt.HostRunner().(toolexec.AsyncCommandRunner); ok {
		t.Fatal("did not expect async host runner without terminal capability")
	}
	if rt.HostRunner() == nil {
		t.Fatal("expected host runner fallback to stay available")
	}
	if rt.SandboxRunner() == nil {
		t.Fatal("expected sandbox runner fallback to stay available")
	}
}

type asyncSandboxStubRunner struct {
	mu         sync.Mutex
	startCalls int
	lastReq    toolexec.CommandRequest
	state      toolexec.SessionState
}

func newAsyncSandboxStubRunner() *asyncSandboxStubRunner {
	return &asyncSandboxStubRunner{state: toolexec.SessionStateCompleted}
}

func (r *asyncSandboxStubRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	_ = ctx
	r.mu.Lock()
	r.lastReq = req
	r.mu.Unlock()
	return toolexec.CommandResult{Stdout: "sandbox-run\n"}, nil
}

func (r *asyncSandboxStubRunner) StartAsync(ctx context.Context, req toolexec.CommandRequest) (string, error) {
	_ = ctx
	r.mu.Lock()
	r.startCalls++
	r.lastReq = req
	r.mu.Unlock()
	return "sandbox-session-1", nil
}

func (r *asyncSandboxStubRunner) WriteInput(sessionID string, input []byte) error {
	_ = sessionID
	_ = input
	return nil
}

func (r *asyncSandboxStubRunner) ReadOutput(sessionID string, stdoutMarker, stderrMarker int64) (stdout, stderr []byte, newStdoutMarker, newStderrMarker int64, err error) {
	_ = sessionID
	return []byte("sandbox-output\n"), nil, stdoutMarker + 15, stderrMarker, nil
}

func (r *asyncSandboxStubRunner) GetSessionStatus(sessionID string) (toolexec.SessionStatus, error) {
	_ = sessionID
	r.mu.Lock()
	defer r.mu.Unlock()
	return toolexec.SessionStatus{
		ID:        "sandbox-session-1",
		Command:   r.lastReq.Command,
		Dir:       r.lastReq.Dir,
		State:     r.state,
		StartTime: time.Now(),
	}, nil
}

func (r *asyncSandboxStubRunner) WaitSession(ctx context.Context, sessionID string, timeout time.Duration) (toolexec.CommandResult, error) {
	_ = ctx
	_ = sessionID
	_ = timeout
	return toolexec.CommandResult{Stdout: "sandbox-output\n", ExitCode: 0}, nil
}

func (r *asyncSandboxStubRunner) TerminateSession(sessionID string) error {
	_ = sessionID
	r.mu.Lock()
	r.state = toolexec.SessionStateTerminated
	r.mu.Unlock()
	return nil
}

func (r *asyncSandboxStubRunner) ListSessions() []toolexec.SessionInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []toolexec.SessionInfo{{
		ID:        "sandbox-session-1",
		Command:   r.lastReq.Command,
		State:     r.state,
		StartTime: time.Now(),
	}}
}

func TestNewRuntime_FullAccessModeBypassesSandbox(t *testing.T) {
	baseRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxType(),
		HostRunner:     stubRunner{},
		SandboxRunner:  stubRunner{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer func() { _ = toolexec.Close(baseRuntime) }()

	rt := NewRuntime(baseRuntime, nil, "session-1", "/workspace", "/workspace", ClientCapabilities{}, func() string {
		return "full_access"
	})
	if rt.PermissionMode() != toolexec.PermissionModeFullControl {
		t.Fatalf("expected full_control permission mode, got %q", rt.PermissionMode())
	}
	if rt.SandboxRunner() == nil {
		t.Fatal("expected sandbox runner fallback to host runner in full_access mode")
	}
	if rt.SandboxRunner() != rt.HostRunner() {
		t.Fatal("expected sandbox runner to reuse host runner in full_access mode")
	}
	decision := rt.DecideRoute("pwd", toolexec.SandboxPermissionAuto)
	if decision.Route != toolexec.ExecutionRouteHost {
		t.Fatalf("expected host route in full_access mode, got %q", decision.Route)
	}
	if rt.FallbackToHost() {
		t.Fatal("did not expect fallback mode in full_access")
	}
	if rt.SandboxPolicy().Type != toolexec.SandboxPolicyDangerFull {
		t.Fatalf("expected danger_full_access sandbox policy, got %q", rt.SandboxPolicy().Type)
	}
}

func TestNewRuntime_FullAccessModeKeepsLocalHostRunnerEvenWithTerminalCapability(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	clientConn := NewConn(s2cR, c2sW)
	serverConn := NewConn(c2sR, s2cW)

	baseHost := stubRunner{}
	baseSandbox := stubRunner{}
	baseRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxType(),
		HostRunner:     baseHost,
		SandboxRunner:  baseSandbox,
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer func() { _ = toolexec.Close(baseRuntime) }()

	go func() {
		_ = serverConn.Serve(ctx, func(ctx context.Context, msg Message) (any, *RPCError) {
			switch msg.Method {
			case MethodTerminalCreate:
				return CreateTerminalResponse{TerminalID: "term-full-access"}, nil
			case MethodTerminalOutput:
				return TerminalOutputResponse{Output: "ok", ExitStatus: &TerminalExitStatus{ExitCode: ptr(0)}}, nil
			case MethodTerminalWaitForExit:
				return WaitForTerminalExitResponse{ExitCode: ptr(0)}, nil
			case MethodTerminalKill:
				return map[string]any{}, nil
			case MethodTerminalRelease:
				return map[string]any{}, nil
			default:
				return nil, &RPCError{Code: -32601, Message: "unexpected method"}
			}
		}, func(context.Context, Message) {})
	}()
	go func() {
		_ = clientConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}, func(context.Context, Message) {})
	}()

	rt := NewRuntime(baseRuntime, clientConn, "session-1", "/workspace", "/workspace", ClientCapabilities{
		Terminal: fullTerminalCapabilities(),
	}, func() string {
		return "full_access"
	})
	if _, ok := rt.HostRunner().(toolexec.AsyncCommandRunner); !ok {
		t.Fatal("expected full_access host runner to stay on ACP terminal bridge")
	}
	if rt.SandboxRunner() != rt.HostRunner() {
		t.Fatal("expected sandbox runner to collapse to host route runner in full_access mode")
	}
}

func TestAsyncRunner_TerminateSessionUsesTerminalKill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	clientConn := NewConn(s2cR, c2sW)
	serverConn := NewConn(c2sR, s2cW)

	killCalls := 0
	releaseCalls := 0
	go func() {
		_ = serverConn.Serve(ctx, func(ctx context.Context, msg Message) (any, *RPCError) {
			switch msg.Method {
			case MethodTerminalCreate:
				return CreateTerminalResponse{TerminalID: "term-kill-1"}, nil
			case MethodTerminalKill:
				killCalls++
				return map[string]any{}, nil
			case MethodTerminalRelease:
				releaseCalls++
				return map[string]any{}, nil
			case MethodTerminalOutput:
				return TerminalOutputResponse{Output: "", Truncated: false}, nil
			default:
				return nil, &RPCError{Code: -32601, Message: "unexpected method"}
			}
		}, func(context.Context, Message) {})
	}()
	go func() {
		_ = clientConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}, func(context.Context, Message) {})
	}()

	runner := NewAsyncCommandRunner(clientConn, "session-1")
	sessionID, err := runner.StartAsync(context.Background(), toolexec.CommandRequest{
		Command: "sleep 10",
		Dir:     "/workspace",
	})
	if err != nil {
		t.Fatalf("start async: %v", err)
	}
	if err := runner.TerminateSession(sessionID); err != nil {
		t.Fatalf("terminate session: %v", err)
	}
	if killCalls != 1 {
		t.Fatalf("expected one terminal/kill call, got %d", killCalls)
	}
	if releaseCalls != 0 {
		t.Fatalf("expected no terminal/release call on terminate, got %d", releaseCalls)
	}
}

func TestNewRuntime_FullAccessModeKeepsACPFileSystemBridge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	clientConn := NewConn(s2cR, c2sW)
	serverConn := NewConn(c2sR, s2cW)
	writes := map[string]string{}

	baseRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxType(),
		HostRunner:     stubRunner{},
		SandboxRunner:  stubRunner{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer func() { _ = toolexec.Close(baseRuntime) }()

	go func() {
		_ = serverConn.Serve(ctx, func(ctx context.Context, msg Message) (any, *RPCError) {
			switch msg.Method {
			case MethodReadTextFile:
				var req ReadTextFileRequest
				if err := decodeParams(msg.Params, &req); err != nil {
					return nil, &RPCError{Code: -32602, Message: err.Error()}
				}
				return ReadTextFileResponse{Content: writes[req.Path]}, nil
			case MethodWriteTextFile:
				var req WriteTextFileRequest
				if err := decodeParams(msg.Params, &req); err != nil {
					return nil, &RPCError{Code: -32602, Message: err.Error()}
				}
				writes[req.Path] = req.Content
				return WriteTextFileResponse{}, nil
			default:
				return nil, &RPCError{Code: -32601, Message: "unexpected method"}
			}
		}, func(context.Context, Message) {})
	}()
	go func() {
		_ = clientConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}, func(context.Context, Message) {})
	}()

	dir := t.TempDir()
	path := filepath.Join(dir, "full-access.txt")
	rt := NewRuntime(baseRuntime, clientConn, "session-1", dir, dir, ClientCapabilities{
		FS: FileSystemCapabilities{
			ReadTextFile:  true,
			WriteTextFile: true,
		},
	}, func() string {
		return "full_access"
	})

	if err := rt.FileSystem().WriteFile(path, []byte("client-data"), 0o644); err != nil {
		t.Fatalf("write file via runtime fs: %v", err)
	}
	if writes[path] != "client-data" {
		t.Fatalf("expected ACP bridge write, got %+v", writes)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected host filesystem to remain untouched, got err=%v", err)
	}

	readBack, err := rt.FileSystem().ReadFile(path)
	if err != nil {
		t.Fatalf("read file via runtime fs: %v", err)
	}
	if string(readBack) != "client-data" {
		t.Fatalf("unexpected runtime fs contents %q", string(readBack))
	}
}

func TestNewRuntime_BaseFullControlOverridesDefaultSessionMode(t *testing.T) {
	baseHost := stubRunner{}
	baseSandbox := stubRunner{}
	baseRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		SandboxType:    testSandboxType(),
		HostRunner:     baseHost,
		SandboxRunner:  baseSandbox,
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer func() { _ = toolexec.Close(baseRuntime) }()

	rt := NewRuntime(baseRuntime, nil, "session-1", "/workspace", "/workspace", ClientCapabilities{
		Terminal: fullTerminalCapabilities(),
	}, func() string {
		return "default"
	})
	if rt.PermissionMode() != toolexec.PermissionModeFullControl {
		t.Fatalf("expected base full_control permission mode to be preserved, got %q", rt.PermissionMode())
	}
	if rt.HostRunner() == nil {
		t.Fatal("expected host runner to stay available when base permission is full_control")
	}
	if rt.SandboxRunner() != rt.HostRunner() {
		t.Fatal("expected sandbox runner to collapse to host runner when base permission is full_control")
	}
}

func TestNewRuntime_FileSystemGetwdUsesSessionCWD(t *testing.T) {
	baseRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxType(),
		HostRunner:     stubRunner{},
		SandboxRunner:  stubRunner{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer func() { _ = toolexec.Close(baseRuntime) }()

	rt := NewRuntime(baseRuntime, nil, "session-1", "/workspace", "/workspace/subdir", ClientCapabilities{}, nil)
	cwd, err := rt.FileSystem().Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if cwd != "/workspace/subdir" {
		t.Fatalf("expected session cwd, got %q", cwd)
	}
}

func TestNewRuntime_DefaultsCommandDirToSessionCWD(t *testing.T) {
	host := &recordingRunner{}
	baseRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    testSandboxType(),
		HostRunner:     host,
		SandboxRunner:  stubRunner{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer func() { _ = toolexec.Close(baseRuntime) }()

	rt := NewRuntime(baseRuntime, nil, "session-1", "/workspace", "/workspace/subdir", ClientCapabilities{}, nil)
	if _, err := rt.HostRunner().Run(context.Background(), toolexec.CommandRequest{Command: "pwd"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if host.lastDir != "/workspace/subdir" {
		t.Fatalf("expected host runner dir /workspace/subdir, got %q", host.lastDir)
	}
}

type recordingRunner struct {
	lastDir string
}

func (r *recordingRunner) Run(ctx context.Context, req toolexec.CommandRequest) (toolexec.CommandResult, error) {
	_ = ctx
	r.lastDir = req.Dir
	return toolexec.CommandResult{Stdout: req.Dir}, nil
}

func TestNewRuntime_BaseFullControlKeepsACPFileSystemBridge(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	clientConn := NewConn(s2cR, c2sW)
	serverConn := NewConn(c2sR, s2cW)
	files := map[string]string{}

	baseRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeFullControl,
		SandboxType:    testSandboxType(),
		HostRunner:     stubRunner{},
		SandboxRunner:  stubRunner{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	defer func() { _ = toolexec.Close(baseRuntime) }()

	go func() {
		_ = serverConn.Serve(ctx, func(ctx context.Context, msg Message) (any, *RPCError) {
			switch msg.Method {
			case MethodReadTextFile:
				var req ReadTextFileRequest
				if err := decodeParams(msg.Params, &req); err != nil {
					return nil, &RPCError{Code: -32602, Message: err.Error()}
				}
				return ReadTextFileResponse{Content: files[req.Path]}, nil
			case MethodWriteTextFile:
				var req WriteTextFileRequest
				if err := decodeParams(msg.Params, &req); err != nil {
					return nil, &RPCError{Code: -32602, Message: err.Error()}
				}
				files[req.Path] = req.Content
				return WriteTextFileResponse{}, nil
			default:
				return nil, &RPCError{Code: -32601, Message: "unexpected method"}
			}
		}, func(context.Context, Message) {})
	}()
	go func() {
		_ = clientConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}, func(context.Context, Message) {})
	}()

	dir := t.TempDir()
	path := filepath.Join(dir, "base-full-control.txt")
	rt := NewRuntime(baseRuntime, clientConn, "session-1", dir, dir, ClientCapabilities{
		FS: FileSystemCapabilities{
			ReadTextFile:  true,
			WriteTextFile: true,
		},
	}, func() string {
		return "default"
	})

	if err := rt.FileSystem().WriteFile(path, []byte("bridged-data"), 0o644); err != nil {
		t.Fatalf("write file via runtime fs: %v", err)
	}
	if files[path] != "bridged-data" {
		t.Fatalf("expected ACP bridge write under base full_control, got %+v", files)
	}
	readBack, err := rt.FileSystem().ReadFile(path)
	if err != nil {
		t.Fatalf("read file via runtime fs: %v", err)
	}
	if string(readBack) != "bridged-data" {
		t.Fatalf("unexpected runtime fs contents %q", string(readBack))
	}
}
