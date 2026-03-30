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

// DecisionAnnotation carries typed routing and compatibility semantics.
type DecisionAnnotation string

const (
	DecisionAnnotationExecutionRouteSandbox         DecisionAnnotation = "execution_route_sandbox"
	DecisionAnnotationExecutionRouteHost            DecisionAnnotation = "execution_route_host"
	DecisionAnnotationFallbackOnCommandNotFound     DecisionAnnotation = "fallback_on_command_not_found"
	DecisionAnnotationHostExecutionWithoutApproval  DecisionAnnotation = "host_execution_without_approval"
	DecisionAnnotationHostExecutionRequiresApproval DecisionAnnotation = "host_execution_requires_approval"
)

// Decision is the mutable policy decision payload propagated across hooks.
type Decision struct {
	Effect      DecisionEffect
	Reason      string
	Metadata    map[string]any
	Annotations []DecisionAnnotation
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
	decision.Annotations = normalizeDecisionAnnotations(decision.Annotations)
	if route, ok := decisionRouteFromMetadataOnly(decision); ok {
		switch route {
		case DecisionRouteSandbox:
			decision.Annotations = appendMissingDecisionAnnotation(decision.Annotations, DecisionAnnotationExecutionRouteSandbox)
		case DecisionRouteHost:
			decision.Annotations = appendMissingDecisionAnnotation(decision.Annotations, DecisionAnnotationExecutionRouteHost)
		}
	}
	if decision.Metadata != nil {
		if raw, ok := decision.Metadata[DecisionMetaFallbackOnCommandNotFound].(bool); ok && raw {
			decision.Annotations = appendMissingDecisionAnnotation(decision.Annotations, DecisionAnnotationFallbackOnCommandNotFound)
		}
	}
	return syncDecisionMetadata(decision)
}

// DecisionWithRoute sets execution route hint on one decision.
func DecisionWithRoute(decision Decision, route string) Decision {
	decision = NormalizeDecision(decision)
	route = strings.TrimSpace(strings.ToLower(route))
	switch route {
	case "":
		return decision
	case DecisionRouteSandbox:
		return DecisionWithAnnotation(decision, DecisionAnnotationExecutionRouteSandbox)
	case DecisionRouteHost:
		return DecisionWithAnnotation(decision, DecisionAnnotationExecutionRouteHost)
	default:
		if decision.Metadata == nil {
			decision.Metadata = map[string]any{}
		}
		decision.Metadata[DecisionMetaExecutionRoute] = route
		return decision
	}
}

// DecisionWithAnnotation adds one normalized annotation to the decision.
func DecisionWithAnnotation(decision Decision, annotation DecisionAnnotation) Decision {
	decision = NormalizeDecision(decision)
	annotation = normalizeDecisionAnnotation(annotation)
	if annotation == "" {
		return decision
	}
	decision.Annotations = appendMissingDecisionAnnotation(decision.Annotations, annotation)
	return syncDecisionMetadata(decision)
}

// DecisionHasAnnotation reports whether the decision contains the annotation.
func DecisionHasAnnotation(decision Decision, annotation DecisionAnnotation) bool {
	annotation = normalizeDecisionAnnotation(annotation)
	if annotation == "" {
		return false
	}
	decision = NormalizeDecision(decision)
	for _, existing := range decision.Annotations {
		if existing == annotation {
			return true
		}
	}
	return false
}

// DecisionRouteFromMetadata extracts execution route hint from one decision.
func DecisionRouteFromMetadata(decision Decision) (string, bool) {
	decision = NormalizeDecision(decision)
	if DecisionHasAnnotation(decision, DecisionAnnotationExecutionRouteSandbox) {
		return DecisionRouteSandbox, true
	}
	if DecisionHasAnnotation(decision, DecisionAnnotationExecutionRouteHost) {
		return DecisionRouteHost, true
	}
	return decisionRouteFromMetadataOnly(decision)
}

func decisionRouteFromMetadataOnly(decision Decision) (string, bool) {
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

func normalizeDecisionAnnotation(annotation DecisionAnnotation) DecisionAnnotation {
	switch DecisionAnnotation(strings.TrimSpace(strings.ToLower(string(annotation)))) {
	case DecisionAnnotationExecutionRouteSandbox,
		DecisionAnnotationExecutionRouteHost,
		DecisionAnnotationFallbackOnCommandNotFound,
		DecisionAnnotationHostExecutionWithoutApproval,
		DecisionAnnotationHostExecutionRequiresApproval:
		return DecisionAnnotation(strings.TrimSpace(strings.ToLower(string(annotation))))
	default:
		return ""
	}
}

func normalizeDecisionAnnotations(annotations []DecisionAnnotation) []DecisionAnnotation {
	if len(annotations) == 0 {
		return nil
	}
	out := make([]DecisionAnnotation, 0, len(annotations))
	for _, annotation := range annotations {
		out = appendMissingDecisionAnnotation(out, annotation)
	}
	return out
}

func appendMissingDecisionAnnotation(annotations []DecisionAnnotation, annotation DecisionAnnotation) []DecisionAnnotation {
	annotation = normalizeDecisionAnnotation(annotation)
	if annotation == "" {
		return annotations
	}
	for _, existing := range annotations {
		if existing == annotation {
			return annotations
		}
	}
	return append(annotations, annotation)
}

func syncDecisionMetadata(decision Decision) Decision {
	if len(decision.Annotations) == 0 && len(decision.Metadata) == 0 {
		return decision
	}
	if decision.Metadata == nil {
		decision.Metadata = map[string]any{}
	}
	switch {
	case hasDecisionAnnotation(decision.Annotations, DecisionAnnotationExecutionRouteSandbox):
		decision.Metadata[DecisionMetaExecutionRoute] = DecisionRouteSandbox
	case hasDecisionAnnotation(decision.Annotations, DecisionAnnotationExecutionRouteHost):
		decision.Metadata[DecisionMetaExecutionRoute] = DecisionRouteHost
	}
	if hasDecisionAnnotation(decision.Annotations, DecisionAnnotationFallbackOnCommandNotFound) {
		decision.Metadata[DecisionMetaFallbackOnCommandNotFound] = true
	}
	return decision
}

func hasDecisionAnnotation(annotations []DecisionAnnotation, target DecisionAnnotation) bool {
	for _, annotation := range annotations {
		if annotation == target {
			return true
		}
	}
	return false
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
