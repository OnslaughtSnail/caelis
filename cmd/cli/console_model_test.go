package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuiapp"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	coreacpmeta "github.com/OnslaughtSnail/caelis/pkg/acpmeta"
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

func TestPersistSessionModelAlias_StoresACPMetadata(t *testing.T) {
	store := inmemory.New()
	console := &cliConsole{
		appName:      "app",
		userID:       "u",
		sessionID:    "s-1",
		modelAlias:   "minimax/minimax-m2.7-highspeed",
		sessionStore: store,
	}

	if err := console.persistSessionModelAlias(context.Background()); err != nil {
		t.Fatal(err)
	}

	values, err := store.SnapshotState(context.Background(), &session.Session{
		AppName: "app",
		UserID:  "u",
		ID:      "s-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	acpState, _ := values["acp"].(map[string]any)
	meta, _ := acpState["meta"].(map[string]any)
	if got := coreacpmeta.ModelAlias(meta); got != "minimax/minimax-m2.7-highspeed" {
		t.Fatalf("expected persisted model alias, got %q", got)
	}
}

func TestHandleModelUse_UsesACPMainConfigOption(t *testing.T) {
	console := &cliConsole{
		baseCtx:   context.Background(),
		appName:   "app",
		userID:    "u",
		sessionID: "sess-1",
		configStore: &appConfigStore{
			path: filepath.Join(t.TempDir(), "config.json"),
			data: appConfig{
				MainAgent: "copilot",
				Agents: map[string]agentRecord{
					"copilot": {Command: "copilot", Args: []string{"--acp", "--stdio"}},
				},
			},
		},
		persistentMainACP: &persistentMainACPState{
			client:          &stubMainACPClient{},
			agentID:         "copilot",
			remoteSessionID: "remote-1",
			configOptions: []acpclient.SessionConfigOption{
				{
					ID:           acpConfigModel,
					Category:     "model",
					CurrentValue: "openai/gpt-5-mini",
					Options: []acpclient.SessionConfigSelectOption{
						{Value: "openai/gpt-5-mini", Name: "GPT-5 Mini"},
						{Value: "openai/gpt-5", Name: "GPT-5"},
					},
				},
			},
		},
		out: &bytes.Buffer{},
	}
	client := console.persistentMainACP.client.(*stubMainACPClient)
	client.configOptions = cloneACPConfigOptions(console.persistentMainACP.configOptions)

	if _, err := handleModel(console, []string{"use", "openai/gpt-5"}); err != nil {
		t.Fatalf("switch ACP main model: %v", err)
	}
	if len(client.setConfigCalls) != 1 || client.setConfigCalls[0] != "model=openai/gpt-5" {
		t.Fatalf("expected ACP config switch call, got %v", client.setConfigCalls)
	}
	if got := console.currentSessionModelAlias(); got != "openai/gpt-5" {
		t.Fatalf("expected mirrored ACP model alias, got %q", got)
	}
}

func TestHandleModelUse_UsesACPMainReasoningConfigOptionAfterModelSwitch(t *testing.T) {
	console := &cliConsole{
		baseCtx:   context.Background(),
		appName:   "app",
		userID:    "u",
		sessionID: "sess-1",
		configStore: &appConfigStore{
			path: filepath.Join(t.TempDir(), "config.json"),
			data: appConfig{
				MainAgent: "copilot",
				Agents: map[string]agentRecord{
					"copilot": {Command: "copilot", Args: []string{"--acp", "--stdio"}},
				},
			},
		},
		persistentMainACP: &persistentMainACPState{
			client:          &stubMainACPClient{},
			agentID:         "copilot",
			remoteSessionID: "remote-1",
			configOptions: []acpclient.SessionConfigOption{
				{
					ID:           acpConfigModel,
					Category:     "model",
					CurrentValue: "openai/gpt-5-mini",
					Options: []acpclient.SessionConfigSelectOption{
						{Value: "openai/gpt-5-mini", Name: "GPT-5 Mini"},
						{Value: "openai/o3", Name: "O3"},
					},
				},
			},
		},
		out: &bytes.Buffer{},
	}
	client := console.persistentMainACP.client.(*stubMainACPClient)
	client.configOptions = cloneACPConfigOptions(console.persistentMainACP.configOptions)
	client.onSetConfig = func(configID string, value string, current []acpclient.SessionConfigOption) []acpclient.SessionConfigOption {
		if strings.EqualFold(configID, acpConfigModel) {
			for i := range current {
				if strings.EqualFold(current[i].ID, acpConfigModel) {
					current[i].CurrentValue = value
				}
			}
			return append(current, acpclient.SessionConfigOption{
				ID:           acpConfigReasoningEffort,
				Category:     "thought_level",
				CurrentValue: "medium",
				Options: []acpclient.SessionConfigSelectOption{
					{Value: "low", Name: "Low"},
					{Value: "medium", Name: "Medium"},
					{Value: "high", Name: "High"},
				},
			})
		}
		for i := range current {
			if strings.EqualFold(current[i].ID, acpConfigReasoningEffort) {
				current[i].CurrentValue = value
			}
		}
		return current
	}

	if _, err := handleModel(console, []string{"use", "openai/o3", "high"}); err != nil {
		t.Fatalf("switch ACP main model + reasoning: %v", err)
	}
	if want := []string{"model=openai/o3", "reasoning_effort=high"}; !slices.Equal(client.setConfigCalls, want) {
		t.Fatalf("expected ACP config switch calls %v, got %v", want, client.setConfigCalls)
	}
	settings, ok := console.configStore.ACPAgentSettings("copilot")
	if !ok {
		t.Fatal("expected persisted ACP agent settings")
	}
	if settings.Model != "openai/o3" || settings.ReasoningEffort != "high" {
		t.Fatalf("unexpected persisted ACP settings %#v", settings)
	}
}

func TestHandleModelUse_ACPMainRejectsUnsupportedReasoningWithoutSwitchingModel(t *testing.T) {
	console := &cliConsole{
		baseCtx:   context.Background(),
		appName:   "app",
		userID:    "u",
		sessionID: "sess-1",
		configStore: &appConfigStore{
			path: filepath.Join(t.TempDir(), "config.json"),
			data: appConfig{
				MainAgent: "copilot",
				Agents: map[string]agentRecord{
					"copilot": {Command: "copilot", Args: []string{"--acp", "--stdio"}},
				},
			},
		},
		persistentMainACP: &persistentMainACPState{
			client:          &stubMainACPClient{},
			agentID:         "copilot",
			remoteSessionID: "remote-1",
			configOptions: []acpclient.SessionConfigOption{
				{
					ID:           acpConfigModel,
					Category:     "model",
					CurrentValue: "openai/gpt-5-mini",
					Options: []acpclient.SessionConfigSelectOption{
						{Value: "openai/gpt-5-mini", Name: "GPT-5 Mini"},
						{Value: "openai/o3", Name: "O3"},
					},
				},
			},
			modelProfiles: []acpMainModelProfile{
				{
					ID:   "openai/o3",
					Name: "O3",
					Reasoning: []tuiapp.SlashArgCandidate{
						{Value: "low", Display: "Low"},
						{Value: "high", Display: "High"},
					},
				},
			},
		},
		out: &bytes.Buffer{},
	}
	client := console.persistentMainACP.client.(*stubMainACPClient)
	client.configOptions = cloneACPConfigOptions(console.persistentMainACP.configOptions)

	if _, err := handleModel(console, []string{"use", "openai/o3", "medium"}); err == nil {
		t.Fatal("expected unsupported ACP reasoning error")
	}
	if len(client.setConfigCalls) != 0 {
		t.Fatalf("expected no ACP config mutations on invalid reasoning, got %v", client.setConfigCalls)
	}
	if got := console.currentSessionModelAlias(); got != "openai/gpt-5-mini" {
		t.Fatalf("expected ACP model alias unchanged, got %q", got)
	}
}
