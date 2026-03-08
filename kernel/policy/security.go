package policy

import (
	"context"
	"fmt"
	"strings"

	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
)

type toolAuthorizerContextKey struct{}

// ToolAuthorizationRequest describes one tool-level authorization request.
type ToolAuthorizationRequest struct {
	ToolName string
	Reason   string
	Path     string
	ScopeKey string
	Preview  string
}

// ToolAuthorizer handles tool-level authorization decisions.
type ToolAuthorizer interface {
	AuthorizeTool(context.Context, ToolAuthorizationRequest) (bool, error)
}

// WithToolAuthorizer injects one ToolAuthorizer into context.
func WithToolAuthorizer(ctx context.Context, authorizer ToolAuthorizer) context.Context {
	if ctx == nil || authorizer == nil {
		return ctx
	}
	return context.WithValue(ctx, toolAuthorizerContextKey{}, authorizer)
}

// ToolAuthorizerFromContext returns ToolAuthorizer from context.
func ToolAuthorizerFromContext(ctx context.Context) (ToolAuthorizer, bool) {
	if ctx == nil {
		return nil, false
	}
	authorizer, ok := ctx.Value(toolAuthorizerContextKey{}).(ToolAuthorizer)
	return authorizer, ok
}

// SecurityBaselineConfig configures tool-authorization baseline behavior.
type SecurityBaselineConfig struct {
	// AutoAllowTools bypass authorization prompts.
	AutoAllowTools []string
	// GuardedTools require authorization before execution.
	GuardedTools []string
}

type securityBaselineHook struct {
	autoAllow map[string]struct{}
	guarded   map[string]struct{}
}

// DefaultSecurityBaseline returns the default kernel security policy.
func DefaultSecurityBaseline() Hook {
	return NewSecurityBaseline(SecurityBaselineConfig{})
}

// NewSecurityBaseline returns one security baseline policy hook.
func NewSecurityBaseline(cfg SecurityBaselineConfig) Hook {
	autoAllow := map[string]struct{}{}
	guarded := map[string]struct{}{}

	defaultAutoAllow := []string{
		"READ", "LIST", "GLOB", "STAT", "SEARCH",
		"WRITE", "PATCH",
		"ECHO", "NOW",
		"BASH", // BASH host escalation is gated by execution runtime approval flow.
	}
	for _, one := range append(defaultAutoAllow, cfg.AutoAllowTools...) {
		name := normalizeToolName(one)
		if name != "" {
			autoAllow[name] = struct{}{}
		}
	}
	for _, one := range cfg.GuardedTools {
		name := normalizeToolName(one)
		if name != "" {
			guarded[name] = struct{}{}
		}
	}

	return securityBaselineHook{
		autoAllow: autoAllow,
		guarded:   guarded,
	}
}

func (h securityBaselineHook) Name() string {
	return "default_security"
}

func (h securityBaselineHook) BeforeModel(ctx context.Context, in ModelInput) (ModelInput, error) {
	_ = ctx
	return in, nil
}

func (h securityBaselineHook) BeforeTool(ctx context.Context, in ToolInput) (ToolInput, error) {
	needApproval, reason := h.requiresToolAuthorization(in.Call.Name)
	if !needApproval {
		return in, nil
	}

	authorizer, ok := ToolAuthorizerFromContext(ctx)
	if !ok {
		return ToolInput{}, &toolexec.ApprovalRequiredError{
			Reason: fmt.Sprintf("tool %q requires authorization: %s", strings.TrimSpace(in.Call.Name), reason),
		}
	}

	allowed, err := authorizer.AuthorizeTool(ctx, ToolAuthorizationRequest{
		ToolName: in.Call.Name,
		Reason:   reason,
	})
	if err != nil {
		return ToolInput{}, err
	}
	if !allowed {
		return ToolInput{}, &toolexec.ApprovalAbortedError{
			Reason: fmt.Sprintf("tool %q authorization denied", strings.TrimSpace(in.Call.Name)),
		}
	}
	return in, nil
}

func (h securityBaselineHook) AfterTool(ctx context.Context, out ToolOutput) (ToolOutput, error) {
	_ = ctx
	return out, nil
}

func (h securityBaselineHook) BeforeOutput(ctx context.Context, out Output) (Output, error) {
	_ = ctx
	return out, nil
}

func (h securityBaselineHook) requiresToolAuthorization(toolName string) (bool, string) {
	name := normalizeToolName(toolName)
	if name == "" {
		return false, ""
	}
	if _, ok := h.guarded[name]; ok {
		return true, "tool requires explicit authorization"
	}
	if _, ok := h.autoAllow[name]; ok {
		return false, ""
	}
	if strings.HasPrefix(name, "LSP_") {
		return false, ""
	}
	if strings.HasPrefix(name, "MCP__") {
		return true, "external MCP tool"
	}
	return true, "unknown tool type"
}

func normalizeToolName(name string) string {
	return strings.ToUpper(strings.TrimSpace(name))
}
