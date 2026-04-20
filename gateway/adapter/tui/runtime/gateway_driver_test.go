package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/app/gatewayapp"
	sdkproviders "github.com/OnslaughtSnail/caelis/sdk/model/providers"
	sdkplugin "github.com/OnslaughtSnail/caelis/sdk/plugin"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
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
		if item.Value == "MiniMax-M2.7-highspeed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("connect model candidates = %#v, want built-in MiniMax-M2.7-highspeed", models)
	}

	deepseekModels, err := driver.CompleteSlashArg(ctx, "connect-model:deepseek|https%3A%2F%2Fapi.deepseek.com%2Fv1|60||", "", 20)
	if err != nil {
		t.Fatalf("CompleteSlashArg(connect-model deepseek) error = %v", err)
	}
	if len(deepseekModels) != 2 {
		t.Fatalf("deepseek connect model candidates = %#v, want exactly 2 built-ins", deepseekModels)
	}
	if deepseekModels[0].Value != "deepseek-chat" || deepseekModels[1].Value != "deepseek-reasoner" {
		t.Fatalf("deepseek connect model candidates = %#v, want deepseek-chat and deepseek-reasoner", deepseekModels)
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
	status, err := driver.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Model == "ollama/alt-model" {
		t.Fatalf("status model = %q, want deleted alias removed", status.Model)
	}
}

func TestGatewayDriverUseModelResolvesCaseInsensitiveAlias(t *testing.T) {
	ctx := context.Background()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "use-model-test",
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
	driver, err := NewGatewayDriver(ctx, stack, "use-model-session", "surface", "ollama/llama3")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}
	if _, err := driver.Connect(ctx, ConnectConfig{
		Provider: "minimax",
		Model:    "MiniMax-M2.7-highspeed",
		APIKey:   "secret",
	}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	status, err := driver.UseModel(ctx, "minimax/minimax-m2.7-highspeed")
	if err != nil {
		t.Fatalf("UseModel() error = %v", err)
	}
	if got := strings.ToLower(strings.TrimSpace(status.Model)); got != "minimax/minimax-m2.7-highspeed" {
		t.Fatalf("status model = %q, want minimax/minimax-m2.7-highspeed", status.Model)
	}
}

func TestGatewayDriverStatusUsesPersistedDefaultAliasOnStartup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workdir := t.TempDir()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "status-startup-test",
		StoreDir:       root,
		WorkspaceKey:   workdir,
		WorkspaceCWD:   workdir,
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	if _, err := stack.Connect(gatewayapp.ModelConfig{
		Provider: "deepseek",
		API:      sdkproviders.APIDeepSeek,
		Model:    "deepseek-reasoner",
		Token:    "secret",
	}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	reloaded, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "status-startup-test",
		StoreDir:       root,
		WorkspaceKey:   workdir,
		WorkspaceCWD:   workdir,
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack(reloaded) error = %v", err)
	}
	driver, err := NewGatewayDriver(ctx, reloaded, "startup-session", "surface", "")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}
	status, err := driver.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if got := strings.TrimSpace(status.Model); got != "deepseek/deepseek-reasoner" {
		t.Fatalf("status model = %q, want deepseek/deepseek-reasoner", status.Model)
	}
}

func TestGatewayDriverStartupUsesRequestedSessionID(t *testing.T) {
	ctx := context.Background()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "lazy-session-test",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   t.TempDir(),
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	driver, err := NewGatewayDriver(ctx, stack, "sticky-session", "surface", "")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}
	session, ok := driver.currentSession()
	if !ok {
		t.Fatal("expected startup driver to create an active session")
	}
	status, err := driver.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if strings.TrimSpace(status.SessionID) == "" {
		t.Fatal("expected startup status to include active session id")
	}
	if status.SessionID != session.SessionID {
		t.Fatalf("status session = %q, want %q", status.SessionID, session.SessionID)
	}
	if status.SessionID != "sticky-session" {
		t.Fatalf("session id = %q, want sticky-session from constructor hint", status.SessionID)
	}
}

