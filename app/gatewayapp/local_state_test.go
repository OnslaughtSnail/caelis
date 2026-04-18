package gatewayapp

import (
	"context"
	"testing"

	sdkproviders "github.com/OnslaughtSnail/caelis/sdk/model/providers"
	sdkplugin "github.com/OnslaughtSnail/caelis/sdk/plugin"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func TestStackSessionRuntimeStateTracksModelAndSandboxOverrides(t *testing.T) {
	ctx := context.Background()
	stack, session := newLocalStateTestStack(t)

	alias, err := stack.Connect(ModelConfig{
		Provider: "ollama",
		API:      sdkproviders.APIOllama,
		Model:    "alt-model",
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := stack.UseModel(ctx, session.SessionRef, alias); err != nil {
		t.Fatalf("UseModel() error = %v", err)
	}
	if mode, err := stack.SetSandboxMode(ctx, session.SessionRef, "full_control"); err != nil {
		t.Fatalf("SetSandboxMode(full_control) error = %v", err)
	} else if mode != "full_control" {
		t.Fatalf("SetSandboxMode() = %q, want full_control", mode)
	}

	state, err := stack.SessionRuntimeState(ctx, session.SessionRef)
	if err != nil {
		t.Fatalf("SessionRuntimeState() error = %v", err)
	}
	if state.ModelAlias != alias {
		t.Fatalf("model alias = %q, want %q", state.ModelAlias, alias)
	}
	if state.SandboxMode != "full_control" {
		t.Fatalf("sandbox mode = %q, want full_control", state.SandboxMode)
	}

	if err := stack.DeleteModel(ctx, session.SessionRef, alias); err != nil {
		t.Fatalf("DeleteModel() error = %v", err)
	}
	if mode, err := stack.SetSandboxMode(ctx, session.SessionRef, "auto"); err != nil {
		t.Fatalf("SetSandboxMode(auto) error = %v", err)
	} else if mode != "auto" {
		t.Fatalf("SetSandboxMode(auto) = %q, want auto", mode)
	}

	state, err = stack.SessionRuntimeState(ctx, session.SessionRef)
	if err != nil {
		t.Fatalf("SessionRuntimeState() after reset error = %v", err)
	}
	if state.ModelAlias != "" {
		t.Fatalf("model alias after delete = %q, want empty", state.ModelAlias)
	}
	if state.SandboxMode != "auto" {
		t.Fatalf("sandbox mode after reset = %q, want auto", state.SandboxMode)
	}
}

func TestStackDeleteModelRemovesConfiguredAlias(t *testing.T) {
	ctx := context.Background()
	stack, session := newLocalStateTestStack(t)

	alias, err := stack.Connect(ModelConfig{
		Provider: "ollama",
		API:      sdkproviders.APIOllama,
		Model:    "alt-model",
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := stack.UseModel(ctx, session.SessionRef, alias); err != nil {
		t.Fatalf("UseModel() error = %v", err)
	}
	if err := stack.DeleteModel(ctx, session.SessionRef, alias); err != nil {
		t.Fatalf("DeleteModel() error = %v", err)
	}

	aliases, err := stack.ListModelAliases(ctx, session.SessionRef)
	if err != nil {
		t.Fatalf("ListModelAliases() error = %v", err)
	}
	for _, item := range aliases {
		if item == alias {
			t.Fatalf("deleted alias %q still present in %#v", alias, aliases)
		}
	}
	if got := stack.DefaultModelAlias(); got == alias {
		t.Fatalf("default alias = %q, want deleted alias removed", got)
	}
}

func newLocalStateTestStack(t *testing.T) (*Stack, sdksession.Session) {
	t.Helper()
	root := t.TempDir()
	workdir := t.TempDir()
	stack, err := NewLocalStack(Config{
		AppName:        "caelis",
		UserID:         "state-test",
		StoreDir:       root,
		WorkspaceKey:   workdir,
		WorkspaceCWD:   workdir,
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
		Model: ModelConfig{
			Provider: "ollama",
			API:      sdkproviders.APIOllama,
			Model:    "llama3",
		},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	session, err := stack.StartSession(context.Background(), "state-test-session", "surface-state-test")
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	return stack, session
}
