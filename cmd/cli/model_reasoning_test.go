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
	if got := normalizeReasoningSelection("very-high"); got != "very_high" {
		t.Fatalf("expected very_high, got %q", got)
	}
}

func TestModelReasoningOptionsForConfig(t *testing.T) {
	toggle := modelproviders.Config{Provider: "deepseek", Model: "deepseek-chat", API: modelproviders.APIDeepSeek}
	toggleOptions := modelReasoningOptionsForConfig(toggle)
	if len(toggleOptions) != 2 || toggleOptions[0].Value != "off" || toggleOptions[1].Value != "on" {
		t.Fatalf("unexpected toggle options: %+v", toggleOptions)
	}

	effort := modelproviders.Config{Provider: "openai", Model: "o3", API: modelproviders.APIOpenAI}
	effortOptions := modelReasoningOptionsForConfig(effort)
	if len(effortOptions) < 5 {
		t.Fatalf("expected effort options, got %+v", effortOptions)
	}
	if effortOptions[4].Value != "very_high" {
		t.Fatalf("expected very_high option, got %+v", effortOptions)
	}
}

func TestResolveModelReasoningOption_ToggleRejectsEffort(t *testing.T) {
	cfg := modelproviders.Config{Provider: "deepseek", Model: "deepseek-chat", API: modelproviders.APIDeepSeek}
	if _, err := resolveModelReasoningOption(cfg, "high"); err == nil {
		t.Fatal("expected error for high on toggle model")
	}
	opt, err := resolveModelReasoningOption(cfg, "on")
	if err != nil {
		t.Fatal(err)
	}
	if opt.ThinkingMode != "on" || opt.ReasoningEffort != "" {
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

func TestParseReasoning_AutoEnablesDeepSeekChatFallback(t *testing.T) {
	cfg, err := parseReasoning("auto", 1024, "", "deepseek", "deepseek-chat")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled == nil || !*cfg.Enabled {
		t.Fatalf("expected auto reasoning enabled for deepseek-chat, got %+v", cfg)
	}
}
