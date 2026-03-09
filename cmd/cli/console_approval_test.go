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
		Reason:   "require_escalated requested",
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

func TestTerminalApprover_FullAccessAutoApprovesBenignCommand(t *testing.T) {
	editor := &stubLineEditor{}
	approver := newTerminalApprover(editor, io.Discard, nil)
	approver.modeResolver = func() string { return "full_access" }

	allowed, err := approver.Approve(context.Background(), toolexec.ApprovalRequest{Command: "go test ./..."})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected full_access to auto-approve benign command")
	}
	if editor.reads != 0 {
		t.Fatalf("expected no prompt reads, got %d", editor.reads)
	}
}

func TestTerminalApprover_FullAccessBlocksDangerousCommand(t *testing.T) {
	editor := &stubLineEditor{}
	approver := newTerminalApprover(editor, io.Discard, nil)
	approver.modeResolver = func() string { return "full_access" }

	allowed, err := approver.Approve(context.Background(), toolexec.ApprovalRequest{Command: "rm -rf /tmp/x"})
	if allowed {
		t.Fatal("expected dangerous command to be denied")
	}
	if err == nil || !toolexec.IsApprovalAborted(err) {
		t.Fatalf("expected approval aborted error, got %v", err)
	}
	if editor.reads != 0 {
		t.Fatalf("expected no prompt reads, got %d", editor.reads)
	}
}

