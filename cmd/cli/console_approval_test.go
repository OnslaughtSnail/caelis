package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
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

type stubChoiceEditor struct {
	stubLineEditor
	lastPrompt        string
	lastChoices       []tuievents.PromptChoice
	lastDefaultChoice string
	response          string
}

func (s *stubChoiceEditor) RequestChoicePrompt(prompt string, choices []tuievents.PromptChoice, defaultChoice string, filterable bool) (string, error) {
	_ = filterable
	s.lastPrompt = prompt
	s.lastChoices = append([]tuievents.PromptChoice(nil), choices...)
	s.lastDefaultChoice = defaultChoice
	return s.response, nil
}

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

func TestSessionApprovalKey_StripsWrappersAndKeepsPrimaryCommandFamily(t *testing.T) {
	key := sessionApprovalKey(`cd /tmp && go test ./kernel/... -count=1 2>&1 | grep PASS`)
	if key != "go test" {
		t.Fatalf("expected primary approval key %q, got %q", "go test", key)
	}
}

func TestSessionApprovalKey_KeepsExactTextForNonWhitelistedCommandFamily(t *testing.T) {
	key := sessionApprovalKey("grep hi a.txt && rm -f a.txt")
	if key != "" {
		t.Fatalf("expected empty approval key for non-whitelisted family, got %q", key)
	}
}

func TestTerminalApprover_ChoicePromptMakesAlwaysScopeExplicit(t *testing.T) {
	editor := &stubChoiceEditor{response: "a"}
	approver := newTerminalApprover(editor, io.Discard, nil)

	allowed, err := approver.Approve(context.Background(), toolexec.ApprovalRequest{
		ToolName: "BASH",
		Action:   "execute_command",
		Command:  `cd /tmp && go test ./kernel/... -count=1 | grep PASS`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected approval to succeed")
	}
	if editor.lastPrompt != "Approve command?" {
		t.Fatalf("unexpected choice prompt text %q", editor.lastPrompt)
	}
	if editor.lastDefaultChoice != "y" {
		t.Fatalf("expected allow to be default, got %q", editor.lastDefaultChoice)
	}
	if len(editor.lastChoices) != 3 {
		t.Fatalf("expected 3 explicit approval choices, got %d", len(editor.lastChoices))
	}
	if !strings.Contains(editor.lastChoices[1].Detail, "go test") {
		t.Fatalf("expected always choice detail to mention scoped key, got %+v", editor.lastChoices[1])
	}
	if !strings.Contains(editor.lastChoices[0].Detail, "Run once") {
		t.Fatalf("expected allow choice to describe one-time approval, got %+v", editor.lastChoices[0])
	}
	if !strings.Contains(editor.lastChoices[2].Detail, "Esc") {
		t.Fatalf("expected deny choice to mention Esc, got %+v", editor.lastChoices[2])
	}
}

func TestTerminalApprover_ChoicePromptOmitsAlwaysForUnknownCommandFamily(t *testing.T) {
	editor := &stubChoiceEditor{response: "y"}
	approver := newTerminalApprover(editor, io.Discard, nil)

	allowed, err := approver.Approve(context.Background(), toolexec.ApprovalRequest{
		ToolName: "BASH",
		Action:   "execute_command",
		Command:  `cd /tmp && npm_config_cache=/tmp/npm-cache npx --yes --package @playwright/cli playwright-cli screenshot --help`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected approval to succeed")
	}
	if len(editor.lastChoices) != 2 {
		t.Fatalf("expected only allow/deny choices for unknown command family, got %d", len(editor.lastChoices))
	}
	for _, choice := range editor.lastChoices {
		if choice.Value == "a" || strings.EqualFold(choice.Label, "always") {
			t.Fatalf("did not expect always choice for unknown command family, got %+v", editor.lastChoices)
		}
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
