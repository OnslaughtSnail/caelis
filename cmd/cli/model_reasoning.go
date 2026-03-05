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
	levels := normalizeReasoningLevels(cfg.ReasoningLevels)
	if len(levels) == 0 {
		return nil
	}
	options := make([]modelReasoningOption, 0, len(levels))
	for _, level := range levels {
		opt := modelReasoningOption{
			Value:           level,
			Display:         level,
			ThinkingMode:    "on",
			ReasoningEffort: level,
		}
		if level == "none" {
			opt.ThinkingMode = "off"
			opt.ReasoningEffort = ""
		}
		options = append(options, opt)
	}
	return options
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
		return modelReasoningOption{Value: "on", Display: "on", ThinkingMode: "on"}, nil
	}
	if normalized == "off" {
		return modelReasoningOption{Value: "off", Display: "off", ThinkingMode: "off"}, nil
	}

	levels := normalizeReasoningLevels(cfg.ReasoningLevels)
	if len(levels) == 0 {
		if isSupportedReasoningLevel(normalized) {
			return optionFromReasoningLevel(normalized), nil
		}
		return modelReasoningOption{}, fmt.Errorf("invalid reasoning level %q, expected one of none|minimal|low|medium|high|xhigh", raw)
	}
	for _, one := range levels {
		if one == normalized {
			return optionFromReasoningLevel(one), nil
		}
	}
	return modelReasoningOption{}, fmt.Errorf("reasoning level %q is not configured for this model, expected one of %s", raw, strings.Join(levels, "|"))
}

func optionFromReasoningLevel(level string) modelReasoningOption {
	opt := modelReasoningOption{
		Value:           level,
		Display:         level,
		ThinkingMode:    "on",
		ReasoningEffort: level,
	}
	if level == "none" {
		opt.ThinkingMode = "off"
		opt.ReasoningEffort = ""
	}
	return opt
}

func normalizeReasoningSelection(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "on", "true", "enabled", "enable", "1":
		return "on"
	case "off", "false", "disabled", "disable", "0":
		return "off"
	case "auto":
		return "auto"
	default:
		return normalizeReasoningLevel(value)
	}
}

func isSupportedReasoningLevel(value string) bool {
	switch normalizeReasoningLevel(value) {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}
