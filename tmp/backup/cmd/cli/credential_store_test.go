package main

import (
	"os"
	"runtime"
	"testing"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

func TestCredentialStore_LoadInitAndPersist(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	store, err := loadOrInitCredentialStore("demo-app", credentialStoreModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	ref := "openai_api_openai_com"
	if err := store.Upsert(ref, credentialRecord{
		Type:  string(modelproviders.AuthAPIKey),
		Token: "secret-token",
	}); err != nil {
		t.Fatal(err)
	}

	path, err := credentialPath("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("expected credential file mode 0600, got %o", info.Mode().Perm())
	}

	store2, err := loadOrInitCredentialStore("demo-app", credentialStoreModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := store2.Get(ref)
	if !ok {
		t.Fatalf("expected credential %q", ref)
	}
	if got.Token != "secret-token" {
		t.Fatalf("unexpected token: %q", got.Token)
	}
}

func TestMergeCredentialStoreProviderTokens(t *testing.T) {
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	cfgStore, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfgStore.UpsertProvider(modelproviders.Config{
		Alias:    "openai/gpt-4o-mini",
		Provider: "openai",
		API:      modelproviders.APIOpenAI,
		Model:    "gpt-4o-mini",
		BaseURL:  "https://api.openai.com/v1",
		Auth: modelproviders.AuthConfig{
			Type:          modelproviders.AuthAPIKey,
			TokenEnv:      "OPENAI_API_KEY",
			CredentialRef: "openai_api_openai_com",
		},
	}); err != nil {
		t.Fatal(err)
	}

	credStore, err := loadOrInitCredentialStore("demo-app", credentialStoreModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if err := credStore.Upsert("openai_api_openai_com", credentialRecord{
		Type:  string(modelproviders.AuthAPIKey),
		Token: "stored-token",
	}); err != nil {
		t.Fatal(err)
	}
	if err := mergeCredentialStoreProviderTokens(cfgStore, credStore); err != nil {
		t.Fatal(err)
	}

	cfgStore2, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	providers := cfgStore2.ProviderConfigs()
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if providers[0].Auth.Token != "" {
		t.Fatalf("expected plaintext token removed from config, got %q", providers[0].Auth.Token)
	}
	if providers[0].Auth.TokenEnv != "" {
		t.Fatalf("expected token_env cleared, got %q", providers[0].Auth.TokenEnv)
	}
	if providers[0].Auth.CredentialRef != "openai_api_openai_com" {
		t.Fatalf("expected credential_ref preserved, got %q", providers[0].Auth.CredentialRef)
	}
	stored, ok := credStore.Get("openai_api_openai_com")
	if !ok || stored.Token != "stored-token" {
		t.Fatalf("expected token persisted in credential store, got %+v (exists=%v)", stored, ok)
	}
}

func TestHydrateProviderAuthToken_PrefersCredentialRef(t *testing.T) {
	store := &credentialStore{
		data: credentialFile{
			Version: 1,
			Credentials: map[string]credentialRecord{
				"openai_api_openai_com": {
					Type:  string(modelproviders.AuthAPIKey),
					Token: "token-from-store",
				},
			},
		},
	}
	cfg := modelproviders.Config{
		Auth: modelproviders.AuthConfig{
			Type:          modelproviders.AuthAPIKey,
			Token:         "token-from-config",
			CredentialRef: "openai_api_openai_com",
		},
	}
	hydrated := hydrateProviderAuthToken(cfg, store)
	if hydrated.Auth.Token != "token-from-store" {
		t.Fatalf("expected credential_ref token to win, got %q", hydrated.Auth.Token)
	}
}
