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

func TestHydrateProviderAuthToken(t *testing.T) {
	store := &credentialStore{
		data: credentialFile{
			Version: credentialFileVersion,
			Credentials: map[string]credentialRecord{
				"openai_api_openai_com": {
					Type:  string(modelproviders.AuthAPIKey),
					Token: "stored-token",
				},
			},
		},
	}

	cfg := modelproviders.Config{
		Alias:    "openai/gpt-4o-mini",
		Provider: "openai",
		BaseURL:  "https://api.openai.com/v1",
		Auth: modelproviders.AuthConfig{
			Type:          modelproviders.AuthAPIKey,
			TokenEnv:      "OPENAI_API_KEY",
			CredentialRef: "openai_api_openai_com",
		},
	}

	if err := os.Unsetenv("OPENAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	hydrated := hydrateProviderAuthToken(cfg, store)
	if hydrated.Auth.Token != "stored-token" {
		t.Fatalf("expected hydrated token, got %q", hydrated.Auth.Token)
	}

	if err := os.Setenv("OPENAI_API_KEY", "env-token"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv("OPENAI_API_KEY")
	})
	hydrated = hydrateProviderAuthToken(cfg, store)
	if hydrated.Auth.Token != "" {
		t.Fatalf("expected env to take precedence, got token %q", hydrated.Auth.Token)
	}
}

func TestMigrateInlineProviderTokens(t *testing.T) {
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
			Type:  modelproviders.AuthAPIKey,
			Token: "legacy-inline-token",
		},
	}); err != nil {
		t.Fatal(err)
	}

	credStore, err := loadOrInitCredentialStore("demo-app", credentialStoreModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateInlineProviderTokens(cfgStore, credStore); err != nil {
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
		t.Fatalf("expected inline token cleared after migration")
	}
	if providers[0].Auth.CredentialRef == "" {
		t.Fatalf("expected credential_ref after migration")
	}
	record, ok := credStore.Get(providers[0].Auth.CredentialRef)
	if !ok {
		t.Fatalf("expected migrated credential for ref %q", providers[0].Auth.CredentialRef)
	}
	if record.Token != "legacy-inline-token" {
		t.Fatalf("unexpected migrated token")
	}
}
