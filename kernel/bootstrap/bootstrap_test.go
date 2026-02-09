package bootstrap

import (
	"context"
	"testing"

	pluginbuiltin "github.com/OnslaughtSnail/caelis/kernel/plugin/builtin"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

func TestAssemble_LocalToolsAndPolicy(t *testing.T) {
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
	if len(got.Policies) != 1 {
		t.Fatalf("expected 1 policy hook, got %d", len(got.Policies))
	}
}

func TestAssemble_CoreReadInjectedByRuntime(t *testing.T) {
	got, err := Assemble(context.Background(), AssembleSpec{
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
