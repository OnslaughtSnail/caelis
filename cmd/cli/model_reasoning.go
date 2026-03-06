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
	profile := reasoningProfileForConfig(cfg)
	switch profile.Mode {
	case reasoningModeToggle:
		return []modelReasoningOption{
			{Value: "off", Display: "off", ThinkingMode: "off"},
			{Value: "on", Display: "on", ThinkingMode: "on"},
		}
	case reasoningModeEffort:
		if len(profile.SupportedEfforts) == 0 {
			return nil
		}
		options := make([]modelReasoningOption, 0, len(profile.SupportedEfforts)+1)
		options = append(options, modelReasoningOption{Value: "none", Display: "none", ThinkingMode: "off"})
		for _, effort := range profile.SupportedEfforts {
			options = append(options, modelReasoningOption{
				Value:           effort,
				Display:         effort,
				ThinkingMode:    "on",
				ReasoningEffort: effort,
			})
		}
		return options
	default:
		return nil
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
	profile := reasoningProfileForConfig(cfg)
	switch profile.Mode {
	case reasoningModeToggle:
		if normalized == "off" {
			return modelReasoningOption{Value: "off", Display: "off", ThinkingMode: "off"}, nil
		}
		if normalized == "none" {
			return modelReasoningOption{Value: "off", Display: "off", ThinkingMode: "off"}, nil
		}
		if normalized == "on" {
			return modelReasoningOption{Value: "on", Display: "on", ThinkingMode: "on"}, nil
		}
		return modelReasoningOption{}, fmt.Errorf("reasoning option %q is not supported for this model, expected one of auto|on|off", raw)
	case reasoningModeEffort:
		if normalized == "off" {
			return modelReasoningOption{Value: "off", Display: "off", ThinkingMode: "off"}, nil
		}
		if normalized == "none" {
			return modelReasoningOption{Value: "none", Display: "none", ThinkingMode: "off"}, nil
		}
		if normalized == "on" {
			opt := modelReasoningOption{Value: "on", Display: "on", ThinkingMode: "on"}
			if profile.DefaultEffort != "" {
				opt.Value = profile.DefaultEffort
				opt.Display = profile.DefaultEffort
				opt.ReasoningEffort = profile.DefaultEffort
			}
			return opt, nil
		}
		if len(profile.SupportedEfforts) == 0 {
			if isSupportedReasoningLevel(normalized) {
				return optionFromReasoningLevel(normalized), nil
			}
			return modelReasoningOption{}, fmt.Errorf("invalid reasoning level %q, expected one of none|minimal|low|medium|high|xhigh", raw)
		}
		for _, one := range profile.SupportedEfforts {
			if one == normalized {
				return optionFromReasoningLevel(one), nil
			}
		}
		return modelReasoningOption{}, fmt.Errorf("reasoning level %q is not supported for this model, expected one of %s", raw, strings.Join(profile.SupportedEfforts, "|"))
	default:
		return modelReasoningOption{}, fmt.Errorf("reasoning is not supported for this model")
	}
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

type reasoningProfile struct {
	Mode             string
	SupportedEfforts []string
	DefaultEffort    string
}

func reasoningProfileForConfig(cfg modelproviders.Config) reasoningProfile {
	profile := reasoningProfile{
		Mode:             normalizeCatalogReasoningMode(cfg.ReasoningMode),
		SupportedEfforts: normalizeReasoningLevels(cfg.SupportedReasoningEfforts),
		DefaultEffort:    normalizeReasoningEffort(cfg.DefaultReasoningEffort),
	}
	fallback := inferredReasoningProfile(cfg.Provider, cfg.Model)
	if profile.Mode == reasoningModeNone && fallback.Mode != reasoningModeNone {
		profile = fallback
	}
	if profile.Mode == reasoningModeToggle && fallback.Mode == reasoningModeEffort {
		profile = fallback
	}
	if profile.Mode == reasoningModeEffort && len(profile.SupportedEfforts) == 0 && len(fallback.SupportedEfforts) > 0 {
		profile.SupportedEfforts = append([]string(nil), fallback.SupportedEfforts...)
	}
	if profile.Mode == reasoningModeEffort && profile.DefaultEffort == "" && fallback.DefaultEffort != "" {
		profile.DefaultEffort = fallback.DefaultEffort
	}
	if profile.Mode == "" {
		levels := normalizeReasoningLevels(cfg.ReasoningLevels)
		efforts := make([]string, 0, len(levels))
		for _, level := range levels {
			if level != "none" {
				efforts = append(efforts, level)
			}
		}
		switch {
		case len(efforts) > 0:
			profile.Mode = reasoningModeEffort
			if len(profile.SupportedEfforts) == 0 {
				profile.SupportedEfforts = efforts
			}
		case len(levels) > 0:
			profile.Mode = reasoningModeToggle
		}
	}
	if profile.Mode == "" {
		profile = reasoningProfileForModel(cfg.Provider, cfg.Model)
	}
	if profile.Mode == "" {
		profile.Mode = reasoningModeNone
	}
	if profile.Mode == reasoningModeEffort && profile.DefaultEffort == "" {
		profile.DefaultEffort = defaultCatalogReasoningEffort(cfg.Provider, cfg.Model)
		if profile.DefaultEffort == "" && len(profile.SupportedEfforts) > 0 {
			profile.DefaultEffort = profile.SupportedEfforts[0]
		}
	}
	if profile.Mode != reasoningModeEffort {
		profile.SupportedEfforts = nil
		profile.DefaultEffort = ""
	}
	return profile
}

func reasoningProfileForModel(provider string, model string) reasoningProfile {
	fallback := inferredReasoningProfile(provider, model)
	if caps, found := lookupSuggestedCatalogModelCapabilities(provider, model); found {
		profile := reasoningProfile{
			Mode:             normalizeCatalogReasoningMode(caps.ReasoningMode),
			SupportedEfforts: normalizeReasoningLevels(caps.ReasoningEfforts),
			DefaultEffort:    normalizeReasoningEffort(caps.DefaultReasoningEffort),
		}
		if profile.Mode != "" {
			if profile.Mode == reasoningModeNone && fallback.Mode != reasoningModeNone {
				return fallback
			}
			if profile.Mode == reasoningModeToggle && fallback.Mode == reasoningModeEffort {
				return fallback
			}
			if profile.Mode == reasoningModeEffort && len(profile.SupportedEfforts) == 0 && len(fallback.SupportedEfforts) > 0 {
				profile.SupportedEfforts = append([]string(nil), fallback.SupportedEfforts...)
			}
			if profile.Mode == reasoningModeEffort && profile.DefaultEffort == "" {
				if fallback.DefaultEffort != "" {
					profile.DefaultEffort = fallback.DefaultEffort
				} else if len(profile.SupportedEfforts) > 0 {
					profile.DefaultEffort = profile.SupportedEfforts[0]
				}
			}
			if profile.Mode != reasoningModeEffort {
				profile.SupportedEfforts = nil
				profile.DefaultEffort = ""
			}
			return profile
		}
	}
	return fallback
}

func inferredReasoningProfile(provider string, model string) reasoningProfile {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(provider, "deepseek") || strings.HasPrefix(model, "deepseek-"):
		return reasoningProfile{Mode: reasoningModeToggle}
	case provider == "xiaomi" || provider == "mimo" || strings.Contains(model, "mimo"):
		return reasoningProfile{Mode: reasoningModeToggle}
	case provider == "gemini" || strings.HasPrefix(model, "gemini-"):
		return reasoningProfile{Mode: reasoningModeEffort, SupportedEfforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"}
	case provider == "anthropic" || strings.HasPrefix(model, "claude-"):
		return reasoningProfile{Mode: reasoningModeEffort, SupportedEfforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"}
	case strings.HasPrefix(model, "o1"), strings.HasPrefix(model, "o3"), strings.HasPrefix(model, "o4"):
		efforts := []string{"low", "medium", "high"}
		if strings.HasPrefix(model, "o3") || strings.HasPrefix(model, "o4") {
			efforts = append(efforts, "xhigh")
		}
		return reasoningProfile{Mode: reasoningModeEffort, SupportedEfforts: efforts, DefaultEffort: "medium"}
	default:
		return reasoningProfile{Mode: reasoningModeNone}
	}
}
