package main

import (
	"context"
	"io"
	"strings"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	kernelpolicy "github.com/OnslaughtSnail/caelis/kernel/policy"
)

type stubLineEditor struct {
	lines []string
	idx   int
	reads int
}

func (s *stubLineEditor) ReadLine(prompt string) (string, error) {
	_ = prompt
	s.reads++
	if s.idx >= len(s.lines) {
		return "", errInputEOF
	}
	line := s.lines[s.idx]
	s.idx++
	return line, nil
}

func (s *stubLineEditor) ReadSecret(prompt string) (string, error) {
	return s.ReadLine(prompt)
}

func (s *stubLineEditor) Output() io.Writer { return io.Discard }
func (s *stubLineEditor) Close() error      { return nil }

func TestTerminalApprover_RequiresExplicitApprovalByDefault(t *testing.T) {
	editor := &stubLineEditor{lines: []string{"y"}}
	approver := newTerminalApprover(editor, io.Discard, nil)

	allowed, err := approver.Approve(context.Background(), toolexec.ApprovalRequest{
		ToolName: "BASH",
		Action:   "execute_command",
		Reason:   "sandbox_permissions=require_escalated requested",
		Command:  "head -70 main.go | tail -20",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected explicit approval to allow command")
	}
	if editor.reads != 1 {
		t.Fatalf("expected one prompt for default approval, got reads=%d", editor.reads)
	}
}

func TestTerminalApprover_RejectsCommandWhenUserDenies(t *testing.T) {
	editor := &stubLineEditor{lines: []string{"n"}}
	approver := newTerminalApprover(editor, io.Discard, nil)

	allowed, err := approver.Approve(context.Background(), toolexec.ApprovalRequest{
		Command: "ls & rm -rf /tmp/x",
	})
	if allowed {
		t.Fatal("expected denied command to return false")
	}
	if err == nil || !toolexec.IsApprovalAborted(err) {
		t.Fatalf("expected approval aborted error, got %v", err)
	}
	if editor.reads != 1 {
		t.Fatalf("expected prompt read once, got %d", editor.reads)
	}
}

func TestTerminalApprover_AlwaysAddsSessionWhitelist(t *testing.T) {
	editor := &stubLineEditor{lines: []string{"a"}}
	approver := newTerminalApprover(editor, io.Discard, nil)
	req := toolexec.ApprovalRequest{Command: "go test ./..."}

	allowed, err := approver.Approve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected first approval with always to pass")
	}
	if editor.reads != 1 {
		t.Fatalf("expected one prompt read, got %d", editor.reads)
	}

	allowed, err = approver.Approve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected session-whitelisted command to pass")
	}
	if editor.reads != 1 {
		t.Fatalf("expected second call to skip prompt, reads=%d", editor.reads)
	}
}

func TestTerminalApprover_CancelReturnsApprovalAborted(t *testing.T) {
	editor := &stubLineEditor{lines: []string{"n"}}
	approver := newTerminalApprover(editor, io.Discard, nil)

	allowed, err := approver.Approve(context.Background(), toolexec.ApprovalRequest{Command: "go test ./..."})
	if allowed {
		t.Fatal("expected cancel to deny")
	}
	if err == nil {
		t.Fatal("expected approval aborted error")
	}
	if !toolexec.IsApprovalAborted(err) {
		t.Fatalf("expected approval aborted error, got %v", err)
	}
}

func TestSessionApprovalKey_ComplexCommandUsesExactText(t *testing.T) {
	key := sessionApprovalKey("grep hi a.txt && rm -f a.txt")
	if !strings.Contains(key, "&&") {
		t.Fatalf("expected complex command key to keep exact text, got %q", key)
	}
}

func TestTerminalApprover_EOFIsCancel(t *testing.T) {
	editor := &stubLineEditor{}
	approver := newTerminalApprover(editor, io.Discard, nil)
	_, err := approver.Approve(context.Background(), toolexec.ApprovalRequest{Command: "go test ./..."})
	if err == nil {
		t.Fatal("expected cancel error")
	}
	if !toolexec.IsApprovalAborted(err) {
		t.Fatalf("expected approval aborted, got %v", err)
	}
}

func TestTerminalApprover_AuthorizeToolAlwaysCachesByToolName(t *testing.T) {
	editor := &stubLineEditor{lines: []string{"a"}}
	approver := newTerminalApprover(editor, io.Discard, nil)
	req := kernelpolicy.ToolAuthorizationRequest{
		ToolName: "WRITE",
		Reason:   "filesystem mutation tool",
	}

	allowed, err := approver.AuthorizeTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected tool authorization to pass")
	}
	if editor.reads != 1 {
		t.Fatalf("expected one prompt read, got %d", editor.reads)
	}

	allowed, err = approver.AuthorizeTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected session-whitelisted tool to pass")
	}
	if editor.reads != 1 {
		t.Fatalf("expected second authorization to skip prompt, reads=%d", editor.reads)
	}
}

func TestTerminalApprover_AuthorizeToolCancelReturnsApprovalAborted(t *testing.T) {
	editor := &stubLineEditor{lines: []string{"n"}}
	approver := newTerminalApprover(editor, io.Discard, nil)

	allowed, err := approver.AuthorizeTool(context.Background(), kernelpolicy.ToolAuthorizationRequest{
		ToolName: "PATCH",
		Reason:   "filesystem mutation tool",
	})
	if allowed {
		t.Fatal("expected cancel to deny tool authorization")
	}
	if err == nil {
		t.Fatal("expected approval aborted error")
	}
	if !toolexec.IsApprovalAborted(err) {
		t.Fatalf("expected approval aborted, got %v", err)
	}
}
