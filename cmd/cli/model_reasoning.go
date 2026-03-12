package main

import (
	"fmt"
	"strings"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

var openAICompatibleStandardEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh"}

type modelReasoningOption struct {
	Value           string
	Display         string
	ReasoningEffort string
}

func modelReasoningOptionsForConfig(cfg modelproviders.Config) []modelReasoningOption {
	profile := reasoningProfileForConfig(cfg)
	switch profile.Mode {
	case reasoningModeToggle:
		defaultEffort := reasoningProfileDefaultEffort(profile)
		return []modelReasoningOption{
			toggleReasoningOption(false),
			toggleReasoningOptionWithEffort(defaultEffort),
		}
	case reasoningModeEffort:
		if len(profile.SupportedEfforts) == 0 {
			return nil
		}
		options := make([]modelReasoningOption, 0, len(profile.SupportedEfforts))
		for _, effort := range profile.SupportedEfforts {
			options = append(options, modelReasoningOption{
				Value:           effort,
				Display:         effort,
				ReasoningEffort: effort,
			})
		}
		return options
	case reasoningModeFixed:
		return nil
	default:
		return nil
	}
}

func resolveModelReasoningOption(cfg modelproviders.Config, raw string) (modelReasoningOption, error) {
	normalized := normalizeReasoningSelection(raw)
	if normalized == "" {
		return modelReasoningOption{}, fmt.Errorf("reasoning option cannot be empty")
	}
	profile := reasoningProfileForConfig(cfg)
	switch profile.Mode {
	case reasoningModeFixed:
		return modelReasoningOption{}, fmt.Errorf("reasoning is fixed for this model")
	case reasoningModeToggle:
		if normalized == "auto" {
			return modelReasoningOption{
				Value:           "auto",
				Display:         "auto",
				ReasoningEffort: "",
			}, nil
		}
		defaultEffort := reasoningProfileDefaultEffort(profile)
		if normalized == "off" || normalized == "none" {
			return toggleReasoningOption(false), nil
		}
		if normalized == "on" {
			return toggleReasoningOptionWithEffort(defaultEffort), nil
		}
		if normalized == defaultEffort {
			return toggleReasoningOptionWithEffort(defaultEffort), nil
		}
		return modelReasoningOption{}, fmt.Errorf("reasoning option %q is not supported for this model, expected one of auto|off|on", raw)
	case reasoningModeEffort:
		if normalized == "auto" {
			return modelReasoningOption{
				Value:           "auto",
				Display:         "auto",
				ReasoningEffort: "",
			}, nil
		}
		if normalized == "off" {
			normalized = "none"
		}
		if normalized == "on" {
			return optionFromReasoningLevel(reasoningProfileDefaultEffort(profile)), nil
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
	level = normalizeReasoningLevel(level)
	if level == "" {
		level = "medium"
	}
	opt := modelReasoningOption{
		Value:           level,
		Display:         level,
		ReasoningEffort: level,
	}
	if level == "none" {
		opt.ReasoningEffort = "none"
	}
	return opt
}

func toggleReasoningOption(enabled bool) modelReasoningOption {
	if enabled {
		return toggleReasoningOptionWithEffort("medium")
	}
	return modelReasoningOption{
		Value:           "off",
		Display:         "off",
		ReasoningEffort: "none",
	}
}

func toggleReasoningOptionWithEffort(effort string) modelReasoningOption {
	effort = normalizeReasoningLevel(effort)
	if effort == "" || effort == "none" {
		effort = "medium"
	}
	return modelReasoningOption{
		Value:           "on",
		Display:         "on",
		ReasoningEffort: effort,
	}
}

func reasoningProfileDefaultEffort(profile reasoningProfile) string {
	if effort := normalizeReasoningLevel(profile.DefaultEffort); effort != "" && effort != "none" {
		return effort
	}
	if len(profile.SupportedEfforts) > 0 {
		if effort := normalizeReasoningLevel(profile.SupportedEfforts[0]); effort != "" && effort != "none" {
			return effort
		}
	}
	return "medium"
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

func canonicalOpenAICompatibleReasoningProfile(cfg modelproviders.Config) (reasoningProfile, bool) {
	if cfg.API != modelproviders.APIOpenAICompatible {
		return reasoningProfile{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.Provider), "openai-compatible") {
		return reasoningProfile{}, false
	}
	supported := normalizeReasoningLevels(cfg.SupportedReasoningEfforts)
	if len(supported) == 0 {
		supported = append([]string(nil), openAICompatibleStandardEfforts...)
	}
	defaultEffort := normalizeReasoningEffort(cfg.DefaultReasoningEffort)
	if defaultEffort == "none" {
		defaultEffort = ""
	}
	if defaultEffort == "" {
		defaultEffort = "medium"
	}
	return reasoningProfile{
		Mode:             reasoningModeEffort,
		SupportedEfforts: supported,
		DefaultEffort:    defaultEffort,
	}, true
}

func reasoningProfileForConfig(cfg modelproviders.Config) reasoningProfile {
	if profile, ok := canonicalOpenAICompatibleReasoningProfile(cfg); ok {
		return profile
	}
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
			efforts = append(efforts, level)
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
		if profile.Mode != reasoningModeFixed {
			profile.DefaultEffort = ""
		}
	}
	return profile
}

func reasoningProfileForModel(provider string, model string) reasoningProfile {
	if strings.EqualFold(strings.TrimSpace(provider), "openai-compatible") {
		return reasoningProfile{
			Mode:             reasoningModeEffort,
			SupportedEfforts: append([]string(nil), openAICompatibleStandardEfforts...),
			DefaultEffort:    "medium",
		}
	}
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
				if profile.Mode != reasoningModeFixed {
					profile.DefaultEffort = ""
				}
			}
			return profile
		}
	}
	return fallback
}

func inferredReasoningProfile(provider string, model string) reasoningProfile {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch {
	case provider == "openai-compatible":
		return reasoningProfile{Mode: reasoningModeEffort, SupportedEfforts: append([]string(nil), openAICompatibleStandardEfforts...), DefaultEffort: "medium"}
	default:
		return reasoningProfile{Mode: reasoningModeNone}
	}
}
