package policy

import (
	"context"
	"strings"
)

// DecisionEffect describes policy decision outcome.
type DecisionEffect string

const (
	DecisionEffectAllow           DecisionEffect = "allow"
	DecisionEffectDeny            DecisionEffect = "deny"
	DecisionEffectRequireApproval DecisionEffect = "require_approval"
)

const (
	// DecisionMetaExecutionRoute can carry execution route hint for tools.
	DecisionMetaExecutionRoute = "execution_route"
	// DecisionMetaFallbackOnCommandNotFound toggles sandbox command-missing fallback.
	DecisionMetaFallbackOnCommandNotFound = "fallback_on_command_not_found"
	// DecisionRouteSandbox indicates sandbox execution.
	DecisionRouteSandbox = "sandbox"
	// DecisionRouteHost indicates host execution.
	DecisionRouteHost = "host"
)

// Decision is the mutable policy decision payload propagated across hooks.
type Decision struct {
	Effect   DecisionEffect
	Reason   string
	Metadata map[string]any
}

type decisionContextKey struct{}

// NormalizeDecision normalizes one decision and defaults to allow.
func NormalizeDecision(decision Decision) Decision {
	effect := DecisionEffect(strings.TrimSpace(strings.ToLower(string(decision.Effect))))
	switch effect {
	case DecisionEffectAllow, DecisionEffectDeny, DecisionEffectRequireApproval:
		decision.Effect = effect
	default:
		decision.Effect = DecisionEffectAllow
	}
	decision.Reason = strings.TrimSpace(decision.Reason)
	return decision
}

// DecisionWithRoute sets execution route hint on one decision.
func DecisionWithRoute(decision Decision, route string) Decision {
	decision = NormalizeDecision(decision)
	route = strings.TrimSpace(strings.ToLower(route))
	if route == "" {
		return decision
	}
	if decision.Metadata == nil {
		decision.Metadata = map[string]any{}
	}
	decision.Metadata[DecisionMetaExecutionRoute] = route
	return decision
}

// DecisionRouteFromMetadata extracts execution route hint from one decision.
func DecisionRouteFromMetadata(decision Decision) (string, bool) {
	if decision.Metadata == nil {
		return "", false
	}
	raw, ok := decision.Metadata[DecisionMetaExecutionRoute]
	if !ok {
		return "", false
	}
	value, ok := raw.(string)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(strings.ToLower(strings.TrimSpace(value)))
	if value == "" {
		return "", false
	}
	return value, true
}

// WithToolDecision attaches one policy decision for downstream tool execution.
func WithToolDecision(ctx context.Context, decision Decision) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, decisionContextKey{}, NormalizeDecision(decision))
}

// ToolDecisionFromContext returns one policy decision from tool context.
func ToolDecisionFromContext(ctx context.Context) (Decision, bool) {
	if ctx == nil {
		return Decision{}, false
	}
	decision, ok := ctx.Value(decisionContextKey{}).(Decision)
	if !ok {
		return Decision{}, false
	}
	return NormalizeDecision(decision), true
}
