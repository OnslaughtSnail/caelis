package main

import (
	"context"
	"net/url"
	"strings"
	"testing"

	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

func TestCompleteModelCandidates_GroupsByProvider(t *testing.T) {
	factory := modelproviders.NewFactory()
	configs := []modelproviders.Config{
		{Alias: "zeta", Provider: "xiaomi", API: modelproviders.APIMimo, Model: "mimo-v2-flash", Auth: modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey}},
		{Alias: "alpha", Provider: "deepseek", API: modelproviders.APIDeepSeek, Model: "deepseek-chat", Auth: modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey}},
		{Alias: "beta", Provider: "xiaomi", API: modelproviders.APIMimo, Model: "mimo-v2-reasoner", Auth: modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey}},
	}
	for _, cfg := range configs {
		modelcatalogApplyConfigDefaults(&cfg)
		if err := factory.Register(cfg); err != nil {
			t.Fatalf("register config: %v", err)
		}
	}

	c := &cliConsole{modelFactory: factory}
	got := c.completeModelCandidates("", 10)
	if len(got) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(got))
	}

	providers := make([]string, 0, len(got))
	for _, one := range got {
		parts := strings.SplitN(one.Display, "/", 2)
		providers = append(providers, parts[0])
	}
	if providers[0] != "deepseek" {
		t.Fatalf("expected deepseek group first, got %v", providers)
	}
	if providers[1] != "xiaomi" || providers[2] != "xiaomi" {
		t.Fatalf("expected xiaomi models grouped together, got %v", providers)
	}
}

func TestShouldHandleAsSlashCommand_AllowsKnownAndTyposButNotPathQuestions(t *testing.T) {
	c := &cliConsole{
		commands: map[string]slashCommand{
			"help":   {Usage: "/help"},
			"status": {Usage: "/status"},
		},
	}
	if !c.shouldHandleAsSlashCommand("/help") {
		t.Fatal("expected known slash command to be handled")
	}
	if !c.shouldHandleAsSlashCommand("/sttaus") {
		t.Fatal("expected command-like typo to still be treated as slash command")
	}
	if c.shouldHandleAsSlashCommand("/v4/ebs/list这个接口的入参都有哪些？") {
		t.Fatal("expected path-like question to bypass slash command handling")
	}
	if c.shouldHandleAsSlashCommand("/v4/ebs/list") {
		t.Fatal("expected path-like endpoint token to bypass slash command handling")
	}
}

func TestCompleteModelCandidates_FiltersByQuery(t *testing.T) {
	factory := modelproviders.NewFactory()
	configs := []modelproviders.Config{
		{Alias: "deepseek/deepseek-chat", Provider: "deepseek", API: modelproviders.APIDeepSeek, Model: "deepseek-chat", Auth: modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey}},
		{Alias: "xiaomi/mimo-v2-flash", Provider: "xiaomi", API: modelproviders.APIMimo, Model: "mimo-v2-flash", Auth: modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey}},
	}
	for _, cfg := range configs {
		modelcatalogApplyConfigDefaults(&cfg)
		if err := factory.Register(cfg); err != nil {
			t.Fatalf("register config: %v", err)
		}
	}

	c := &cliConsole{modelFactory: factory}
	got := c.completeModelCandidates("xiaomi", 10)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	if got[0].Value != "xiaomi/mimo-v2-flash" {
		t.Fatalf("unexpected candidate: %+v", got[0])
	}
}

