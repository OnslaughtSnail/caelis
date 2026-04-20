package tuiapp

import (
	"net/url"
	"strings"
)

// defaults.go provides DefaultCommands and DefaultWizards for the TUI shell.

// joinNonEmpty joins non-empty parts with the given separator.
func joinNonEmpty(parts []string, sep string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

// DefaultCommands returns the set of slash commands available in the TUI.
func DefaultCommands() []string {
	return []string{
		"help",
		"exit",
		"quit",
		"new",
		"status",
		"model",
		"sandbox",
		"connect",
		"resume",
	}
}

// DefaultWizards returns the set of multi-step wizard flows for the TUI.
func DefaultWizards() []WizardDef {
	return []WizardDef{
		connectWizard(),
	}
}

func connectWizard() WizardDef {
	return WizardDef{
		Command:     "connect",
		DisplayLine: "/connect",
		Steps: []WizardStepDef{
			{
				Key:       "provider",
				HintLabel: "/connect provider",
				CompletionCommand: func(state map[string]string) string {
					return "connect"
				},
			},
			{
				Key:       "endpoint",
				HintLabel: "/connect endpoint",
				CompletionCommand: func(state map[string]string) string {
					return "connect-baseurl:" + state["provider"]
				},
				ShouldSkip: func(state map[string]string) bool {
					return !strings.EqualFold(strings.TrimSpace(state["provider"]), "volcengine")
				},
			},
			{
				Key:       "baseurl",
				HintLabel: "/connect base_url",
				CompletionCommand: func(state map[string]string) string {
					return "connect-baseurl:" + state["provider"]
				},
				ShouldSkip: func(state map[string]string) bool {
					switch strings.ToLower(strings.TrimSpace(state["provider"])) {
					case "openai-compatible", "anthropic-compatible":
						return false
					default:
						return true
					}
				},
			},
			{
				Key:          "apikey",
				HintLabel:    "/connect api_key",
				HideInput:    true,
				FreeformHint: "/connect api_key: type and press enter",
				CompletionCommand: func(state map[string]string) string {
					return "connect-apikey:" + state["provider"]
				},
				ShouldSkip: func(state map[string]string) bool {
					return state["_noauth"] == "true"
				},
			},
			{
				Key:          "model",
				HintLabel:    "/connect model",
				FreeformHint: "/connect model: type model name and press enter",
				CompletionCommand: func(state map[string]string) string {
					return "connect-model:" + buildConnectWizardPayload(state)
				},
			},
			{
				Key:          "context_window_tokens",
				HintLabel:    "/connect context_window_tokens",
				Validate:     ValidateInt,
				FreeformHint: "/connect context_window_tokens: type integer and press enter",
				CompletionCommand: func(state map[string]string) string {
					return "connect-context:" + buildConnectWizardPayload(state)
				},
				ShouldSkip: func(state map[string]string) bool {
					return state["_known_model"] == "true"
				},
			},
			{
				Key:          "max_output_tokens",
				HintLabel:    "/connect max_output_tokens",
				Validate:     ValidateInt,
				FreeformHint: "/connect max_output_tokens: type integer and press enter",
				CompletionCommand: func(state map[string]string) string {
					return "connect-maxout:" + buildConnectWizardPayload(state)
				},
				ShouldSkip: func(state map[string]string) bool {
					return state["_known_model"] == "true"
				},
			},
			{
				Key:          "reasoning_levels",
				HintLabel:    "/connect reasoning_levels(csv)",
				FreeformHint: "/connect reasoning_levels(csv): e.g. low,medium (use - for empty)",
				CompletionCommand: func(state map[string]string) string {
					return "connect-reasoning-levels:" + buildConnectWizardPayload(state)
				},
				ShouldSkip: func(state map[string]string) bool {
					return state["_known_model"] == "true"
				},
			},
		},
		OnStepConfirm: func(stepKey string, value string, candidate *SlashArgCandidate, state map[string]string) {
			if stepKey == "provider" {
				state["provider"] = strings.ToLower(strings.TrimSpace(value))
			}
			if stepKey == "provider" && candidate != nil && candidate.NoAuth {
				state["_noauth"] = "true"
			}
			if stepKey == "endpoint" {
				state["baseurl"] = strings.TrimSpace(value)
			}
			if stepKey == "model" {
				if candidate != nil && strings.TrimSpace(candidate.Value) != "" && !strings.EqualFold(strings.TrimSpace(candidate.Value), "__custom_model__") {
					state["_known_model"] = "true"
				} else {
					delete(state, "_known_model")
				}
			}
		},
		BuildExecLine: func(state map[string]string) string {
			apiKey := strings.TrimSpace(state["apikey"])
			if apiKey == "" {
				apiKey = "-"
			}
			reasoningLevels := strings.TrimSpace(state["reasoning_levels"])
			if reasoningLevels == "" {
				reasoningLevels = "-"
			}
			parts := []string{
				"/connect",
				state["provider"],
				state["model"],
				emptyAsDash(state["baseurl"]),
				connectWizardTimeout(state),
				apiKey,
				emptyAsDash(state["context_window_tokens"]),
				emptyAsDash(state["max_output_tokens"]),
				reasoningLevels,
			}
			return joinNonEmpty(parts, " ")
		},
	}
}

func buildConnectWizardPayload(state map[string]string) string {
	return strings.TrimSpace(state["provider"]) +
		"|" + url.QueryEscape(strings.TrimSpace(state["baseurl"])) +
		"|" + connectWizardTimeout(state) +
		"|" + url.QueryEscape(strings.TrimSpace(state["apikey"])) +
		"|" + url.QueryEscape(strings.TrimSpace(state["model"]))
}

func connectWizardTimeout(state map[string]string) string {
	timeout := strings.TrimSpace(state["timeout"])
	if timeout == "" {
		return "60"
	}
	return timeout
}

func emptyAsDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
