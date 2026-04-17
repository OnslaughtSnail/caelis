package acp

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
)

func TestPermissionBridge_DeduplicatesToolAndCommandApprovalPerCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	clientConn := NewConn(s2cR, c2sW)
	serverConn := NewConn(c2sR, s2cW)

	requests := 0
	go func() {
		_ = serverConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			requests++
			return RequestPermissionResponse{
				Outcome: mustMarshalRaw(SelectedPermissionOutcome{
					Outcome:  "selected",
					OptionID: "allow_once",
				}),
			}, nil
		}, func(context.Context, Message) {})
	}()
	go func() {
		_ = clientConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}, func(context.Context, Message) {})
	}()

	bridge := newPermissionBridge(clientConn, "session-1", nil)
	callCtx := toolexec.WithToolCallInfo(context.Background(), "BASH", "call-1")

	allowed, err := bridge.AuthorizeTool(callCtx, policy.ToolAuthorizationRequest{
		ToolName:   "BASH",
		Permission: "tool authorization",
		Reason:     "guarded tool",
	})
	if err != nil {
		t.Fatalf("authorize tool: %v", err)
	}
	if !allowed {
		t.Fatal("expected tool authorization to allow")
	}

	allowed, err = bridge.Approve(callCtx, toolexec.ApprovalRequest{
		ToolName: "BASH",
		Command:  "sleep 1",
		Reason:   "host execution requires approval",
	})
	if err != nil {
		t.Fatalf("command approval: %v", err)
	}
	if !allowed {
		t.Fatal("expected command approval to allow")
	}
	if requests != 1 {
		t.Fatalf("expected a single permission request for one tool call, got %d", requests)
	}
}