func TestCompleteModelCandidates_ShowsEndpointOnlyForDuplicateProviderModel(t *testing.T) {
	factory := modelproviders.NewFactory()
	configs := []modelproviders.Config{
		{Alias: "openai-compatible/minimax-m2.5", Provider: "openai-compatible", API: modelproviders.APIOpenAICompatible, Model: "minimax-m2.5", BaseURL: "https://a.example/v1", Auth: modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey}},
		{Alias: "openai-compatible/minimax-m2.5@b_example_v1", Provider: "openai-compatible", API: modelproviders.APIOpenAICompatible, Model: "minimax-m2.5", BaseURL: "https://b.example/v1", Auth: modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey}},
	}
	for _, cfg := range configs {
		modelcatalogApplyConfigDefaults(&cfg)
		if err := factory.Register(cfg); err != nil {
			t.Fatalf("register config: %v", err)
		}
	}

	store := &appConfigStore{data: appConfig{
		Providers: []providerRecord{
			{Alias: configs[0].Alias, Provider: configs[0].Provider, API: string(configs[0].API), Model: configs[0].Model, BaseURL: configs[0].BaseURL},
			{Alias: configs[1].Alias, Provider: configs[1].Provider, API: string(configs[1].API), Model: configs[1].Model, BaseURL: configs[1].BaseURL},
		},
	}}
	c := &cliConsole{modelFactory: factory, configStore: store}
	got := c.completeModelCandidates("", 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(got))
	}
	if !strings.Contains(got[0].Display, "a.example/v1") && !strings.Contains(got[1].Display, "a.example/v1") {
		t.Fatalf("expected duplicate display to include first endpoint, got %+v", got)
	}
	if !strings.Contains(got[0].Display, "b.example/v1") && !strings.Contains(got[1].Display, "b.example/v1") {
		t.Fatalf("expected duplicate display to include second endpoint, got %+v", got)
	}
	filtered := c.completeModelCandidates("b.example", 10)
	if len(filtered) != 0 {
		t.Fatalf("expected endpoint excluded from filter, got %+v", filtered)
	}
}

func TestCompleteModelCommandCandidates_UsesSubcommands(t *testing.T) {
	c := &cliConsole{}
	got := c.completeModelCommandCandidates("u", 10)
	if len(got) != 1 || got[0].Value != "use" {
		t.Fatalf("unexpected model action candidates: %+v", got)
	}
}

func TestCompleteSlashArgCandidates_ModelUseReturnsAliasCandidates(t *testing.T) {
	factory := modelproviders.NewFactory()
	cfg := modelproviders.Config{
		Alias:    "deepseek/deepseek-chat",
		Provider: "deepseek",
		API:      modelproviders.APIDeepSeek,
		Model:    "deepseek-chat",
		Auth:     modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey},
	}
	modelcatalogApplyConfigDefaults(&cfg)
	if err := factory.Register(cfg); err != nil {
		t.Fatalf("register config: %v", err)
	}
	c := &cliConsole{modelFactory: factory}
	got, err := c.completeSlashArgCandidates("model use", "", 10)
	if err != nil {
		t.Fatalf("completeSlashArgCandidates failed: %v", err)
	}
	if len(got) != 1 || got[0].Value != "deepseek/deepseek-chat" {
		t.Fatalf("unexpected alias candidates: %+v", got)
	}
}

func TestCompleteAgentCommandCandidates_UsesSubcommands(t *testing.T) {
	c := &cliConsole{}
	got := c.completeAgentCommandCandidates("r", 10)
	if len(got) != 1 || got[0].Value != "rm" {
		t.Fatalf("unexpected agent action candidates: %+v", got)
	}
}

func TestCompleteSlashArgCandidates_AgentAddReturnsBuiltinCandidates(t *testing.T) {
	c := &cliConsole{}
	got, err := c.completeSlashArgCandidates("agent add", "", 20)
	if err != nil {
		t.Fatalf("completeSlashArgCandidates failed: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected builtin agent candidates")
	}
	values := make([]string, 0, len(got))
	for _, one := range got {
		values = append(values, one.Value)
	}
	if !containsString(values, "codex") || !containsString(values, "copilot") {
		t.Fatalf("expected builtin candidates to include codex and copilot, got %v", values)
	}
}

func TestCompleteSlashArgCandidates_AgentRmReturnsConfiguredCandidates(t *testing.T) {
	store := &appConfigStore{data: appConfig{
		Agents: map[string]agentRecord{
			"copilot": {Command: "copilot", Args: []string{"--acp", "--stdio"}, Stability: appagents.StabilityStable},
			"claude":  {Command: "npx", Args: []string{"-y", "@zed-industries/claude-agent-acp"}},
		},
	}}
	c := &cliConsole{configStore: store}
	got, err := c.completeSlashArgCandidates("agent rm", "", 10)
	if err != nil {
		t.Fatalf("completeSlashArgCandidates failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 configured agent candidates, got %d", len(got))
	}
	if got[0].Value != "claude" || got[1].Value != "copilot" {
		t.Fatalf("unexpected configured agent candidates: %+v", got)
	}
}

