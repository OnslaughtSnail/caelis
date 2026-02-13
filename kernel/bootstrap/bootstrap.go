package bootstrap

import (
	"context"
	"fmt"

	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

// AssembleSpec describes plugin-level assembly settings.
type AssembleSpec struct {
	Registry        *plugin.Registry
	ToolProviders   []string
	PolicyProviders []string
}

// ResolvedSpec is the assembled runtime capability set.
type ResolvedSpec struct {
	Tools    []tool.Tool
	Policies []policy.Hook
	closeFn  func(context.Context) error
}

func (r *ResolvedSpec) Close(ctx context.Context) error {
	if r == nil || r.closeFn == nil {
		return nil
	}
	return r.closeFn(ctx)
}

// Assemble resolves runtime capabilities from plugin providers.
func Assemble(ctx context.Context, spec AssembleSpec) (*ResolvedSpec, error) {
	preg := spec.Registry
	if preg == nil {
		preg = plugin.NewRegistry()
	}
	toolProviders, err := preg.ToolProviders(spec.ToolProviders)
	if err != nil {
		return nil, err
	}
	policyProviders, err := preg.PolicyProviders(spec.PolicyProviders)
	if err != nil {
		return nil, err
	}

	lifecycleProviders := collectLifecycleProviders(toolProviders, policyProviders)
	stops, err := startProviders(ctx, lifecycleProviders)
	if err != nil {
		return nil, err
	}
	tools, err := preg.ResolveTools(ctx, spec.ToolProviders)
	if err != nil {
		_ = stopProviders(ctx, stops)
		return nil, err
	}
	policies, err := preg.ResolvePolicies(ctx, spec.PolicyProviders)
	if err != nil {
		_ = stopProviders(ctx, stops)
		return nil, err
	}

	resolved := &ResolvedSpec{
		Tools:    tools,
		Policies: policies,
		closeFn: func(closeCtx context.Context) error {
			return stopProviders(closeCtx, stops)
		},
	}
	return resolved, nil
}

func collectLifecycleProviders(toolProviders []plugin.ToolProvider, policyProviders []plugin.PolicyProvider) []namedProvider {
	seen := map[string]struct{}{}
	out := make([]namedProvider, 0, len(toolProviders)+len(policyProviders))
	for _, one := range toolProviders {
		if one == nil {
			continue
		}
		name := "tool:" + one.Name()
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, namedProvider{Name: name, Value: one})
	}
	for _, one := range policyProviders {
		if one == nil {
			continue
		}
		name := "policy:" + one.Name()
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, namedProvider{Name: name, Value: one})
	}
	return out
}

type namedProvider struct {
	Name  string
	Value any
}

func startProviders(ctx context.Context, providers []namedProvider) ([]namedProvider, error) {
	started := make([]namedProvider, 0, len(providers))
	for _, one := range providers {
		if initializer, ok := one.Value.(plugin.ProviderInitializer); ok {
			if err := initializer.Init(ctx); err != nil {
				_ = stopProviders(ctx, started)
				return nil, fmt.Errorf("bootstrap: init %s: %w", one.Name, err)
			}
		}
		if starter, ok := one.Value.(plugin.ProviderStarter); ok {
			if err := starter.Start(ctx); err != nil {
				_ = stopProviders(ctx, started)
				return nil, fmt.Errorf("bootstrap: start %s: %w", one.Name, err)
			}
		}
		started = append(started, one)
	}
	return started, nil
}

func stopProviders(ctx context.Context, providers []namedProvider) error {
	var firstErr error
	for i := len(providers) - 1; i >= 0; i-- {
		one := providers[i]
		stopper, ok := one.Value.(plugin.ProviderStopper)
		if !ok {
			continue
		}
		if err := stopper.Stop(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("bootstrap: stop %s: %w", one.Name, err)
		}
	}
	return firstErr
}
