package main

import (
	"context"
	"net/url"
	"strings"
	"testing"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

func TestCompleteModelCandidates_GroupsByProvider(t *testing.T) {
	factory := modelproviders.NewFactory()
	configs := []modelproviders.Config{
		{Alias: "zeta", Provider: "xiaomi", API: modelproviders.APIOpenAICompatible, Model: "mimo-v2-flash", Auth: modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey}},
		{Alias: "alpha", Provider: "deepseek", API: modelproviders.APIDeepSeek, Model: "deepseek-chat", Auth: modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey}},
		{Alias: "beta", Provider: "xiaomi", API: modelproviders.APIOpenAICompatible, Model: "mimo-v2-reasoner", Auth: modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey}},
	}
	for _, cfg := range configs {
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

func TestCompleteModelCandidates_FiltersByQuery(t *testing.T) {
	factory := modelproviders.NewFactory()
	configs := []modelproviders.Config{
		{Alias: "deepseek/deepseek-chat", Provider: "deepseek", API: modelproviders.APIDeepSeek, Model: "deepseek-chat", Auth: modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey}},
		{Alias: "xiaomi/mimo-v2-flash", Provider: "xiaomi", API: modelproviders.APIOpenAICompatible, Model: "mimo-v2-flash", Auth: modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey}},
	}
	for _, cfg := range configs {
		if err := factory.Register(cfg); err != nil {
			t.Fatalf("register config: %v", err)
		}
	}

	c := &cliConsole{modelFactory: factory}
	got := c.completeModelCandidates("mimo", 10)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	if got[0].Value != "xiaomi/mimo-v2-flash" {
		t.Fatalf("unexpected candidate: %+v", got[0])
	}
}

func TestCompleteModelReasoningCandidates_ToggleModel(t *testing.T) {
	factory := modelproviders.NewFactory()
	cfg := modelproviders.Config{
		Alias:    "deepseek/deepseek-chat",
		Provider: "deepseek",
		API:      modelproviders.APIDeepSeek,
		Model:    "deepseek-chat",
		Auth:     modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey},
	}
	if err := factory.Register(cfg); err != nil {
		t.Fatalf("register config: %v", err)
	}
	c := &cliConsole{modelFactory: factory}
	got := c.completeModelReasoningCandidates("deepseek/deepseek-chat", "", 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 reasoning candidates, got %d", len(got))
	}
	if got[0].Value != "off" || got[1].Value != "on" {
		t.Fatalf("unexpected reasoning candidates: %+v", got)
	}
}

func TestCompleteModelReasoningCandidates_EffortModel(t *testing.T) {
	factory := modelproviders.NewFactory()
	cfg := modelproviders.Config{
		Alias:    "openai/o3",
		Provider: "openai",
		API:      modelproviders.APIOpenAI,
		Model:    "o3",
		Auth:     modelproviders.AuthConfig{Type: modelproviders.AuthAPIKey},
	}
	if err := factory.Register(cfg); err != nil {
		t.Fatalf("register config: %v", err)
	}
	c := &cliConsole{modelFactory: factory}
	got := c.completeModelReasoningCandidates("openai/o3", "", 10)
	if len(got) < 5 {
		t.Fatalf("expected effort reasoning candidates, got %d", len(got))
	}
	if got[0].Value != "off" || got[4].Value != "very_high" {
		t.Fatalf("unexpected reasoning candidates: %+v", got)
	}
}

func TestParseModelReasoningPayload(t *testing.T) {
	payload := "model-reasoning:" + url.QueryEscape("deepseek/deepseek-chat")
	alias, ok := parseModelReasoningPayload(payload)
	if !ok {
		t.Fatal("expected parse success")
	}
	if alias != "deepseek/deepseek-chat" {
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

func TestCompletePermissionCandidates(t *testing.T) {
	c := &cliConsole{}
	got := c.completePermissionCandidates("", 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 permission candidates, got %d", len(got))
	}
	if got[0].Value != "default" || got[1].Value != "full_control" {
		t.Fatalf("unexpected permission candidates: %+v", got)
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