func TestCompleteModelReasoningCandidates_FixedModel(t *testing.T) {
	factory := modelproviders.NewFactory()
	cfg := modelproviders.Config{
		Alias:    "deepseek/deepseek-reasoner",
		Provider: "deepseek",
		API:      modelproviders.APIDeepSeek,
		Model:    "deepseek-reasoner",
		Auth:     modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey},
	}
	modelcatalogApplyConfigDefaults(&cfg)
	if err := factory.Register(cfg); err != nil {
		t.Fatalf("register config: %v", err)
	}
	c := &cliConsole{modelFactory: factory}
	got := c.completeModelReasoningCandidates("deepseek/deepseek-reasoner", "", 10)
	if len(got) != 0 {
		t.Fatalf("expected no reasoning candidates for fixed model, got %+v", got)
	}
}

func TestCompleteModelReasoningCandidates_ToggleModelUsesOnOff(t *testing.T) {
	factory := modelproviders.NewFactory()
	cfg := modelproviders.Config{
		Alias:    "xiaomi/mimo-v2-flash",
		Provider: "xiaomi",
		API:      modelproviders.APIMimo,
		Model:    "mimo-v2-flash",
		Auth:     modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey},
	}
	modelcatalogApplyConfigDefaults(&cfg)
	if err := factory.Register(cfg); err != nil {
		t.Fatalf("register config: %v", err)
	}
	c := &cliConsole{modelFactory: factory}
	got := c.completeModelReasoningCandidates("xiaomi/mimo-v2-flash", "", 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 reasoning candidates, got %d", len(got))
	}
	if got[0].Value != "off" || got[1].Value != "on" {
		t.Fatalf("unexpected toggle reasoning candidates: %+v", got)
	}
}

func TestCompleteModelReasoningCandidates_EffortModel(t *testing.T) {
	factory := modelproviders.NewFactory()
	cfg := modelproviders.Config{
		Alias:           "openai/o3",
		Provider:        "openai",
		API:             modelproviders.APIOpenAI,
		Model:           "o3",
		ReasoningLevels: []string{"none", "minimal", "low", "medium", "high", "xhigh"},
		Auth:            modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey},
	}
	modelcatalogApplyConfigDefaults(&cfg)
	if err := factory.Register(cfg); err != nil {
		t.Fatalf("register config: %v", err)
	}
	c := &cliConsole{modelFactory: factory}
	got := c.completeModelReasoningCandidates("openai/o3", "", 10)
	if len(got) != 4 {
		t.Fatalf("expected effort reasoning candidates, got %d", len(got))
	}
	if got[0].Value != "low" || got[3].Value != "xhigh" {
		t.Fatalf("unexpected reasoning candidates: %+v", got)
	}
}

func TestParseModelReasoningPayload(t *testing.T) {
	payload := "model-reasoning:" + url.QueryEscape("deepseek/deepseek-reasoner")
	alias, ok := parseModelReasoningPayload(payload)
	if !ok {
		t.Fatal("expected parse success")
	}
	if alias != "deepseek/deepseek-reasoner" {
		t.Fatalf("unexpected alias %q", alias)
	}
}

func TestCompleteSandboxCandidates_PrioritizesCurrent(t *testing.T) {
	c := &cliConsole{sandboxType: "seatbelt"}
	got := c.completeSandboxCandidates("", 10)
	if len(got) == 0 {
		t.Fatal("expected sandbox candidates")
	}
	if got[0].Value != "seatbelt" {
		t.Fatalf("expected current sandbox first, got %q", got[0].Value)
	}
	if got[0].Display != "seatbelt" {
		t.Fatalf("expected seatbelt display, got %q", got[0].Display)
	}
}

func TestAvailableSandboxTypesForPlatform(t *testing.T) {
	tests := []struct {
		goos string
		want []string
	}{
		{goos: "darwin", want: []string{"seatbelt"}},
		{goos: "linux", want: []string{"bwrap", "landlock"}},
		{goos: "windows", want: []string{"bwrap"}},
	}
	for _, tc := range tests {
		got := availableSandboxTypesForPlatform(tc.goos)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: expected %d sandbox types, got %d (%v)", tc.goos, len(tc.want), len(got), got)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: expected %v, got %v", tc.goos, tc.want, got)
			}
		}
	}
}

