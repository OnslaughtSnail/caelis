package main

import (
	"fmt"
	"strings"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

type modelReasoningOption struct {
	Value           string
	Display         string
	ThinkingMode    string
	ReasoningEffort string
}

func modelReasoningOptionsForConfig(cfg modelproviders.Config) []modelReasoningOption {
	supportsReasoning := modelSupportsReasoning(cfg.Provider, cfg.Model)
	if !supportsReasoning {
		return []modelReasoningOption{{
			Value:        "off",
			Display:      "off",
			ThinkingMode: "off",
		}}
	}
	if isToggleReasoningModel(cfg) {
		return []modelReasoningOption{
			{Value: "off", Display: "off", ThinkingMode: "off"},
			{Value: "on", Display: "on", ThinkingMode: "on"},
		}
	}
	return []modelReasoningOption{
		{Value: "off", Display: "off", ThinkingMode: "off"},
		{Value: "low", Display: "low", ThinkingMode: "on", ReasoningEffort: "low"},
		{Value: "medium", Display: "medium", ThinkingMode: "on", ReasoningEffort: "medium"},
		{Value: "high", Display: "high", ThinkingMode: "on", ReasoningEffort: "high"},
		{Value: "very_high", Display: "very_high", ThinkingMode: "on", ReasoningEffort: "very_high"},
	}
}

func resolveModelReasoningOption(cfg modelproviders.Config, raw string) (modelReasoningOption, error) {
	normalized := normalizeReasoningSelection(raw)
	if normalized == "" {
		return modelReasoningOption{}, fmt.Errorf("reasoning option cannot be empty")
	}
	if normalized == "auto" {
		return modelReasoningOption{
			Value:        "auto",
			Display:      "auto",
			ThinkingMode: "auto",
		}, nil
	}
	if normalized == "on" {
		if !modelSupportsReasoning(cfg.Provider, cfg.Model) {
			return modelReasoningOption{}, fmt.Errorf("model %q does not support reasoning", strings.TrimSpace(cfg.Model))
		}
		return modelReasoningOption{Value: "on", Display: "on", ThinkingMode: "on"}, nil
	}
	if normalized == "off" {
		return modelReasoningOption{Value: "off", Display: "off", ThinkingMode: "off"}, nil
	}

	options := modelReasoningOptionsForConfig(cfg)
	for _, one := range options {
		if one.Value == normalized {
			return one, nil
		}
	}
	allowed := make([]string, 0, len(options)+1)
	for _, one := range options {
		allowed = append(allowed, one.Value)
	}
	allowed = append(allowed, "auto")
	return modelReasoningOption{}, fmt.Errorf("invalid reasoning option %q, expected one of %s", raw, strings.Join(allowed, "|"))
}

func normalizeReasoningSelection(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case "on", "true", "enabled", "enable", "1":
		return "on"
	case "off", "false", "disabled", "disable", "0":
		return "off"
	case "very-high", "veryhigh":
		return "very_high"
	default:
		return value
	}
}

func modelSupportsReasoning(provider string, modelName string) bool {
	if caps, found := modelproviders.LookupModelCapabilities(provider, modelName); found && caps.SupportsReasoning {
		return true
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if provider == "" || modelName == "" {
		return false
	}
	if strings.Contains(modelName, "reasoner") || strings.Contains(modelName, "thinking") {
		return true
	}
	if strings.Contains(provider, "deepseek") && strings.HasPrefix(modelName, "deepseek-chat") {
		return true
	}
	if isMimoModel(provider, modelName) {
		return true
	}
	return false
}

func isToggleReasoningModel(cfg modelproviders.Config) bool {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	modelName := strings.ToLower(strings.TrimSpace(cfg.Model))
	if cfg.API == modelproviders.APIDeepSeek {
		return true
	}
	if strings.Contains(provider, "deepseek") || strings.HasPrefix(modelName, "deepseek-") {
		return true
	}
	if isMimoModel(provider, modelName) {
		return true
	}
	return false
}

func isMimoModel(provider string, modelName string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if provider == "xiaomi" || provider == "mimo" {
		return true
	}
	return strings.Contains(modelName, "mimo")
}
