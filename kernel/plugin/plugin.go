package plugin

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

// ToolProvider provides tools by provider name.
type ToolProvider interface {
	Name() string
	Tools(context.Context) ([]tool.Tool, error)
}

// PolicyProvider provides policy hooks by provider name.
type PolicyProvider interface {
	Name() string
	Policies(context.Context) ([]policy.Hook, error)
}

// Registry is a compile-time registration container.
type Registry struct {
	mu sync.RWMutex

	toolProviders   map[string]ToolProvider
	policyProviders map[string]PolicyProvider
}

func NewRegistry() *Registry {
	return &Registry{
		toolProviders:   map[string]ToolProvider{},
		policyProviders: map[string]PolicyProvider{},
	}
}

func (r *Registry) RegisterToolProvider(p ToolProvider) error {
	if p == nil || p.Name() == "" {
		return fmt.Errorf("plugin: invalid tool provider")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.toolProviders[p.Name()]; exists {
		return fmt.Errorf("plugin: duplicate tool provider %q", p.Name())
	}
	r.toolProviders[p.Name()] = p
	return nil
}

func (r *Registry) RegisterPolicyProvider(p PolicyProvider) error {
	if p == nil || p.Name() == "" {
		return fmt.Errorf("plugin: invalid policy provider")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.policyProviders[p.Name()]; exists {
		return fmt.Errorf("plugin: duplicate policy provider %q", p.Name())
	}
	r.policyProviders[p.Name()] = p
	return nil
}

func (r *Registry) ResolveTools(ctx context.Context, names []string) ([]tool.Tool, error) {
	r.mu.RLock()
	providers := make([]ToolProvider, 0, len(names))
	for _, name := range names {
		p, ok := r.toolProviders[name]
		if !ok {
			r.mu.RUnlock()
			return nil, fmt.Errorf("plugin: unknown tool provider %q", name)
		}
		providers = append(providers, p)
	}
	r.mu.RUnlock()

	var out []tool.Tool
	for _, p := range providers {
		tools, err := p.Tools(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, tools...)
	}
	return out, nil
}

func (r *Registry) ResolvePolicies(ctx context.Context, names []string) ([]policy.Hook, error) {
	r.mu.RLock()
	providers := make([]PolicyProvider, 0, len(names))
	for _, name := range names {
		p, ok := r.policyProviders[name]
		if !ok {
			r.mu.RUnlock()
			return nil, fmt.Errorf("plugin: unknown policy provider %q", name)
		}
		providers = append(providers, p)
	}
	r.mu.RUnlock()

	var out []policy.Hook
	for _, p := range providers {
		hooks, err := p.Policies(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, hooks...)
	}
	return out, nil
}

func (r *Registry) ListToolProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.toolProviders))
	for name := range r.toolProviders {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) ListPolicyProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.policyProviders))
	for name := range r.policyProviders {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