func TestPermissionBridge_FullAccessSkipsPermissionRequests(t *testing.T) {
	bridge := newPermissionBridge(nil, "session-1", func() string { return "full_access" })
	allowed, err := bridge.AuthorizeTool(context.Background(), policy.ToolAuthorizationRequest{
		ToolName:   "WRITE",
		Permission: "write file",
		Path:       "/tmp/file.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected full_access tool authorization bypass")
	}

	allowed, err = bridge.Approve(context.Background(), toolexec.ApprovalRequest{
		ToolName: "BASH",
		Command:  "go test ./...",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected benign command approval bypass")
	}
}

func TestPermissionBridge_FullAccessBlocksDangerousCommands(t *testing.T) {
	bridge := newPermissionBridge(nil, "session-1", func() string { return "full_access" })
	allowed, err := bridge.Approve(context.Background(), toolexec.ApprovalRequest{
		ToolName: "BASH",
		Command:  "rm -rf /",
	})
	if allowed {
		t.Fatal("expected dangerous command to be denied")
	}
	if err == nil || !toolexec.IsApprovalAborted(err) {
		t.Fatalf("expected approval aborted error, got %v", err)
	}
}

func TestPermissionBridge_QueuesConcurrentPermissionRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	clientConn := NewConn(s2cR, c2sW)
	serverConn := NewConn(c2sR, s2cW)

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	var mu sync.Mutex
	requests := 0
	inFlight := 0
	maxInFlight := 0

	go func() {
		_ = serverConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			mu.Lock()
			requests++
			inFlight++
			if inFlight > maxInFlight {
				maxInFlight = inFlight
			}
			current := requests
			mu.Unlock()

			if current == 1 {
				close(firstStarted)
				<-releaseFirst
			}

			mu.Lock()
			inFlight--
			mu.Unlock()

			return RequestPermissionResponse{
				Outcome: mustMarshalRaw(SelectedPermissionOutcome{
					Outcome:  "selected",
					OptionID: "allow_once",
				}),
			}, nil
		}, func(context.Context, Message) {})
	}()
	go func() {
		_ = clientConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}, func(context.Context, Message) {})
	}()

	bridge := newPermissionBridge(clientConn, "session-1", nil)

	resultCh := make(chan error, 2)
	go func() {
		_, err := bridge.Approve(context.Background(), toolexec.ApprovalRequest{
			ToolName: "BASH",
			Command:  "sleep 1",
		})
		resultCh <- err
	}()
	<-firstStarted
	go func() {
		_, err := bridge.Approve(context.Background(), toolexec.ApprovalRequest{
			ToolName: "BASH",
			Command:  "sleep 2",
		})
		resultCh <- err
	}()

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	gotRequests := requests
	gotMaxInFlight := maxInFlight
	mu.Unlock()
	if gotRequests != 1 {
		t.Fatalf("expected second request to stay queued while first is pending, got %d requests", gotRequests)
	}
	if gotMaxInFlight != 1 {
		t.Fatalf("expected at most one in-flight permission request, got %d", gotMaxInFlight)
	}

	close(releaseFirst)
	for range 2 {
		if err := <-resultCh; err != nil {
			t.Fatalf("expected queued permission requests to succeed, got %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("expected two total permission requests, got %d", requests)
	}
	if maxInFlight != 1 {
		t.Fatalf("expected serialized permission requests, got maxInFlight=%d", maxInFlight)
	}
}

func TestPermissionBridge_KeepsRequestsScopedPerSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	clientConn := NewConn(s2cR, c2sW)
	serverConn := NewConn(c2sR, s2cW)

	requests := make(chan RequestPermissionRequest, 2)
	go func() {
		_ = serverConn.Serve(ctx, func(_ context.Context, msg Message) (any, *RPCError) {
			if msg.Method != MethodSessionReqPermission {
				return nil, &RPCError{Code: -32601, Message: "method not found"}
			}
			var req RequestPermissionRequest
			if err := decodeParams(msg.Params, &req); err != nil {
				return nil, invalidParamsError(err)
			}
			requests <- req
			return RequestPermissionResponse{
				Outcome: mustMarshalRaw(SelectedPermissionOutcome{
					Outcome:  "selected",
					OptionID: "allow_once",
				}),
			}, nil
		}, func(context.Context, Message) {})
	}()
	go func() {
		_ = clientConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}, func(context.Context, Message) {})
	}()

	bridgeA := newPermissionBridge(clientConn, "child-a", nil)
	bridgeB := newPermissionBridge(clientConn, "child-b", nil)
	if _, err := bridgeA.Approve(context.Background(), toolexec.ApprovalRequest{ToolName: "BASH", Command: "echo a"}); err != nil {
		t.Fatalf("bridgeA approve: %v", err)
	}
	if _, err := bridgeB.Approve(context.Background(), toolexec.ApprovalRequest{ToolName: "BASH", Command: "echo b"}); err != nil {
		t.Fatalf("bridgeB approve: %v", err)
	}

	first := <-requests
	second := <-requests
	if first.SessionID == second.SessionID {
		t.Fatalf("expected distinct session-scoped permission requests, got %+v and %+v", first, second)
	}
	if first.SessionID != "child-a" && second.SessionID != "child-a" {
		t.Fatalf("expected one request for child-a, got %+v and %+v", first, second)
	}
	if first.SessionID != "child-b" && second.SessionID != "child-b" {
		t.Fatalf("expected one request for child-b, got %+v and %+v", first, second)
	}
}

