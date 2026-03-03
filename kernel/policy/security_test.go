package policy

import (
	"context"
	"errors"
	"testing"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
)

type stubToolAuthorizer struct {
	allow bool
	err   error

	calls int
	last  ToolAuthorizationRequest
}

func (a *stubToolAuthorizer) AuthorizeTool(ctx context.Context, req ToolAuthorizationRequest) (bool, error) {
	_ = ctx
	a.calls++
	a.last = req
	if a.err != nil {
		return false, a.err
	}
	return a.allow, nil
}

func TestSecurityBaseline_AllowsSafeReadOnlyTools(t *testing.T) {
	hook := DefaultSecurityBaseline()
	in := ToolInput{Call: model.ToolCall{Name: "READ"}}
	out, err := hook.BeforeTool(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Call.Name != "READ" {
		t.Fatalf("unexpected tool passthrough: %q", out.Call.Name)
	}
}

func TestSecurityBaseline_DefaultAllowsWriteTool(t *testing.T) {
	hook := DefaultSecurityBaseline()
	if _, err := hook.BeforeTool(context.Background(), ToolInput{Call: model.ToolCall{Name: "WRITE"}}); err != nil {
		t.Fatalf("expected default WRITE allow, got %v", err)
	}
}

func TestSecurityBaseline_RequiresAuthorizationWhenGuardedAndAuthorizerMissing(t *testing.T) {
	hook := NewSecurityBaseline(SecurityBaselineConfig{GuardedTools: []string{"WRITE"}})
	_, err := hook.BeforeTool(context.Background(), ToolInput{Call: model.ToolCall{Name: "WRITE"}})
	if err == nil {
		t.Fatal("expected approval required error")
	}
	var target *toolexec.ApprovalRequiredError
	if !errors.As(err, &target) {
		t.Fatalf("expected ApprovalRequiredError, got %v", err)
	}
}

func TestSecurityBaseline_UsesToolAuthorizerForGuardedTools(t *testing.T) {
	hook := NewSecurityBaseline(SecurityBaselineConfig{GuardedTools: []string{"PATCH"}})
	authorizer := &stubToolAuthorizer{allow: true}
	ctx := WithToolAuthorizer(context.Background(), authorizer)
	_, err := hook.BeforeTool(ctx, ToolInput{Call: model.ToolCall{Name: "PATCH"}})
	if err != nil {
		t.Fatal(err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("expected one authorization call, got %d", authorizer.calls)
	}
	if authorizer.last.ToolName != "PATCH" {
		t.Fatalf("unexpected tool name in authorization request: %q", authorizer.last.ToolName)
	}
}

func TestSecurityBaseline_DefaultMCPToolRequiresAuthorization(t *testing.T) {
	hook := DefaultSecurityBaseline()
	authorizer := &stubToolAuthorizer{allow: true}
	ctx := WithToolAuthorizer(context.Background(), authorizer)
	_, err := hook.BeforeTool(ctx, ToolInput{Call: model.ToolCall{Name: "mcp__filesystem__write_file"}})
	if err != nil {
		t.Fatal(err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("expected one authorization call by default, got %d", authorizer.calls)
	}
}

func TestSecurityBaseline_UnknownToolRequiresAuthorization(t *testing.T) {
	hook := DefaultSecurityBaseline()
	_, err := hook.BeforeTool(context.Background(), ToolInput{Call: model.ToolCall{Name: "RISKY_CUSTOM_TOOL"}})
	if err == nil {
		t.Fatal("expected approval required error for unknown tool")
	}
	var target *toolexec.ApprovalRequiredError
	if !errors.As(err, &target) {
		t.Fatalf("expected ApprovalRequiredError, got %v", err)
	}
}

func TestSecurityBaseline_BashBypassesToolAuthorization(t *testing.T) {
	hook := DefaultSecurityBaseline()
	_, err := hook.BeforeTool(context.Background(), ToolInput{Call: model.ToolCall{Name: "BASH"}})
	if err != nil {
		t.Fatalf("expected BASH to bypass tool-level authorization, got %v", err)
	}
}