func TestGatewayDriverStartupBindsRequestedSessionInsteadOfFreshOne(t *testing.T) {
	ctx := context.Background()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "binding-reset-test",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   t.TempDir(),
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	stale, err := stack.StartSession(ctx, "stale-session", "surface")
	if err != nil {
		t.Fatalf("StartSession(stale) error = %v", err)
	}
	driver, err := NewGatewayDriver(ctx, stack, "sticky-session", "surface", "")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}
	status, err := driver.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if strings.TrimSpace(status.SessionID) == "" {
		t.Fatal("expected startup driver to bind the requested session")
	}
	if status.SessionID != "sticky-session" {
		t.Fatalf("startup session = %q, want sticky-session", status.SessionID)
	}
	if status.SessionID == stale.SessionID {
		t.Fatalf("startup session = %q, want sticky-session instead of stale bound session", status.SessionID)
	}
	current, ok := stack.Gateway.CurrentSession("surface")
	if !ok {
		t.Fatal("expected surface binding to exist after startup")
	}
	if current.SessionID != status.SessionID {
		t.Fatalf("current binding session = %q, want %q", current.SessionID, status.SessionID)
	}
}

func TestGatewayDriverStartupReusesExistingRequestedSession(t *testing.T) {
	ctx := context.Background()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "startup-resume-test",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   t.TempDir(),
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	existing, err := stack.StartSession(ctx, "sticky-session", "other-surface")
	if err != nil {
		t.Fatalf("StartSession(sticky-session) error = %v", err)
	}

	driver, err := NewGatewayDriver(ctx, stack, "sticky-session", "surface", "")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}
	status, err := driver.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.SessionID != existing.SessionID {
		t.Fatalf("status session = %q, want existing session %q", status.SessionID, existing.SessionID)
	}
}

func TestGatewayDriverCycleSessionModeUsesStartupSession(t *testing.T) {
	ctx := context.Background()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "lazy-session-mode-test",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   t.TempDir(),
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	driver, err := NewGatewayDriver(ctx, stack, "sticky-session", "surface", "")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}
	startup, ok := driver.currentSession()
	if !ok {
		t.Fatal("expected startup session")
	}
	status, err := driver.CycleSessionMode(ctx)
	if err != nil {
		t.Fatalf("CycleSessionMode() error = %v", err)
	}
	if strings.TrimSpace(status.SessionID) == "" {
		t.Fatal("expected CycleSessionMode() to keep an active session")
	}
	if status.SessionID != startup.SessionID {
		t.Fatalf("session id = %q, want startup session %q", status.SessionID, startup.SessionID)
	}
	if status.SessionMode != "plan" {
		t.Fatalf("session mode = %q, want plan", status.SessionMode)
	}
}

func TestGatewayDriverIgnoresStaleSessionAliasOutsideConfiguredModels(t *testing.T) {
	ctx := context.Background()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "stale-session-alias-test",
		StoreDir:       t.TempDir(),
		WorkspaceKey:   t.TempDir(),
		WorkspaceCWD:   t.TempDir(),
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	driver, err := NewGatewayDriver(ctx, stack, "stale-session", "surface", "")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}
	session, err := driver.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
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
	status, err := driver.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if got := strings.TrimSpace(status.Model); got != "" {
		t.Fatalf("status model = %q, want empty because alias is stale", status.Model)
	}
	candidates, err := driver.CompleteSlashArg(ctx, "model use", "", 10)
	if err != nil {
		t.Fatalf("CompleteSlashArg(model use) error = %v", err)
	}
	for _, item := range candidates {
		if strings.EqualFold(strings.TrimSpace(item.Value), "minimax/minimax-m2.7-highspeed") {
			t.Fatalf("stale session alias leaked into candidates: %#v", candidates)
		}
	}
}

