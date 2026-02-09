package main

import (
	"os"
	"path/filepath"
	"testing"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

func TestAppConfig_LoadOrInitAndPersist(t *testing.T) {
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
	if store.DefaultModel() != "" {
		t.Fatalf("unexpected default model: %q", store.DefaultModel())
	}
	if store.MaxSteps() != 64 {
		t.Fatalf("unexpected default max steps: %d", store.MaxSteps())
	}
	if store.CredentialStoreMode() != credentialStoreModeAuto {
		t.Fatalf("unexpected default credential store mode: %q", store.CredentialStoreMode())
	}

	cfgPath, err := configPath("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("expected config file to exist: %v", err)
	}

	provider := modelproviders.Config{
		Alias:               "openai/gpt-4o-mini",
		Provider:            "openai",
		API:                 modelproviders.APIOpenAI,
		Model:               "gpt-4o-mini",
		BaseURL:             "https://api.openai.com/v1",
		ContextWindowTokens: 128000,
		Auth: modelproviders.AuthConfig{
			Type:  modelproviders.AuthAPIKey,
			Token: "secret",
		},
	}
	if err := store.UpsertProvider(provider); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefaultModel("openai/gpt-4o-mini"); err != nil {
		t.Fatal(err)
	}

	store2, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	if store2.DefaultModel() != "openai/gpt-4o-mini" {
		t.Fatalf("unexpected persisted model: %q", store2.DefaultModel())
	}
	providers := store2.ProviderConfigs()
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if providers[0].Alias != "openai/gpt-4o-mini" {
		t.Fatalf("unexpected provider alias: %q", providers[0].Alias)
	}
	if providers[0].Auth.Token != "secret" {
		t.Fatalf("unexpected provider token")
	}
	if providers[0].Auth.CredentialRef == "" {
		t.Fatalf("expected credential ref")
	}
	if got := store2.ConfiguredModelRefs(); len(got) != 1 || got[0] != "openai/gpt-4o-mini" {
		t.Fatalf("unexpected configured model refs: %v", got)
	}
	if got := store2.ResolveModelAlias("OPENAI/GPT-4O-MINI"); got != "openai/gpt-4o-mini" {
		t.Fatalf("unexpected resolved alias: %q", got)
	}
}

func TestAppNameFromArgs(t *testing.T) {
	got := appNameFromArgs([]string{"-model", "fake", "-app", "my-app"}, "fallback")
	if got != "my-app" {
		t.Fatalf("unexpected app name: %q", got)
	}
	got = appNameFromArgs([]string{"--app=from-eq"}, "fallback")
	if got != "from-eq" {
		t.Fatalf("unexpected app name from --app=: %q", got)
	}
	got = appNameFromArgs([]string{"-model", "fake"}, "fallback")
	if got != "fallback" {
		t.Fatalf("unexpected fallback app name: %q", got)
	}
}

func TestSanitizeAppName(t *testing.T) {
	got := sanitizeAppName(" A/B C ")
	if got != "a_b_c" {
		t.Fatalf("unexpected sanitize result: %q", got)
	}
	path, err := configPath("A/B C")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "a_b_c_config.json" {
		t.Fatalf("unexpected config filename: %q", filepath.Base(path))
	}
	if filepath.Base(filepath.Dir(path)) != ".a_b_c" {
		t.Fatalf("unexpected config dir: %q", filepath.Dir(path))
	}
	storeDir, err := sessionStoreDir("A/B C")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(storeDir) != "sessions" {
		t.Fatalf("unexpected session store basename: %q", filepath.Base(storeDir))
	}
	if filepath.Base(filepath.Dir(storeDir)) != ".a_b_c" {
		t.Fatalf("unexpected session root: %q", filepath.Dir(storeDir))
	}
	idxPath, err := sessionIndexPath("A/B C")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(idxPath) != "session_index.db" {
		t.Fatalf("unexpected session index filename: %q", filepath.Base(idxPath))
	}
	if filepath.Base(filepath.Dir(idxPath)) != "sessions" {
		t.Fatalf("unexpected session index dir: %q", filepath.Dir(idxPath))
	}
}
