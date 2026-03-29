package acpclient

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
)

type PermissionDecision string

const (
	PermissionDecisionAutoAllowOnce       PermissionDecision = "auto_allow_once"
	PermissionDecisionAskUser             PermissionDecision = "ask_user"
	PermissionDecisionDeferExistingPolicy PermissionDecision = "defer_to_existing_policy"
)

type PermissionResolution struct {
	Decision PermissionDecision
	OptionID string
}

type permissionOptionAliases struct {
	AllowOnce  []string
	RejectOnce []string
}

var (
	genericAllowOnceAliases = []string{
		"allow_once",
		"allow-once",
		"allow",
		"approve_once",
		"approve-once",
		"approve",
		"accept_once",
		"accept-once",
		"accept",
	}
	genericRejectOnceAliases = []string{
		"reject_once",
		"reject-once",
		"reject",
		"deny_once",
		"deny-once",
		"deny",
		"decline_once",
		"decline-once",
		"decline",
	}
	knownAgentPermissionAliases = map[string]permissionOptionAliases{
		"self":          defaultPermissionAliases(),
		"claude":        defaultPermissionAliases(),
		"codex":         defaultPermissionAliases(),
		"copilot":       defaultPermissionAliases(),
		"cursor":        defaultPermissionAliases(),
		"droid":         defaultPermissionAliases(),
		"factory-droid": defaultPermissionAliases(),
		"factorydroid":  defaultPermissionAliases(),
		"gemini":        defaultPermissionAliases(),
		"iflow":         defaultPermissionAliases(),
		"kilocode":      defaultPermissionAliases(),
		"kimi":          defaultPermissionAliases(),
		"kiro":          defaultPermissionAliases(),
		"openclaw":      defaultPermissionAliases(),
		"opencode":      defaultPermissionAliases(),
		"pi":            defaultPermissionAliases(),
		"qwen":          defaultPermissionAliases(),
	}
)

func defaultPermissionAliases() permissionOptionAliases {
	return permissionOptionAliases{
		AllowOnce:  append([]string(nil), genericAllowOnceAliases...),
		RejectOnce: append([]string(nil), genericRejectOnceAliases...),
	}
}

func ResolveApproveAllOnce(sessionMode string, agentID string, req RequestPermissionRequest) PermissionResolution {
	if !sessionmode.IsFullAccess(sessionmode.Normalize(sessionMode)) {
		return PermissionResolution{Decision: PermissionDecisionDeferExistingPolicy}
	}
	if optionID, ok := selectPermissionOptionID(req.Options, true, true, agentID); ok {
		return PermissionResolution{
			Decision: PermissionDecisionAutoAllowOnce,
			OptionID: optionID,
		}
	}
	return PermissionResolution{Decision: PermissionDecisionAskUser}
}

func PermissionSelectedOutcome(optionID string) RequestPermissionResponse {
	return RequestPermissionResponse{
		Outcome: mustMarshalRaw(map[string]any{
			"outcome":  "selected",
			"optionId": strings.TrimSpace(optionID),
		}),
	}
}

func PermissionSelectedOptionID(resp RequestPermissionResponse) string {
	var outcome struct {
		OptionID string `json:"optionId"`
	}
	if err := json.Unmarshal(resp.Outcome, &outcome); err != nil {
		return ""
	}
	return strings.TrimSpace(outcome.OptionID)
}

func SelectPermissionOptionID(options []PermissionOption, allowed bool) string {
	optionID, ok := selectPermissionOptionID(options, allowed, false, "")
	if ok {
		return optionID
	}
	if allowed {
		return "allow_once"
	}
	return "reject_once"
}

func selectPermissionOptionID(options []PermissionOption, allowed bool, singleUseOnly bool, agentID string) (string, bool) {
	if len(options) == 0 {
		return "", false
	}
	if allowed {
		if optionID, ok := findOptionByKinds(options, "allow_once"); ok {
			return optionID, true
		}
		if optionID, ok := findOptionByAliases(options, knownAllowOnceAliases(agentID), "allow_always"); ok {
			return optionID, true
		}
		if !singleUseOnly {
			if optionID, ok := findOptionByKinds(options, "allow_always"); ok {
				return optionID, true
			}
			if optionID, ok := findOptionByAliases(options, genericAllowOnceAliases); ok {
				return optionID, true
			}
		}
		return "", false
	}
	if optionID, ok := findOptionByKinds(options, "reject_once"); ok {
		return optionID, true
	}
	if optionID, ok := findOptionByAliases(options, genericRejectOnceAliases); ok {
		return optionID, true
	}
	if !singleUseOnly {
		if optionID, ok := findOptionByKinds(options, "reject_always"); ok {
			return optionID, true
		}
	}
	return "", false
}

func knownAllowOnceAliases(agentID string) []string {
	aliases, ok := knownAgentPermissionAliases[strings.ToLower(strings.TrimSpace(agentID))]
	if !ok {
		return nil
	}
	return aliases.AllowOnce
}

func findOptionByKinds(options []PermissionOption, wantKinds ...string) (string, bool) {
	for _, wantKind := range wantKinds {
		wantKind = normalizePermissionToken(wantKind)
		if wantKind == "" {
			continue
		}
		for _, option := range options {
			if normalizePermissionToken(option.Kind) == wantKind && strings.TrimSpace(option.OptionID) != "" {
				return strings.TrimSpace(option.OptionID), true
			}
		}
	}
	return "", false
}

func findOptionByAliases(options []PermissionOption, aliases []string, excludeKinds ...string) (string, bool) {
	if len(aliases) == 0 {
		return "", false
	}
	normalizedAliases := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if normalized := normalizePermissionToken(alias); normalized != "" && !slices.Contains(normalizedAliases, normalized) {
			normalizedAliases = append(normalizedAliases, normalized)
		}
	}
	normalizedExcludedKinds := make([]string, 0, len(excludeKinds))
	for _, kind := range excludeKinds {
		if normalized := normalizePermissionToken(kind); normalized != "" && !slices.Contains(normalizedExcludedKinds, normalized) {
			normalizedExcludedKinds = append(normalizedExcludedKinds, normalized)
		}
	}
	for _, option := range options {
		if strings.TrimSpace(option.OptionID) == "" {
			continue
		}
		if slices.Contains(normalizedExcludedKinds, normalizePermissionToken(option.Kind)) {
			continue
		}
		for _, candidate := range []string{option.OptionID, option.Name} {
			if slices.Contains(normalizedAliases, normalizePermissionToken(candidate)) {
				return strings.TrimSpace(option.OptionID), true
			}
		}
	}
	return "", false
}

func normalizePermissionToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(strings.TrimSpace(b.String()), "_")
}
