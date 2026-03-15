package main

import (
	"testing"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

func TestNormalizeReasoningSelection(t *testing.T) {
	if got := normalizeReasoningSelection("true"); got != "on" {
		t.Fatalf("expected on, got %q", got)
	}
	if got := normalizeReasoningSelection("false"); got != "off" {
		t.Fatalf("expected off, got %q", got)
	}
	if got := normalizeReasoningSelection("very-high"); got != "xhigh" {
		t.Fatalf("expected xhigh, got %q", got)
	}
}

func TestModelReasoningOptionsForConfig(t *testing.T) {
	toggle := modelproviders.Config{
		Provider:      "xiaomi",
		Model:         "mimo-v2-flash",
		API:           modelproviders.APIMimo,
		ReasoningMode: reasoningModeToggle,
	}
	toggleOptions := modelReasoningOptionsForConfig(toggle)
	if len(toggleOptions) != 2 || toggleOptions[0].Value != "off" || toggleOptions[1].Value != "on" {
		t.Fatalf("unexpected toggle options: %+v", toggleOptions)
	}

	effort := modelproviders.Config{
		Provider:                  "openai",
		Model:                     "o3",
		API:                       modelproviders.APIOpenAI,
		ReasoningMode:             reasoningModeEffort,
		SupportedReasoningEfforts: []string{"minimal", "low", "medium", "high", "xhigh"},
	}
	effortOptions := modelReasoningOptionsForConfig(effort)
	if len(effortOptions) != 5 {
		t.Fatalf("expected effort options, got %+v", effortOptions)
	}
	if effortOptions[4].Value != "xhigh" {
		t.Fatalf("expected xhigh option, got %+v", effortOptions)
	}

	fixed := modelproviders.Config{
		Provider:      "deepseek",
		Model:         "deepseek-reasoner",
		API:           modelproviders.APIDeepSeek,
		ReasoningMode: reasoningModeFixed,
	}
	if got := modelReasoningOptionsForConfig(fixed); len(got) != 0 {
		t.Fatalf("expected no configurable options for fixed reasoning, got %+v", got)
	}
}

func TestResolveModelReasoningOption_ToggleRejectsEffort(t *testing.T) {
	cfg := modelproviders.Config{
		Provider:      "xiaomi",
		Model:         "mimo-v2-flash",
		API:           modelproviders.APIMimo,
		ReasoningMode: reasoningModeToggle,
	}
	if _, err := resolveModelReasoningOption(cfg, "high"); err == nil {
		t.Fatal("expected error for high on toggle model")
	}
	opt, err := resolveModelReasoningOption(cfg, "off")
	if err != nil {
		t.Fatal(err)
	}
	if opt.ReasoningEffort != "none" {
		t.Fatalf("unexpected option: %+v", opt)
	}
	opt, err = resolveModelReasoningOption(cfg, "on")
	if err != nil {
		t.Fatal(err)
	}
	if opt.ReasoningEffort != "medium" || opt.Value != "on" {
		t.Fatalf("unexpected toggle on option: %+v", opt)
	}
}

func TestResolveModelReasoningOption_EffortUsesDefaultForOn(t *testing.T) {
	cfg := modelproviders.Config{
		Provider:                  "openai",
		Model:                     "o3",
		API:                       modelproviders.APIOpenAI,
		ReasoningMode:             reasoningModeEffort,
		SupportedReasoningEfforts: []string{"low", "medium", "high"},
		DefaultReasoningEffort:    "medium",
	}
	opt, err := resolveModelReasoningOption(cfg, "on")
	if err != nil {
		t.Fatal(err)
	}
	if opt.ReasoningEffort != "medium" {
		t.Fatalf("unexpected option: %+v", opt)
	}
}

func TestResolveModelReasoningOption_EffortRejectsOffWhenNoneUnsupported(t *testing.T) {
	cfg := modelproviders.Config{
		Provider:                  "openai-compatible",
		API:                       modelproviders.APIOpenAICompatible,
		Model:                     "glm-5",
		ReasoningMode:             reasoningModeToggle,
		SupportedReasoningEfforts: []string{"low", "medium", "high"},
		DefaultReasoningEffort:    "medium",
	}
	if _, err := resolveModelReasoningOption(cfg, "off"); err == nil {
		t.Fatal("expected off to be rejected when none is unsupported")
	}
}

func TestParseReasoning_AcceptsTrueFalse(t *testing.T) {
	on, err := parseReasoning("true", 1024, "", "deepseek", "deepseek-reasoner")
	if err != nil {
		t.Fatal(err)
	}
	if on.Effort != "" || on.BudgetTokens != 0 {
		t.Fatalf("expected fixed reasoning to ignore explicit enable, got %+v", on)
	}
	off, err := parseReasoning("false", 1024, "", "deepseek", "deepseek-reasoner")
	if err != nil {
		t.Fatal(err)
	}
	if off.Effort != "" || off.BudgetTokens != 0 {
		t.Fatalf("expected fixed reasoning to ignore explicit disable, got %+v", off)
	}
}

func TestParseReasoning_AutoKeepsUnsetWithoutExplicitLevel(t *testing.T) {
	cfg, err := parseReasoning("auto", 1024, "", "deepseek", "deepseek-reasoner")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Effort != "" {
		t.Fatalf("expected auto reasoning unset, got %+v", cfg)
	}
}

func TestParseReasoning_GeminiHighDoesNotForceBudget(t *testing.T) {
	cfg, err := parseReasoning("on", 1024, "high", "gemini", "gemini-2.5-pro")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BudgetTokens != 1024 {
		t.Fatalf("expected budget unchanged, got %+v", cfg)
	}
}

func TestParseReasoning_AcceptsXHighAsUserInput(t *testing.T) {
	cfg, err := parseReasoning("on", 1024, "xhigh", "openai", "o3")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Effort != "xhigh" {
		t.Fatalf("expected xhigh effort, got %+v", cfg)
	}
}

func TestParseReasoning_NoneDisablesReasoning(t *testing.T) {
	cfg, err := parseReasoning("on", 1024, "none", "gemini", "gemini-2.5-pro")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Effort != "medium" || cfg.BudgetTokens != 1024 {
		t.Fatalf("expected unsupported none to fall back to default effort, got %+v", cfg)
	}
}

func TestParseReasoning_ToggleModelClearsEffort(t *testing.T) {
	cfg, err := parseReasoning("on", 1024, "low", "deepseek", "deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Effort != "" || cfg.BudgetTokens != 0 {
		t.Fatalf("expected non-reasoning model to clear effort, got %+v", cfg)
	}
}

func TestResolveModelReasoningOption_FixedModelRejectsSelection(t *testing.T) {
	cfg := modelproviders.Config{
		Provider:      "deepseek",
		Model:         "deepseek-reasoner",
		API:           modelproviders.APIDeepSeek,
		ReasoningMode: reasoningModeFixed,
	}
	if _, err := resolveModelReasoningOption(cfg, "none"); err == nil {
		t.Fatal("expected fixed reasoning model to reject explicit selection")
	}
	if _, err := resolveModelReasoningOption(cfg, "auto"); err == nil {
		t.Fatal("expected fixed reasoning model to reject auto selection")
	}
}

func TestParseReasoning_EffortModelUsesDefaultWhenMissing(t *testing.T) {
	cfg, err := parseReasoning("on", 1024, "", "gemini", "gemini-2.5-pro")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Effort != "medium" {
		t.Fatalf("expected default effort medium, got %+v", cfg)
	}
}

func TestParseReasoningForConfig_UsesConfiguredToggleMode(t *testing.T) {
	disabledCfg, err := parseReasoningForConfig("off", 1024, "", "openai-compatible", "doubao-seed-2.0-pro", modelproviders.Config{
		Provider:      "openai-compatible",
		API:           modelproviders.APIOpenAICompatible,
		Model:         "doubao-seed-2.0-pro",
		ReasoningMode: reasoningModeToggle,
	})
	if err != nil {
		t.Fatal(err)
	}
	if disabledCfg.Effort != "none" {
		t.Fatalf("expected openai-compatible config to normalize off to none, got %+v", disabledCfg)
	}
}

func TestModelReasoningOptionsForConfig_OpenAICompatibleUsesStandardEfforts(t *testing.T) {
	options := modelReasoningOptionsForConfig(modelproviders.Config{
		Provider:      "openai-compatible",
		API:           modelproviders.APIOpenAICompatible,
		Model:         "glm-5",
		ReasoningMode: reasoningModeToggle,
	})
	want := []string{"none", "minimal", "low", "medium", "high", "xhigh"}
	if len(options) != len(want) {
		t.Fatalf("unexpected options: %+v", options)
	}
	for i, one := range want {
		if options[i].Value != one {
			t.Fatalf("unexpected option[%d]=%q want %q", i, options[i].Value, one)
		}
	}
}

func TestModelReasoningOptionsForConfig_OpenAICompatibleUsesConfiguredSubset(t *testing.T) {
	options := modelReasoningOptionsForConfig(modelproviders.Config{
		Provider:                  "openai-compatible",
		API:                       modelproviders.APIOpenAICompatible,
		Model:                     "glm-5",
		ReasoningMode:             reasoningModeEffort,
		SupportedReasoningEfforts: []string{"low", "medium", "high"},
		DefaultReasoningEffort:    "medium",
	})
	want := []string{"low", "medium", "high"}
	if len(options) != len(want) {
		t.Fatalf("unexpected options: %+v", options)
	}
	for i, one := range want {
		if options[i].Value != one {
			t.Fatalf("unexpected option[%d]=%q want %q", i, options[i].Value, one)
		}
	}
}

func TestModelReasoningOptionsForConfig_OpenRouterUsesStandardEfforts(t *testing.T) {
	options := modelReasoningOptionsForConfig(modelproviders.Config{
		Provider:      "openrouter",
		API:           modelproviders.APIOpenRouter,
		Model:         "openai/gpt-4o-mini",
		ReasoningMode: reasoningModeToggle,
	})
	want := []string{"none", "minimal", "low", "medium", "high", "xhigh"}
	if len(options) != len(want) {
		t.Fatalf("unexpected options: %+v", options)
	}
	for i, one := range want {
		if options[i].Value != one {
			t.Fatalf("unexpected option[%d]=%q want %q", i, options[i].Value, one)
		}
	}
}
