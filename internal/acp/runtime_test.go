package acp

import (
	"context"
	"io"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

func TestNewRuntime_UsesACPAsyncRunnerForTerminalCapability(t *testing.T) {
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

	rt := NewRuntime(baseRuntime, clientConn, "session-1", "/workspace", ClientCapabilities{
		Terminal: true,
	})
	asyncRunner, ok := rt.HostRunner().(toolexec.AsyncCommandRunner)
	if !ok {
		t.Fatal("expected host runner to implement AsyncCommandRunner")
	}
	if _, ok := rt.SandboxRunner().(toolexec.AsyncCommandRunner); !ok {
		t.Fatal("expected sandbox runner to use ACP async runner")
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

	rt := NewRuntime(baseRuntime, nil, "session-1", "/workspace", ClientCapabilities{})
	if _, ok := rt.HostRunner().(toolexec.AsyncCommandRunner); ok {
		t.Fatal("did not expect async host runner without terminal capability")
	}
	if rt.HostRunner() != baseHost {
		t.Fatal("expected host runner fallback to base runtime")
	}
	if rt.SandboxRunner() != baseSandbox {
		t.Fatal("expected sandbox runner fallback to base runtime")
	}
}
