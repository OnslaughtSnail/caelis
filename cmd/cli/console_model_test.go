package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

func TestHandleModel_FixedReasoningRejectsOverrides(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	store, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	cfg := modelproviders.Config{
		Alias:    "deepseek/deepseek-reasoner",
		Provider: "deepseek",
		API:      modelproviders.APIDeepSeek,
		Model:    "deepseek-reasoner",
		Auth: modelproviders.AuthConfig{
			Type:  modelproviders.AuthAPIKey,
			Token: "test-token",
		},
	}
	if err := store.UpsertProvider(cfg); err != nil {
		t.Fatal(err)
	}

	factory := modelproviders.NewFactory()
	modelcatalogApplyConfigDefaults(&cfg)
	if err := factory.Register(cfg); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	console := &cliConsole{
		modelFactory: factory,
		configStore:  store,
		out:          &out,
	}
	if _, err := handleModel(console, []string{"deepseek/deepseek-reasoner", "on"}); err == nil {
		t.Fatal("expected fixed reasoning model to reject explicit overrides")
	}
	settings := store.ModelRuntimeSettings("deepseek/deepseek-reasoner")
	if settings.ReasoningEffort != "" {
		t.Fatalf("expected no persisted reasoning effort for fixed model, got %q", settings.ReasoningEffort)
	}
}

func TestHandleModel_InvalidReasoningDoesNotSwitchModel(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	store, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	openaiCfg := modelproviders.Config{
		Alias:    "openai/o3",
		Provider: "openai",
		API:      modelproviders.APIOpenAI,
		Model:    "o3",
		Auth: modelproviders.AuthConfig{
			Type:  modelproviders.AuthAPIKey,
			Token: "test-token",
		},
	}
	deepseekCfg := modelproviders.Config{
		Alias:           "deepseek/deepseek-reasoner",
		Provider:        "deepseek",
		API:             modelproviders.APIDeepSeek,
		Model:           "deepseek-reasoner",
		ReasoningLevels: []string{"none", "low"},
		Auth: modelproviders.AuthConfig{
			Type:  modelproviders.AuthAPIKey,
			Token: "test-token",
		},
	}
	if err := store.UpsertProvider(openaiCfg); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertProvider(deepseekCfg); err != nil {
		t.Fatal(err)
	}

	factory := modelproviders.NewFactory()
	modelcatalogApplyConfigDefaults(&openaiCfg)
	if err := factory.Register(openaiCfg); err != nil {
		t.Fatal(err)
	}
	modelcatalogApplyConfigDefaults(&deepseekCfg)
	if err := factory.Register(deepseekCfg); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	console := &cliConsole{
		modelFactory: factory,
		configStore:  store,
		out:          &out,
	}
	if _, err := handleModel(console, []string{"openai/o3", "on"}); err != nil {
		t.Fatal(err)
	}
	if console.modelAlias != "openai/o3" {
		t.Fatalf("expected model alias openai/o3, got %q", console.modelAlias)
	}

	if _, err := handleModel(console, []string{"deepseek/deepseek-reasoner", "high"}); err == nil {
		t.Fatal("expected invalid reasoning option error")
	}
	if console.modelAlias != "openai/o3" {
		t.Fatalf("expected model alias unchanged after failed command, got %q", console.modelAlias)
	}
	if got := store.DefaultModel(); got != "openai/o3" {
		t.Fatalf("expected persisted default model unchanged, got %q", got)
	}
}

func TestHandleModel_DelDeletesConfigAndCredential(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	store, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := loadOrInitCredentialStore("demo-app", credentialStoreModeFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := modelproviders.Config{
		Alias:    "openai-compatible/minimax-m2.5",
		Provider: "openai-compatible",
		API:      modelproviders.APIOpenAICompatible,
		Model:    "minimax-m2.5",
		BaseURL:  "https://a.example/v1",
		Auth: modelproviders.AuthConfig{
			Type:          modelproviders.AuthAPIKey,
			CredentialRef: defaultCredentialRef("openai-compatible", "https://a.example/v1"),
		},
	}
	if err := store.UpsertProvider(cfg); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Upsert(cfg.Auth.CredentialRef, credentialRecord{Type: string(cfg.Auth.Type), Token: "secret"}); err != nil {
		t.Fatal(err)
	}

	factory := modelproviders.NewFactory()
	modelcatalogApplyConfigDefaults(&cfg)
	hydrated := hydrateProviderAuthToken(cfg, credentials)
	if err := factory.Register(hydrated); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	console := &cliConsole{
		modelFactory:    factory,
		configStore:     store,
		credentialStore: credentials,
		modelAlias:      cfg.Alias,
		out:             &out,
	}
	if _, err := handleModel(console, []string{"del", cfg.Alias}); err != nil {
		t.Fatal(err)
	}
	if refs := store.ConfiguredModelAliases(); len(refs) != 0 {
		t.Fatalf("expected provider removed, got %v", refs)
	}
	if _, ok := credentials.Get(cfg.Auth.CredentialRef); ok {
		t.Fatal("expected credential removed")
	}
	if console.llm != nil || console.modelAlias != "" {
		t.Fatalf("expected current model cleared, got alias=%q llm=%v", console.modelAlias, console.llm != nil)
	}
}

func TestHandleModel_DelPromptsForMultipleRemovals(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	store, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	configs := []modelproviders.Config{
		{
			Alias:    "xiaomi/mimo-v2-flash",
			Provider: "xiaomi",
			API:      modelproviders.APIMimo,
			Model:    "mimo-v2-flash",
			Auth:     modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey, Token: "t1"},
		},
		{
			Alias:    "openai/o3",
			Provider: "openai",
			API:      modelproviders.APIOpenAI,
			Model:    "o3",
			Auth:     modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey, Token: "t2"},
		},
	}
	factory := modelproviders.NewFactory()
	for _, cfg := range configs {
		if err := store.UpsertProvider(cfg); err != nil {
			t.Fatal(err)
		}
		modelcatalogApplyConfigDefaults(&cfg)
		if err := factory.Register(cfg); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	console := &cliConsole{
		modelFactory: factory,
		configStore:  store,
		out:          &out,
		prompter: &stubChoicePrompter{
			choices: []string{"xiaomi/mimo-v2-flash openai/o3"},
		},
	}
	if _, err := handleModel(console, []string{"del"}); err != nil {
		t.Fatal(err)
	}
	if refs := store.ConfiguredModelAliases(); len(refs) != 0 {
		t.Fatalf("expected all providers removed, got %v", refs)
	}
	text := out.String()
	if !strings.Contains(text, "model removed: xiaomi/mimo-v2-flash") || !strings.Contains(text, "model removed: openai/o3") {
		t.Fatalf("expected both removal notes, got %q", text)
	}
}
