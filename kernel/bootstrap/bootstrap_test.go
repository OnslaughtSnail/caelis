package bootstrap

import (
	"context"
	"fmt"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	pluginbuiltin "github.com/OnslaughtSnail/caelis/kernel/plugin/builtin"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

func TestAssemble_LocalToolsAndPolicy(t *testing.T) {
	got, err := Assemble(context.Background(), AssembleSpec{
		Registry:        mustBuiltinRegistry(t),
		ToolProviders:   []string{pluginbuiltin.ProviderLocalTools},
		PolicyProviders: []string{pluginbuiltin.ProviderDefaultPolicy},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil resolved spec")
	}
	foundEcho := false
	for _, one := range got.Tools {
		if one.Name() == "echo" {
			foundEcho = true
			break
		}
	}
	if !foundEcho {
		t.Fatalf("expected %q tool in assembled result", "echo")
	}
	if len(got.Policies) != 2 {
		t.Fatalf("expected 2 policy hooks, got %d", len(got.Policies))
	}
}

func TestAssemble_NilRegistryFallsBackToBuiltinProviders(t *testing.T) {
	got, err := Assemble(context.Background(), AssembleSpec{
		ToolProviders:   []string{pluginbuiltin.ProviderLocalTools},
		PolicyProviders: []string{pluginbuiltin.ProviderDefaultPolicy},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil resolved spec")
	}
	foundEcho := false
	for _, one := range got.Tools {
		if one.Name() == "echo" {
			foundEcho = true
			break
		}
	}
	if !foundEcho {
		t.Fatalf("expected %q tool in assembled result", "echo")
	}
	if len(got.Policies) != 2 {
		t.Fatalf("expected 2 policy hooks, got %d", len(got.Policies))
	}
}

func TestAssemble_CoreReadInjectedByRuntime(t *testing.T) {
	got, err := Assemble(context.Background(), AssembleSpec{
		Registry:      mustBuiltinRegistry(t),
		ToolProviders: []string{pluginbuiltin.ProviderLocalTools},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, one := range got.Tools {
		if one.Name() == tool.ReadToolName {
			t.Fatalf("READ should be injected by runtime, not plugin assembly")
		}
	}
}

func mustBuiltinRegistry(t *testing.T) *plugin.Registry {
	t.Helper()
	reg := plugin.NewRegistry()
	if err := pluginbuiltin.RegisterAll(reg, pluginbuiltin.RegisterOptions{}); err != nil {
		t.Fatalf("register builtin providers: %v", err)
	}
	return reg
}

func TestAssemble_ProviderLifecycleStartAndStop(t *testing.T) {
	reg := plugin.NewRegistry()
	lp := &lifecycleProvider{name: "lp"}
	if err := reg.RegisterToolProvider(lp); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterPolicyProvider(lp); err != nil {
		t.Fatal(err)
	}

	resolved, err := Assemble(context.Background(), AssembleSpec{
		Registry:        reg,
		ToolProviders:   []string{"lp"},
		PolicyProviders: []string{"lp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lp.initCalls != 2 {
		t.Fatalf("expected 2 init calls (tool+policy), got %d", lp.initCalls)
	}
	if lp.startCalls != 2 {
		t.Fatalf("expected 2 start calls (tool+policy), got %d", lp.startCalls)
	}
	if err := resolved.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lp.stopCalls != 2 {
		t.Fatalf("expected 2 stop calls (tool+policy), got %d", lp.stopCalls)
	}
}

func TestAssemble_ProviderLifecycleStartFailureStopsStarted(t *testing.T) {
	reg := plugin.NewRegistry()
	ok := &lifecycleProvider{name: "ok"}
	bad := &lifecycleProvider{name: "bad", failStart: true}
	if err := reg.RegisterToolProvider(ok); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterToolProvider(bad); err != nil {
		t.Fatal(err)
	}

	_, err := Assemble(context.Background(), AssembleSpec{
		Registry:      reg,
		ToolProviders: []string{"ok", "bad"},
	})
	if err == nil {
		t.Fatal("expected lifecycle start failure")
	}
	if ok.stopCalls == 0 {
		t.Fatalf("expected started provider to be stopped on failure")
	}
}

type lifecycleProvider struct {
	name       string
	failStart  bool
	initCalls  int
	startCalls int
	stopCalls  int
}

func (p *lifecycleProvider) Name() string { return p.name }

func (p *lifecycleProvider) Init(ctx context.Context) error {
	_ = ctx
	p.initCalls++
	return nil
}

func (p *lifecycleProvider) Start(ctx context.Context) error {
	_ = ctx
	p.startCalls++
	if p.failStart {
		return fmt.Errorf("start failed")
	}
	return nil
}

func (p *lifecycleProvider) Stop(ctx context.Context) error {
	_ = ctx
	p.stopCalls++
	return nil
}

func (p *lifecycleProvider) Tools(ctx context.Context) ([]tool.Tool, error) {
	_ = ctx
	return nil, nil
}

func (p *lifecycleProvider) Policies(ctx context.Context) ([]policy.Hook, error) {
	_ = ctx
	return nil, nil
}
