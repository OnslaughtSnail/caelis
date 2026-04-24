package gatewayapp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	sdkproviders "github.com/OnslaughtSnail/caelis/sdk/model/providers"
)

func TestAppConfigStoreSaveUsesSecurePermissionsAndRedactsTokenByDefault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newAppConfigStore(root)
	err := store.Save(AppConfig{
		Models: persistedModelConfig{
			DefaultAlias: "minimax/minimax-m1",
			Configs: []ModelConfig{{
				Alias:    "minimax/minimax-m1",
				Provider: "minimax",
				API:      sdkproviders.APIAnthropicCompatible,
				Model:    "MiniMax-M1",
				Token:    "super-secret",
				TokenEnv: "MINIMAX_API_KEY",
			}},
		},
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	doc, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(doc.Models.Configs) != 1 {
		t.Fatalf("len(doc.Models.Configs) = %d, want 1", len(doc.Models.Configs))
	}
	if got := doc.Models.Configs[0].Token; got != "" {
		t.Fatalf("persisted token = %q, want redacted empty token", got)
	}
	if got := doc.Models.Configs[0].TokenEnv; got != "MINIMAX_API_KEY" {
		t.Fatalf("persisted token_env = %q, want MINIMAX_API_KEY", got)
	}

	if runtime.GOOS == "windows" {
		return
	}
	configInfo, err := os.Stat(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatalf("Stat(config.json) error = %v", err)
	}
	if got := configInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("config.json mode = %#o, want %#o", got, os.FileMode(0o600))
	}
	dirInfo, err := os.Stat(root)
	if err != nil {
		t.Fatalf("Stat(root) error = %v", err)
	}
	if got := dirInfo.Mode().Perm() & 0o077; got != 0 {
		t.Fatalf("root mode = %#o, want no group/world bits", dirInfo.Mode().Perm())
	}
}

func TestAppConfigStoreCanPersistTokenOnlyWhenExplicitlyEnabled(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newAppConfigStore(root)
	err := store.Save(AppConfig{
		Models: persistedModelConfig{
			DefaultAlias: "deepseek/reasoner",
			Configs: []ModelConfig{{
				Alias:        "deepseek/reasoner",
				Provider:     "deepseek",
				API:          sdkproviders.APIDeepSeek,
				Model:        "deepseek-reasoner",
				Token:        "persist-me",
				PersistToken: true,
			}},
		},
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	doc, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(doc.Models.Configs) != 1 || doc.Models.Configs[0].Token != "persist-me" {
		t.Fatalf("persisted configs = %#v, want explicit token persistence", doc.Models.Configs)
	}
}