func TestTerminalApprover_FullAccessAutoAuthorizesTools(t *testing.T) {
	editor := &stubLineEditor{}
	approver := newTerminalApprover(editor, io.Discard, nil)
	approver.modeResolver = func() string { return "full_access" }

	allowed, err := approver.AuthorizeTool(context.Background(), kernelpolicy.ToolAuthorizationRequest{
		ToolName:   "WRITE",
		Permission: "write file",
		Path:       "/tmp/file.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected tool authorization bypass in full_access")
	}
	if editor.reads != 0 {
		t.Fatalf("expected no prompt reads, got %d", editor.reads)
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
	var out strings.Builder
	approver := newTerminalApprover(editor, &out, newUI(&out, true, false))

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
	if editor.lastPrompt != "Would you like to run the following command?" {
		t.Fatalf("unexpected choice prompt text %q", editor.lastPrompt)
	}
	if editor.lastDefaultChoice != "y" {
		t.Fatalf("expected allow to be default, got %q", editor.lastDefaultChoice)
	}
	if len(editor.lastChoices) != 3 {
		t.Fatalf("expected 3 explicit approval choices, got %d", len(editor.lastChoices))
	}
	if editor.lastChoices[0].Label != "proceed" || editor.lastChoices[0].Detail != "just this once" {
		t.Fatalf("unexpected one-time approval choice %+v", editor.lastChoices[0])
	}
	if editor.lastChoices[1].Label != "session" || !strings.Contains(editor.lastChoices[1].Detail, "don't ask again for: go test") {
		t.Fatalf("expected always choice detail to mention scoped key, got %+v", editor.lastChoices[1])
	}
	if editor.lastChoices[2].Label != "cancel" || editor.lastChoices[2].Detail != "continue without it" {
		t.Fatalf("unexpected cancel choice %+v", editor.lastChoices[2])
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Would you like to run the following command?") {
		t.Fatalf("expected command approval title, got %q", rendered)
	}
	if !strings.Contains(rendered, "Permission: execute command") {
		t.Fatalf("expected permission line, got %q", rendered)
	}
	if !strings.Contains(rendered, "Command: $ cd /tmp && go test ./kernel/... -count=1 | grep PASS") {
		t.Fatalf("expected command preview, got %q", rendered)
	}
	if !strings.Contains(rendered, "You approved this session for commands matching go test.") {
		t.Fatalf("expected approval transcript, got %q", rendered)
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
		if choice.Value == "a" || strings.EqualFold(choice.Label, "session") {
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

func TestTerminalApprover_AuthorizeToolAlwaysCachesByPathScope(t *testing.T) {
	editor := &stubLineEditor{lines: []string{"a"}}
	approver := newTerminalApprover(editor, io.Discard, nil)
	req := kernelpolicy.ToolAuthorizationRequest{
		ToolName: "WRITE",
		Reason:   "write target is outside workspace writable roots",
		Path:     "/tmp/external/file.txt",
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

	allowed, err = approver.AuthorizeTool(context.Background(), kernelpolicy.ToolAuthorizationRequest{
		ToolName: "PATCH",
		Reason:   "write target is outside workspace writable roots",
		Path:     "/tmp/external/another.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected session-whitelisted scope to pass")
	}
	if editor.reads != 1 {
		t.Fatalf("expected second authorization to skip prompt, reads=%d", editor.reads)
	}
}

func TestTerminalApprover_AuthorizeToolPromptShowsPathPreviewAndScopedAlways(t *testing.T) {
	editor := &stubChoiceEditor{response: "a"}
	var out strings.Builder
	approver := newTerminalApprover(editor, &out, newUI(&out, true, false))
	req := kernelpolicy.ToolAuthorizationRequest{
		ToolName: "PATCH",
		Reason:   "write target is outside workspace writable roots",
		Path:     "/tmp/external/docs/SKILL.md",
		Preview:  "--- old\n+++ new\n-old\n+new",
	}

	allowed, err := approver.AuthorizeTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected approval to pass")
	}
	if editor.lastPrompt != "Would you like to make the following edits?" {
		t.Fatalf("unexpected prompt %q", editor.lastPrompt)
	}
	if len(editor.lastChoices) != 3 {
		t.Fatalf("expected 3 choices, got %d", len(editor.lastChoices))
	}
	if got := editor.lastChoices[0]; got.Label != "proceed" || got.Detail != "just this once" {
		t.Fatalf("unexpected proceed choice %+v", got)
	}
	if got := editor.lastChoices[1].Detail; got != "don't ask again for: /tmp/external/docs" {
		t.Fatalf("unexpected always detail %q", got)
	}
	if got := editor.lastChoices[2]; got.Label != "cancel" || got.Detail != "don't allow" {
		t.Fatalf("unexpected cancel choice %+v", got)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Would you like to make the following edits?") {
		t.Fatalf("expected edit approval title, got %q", rendered)
	}
	if !strings.Contains(rendered, "Permission: write outside workspace writable roots") {
		t.Fatalf("expected permission line, got %q", rendered)
	}
	if !strings.Contains(rendered, "Path: /tmp/external/docs/SKILL.md") {
		t.Fatalf("expected path in approval output, got %q", rendered)
	}
	if !strings.Contains(rendered, "Reason: write target is outside workspace writable roots") {
		t.Fatalf("expected reason in approval output, got %q", rendered)
	}
	if !strings.Contains(rendered, "You approved this session for edits under \"/tmp/external/docs\".") {
		t.Fatalf("expected approval transcript, got %q", rendered)
	}
	if strings.Contains(rendered, "diff:") || strings.Contains(rendered, "--- old") || strings.Contains(rendered, "+new") {
		t.Fatalf("did not expect legacy diff fallback in approval output, got %q", rendered)
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

func TestTerminalApprover_AuthorizeToolPromptShowsExternalMCPContext(t *testing.T) {
	editor := &stubChoiceEditor{response: "a"}
	var out strings.Builder
	approver := newTerminalApprover(editor, &out, newUI(&out, true, false))
	req := kernelpolicy.ToolAuthorizationRequest{
		ToolName:   "web__search",
		Permission: "external MCP tool call",
		Reason:     "external MCP tool",
		Target:     "https://example.com/search?q=caelis",
		ScopeKey:   "example.com",
		Preview:    "url=https://example.com/search?q=caelis, query=caelis",
	}

	allowed, err := approver.AuthorizeTool(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Fatal("expected approval to pass")
	}
	if editor.lastPrompt != "Would you like to call the following external tool?" {
		t.Fatalf("unexpected prompt %q", editor.lastPrompt)
	}
	if got := editor.lastChoices[1].Detail; got != "don't ask again for: example.com" {
		t.Fatalf("unexpected always detail %q", got)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Permission: external MCP tool call") {
		t.Fatalf("expected external tool permission, got %q", rendered)
	}
	if !strings.Contains(rendered, "Tool: web__search") {
		t.Fatalf("expected tool name, got %q", rendered)
	}
	if !strings.Contains(rendered, "Target: https://example.com/search?q=caelis") {
		t.Fatalf("expected target, got %q", rendered)
	}
	if !strings.Contains(rendered, "Request: url=https://example.com/search?q=caelis, query=caelis") {
		t.Fatalf("expected request preview, got %q", rendered)
	}
	if !strings.Contains(rendered, "You approved this session for tool requests under \"example.com\".") {
		t.Fatalf("expected session approval transcript, got %q", rendered)
	}
}
