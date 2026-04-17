package plugin

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

type tp struct{}

func (tp) Name() string { return "t1" }
func (tp) Tools(ctx context.Context) ([]tool.Tool, error) {
	_ = ctx
	return nil, nil
}

type pp struct{}

func (pp) Name() string { return "p1" }
func (pp) Policies(ctx context.Context) ([]policy.Hook, error) {
	_ = ctx
	return []policy.Hook{policy.DefaultAllow()}, nil
}

func TestRegistry_RegisterAndResolve(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterToolProvider(tp{}); err != nil {
		t.Fatalf("register tool provider: %v", err)
	}
	if err := r.RegisterPolicyProvider(pp{}); err != nil {
		t.Fatalf("register policy provider: %v", err)
	}
	tools, err := r.ResolveTools(context.Background(), []string{"t1"})
	if err != nil {
		t.Fatalf("resolve tools: %v", err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected no tools, got %d", len(tools))
	}
	hooks, err := r.ResolvePolicies(context.Background(), []string{"p1"})
	if err != nil {
		t.Fatalf("resolve policies: %v", err)
	}
	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hooks))
	}
}

func TestRegistry_Duplicate(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterToolProvider(tp{}); err != nil {
		t.Fatalf("register first: %v", err)
	}
	if err := r.RegisterToolProvider(tp{}); err == nil {
		t.Fatalf("expected duplicate registration error")
	}
}

type schemaToolProvider struct{}

func (schemaToolProvider) Name() string { return "schema_tool" }
func (schemaToolProvider) Tools(ctx context.Context) ([]tool.Tool, error) {
	_ = ctx
	return nil, nil
}
func (schemaToolProvider) ConfigSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"enabled": map[string]any{"type": "boolean"}}}
}

func TestRegistry_ProviderLookupAndSchema(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterToolProvider(schemaToolProvider{}); err != nil {
		t.Fatalf("register schema tool provider: %v", err)
	}
	providers, err := r.ToolProviders([]string{"schema_tool"})
	if err != nil {
		t.Fatalf("lookup tool providers: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	schemas := r.ToolProviderSchemas()
	if _, ok := schemas["schema_tool"]; !ok {
		t.Fatalf("expected schema entry for schema_tool")
	}
}
