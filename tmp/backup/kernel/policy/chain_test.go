package policy

import (
	"context"
	"testing"
)

func TestChainApply_DefaultAllow(t *testing.T) {
	hooks := []Hook{DefaultAllow()}
	in, err := ApplyBeforeModel(context.Background(), hooks, ModelInput{})
	if err != nil {
		t.Fatal(err)
	}
	if in.Messages != nil {
		t.Fatalf("expected nil messages, got %#v", in.Messages)
	}
}

// denyHook is a test hook that sets deny on every tool call.
type denyHook struct{ NoopHook }

func (h denyHook) BeforeTool(_ context.Context, in ToolInput) (ToolInput, error) {
	in.Decision = Decision{Effect: DecisionEffectDeny, Reason: "denied by test"}
	return in, nil
}

// allowHook is a test hook that tries to set allow on every tool call.
type allowHook struct{ NoopHook }

func (h allowHook) BeforeTool(_ context.Context, in ToolInput) (ToolInput, error) {
	in.Decision = Decision{Effect: DecisionEffectAllow, Reason: "allowed by test"}
	return in, nil
}

// approvalHook is a test hook that sets require_approval.
type approvalHook struct{ NoopHook }

func (h approvalHook) BeforeTool(_ context.Context, in ToolInput) (ToolInput, error) {
	in.Decision = Decision{Effect: DecisionEffectRequireApproval, Reason: "needs approval"}
	return in, nil
}

func TestApplyBeforeTool_DenyCannotBeRelaxedToAllow(t *testing.T) {
	hooks := []Hook{
		denyHook{NoopHook{HookName: "denier"}},
		allowHook{NoopHook{HookName: "allower"}},
	}
	out, err := ApplyBeforeTool(context.Background(), hooks, ToolInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision.Effect != DecisionEffectDeny {
		t.Fatalf("expected deny to be preserved, got %q", out.Decision.Effect)
	}
}

func TestApplyBeforeTool_DenyCannotBeRelaxedToApproval(t *testing.T) {
	hooks := []Hook{
		denyHook{NoopHook{HookName: "denier"}},
		approvalHook{NoopHook{HookName: "approver"}},
	}
	out, err := ApplyBeforeTool(context.Background(), hooks, ToolInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision.Effect != DecisionEffectDeny {
		t.Fatalf("expected deny to be preserved, got %q", out.Decision.Effect)
	}
}

func TestApplyBeforeTool_ApprovalCannotBeRelaxedToAllow(t *testing.T) {
	hooks := []Hook{
		approvalHook{NoopHook{HookName: "approver"}},
		allowHook{NoopHook{HookName: "allower"}},
	}
	out, err := ApplyBeforeTool(context.Background(), hooks, ToolInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision.Effect != DecisionEffectRequireApproval {
		t.Fatalf("expected require_approval to be preserved, got %q", out.Decision.Effect)
	}
}

func TestApplyBeforeTool_ApprovalCanBeEscalatedToDeny(t *testing.T) {
	hooks := []Hook{
		approvalHook{NoopHook{HookName: "approver"}},
		denyHook{NoopHook{HookName: "denier"}},
	}
	out, err := ApplyBeforeTool(context.Background(), hooks, ToolInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision.Effect != DecisionEffectDeny {
		t.Fatalf("expected deny to win over require_approval, got %q", out.Decision.Effect)
	}
}