func TestGatewayDriverCompleteSlashArgUsesPrefixMatching(t *testing.T) {
	ctx := context.Background()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "prefix-test",
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
	driver, err := NewGatewayDriver(ctx, stack, "prefix-model-session", "surface", "ollama/llama3")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}

	modelActions, err := driver.CompleteSlashArg(ctx, "model", "de", 10)
	if err != nil {
		t.Fatalf("CompleteSlashArg(model, de) error = %v", err)
	}
	if len(modelActions) != 1 || modelActions[0].Value != "del" {
		t.Fatalf("model action candidates = %#v, want only del", modelActions)
	}

	if _, err := driver.Connect(ctx, ConnectConfig{
		Provider: "deepseek",
		Model:    "deepseek-reasoner",
	}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	modelAliases, err := driver.CompleteSlashArg(ctx, "model use", "dee", 10)
	if err != nil {
		t.Fatalf("CompleteSlashArg(model use, dee) error = %v", err)
	}
	if len(modelAliases) == 0 || modelAliases[0].Value != "deepseek/deepseek-reasoner" {
		t.Fatalf("model alias candidates = %#v, want deepseek/deepseek-reasoner first", modelAliases)
	}
}

func TestGatewayDriverConnectPersistsMultipleProviders(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workdir := t.TempDir()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "multi-provider-test",
		StoreDir:       root,
		WorkspaceKey:   workdir,
		WorkspaceCWD:   workdir,
		PermissionMode: "default",
		Assembly:       sdkplugin.ResolvedAssembly{},
	})
	if err != nil {
		t.Fatalf("NewLocalStack() error = %v", err)
	}
	driver, err := NewGatewayDriver(ctx, stack, "multi-provider-session", "surface", "")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}
	if _, err := driver.Connect(ctx, ConnectConfig{
		Provider: "minimax",
		Model:    "MiniMax-M2.7-highspeed",
		APIKey:   "minimax-secret",
	}); err != nil {
		t.Fatalf("Connect(minimax) error = %v", err)
	}
	if _, err := driver.Connect(ctx, ConnectConfig{
		Provider: "deepseek",
		Model:    "deepseek-reasoner",
		APIKey:   "deepseek-secret",
	}); err != nil {
		t.Fatalf("Connect(deepseek) error = %v", err)
	}
	candidates, err := driver.CompleteSlashArg(ctx, "model use", "", 10)
	if err != nil {
		t.Fatalf("CompleteSlashArg(model use) error = %v", err)
	}
	if len(candidates) < 2 {
		t.Fatalf("model use candidates = %#v, want both providers", candidates)
	}
	if candidates[0].Value != "deepseek/deepseek-reasoner" {
		t.Fatalf("first candidate = %q, want deepseek/deepseek-reasoner", candidates[0].Value)
	}
	foundMinimax := false
	for _, candidate := range candidates {
		if candidate.Value == "minimax/minimax-m2.7-highspeed" {
			foundMinimax = true
			break
		}
	}
	if !foundMinimax {
		t.Fatalf("model use candidates = %#v, missing minimax alias", candidates)
	}
}

func TestGatewayDriverDeleteModelRejectsUnknownAlias(t *testing.T) {
	ctx := context.Background()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "delete-unknown-test",
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
	driver, err := NewGatewayDriver(ctx, stack, "delete-unknown-session", "surface", "ollama/llama3")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}
	if err := driver.DeleteModel(ctx, "minimax/minimax-m1"); err == nil {
		t.Fatal("DeleteModel() error = nil, want unknown alias error")
	}
}

func TestGatewayDriverConnectModelCandidatesIncludeConfiguredProviderModels(t *testing.T) {
	ctx := context.Background()
	stack, err := gatewayapp.NewLocalStack(gatewayapp.Config{
		AppName:        "caelis",
		UserID:         "connect-candidates-test",
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
	driver, err := NewGatewayDriver(ctx, stack, "connect-candidates-session", "surface", "ollama/llama3")
	if err != nil {
		t.Fatalf("NewGatewayDriver() error = %v", err)
	}
	if _, err := driver.Connect(ctx, ConnectConfig{
		Provider: "minimax",
		Model:    "MiniMax-M2.7-highspeed",
		APIKey:   "secret",
	}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	models, err := driver.CompleteSlashArg(ctx, "connect-model:minimax|https%3A%2F%2Fapi.minimaxi.com%2Fanthropic|60|secret|", "", 20)
	if err != nil {
		t.Fatalf("CompleteSlashArg(connect-model) error = %v", err)
	}
	found := false
	for _, item := range models {
		if item.Value == "MiniMax-M2.7-highspeed" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("connect model candidates = %#v, want configured minimax model", models)
	}
}
