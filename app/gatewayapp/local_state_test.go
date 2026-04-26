package gatewayapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkproviders "github.com/OnslaughtSnail/caelis/sdk/model/providers"
	sdkplugin "github.com/OnslaughtSnail/caelis/sdk/plugin"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func TestStackSessionRuntimeStateTracksModelAndSessionModeOverrides(t *testing.T) {
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
	if mode, err := stack.SetSessionMode(ctx, session.SessionRef, "full_access"); err != nil {
		t.Fatalf("SetSessionMode(full_access) error = %v", err)
	} else if mode != "full_access" {
		t.Fatalf("SetSessionMode() = %q, want full_access", mode)
	}

	state, err := stack.SessionRuntimeState(ctx, session.SessionRef)
	if err != nil {
		t.Fatalf("SessionRuntimeState() error = %v", err)
	}
	if state.ModelAlias != alias {
		t.Fatalf("model alias = %q, want %q", state.ModelAlias, alias)
	}
	if state.SessionMode != "full_access" {
		t.Fatalf("session mode = %q, want full_access", state.SessionMode)
	}

	if err := stack.DeleteModel(ctx, session.SessionRef, alias); err != nil {
		t.Fatalf("DeleteModel() error = %v", err)
	}
	if mode, err := stack.SetSessionMode(ctx, session.SessionRef, "default"); err != nil {
		t.Fatalf("SetSessionMode(default) error = %v", err)
	} else if mode != "default" {
		t.Fatalf("SetSessionMode(default) = %q, want default", mode)
	}

	state, err = stack.SessionRuntimeState(ctx, session.SessionRef)
	if err != nil {
		t.Fatalf("SessionRuntimeState() after reset error = %v", err)
	}
	if state.ModelAlias != "" {
		t.Fatalf("model alias after delete = %q, want empty", state.ModelAlias)
	}
	if state.SessionMode != "default" {
		t.Fatalf("session mode after reset = %q, want default", state.SessionMode)
	}
}

func TestStackSandboxBackendPersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workdir := t.TempDir()

	stack, err := NewLocalStack(Config{
		AppName:        "caelis",
		UserID:         "sandbox-persist-test",
		StoreDir:       root,
		WorkspaceKey:   workdir,
		WorkspaceCWD:   workdir,
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	status, err := stack.SetSandboxBackend(ctx, "auto")
	if err != nil {
		t.Fatalf("SetSandboxBackend(auto) error = %v", err)
	}
	if status.RequestedBackend != "auto" {
		t.Fatalf("requested backend = %q, want auto", status.RequestedBackend)
	}

	reloaded, err := NewLocalStack(Config{
		AppName:        "caelis",
		UserID:         "sandbox-persist-test",
		StoreDir:       root,
		WorkspaceKey:   workdir,
		WorkspaceCWD:   workdir,
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack(reloaded) error = %v", err)
	}
	if got := reloaded.SandboxStatus().RequestedBackend; got != "auto" {
		t.Fatalf("SandboxStatus().RequestedBackend = %q, want auto", got)
	}
	doc, err := LoadAppConfig(root)
	if err != nil {
		t.Fatalf("LoadAppConfig() error = %v", err)
	}
	if got := doc.Sandbox.RequestedType; got != "auto" {
		t.Fatalf("config sandbox requested_type = %q, want auto", got)
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

func TestSessionRuntimeStateIgnoresStaleModelAliasOutsideConfig(t *testing.T) {
	ctx := context.Background()
	stack, session := newLocalStateTestStack(t)
	if err := stack.Sessions.UpdateState(ctx, session.SessionRef, func(state map[string]any) (map[string]any, error) {
		next := sdksession.CloneState(state)
		if next == nil {
			next = map[string]any{}
		}
		next["gateway.current_model_alias"] = "minimax/minimax-m2.7-highspeed"
		return next, nil
	}); err != nil {
		t.Fatalf("UpdateState() error = %v", err)
	}
	state, err := stack.SessionRuntimeState(ctx, session.SessionRef)
	if err != nil {
		t.Fatalf("SessionRuntimeState() error = %v", err)
	}
	if state.ModelAlias != "" {
		t.Fatalf("ModelAlias = %q, want empty because alias is not in config", state.ModelAlias)
	}
}

func TestLocalStackPersistsMultipleProviderModelsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workdir := t.TempDir()

	stack, err := NewLocalStack(Config{
		AppName:        "caelis",
		UserID:         "persist-test",
		StoreDir:       root,
		WorkspaceKey:   workdir,
		WorkspaceCWD:   workdir,
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	session, err := stack.StartSession(ctx, "persist-session", "surface-persist")
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}

	minimaxAlias, err := stack.Connect(ModelConfig{
		Provider: "minimax",
		Model:    "MiniMax-M2.7-highspeed",
		Token:    "minimax-secret",
	})
	if err != nil {
		t.Fatalf("Connect(minimax) error = %v", err)
	}
	deepseekAlias, err := stack.Connect(ModelConfig{
		Provider: "deepseek",
		API:      sdkproviders.APIDeepSeek,
		Model:    "deepseek-reasoner",
		Token:    "deepseek-secret",
	})
	if err != nil {
		t.Fatalf("Connect(deepseek) error = %v", err)
	}
	if err := stack.UseModel(ctx, session.SessionRef, minimaxAlias); err != nil {
		t.Fatalf("UseModel(minimax) error = %v", err)
	}

	reloaded, err := NewLocalStack(Config{
		AppName:        "caelis",
		UserID:         "persist-test",
		StoreDir:       root,
		WorkspaceKey:   workdir,
		WorkspaceCWD:   workdir,
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack(reloaded) error = %v", err)
	}
	reloadedSession, err := reloaded.StartSession(ctx, "persist-session", "surface-persist")
	if err != nil {
		t.Fatalf("StartSession(reloaded) error = %v", err)
	}
	aliases, err := reloaded.ListModelAliases(ctx, reloadedSession.SessionRef)
	if err != nil {
		t.Fatalf("ListModelAliases(reloaded) error = %v", err)
	}
	if len(aliases) < 2 {
		t.Fatalf("reloaded aliases = %#v, want both minimax and deepseek aliases", aliases)
	}
	if !containsStringFold(aliases, minimaxAlias) {
		t.Fatalf("reloaded aliases = %#v, missing %q", aliases, minimaxAlias)
	}
	if !containsStringFold(aliases, deepseekAlias) {
		t.Fatalf("reloaded aliases = %#v, missing %q", aliases, deepseekAlias)
	}
	if got := reloaded.DefaultModelAlias(); got != minimaxAlias {
		t.Fatalf("DefaultModelAlias(reloaded) = %q, want %q", got, minimaxAlias)
	}
	doc, err := LoadAppConfig(root)
	if err != nil {
		t.Fatalf("LoadAppConfig() error = %v", err)
	}
	if got := doc.Models.DefaultAlias; got != minimaxAlias {
		t.Fatalf("config default alias = %q, want %q", got, minimaxAlias)
	}
	if len(doc.Models.Configs) < 2 {
		t.Fatalf("config models = %#v, want both minimax and deepseek configs", doc.Models.Configs)
	}
	if _, err := os.Stat(filepath.Join(root, "config.json")); err != nil {
		t.Fatalf("config.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "config", "models.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy models.json should be removed, stat err = %v", err)
	}
}

func TestNewLocalStackAllowsEmptyInitialModelConfig(t *testing.T) {
	stack, err := NewLocalStack(Config{
		AppName:        "caelis",
		UserID:         "empty-model-test",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   t.TempDir(),
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	if got := stack.DefaultModelAlias(); got != "" {
		t.Fatalf("DefaultModelAlias() = %q, want empty", got)
	}
}

func TestNewLocalStackInfersCodeFreeAPIFromProvider(t *testing.T) {
	stack, err := NewLocalStack(Config{
		AppName:        "caelis",
		UserID:         "codefree-api-test",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   t.TempDir(),
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
		Model: ModelConfig{
			Provider: "codefree",
			Model:    "GLM-5.1",
		},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	cfg, ok := stack.lookup.Config("codefree/glm-5.1")
	if !ok {
		t.Fatal("missing codefree model config")
	}
	if cfg.API != sdkproviders.APICodeFree {
		t.Fatalf("codefree API = %q, want %q", cfg.API, sdkproviders.APICodeFree)
	}
}

func TestDefaultStoreDirUsesHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("home directory unavailable")
	}
	want := filepath.Join(home, ".caelis")
	if got := defaultStoreDir(); got != want {
		t.Fatalf("defaultStoreDir() = %q, want %q", got, want)
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

func containsStringFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}
