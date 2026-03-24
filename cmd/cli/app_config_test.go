package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
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
	if !store.StreamModel() {
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
		ThinkingBudget:      2048,
		ReasoningEffort:     "high",
		OpenRouter: modelproviders.OpenRouterConfig{
			Models:     []string{"openai/gpt-4o-mini"},
			Route:      "fallback",
			Transforms: []string{"middle-out"},
		},
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
	if providers[0].OpenRouter.Route != "fallback" {
		t.Fatalf("expected openrouter options persisted, got %+v", providers[0].OpenRouter)
	}
	if providers[0].Auth.CredentialRef != "" {
		t.Fatalf("expected credential_ref empty when token is configured, got %q", providers[0].Auth.CredentialRef)
	}
	if got := store2.ConfiguredModelRefs(); len(got) != 1 || got[0] != "openai/gpt-4o-mini" {
		t.Fatalf("unexpected configured model refs: %v", got)
	}
	if got := store2.ResolveModelAlias("OPENAI/GPT-4O-MINI"); got != "openai/gpt-4o-mini" {
		t.Fatalf("unexpected resolved alias: %q", got)
	}
	settings := store2.ModelRuntimeSettings("openai/gpt-4o-mini")
	if settings.ThinkingBudget != 2048 {
		t.Fatalf("expected provider thinking budget 2048, got %d", settings.ThinkingBudget)
	}
	if settings.ReasoningEffort != "high" {
		t.Fatalf("expected provider reasoning effort high, got %q", settings.ReasoningEffort)
	}

	if err := store2.SetRuntimeSettings(runtimeSettings{
		PermissionMode: "full_control",
		SandboxType:    "bwrap",
	}); err != nil {
		t.Fatal(err)
	}
	store3, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	if store3.PermissionMode() != "full_control" {
		t.Fatalf("expected permission mode full_control, got %q", store3.PermissionMode())
	}
	settings = store3.ModelRuntimeSettings("openai/gpt-4o-mini")
	if settings.ThinkingBudget != 2048 || settings.ReasoningEffort != "high" {
		t.Fatalf("expected provider runtime settings persisted, got %#v", settings)
	}
	if err := store3.SetModelRuntimeSettings("openai/gpt-4o-mini", modelRuntimeSettings{
		ThinkingBudget:  2048,
		ReasoningEffort: "very-high",
	}); err != nil {
		t.Fatal(err)
	}
	store4, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	settings = store4.ModelRuntimeSettings("openai/gpt-4o-mini")
	if settings.ReasoningEffort != "xhigh" {
		t.Fatalf("expected normalized runtime settings, got %#v", settings)
	}
}

