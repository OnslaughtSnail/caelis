package main

import (
	"context"
	"io"
	"strings"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
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

func TestTerminalApprover_DefaultWhitelistAllowsSafeCommands(t *testing.T) {
	editor := &stubLineEditor{}
	approver := newTerminalApprover(editor, io.Discard, []string{"cat", "head", "grep", "tail"})

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
		t.Fatal("expected safe command pipeline to be auto-approved")
	}
	if editor.reads != 0 {
		t.Fatalf("expected no prompt for safe command, got reads=%d", editor.reads)
	}
}

func TestTerminalApprover_DefaultWhitelistAllowsGitStatus(t *testing.T) {
	editor := &stubLineEditor{}
	approver := newTerminalApprover(editor, io.Discard, []string{"cat"})

	allowed, err := approver.Approve(context.Background(), toolexec.ApprovalRequest{Command: "git status --short"})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected git status to be auto-approved")
	}
	if editor.reads != 0 {
		t.Fatalf("expected no prompt for git status, got reads=%d", editor.reads)
	}
}

func TestTerminalApprover_AlwaysAddsSessionWhitelist(t *testing.T) {
	editor := &stubLineEditor{lines: []string{"a"}}
	approver := newTerminalApprover(editor, io.Discard, []string{"cat"})
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
	approver := newTerminalApprover(editor, io.Discard, []string{"cat"})

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

func TestIsEnvAssignmentToken(t *testing.T) {
	if !isEnvAssignmentToken("MODEL_NAME=deepseek") {
		t.Fatal("expected env assignment token")
	}
	if isEnvAssignmentToken("1A=bad") {
		t.Fatal("expected invalid assignment to be rejected")
	}
}

func TestTerminalApprover_EOFIsCancel(t *testing.T) {
	editor := &stubLineEditor{}
	approver := newTerminalApprover(editor, io.Discard, []string{"cat"})
	_, err := approver.Approve(context.Background(), toolexec.ApprovalRequest{Command: "go test ./..."})
	if err == nil {
		t.Fatal("expected cancel error")
	}
	if !toolexec.IsApprovalAborted(err) {
		t.Fatalf("expected approval aborted, got %v", err)
	}
}
