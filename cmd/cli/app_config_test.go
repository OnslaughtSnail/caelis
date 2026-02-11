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
	if store.CredentialStoreMode() != credentialStoreModeAuto {
		t.Fatalf("unexpected default credential store mode: %q", store.CredentialStoreMode())
	}
	if store.StreamModel() {
		t.Fatalf("unexpected default stream mode: %v", store.StreamModel())
	}
	if store.ThinkingMode() != "auto" {
		t.Fatalf("unexpected default thinking mode: %q", store.ThinkingMode())
	}
	if store.ThinkingBudget() != 1024 {
		t.Fatalf("unexpected default thinking budget: %d", store.ThinkingBudget())
	}
	if store.ShowReasoning() != true {
		t.Fatalf("unexpected default show reasoning: %v", store.ShowReasoning())
	}
	if store.PermissionMode() != "default" {
		t.Fatalf("unexpected default permission mode: %q", store.PermissionMode())
	}
	if store.SandboxType() != platformDefaultSandboxType() {
		t.Fatalf("unexpected default sandbox type: %q", store.SandboxType())
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

	if err := store2.SetRuntimeSettings(runtimeSettings{
		StreamModel:     true,
		ThinkingMode:    "on",
		ThinkingBudget:  2048,
		ReasoningEffort: "high",
		ShowReasoning:   false,
		PermissionMode:  "full_control",
		SandboxType:     "docker",
	}); err != nil {
		t.Fatal(err)
	}
	store3, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	if !store3.StreamModel() {
		t.Fatalf("expected stream mode persisted")
	}
	if store3.ThinkingMode() != "on" {
		t.Fatalf("expected thinking mode on, got %q", store3.ThinkingMode())
	}
	if store3.ThinkingBudget() != 2048 {
		t.Fatalf("expected thinking budget 2048, got %d", store3.ThinkingBudget())
	}
	if store3.ReasoningEffort() != "high" {
		t.Fatalf("expected reasoning effort high, got %q", store3.ReasoningEffort())
	}
	if store3.ShowReasoning() {
		t.Fatalf("expected show reasoning persisted false")
	}
	if store3.PermissionMode() != "full_control" {
		t.Fatalf("expected permission mode full_control, got %q", store3.PermissionMode())
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