func TestSandboxTypeDisplayLabel(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "auto", value: "", want: autoSandboxTypeDisplayLabel(runtime.GOOS)},
		{name: "seatbelt", value: "seatbelt", want: "seatbelt"},
		{name: "landlock", value: "landlock", want: "landlock (experimental)"},
		{name: "bwrap", value: "bwrap", want: "bwrap"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sandboxTypeDisplayLabel(tc.value); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestNormalizeProviderAuthRecord_PrefersCredentialRefAndKeepsLegacyTokenFallback(t *testing.T) {
	auth := authRecord{
		Type:          string(modelproviders.AuthAPIKey),
		Token:         "plaintext-token",
		CredentialRef: "openai_api_openai_com",
	}
	normalizeProviderAuthRecord("openai", "https://api.openai.com/v1", &auth)
	if auth.CredentialRef != "openai_api_openai_com" {
		t.Fatalf("expected credential_ref kept, got %q", auth.CredentialRef)
	}
	if auth.Token != "plaintext-token" {
		t.Fatalf("expected plaintext token kept as fallback when credential_ref exists, got %q", auth.Token)
	}
}

func TestAppConfig_ResolveOrAllocateModelAlias_DistinguishesEndpoint(t *testing.T) {
	store := &appConfigStore{path: filepath.Join(t.TempDir(), "config.json"), data: defaultAppConfig()}
	if got := store.ResolveOrAllocateModelAlias("openai-compatible", "minimax-m2.5", "https://a.example/v1"); got != "openai-compatible/minimax-m2.5" {
		t.Fatalf("unexpected initial alias %q", got)
	}
	if err := store.UpsertProvider(modelproviders.Config{
		Alias:    "openai-compatible/minimax-m2.5",
		Provider: "openai-compatible",
		API:      modelproviders.APIOpenAICompatible,
		Model:    "minimax-m2.5",
		BaseURL:  "https://a.example/v1",
	}); err != nil {
		t.Fatal(err)
	}
	if got := store.ResolveOrAllocateModelAlias("openai-compatible", "minimax-m2.5", "https://a.example/v1"); got != "openai-compatible/minimax-m2.5" {
		t.Fatalf("expected same endpoint to reuse alias, got %q", got)
	}
	got := store.ResolveOrAllocateModelAlias("openai-compatible", "minimax-m2.5", "https://b.example/v1")
	if got == "openai-compatible/minimax-m2.5" || !strings.HasPrefix(got, "openai-compatible/minimax-m2.5@") {
		t.Fatalf("expected endpoint-scoped alias, got %q", got)
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

func TestAppConfig_ResolvesEnvPlaceholderFromCwdDotEnv(t *testing.T) {
	const tokenEnv = "CAELIS_TEST_TOKEN_FROM_CWD"
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	workspace := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})

	cfgPath, err := configPath("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{
  "version": 1,
  "default_model": "deepseek/deepseek-chat",
  "providers": [
    {
      "alias": "deepseek/deepseek-chat",
      "provider": "deepseek",
      "api": "deepseek",
      "model": "deepseek-chat",
      "base_url": "https://api.deepseek.com/v1",
      "auth": {"type": "api_key", "token": "${` + tokenEnv + `}"}
    }
  ]
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".env"), []byte(tokenEnv+"=from-cwd-env\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	providers := store.ProviderConfigs()
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if providers[0].Auth.Token != "from-cwd-env" {
		t.Fatalf("expected resolved token from cwd .env, got %q", providers[0].Auth.Token)
	}
}

func TestAppConfig_ResolvesEnvPlaceholderFromConfigRootDotEnv(t *testing.T) {
	const tokenEnv = "CAELIS_TEST_TOKEN_FROM_CONFIG_ROOT"
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	cfgPath, err := configPath("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{
  "version": 1,
  "default_model": "deepseek/deepseek-chat",
  "providers": [
    {
      "alias": "deepseek/deepseek-chat",
      "provider": "deepseek",
      "api": "deepseek",
      "model": "deepseek-chat",
      "base_url": "https://api.deepseek.com/v1",
      "auth": {"type": "api_key", "token": "${` + tokenEnv + `}"}
    }
  ]
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfgPath), ".env"), []byte(tokenEnv+"=from-config-root-env\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	providers := store.ProviderConfigs()
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}
	if providers[0].Auth.Token != "from-config-root-env" {
		t.Fatalf("expected resolved token from config-root .env, got %q", providers[0].Auth.Token)
	}
}

func TestAppConfig_FailsOnUnresolvedEnvPlaceholder(t *testing.T) {
	const tokenEnv = "CAELIS_TEST_TOKEN_UNRESOLVED"
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	cfgPath, err := configPath("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{
  "version": 1,
  "providers": [
    {
      "alias": "deepseek/deepseek-chat",
      "provider": "deepseek",
      "api": "deepseek",
      "model": "deepseek-chat",
      "base_url": "https://api.deepseek.com/v1",
      "auth": {"type": "api_key", "token": "${` + tokenEnv + `}"}
    }
  ]
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = loadOrInitAppConfig("demo-app")
	if err == nil {
		t.Fatal("expected unresolved placeholder error")
	}
	if !strings.Contains(err.Error(), "invalid config") || !strings.Contains(err.Error(), tokenEnv) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAppConfig_ResolvesAgentServerPlaceholdersAndBuildsRegistry(t *testing.T) {
	const (
		cmdEnv   = "CAELIS_AGENT_CMD"
		tokenEnv = "CAELIS_AGENT_TOKEN"
		dirEnv   = "CAELIS_AGENT_WORKDIR"
	)
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	oldCmd := os.Getenv(cmdEnv)
	oldToken := os.Getenv(tokenEnv)
	oldDir := os.Getenv(dirEnv)
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv(cmdEnv, "caelis-dev"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv(tokenEnv, "secret-token"); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv(dirEnv, "/tmp/caelis-agent"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv(cmdEnv, oldCmd)
		_ = os.Setenv(tokenEnv, oldToken)
		_ = os.Setenv(dirEnv, oldDir)
	})

	cfgPath, err := configPath("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
raw := `{
  "version": 1,
  "defaultAgent": "codex",
  "agents": {
    "codex": {
      "description": "Codex ACP adapter",
      "command": "npx",
      "args": ["@zed-industries/codex-acp"],
      "env": {
        "CODEX_API_KEY": "${` + tokenEnv + `}"
      },
      "stability": "stable"
    },
    "caelis-cli": {
      "description": "Local custom ACP server",
      "command": "${` + cmdEnv + `}",
      "args": ["acp", "--stdio"],
      "env": {
        "CAELIS_AGENT_TOKEN": "${` + tokenEnv + `}"
      },
      "workDir": "${` + dirEnv + `}",
      "stability": "experimental"
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := loadOrInitAppConfig("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	reg, err := store.AgentRegistry()
	if err != nil {
		t.Fatalf("AgentRegistry: %v", err)
	}
	if _, ok := reg.Lookup("self"); !ok {
		t.Fatal("expected builtin self agent")
	}
	descs := store.AgentDescriptors()
	if len(descs) != 2 {
		t.Fatalf("expected 2 configured agent descriptors, got %d", len(descs))
	}
	var custom appagents.Descriptor
	var registryDesc appagents.Descriptor
	for _, d := range descs {
		switch d.ID {
		case "caelis-cli":
			custom = d
		case "codex":
			registryDesc = d
		}
	}
	if custom.Transport != appagents.TransportACP || custom.Command != "caelis-dev" || custom.WorkDir != "/tmp/caelis-agent" {
		t.Fatalf("unexpected custom agent descriptor: %+v", custom)
	}
	if custom.Env["CAELIS_AGENT_TOKEN"] != "secret-token" {
		t.Fatalf("expected resolved custom env, got %+v", custom.Env)
	}
	if registryDesc.Transport != appagents.TransportACP || registryDesc.Command != "npx" {
		t.Fatalf("unexpected registry descriptor: %+v", registryDesc)
	}
	if registryDesc.Env["CODEX_API_KEY"] != "secret-token" {
		t.Fatalf("expected resolved registry env, got %+v", registryDesc.Env)
	}
}

func TestAppConfig_FailsOnLegacyAgentServersKey(t *testing.T) {
	const tokenEnv = "CAELIS_AGENT_TOKEN_UNRESOLVED"
	home := t.TempDir()
	oldHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", oldHome)
	})

	cfgPath, err := configPath("demo-app")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
raw := `{
  "version": 1,
  "agent_servers": {
    "codex": {
      "command": "npx",
      "args": ["@zed-industries/codex-acp"],
      "env": {
        "CODEX_API_KEY": "${` + tokenEnv + `}"
      }
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = loadOrInitAppConfig("demo-app")
	if err == nil {
		t.Fatal("expected legacy agent_servers error")
	}
	if !strings.Contains(err.Error(), "agent_servers") || !strings.Contains(err.Error(), "agents") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAppConfig_AgentRegistryValidatesACPCommands(t *testing.T) {
	store := &appConfigStore{
		path: filepath.Join(t.TempDir(), "config.json"),
		data: appConfig{
			Agents: map[string]agentRecord{
				"codex": {
					Command:   "npx",
					Args:      []string{"@zed-industries/codex-acp"},
					Stability: "stable",
				},
				"broken": {},
			},
		},
	}
	mergeAppConfigDefaults(&store.data)
	_, err := store.AgentRegistry()
	if err == nil {
		t.Fatal("expected invalid ACP agent config to fail validation")
	}
	if !strings.Contains(err.Error(), "requires a command") {
		t.Fatalf("unexpected error: %v", err)
	}

	delete(store.data.Agents, "broken")
	mergeAppConfigDefaults(&store.data)
	reg, err := store.AgentRegistry()
	if err != nil {
		t.Fatalf("configured ACP agent should validate: %v", err)
	}
	if _, ok := reg.Lookup("codex"); !ok {
		t.Fatal("expected codex agent in registry")
	}
}