func TestPermissionBridge_ApproveRequestUsesOriginalToolMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	clientConn := NewConn(s2cR, c2sW)
	serverConn := NewConn(c2sR, s2cW)

	requests := make(chan RequestPermissionRequest, 1)
	go func() {
		_ = serverConn.Serve(ctx, func(_ context.Context, msg Message) (any, *RPCError) {
			if msg.Method != MethodSessionReqPermission {
				return nil, &RPCError{Code: -32601, Message: "method not found"}
			}
			var req RequestPermissionRequest
			if err := decodeParams(msg.Params, &req); err != nil {
				return nil, invalidParamsError(err)
			}
			requests <- req
			return RequestPermissionResponse{
				Outcome: mustMarshalRaw(SelectedPermissionOutcome{
					Outcome:  "selected",
					OptionID: "allow_once",
				}),
			}, nil
		}, func(context.Context, Message) {})
	}()
	go func() {
		_ = clientConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}, func(context.Context, Message) {})
	}()

	bridge := newPermissionBridge(clientConn, "session-1", nil)
	callCtx := toolexec.WithToolCallInfo(context.Background(), "BASH", "call-1")
	allowed, err := bridge.Approve(callCtx, toolexec.ApprovalRequest{
		ToolName: "BASH",
		Command:  "acpx --help",
		Reason:   "host execution requires approval",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !allowed {
		t.Fatal("expected approval to allow")
	}

	select {
	case req := <-requests:
		if req.ToolCall.ToolCallID != "call-1" {
			t.Fatalf("expected permission request to reuse tool call id, got %q", req.ToolCall.ToolCallID)
		}
		if req.ToolCall.Title == nil || *req.ToolCall.Title != "BASH acpx --help" {
			t.Fatalf("expected summarized title, got %#v", req.ToolCall.Title)
		}
		raw, ok := req.ToolCall.RawInput.(map[string]any)
		if !ok {
			t.Fatalf("expected rawInput map, got %#v", req.ToolCall.RawInput)
		}
		if raw["command"] != "acpx --help" {
			t.Fatalf("expected rawInput command preserved, got %#v", raw)
		}
		if _, ok := raw["reason"]; ok {
			t.Fatalf("did not expect approval-only metadata in rawInput, got %#v", raw)
		}
	default:
		t.Fatal("expected permission request payload")
	}
}

func TestPermissionBridge_AuthorizeToolRequestUsesScopedToolMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	clientConn := NewConn(s2cR, c2sW)
	serverConn := NewConn(c2sR, s2cW)

	requests := make(chan RequestPermissionRequest, 1)
	go func() {
		_ = serverConn.Serve(ctx, func(_ context.Context, msg Message) (any, *RPCError) {
			if msg.Method != MethodSessionReqPermission {
				return nil, &RPCError{Code: -32601, Message: "method not found"}
			}
			var req RequestPermissionRequest
			if err := decodeParams(msg.Params, &req); err != nil {
				return nil, invalidParamsError(err)
			}
			requests <- req
			return RequestPermissionResponse{
				Outcome: mustMarshalRaw(SelectedPermissionOutcome{
					Outcome:  "selected",
					OptionID: "allow_once",
				}),
			}, nil
		}, func(context.Context, Message) {})
	}()
	go func() {
		_ = clientConn.Serve(ctx, func(context.Context, Message) (any, *RPCError) {
			return nil, &RPCError{Code: -32601, Message: "method not found"}
		}, func(context.Context, Message) {})
	}()

	bridge := newPermissionBridge(clientConn, "session-1", nil)
	callCtx := toolexec.WithToolCallInfo(context.Background(), "PATCH", "call-patch-1")
	allowed, err := bridge.AuthorizeTool(callCtx, policy.ToolAuthorizationRequest{
		ToolName: "PATCH",
		Path:     "/tmp/demo.txt",
		Reason:   "filesystem mutation tool",
	})
	if err != nil {
		t.Fatalf("authorize tool: %v", err)
	}
	if !allowed {
		t.Fatal("expected authorization to allow")
	}

	select {
	case req := <-requests:
		if req.ToolCall.ToolCallID != "call-patch-1" {
			t.Fatalf("expected tool authorization to reuse tool call id, got %q", req.ToolCall.ToolCallID)
		}
		if req.ToolCall.Title == nil || *req.ToolCall.Title != "PATCH /tmp/demo.txt" {
			t.Fatalf("expected summarized title, got %#v", req.ToolCall.Title)
		}
		raw, ok := req.ToolCall.RawInput.(map[string]any)
		if !ok {
			t.Fatalf("expected rawInput map, got %#v", req.ToolCall.RawInput)
		}
		if raw["path"] != "/tmp/demo.txt" {
			t.Fatalf("expected rawInput path preserved, got %#v", raw)
		}
		if _, ok := raw["preview"]; ok {
			t.Fatalf("did not expect approval-only metadata in rawInput, got %#v", raw)
		}
	default:
		t.Fatal("expected tool authorization payload")
	}
}