func TestCompleteSandboxCandidates_UsesExperimentalDisplayLabel(t *testing.T) {
	c := &cliConsole{sandboxType: "landlock"}
	got := c.completeSandboxCandidates("land", 10)
	if len(got) == 0 {
		t.Fatal("expected sandbox candidates")
	}
	if got[0].Value != "landlock" {
		t.Fatalf("expected landlock value, got %q", got[0].Value)
	}
	if got[0].Display != "landlock" {
		t.Fatalf("expected plain display label, got %q", got[0].Display)
	}
	if got[0].Detail == "" {
		t.Fatal("expected sandbox detail note")
	}
}

func TestCompleteSandboxCandidates_DefaultsToAutoWhenUnset(t *testing.T) {
	c := &cliConsole{}
	got := c.completeSandboxCandidates("", 10)
	if len(got) == 0 {
		t.Fatal("expected sandbox candidates")
	}
	if got[0].Value != "auto" {
		t.Fatalf("expected auto candidate first, got %q", got[0].Value)
	}
	if got[0].Display != "auto" {
		t.Fatalf("unexpected auto display label: %q", got[0].Display)
	}
	if got[0].Detail == "" {
		t.Fatal("expected auto detail note")
	}
}

func TestCompleteConnectCandidates_FiltersByQuery(t *testing.T) {
	c := &cliConsole{}
	got := c.completeConnectCandidates("xiao", 10)
	if len(got) != 1 {
		t.Fatalf("expected 1 connect candidate, got %d", len(got))
	}
	if got[0].Value != "xiaomi" {
		t.Fatalf("unexpected connect candidate: %+v", got[0])
	}
}

func TestCompleteConnectModelCandidates(t *testing.T) {
	c := &cliConsole{}
	got := c.completeConnectModelCandidates("deepseek", "reasoner", 10)
	if len(got) == 0 {
		t.Fatal("expected connect model candidates")
	}
	found := false
	for _, one := range got {
		if one.Value == "deepseek-reasoner" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected deepseek-reasoner in candidates: %+v", got)
	}
}

func TestCompleteConnectBaseURLCandidates(t *testing.T) {
	c := &cliConsole{}
	got := c.completeConnectBaseURLCandidates("openai", "api.openai.com", 10)
	if len(got) == 0 {
		t.Fatal("expected connect base_url candidates")
	}
	if got[0].Value != "https://api.openai.com/v1" {
		t.Fatalf("unexpected connect base_url candidate: %+v", got[0])
	}
}

func TestCompleteConnectTimeoutCandidates(t *testing.T) {
	c := &cliConsole{}
	got := c.completeConnectTimeoutCandidates("6", 10)
	if len(got) == 0 {
		t.Fatal("expected connect timeout candidates")
	}
	found := false
	for _, one := range got {
		if one.Value == "60" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected timeout 60 in candidates: %+v", got)
	}
}

func TestParseConnectModelPayload(t *testing.T) {
	provider, baseURL, timeout, apiKey, ok := parseConnectModelPayload("openai|https%3A%2F%2Fapi.openai.com%2Fv1|60|sk-test")
	if !ok {
		t.Fatal("expected parse success")
	}
	if provider != "openai" || baseURL != "https://api.openai.com/v1" || timeout != 60 || apiKey != "sk-test" {
		t.Fatalf("unexpected payload parse result: provider=%q base_url=%q timeout=%d api_key=%q", provider, baseURL, timeout, apiKey)
	}
}

func TestParseConnectSettingsPayload(t *testing.T) {
	provider, baseURL, timeout, apiKey, model, ok := parseConnectSettingsPayload("openai|https%3A%2F%2Fapi.openai.com%2Fv1|60|sk-test|gpt-4o-mini")
	if !ok {
		t.Fatal("expected parse success")
	}
	if provider != "openai" || baseURL != "https://api.openai.com/v1" || timeout != 60 || apiKey != "sk-test" || model != "gpt-4o-mini" {
		t.Fatalf("unexpected payload parse result: provider=%q base_url=%q timeout=%d api_key=%q model=%q", provider, baseURL, timeout, apiKey, model)
	}
}

func TestCompleteConnectReasoningLevelsCandidates_UnknownModel(t *testing.T) {
	c := &cliConsole{}
	got := c.completeConnectReasoningLevelsCandidates("openai|https%3A%2F%2Fapi.openai.com%2Fv1|60|sk-test|unknown-model", "", 10)
	if len(got) == 0 {
		t.Fatal("expected fallback reasoning-level candidate")
	}
	if got[0].Value != "-" {
		t.Fatalf("expected '-' fallback candidate, got %+v", got[0])
	}
}

