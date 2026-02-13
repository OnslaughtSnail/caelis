package policy

import (
	"context"
	"testing"
)

func TestNormalizeDecision_DefaultAllow(t *testing.T) {
	normalized := NormalizeDecision(Decision{})
	if normalized.Effect != DecisionEffectAllow {
		t.Fatalf("expected default effect allow, got %q", normalized.Effect)
	}
}

func TestDecisionWithRoute_RoundTrip(t *testing.T) {
	decision := DecisionWithRoute(Decision{Effect: DecisionEffectRequireApproval}, DecisionRouteHost)
	if decision.Effect != DecisionEffectRequireApproval {
		t.Fatalf("unexpected effect %q", decision.Effect)
	}
	route, ok := DecisionRouteFromMetadata(decision)
	if !ok {
		t.Fatal("expected route in decision metadata")
	}
	if route != DecisionRouteHost {
		t.Fatalf("expected route host, got %q", route)
	}
}

func TestWithToolDecision_RoundTrip(t *testing.T) {
	ctx := WithToolDecision(context.Background(), Decision{
		Effect: DecisionEffectRequireApproval,
		Reason: "policy requires approval",
	})
	decision, ok := ToolDecisionFromContext(ctx)
	if !ok {
		t.Fatal("expected decision in context")
	}
	if decision.Effect != DecisionEffectRequireApproval {
		t.Fatalf("unexpected effect %q", decision.Effect)
	}
	if decision.Reason != "policy requires approval" {
		t.Fatalf("unexpected reason %q", decision.Reason)
	}
}
