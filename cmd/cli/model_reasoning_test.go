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
		Provider:        "deepseek",
		Model:           "deepseek-chat",
		API:             modelproviders.APIDeepSeek,
		ReasoningLevels: []string{"none", "high"},
	}
	toggleOptions := modelReasoningOptionsForConfig(toggle)
	if len(toggleOptions) != 2 || toggleOptions[0].Value != "none" || toggleOptions[1].Value != "high" {
		t.Fatalf("unexpected toggle options: %+v", toggleOptions)
	}

	effort := modelproviders.Config{
		Provider:        "openai",
		Model:           "o3",
		API:             modelproviders.APIOpenAI,
		ReasoningLevels: []string{"none", "minimal", "low", "medium", "high", "xhigh"},
	}
	effortOptions := modelReasoningOptionsForConfig(effort)
	if len(effortOptions) != 6 {
		t.Fatalf("expected effort options, got %+v", effortOptions)
	}
	if effortOptions[5].Value != "xhigh" {
		t.Fatalf("expected xhigh option, got %+v", effortOptions)
	}
}

func TestResolveModelReasoningOption_ToggleRejectsEffort(t *testing.T) {
	cfg := modelproviders.Config{
		Provider:        "deepseek",
		Model:           "deepseek-chat",
		API:             modelproviders.APIDeepSeek,
		ReasoningLevels: []string{"none"},
	}
	if _, err := resolveModelReasoningOption(cfg, "high"); err == nil {
		t.Fatal("expected error for high on toggle model")
	}
	opt, err := resolveModelReasoningOption(cfg, "none")
	if err != nil {
		t.Fatal(err)
	}
	if opt.ThinkingMode != "off" || opt.ReasoningEffort != "" {
		t.Fatalf("unexpected option: %+v", opt)
	}
}

func TestParseReasoning_AcceptsTrueFalse(t *testing.T) {
	on, err := parseReasoning("true", 1024, "", "deepseek", "deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	if on.Enabled == nil || !*on.Enabled {
		t.Fatalf("expected reasoning enabled, got %+v", on)
	}
	off, err := parseReasoning("false", 1024, "", "deepseek", "deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	if off.Enabled == nil || *off.Enabled {
		t.Fatalf("expected reasoning disabled, got %+v", off)
	}
}

func TestParseReasoning_AutoKeepsUnsetWithoutExplicitLevel(t *testing.T) {
	cfg, err := parseReasoning("auto", 1024, "", "deepseek", "deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled != nil {
		t.Fatalf("expected auto reasoning unset, got %+v", cfg)
	}
}

func TestParseReasoning_GeminiHighDoesNotForceBudget(t *testing.T) {
	cfg, err := parseReasoning("on", 1024, "high", "gemini", "gemini-3.1-flash-lite-preview")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BudgetTokens != 1024 {
		t.Fatalf("expected budget unchanged, got %+v", cfg)
	}
}

func TestParseReasoning_AcceptsXHighAsUserInput(t *testing.T) {
	cfg, err := parseReasoning("on", 1024, "xhigh", "gemini", "gemini-3.1-flash-lite-preview")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Effort != "xhigh" {
		t.Fatalf("expected xhigh effort, got %+v", cfg)
	}
}

func TestParseReasoning_NoneDisablesReasoning(t *testing.T) {
	cfg, err := parseReasoning("on", 1024, "none", "gemini", "gemini-3.1-flash-lite-preview")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled == nil || *cfg.Enabled {
		t.Fatalf("expected reasoning disabled for none, got %+v", cfg)
	}
	if cfg.Effort != "" || cfg.BudgetTokens != 0 {
		t.Fatalf("expected none to clear effort/budget, got %+v", cfg)
	}
}
