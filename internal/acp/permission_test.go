package acp

import (
	"context"
	"io"
	"testing"

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
		Command:  "rm -rf /tmp/x",
	})
	if allowed {
		t.Fatal("expected dangerous command to be denied")
	}
	if err == nil || !toolexec.IsApprovalAborted(err) {
		t.Fatalf("expected approval aborted error, got %v", err)
	}
}