func TestFindProviderTemplate(t *testing.T) {
	tpl, ok := findProviderTemplate(" OpenAI-Compatible ")
	if !ok {
		t.Fatal("expected provider template found")
	}
	if tpl.label != "openai-compatible" {
		t.Fatalf("unexpected template: %+v", tpl)
	}
}

func TestCompleteConnectModelCandidatesRemote_UsesCache(t *testing.T) {
	calls := 0
	previous := discoverModelsFn
	discoverModelsFn = func(ctx context.Context, cfg modelproviders.Config) ([]modelproviders.RemoteModel, error) {
		calls++
		return []modelproviders.RemoteModel{
			{Name: "gpt-4o"},
			{Name: "gpt-4o-mini"},
		}, nil
	}
	t.Cleanup(func() {
		discoverModelsFn = previous
	})

	c := &cliConsole{
		baseCtx:           context.Background(),
		connectModelCache: map[string]connectModelCacheEntry{},
	}

	first := c.completeConnectModelCandidatesRemote("openai", "https://api.openai.com/v1", 60, "sk-test", "gpt", 20)
	second := c.completeConnectModelCandidatesRemote("openai", "https://api.openai.com/v1", 60, "sk-test", "mini", 20)

	if calls != 1 {
		t.Fatalf("expected one remote discovery call, got %d", calls)
	}
	if len(first) != 2 {
		t.Fatalf("expected first query candidates, got %d", len(first))
	}
	if len(second) != 1 || second[0].Value != "gpt-4o-mini" {
		t.Fatalf("unexpected second query candidates: %+v", second)
	}
}

func TestReadTUIStatus_ZeroUsageStillShowsContextWindow(t *testing.T) {
	c := &cliConsole{
		modelAlias:       "deepseek/deepseek-chat",
		lastPromptTokens: 0,
		contextWindow:    128000,
	}
	modelText, contextText := c.readTUIStatus()
	if modelText != "deepseek/deepseek-chat" {
		t.Fatalf("unexpected model text %q", modelText)
	}
	if contextText != "0/128.0k(0%)" {
		t.Fatalf("expected zero context usage display, got %q", contextText)
	}
}

func TestReadTUIStatus_UsesConnectedModelContextAndReasoningLabel(t *testing.T) {
	factory := modelproviders.NewFactory()
	cfg := modelproviders.Config{
		Alias:               "gemini/gemini-2.5-pro",
		Provider:            "gemini",
		API:                 modelproviders.APIGemini,
		Model:               "gemini-2.5-pro",
		ContextWindowTokens: 1_000_000,
		Auth:                modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey, Token: "token"},
	}
	modelcatalogApplyConfigDefaults(&cfg)
	if err := factory.Register(cfg); err != nil {
		t.Fatalf("register config: %v", err)
	}

	c := &cliConsole{
		modelAlias:       "gemini/gemini-2.5-pro",
		modelFactory:     factory,
		lastPromptTokens: 5200,
		reasoningEffort:  "high",
	}
	modelText, contextText := c.readTUIStatus()
	if modelText != "gemini/gemini-2.5-pro [high]" {
		t.Fatalf("unexpected model text %q", modelText)
	}
	if contextText != "5.2k/1.0m(0%)" {
		t.Fatalf("expected context ratio display for gemini, got %q", contextText)
	}
}

func TestReadTUIStatus_ShowsFixedReasoningState(t *testing.T) {
	factory := modelproviders.NewFactory()
	cfg := modelproviders.Config{
		Alias:    "deepseek/deepseek-reasoner",
		Provider: "deepseek",
		API:      modelproviders.APIDeepSeek,
		Model:    "deepseek-reasoner",
		Auth:     modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey, Token: "token"},
	}
	modelcatalogApplyConfigDefaults(&cfg)
	if err := factory.Register(cfg); err != nil {
		t.Fatalf("register config: %v", err)
	}

	c := &cliConsole{
		modelAlias:      "deepseek/deepseek-reasoner",
		modelFactory:    factory,
		contextWindow:   128000,
		reasoningEffort: "none",
	}
	modelText, _ := c.readTUIStatus()
	if modelText != "deepseek/deepseek-reasoner [reasoning on]" {
		t.Fatalf("unexpected fixed model text %q", modelText)
	}
}
