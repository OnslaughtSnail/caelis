package runtime

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/app/gatewayapp"
	sdkproviders "github.com/OnslaughtSnail/caelis/sdk/model/providers"
	sdkplugin "github.com/OnslaughtSnail/caelis/sdk/plugin"
)

func TestGatewayDriverCompleteSlashArgConnectFlowUsesLegacyCommands(t *testing.T) {
	ctx := context.Background()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "connect-test",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   t.TempDir(),
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
		Model: gatewayapp.ModelConfig{
			Provider: "ollama",
			API:      sdkproviders.APIOllama,
			Model:    "llama3",
		},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	driver, err := NewGatewayDriver(ctx, stack, "connect-flow-session", "surface", "ollama/llama3")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}
	origDiscover := connectDiscoverModelsFn
	connectDiscoverModelsFn = func(_ context.Context, cfg sdkproviders.Config) ([]sdkproviders.RemoteModel, error) {
		if cfg.Provider != "minimax" {
			return nil, nil
		}
		return []sdkproviders.RemoteModel{
			{Name: "MiniMax-M1", ContextWindowTokens: 204800, MaxOutputTokens: 8192, Capabilities: []string{"reasoning"}},
		}, nil
	}
	defer func() { connectDiscoverModelsFn = origDiscover }()

	providers, err := driver.CompleteSlashArg(ctx, "connect", "", 20)
	if err != nil {
		t.Fatalf("CompleteSlashArg(connect) error = %v", err)
	}
	if len(providers) == 0 || providers[0].Value == "" {
		t.Fatalf("provider candidates = %#v, want non-empty", providers)
	}

	models, err := driver.CompleteSlashArg(ctx, "connect-model:minimax|https%3A%2F%2Fapi.minimaxi.com%2Fanthropic|60||", "", 20)
	if err != nil {
		t.Fatalf("CompleteSlashArg(connect-model) error = %v", err)
	}
	found := false
	for _, item := range models {
		if item.Value == "MiniMax-M1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("connect model candidates = %#v, want discovered MiniMax-M1", models)
	}
}

func TestGatewayDriverCompleteSlashArgUsesRealModelAliases(t *testing.T) {
	ctx := context.Background()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "slash-test",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   t.TempDir(),
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
		Model: gatewayapp.ModelConfig{
			Provider: "ollama",
			API:      sdkproviders.APIOllama,
			Model:    "llama3",
		},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}

	driver, err := NewGatewayDriver(ctx, stack, "slash-model-session", "surface", "ollama/llama3")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}

	if _, err := driver.Connect(ctx, ConnectConfig{
		Provider: "ollama",
		Model:    "alt-model",
	}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	useCandidates, err := driver.CompleteSlashArg(ctx, "model use", "", 10)
	if err != nil {
		t.Fatalf("CompleteSlashArg(model use) error = %v", err)
	}
	if len(useCandidates) < 2 {
		t.Fatalf("model use candidates = %#v, want at least default and session aliases", useCandidates)
	}
	if got := useCandidates[0].Value; got != "ollama/alt-model" {
		t.Fatalf("first model use candidate = %q, want ollama/alt-model", got)
	}

	delCandidates, err := driver.CompleteSlashArg(ctx, "model del", "", 10)
	if err != nil {
		t.Fatalf("CompleteSlashArg(model del) error = %v", err)
	}
	if len(delCandidates) < 2 {
		t.Fatalf("model del candidates = %#v, want at least default and session aliases", delCandidates)
	}
	if got := delCandidates[0].Value; got != "ollama/alt-model" {
		t.Fatalf("first model del candidate = %q, want ollama/alt-model", got)
	}
}

func TestGatewayDriverDeleteModelRemovesConfiguredAlias(t *testing.T) {
	ctx := context.Background()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "slash-test",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   t.TempDir(),
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
		Model: gatewayapp.ModelConfig{
			Provider: "ollama",
			API:      sdkproviders.APIOllama,
			Model:    "llama3",
		},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	driver, err := NewGatewayDriver(ctx, stack, "delete-model-session", "surface", "ollama/llama3")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}
	if _, err := driver.Connect(ctx, ConnectConfig{
		Provider: "ollama",
		Model:    "alt-model",
	}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := driver.DeleteModel(ctx, "ollama/alt-model"); err != nil {
		t.Fatalf("DeleteModel() error = %v", err)
	}
	candidates, err := driver.CompleteSlashArg(ctx, "model del", "", 10)
	if err != nil {
		t.Fatalf("CompleteSlashArg(model del) error = %v", err)
	}
	for _, item := range candidates {
		if item.Value == "ollama/alt-model" {
			t.Fatalf("deleted alias still present in %#v", candidates)
		}
	}
}
