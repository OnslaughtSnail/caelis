package providers

import (
	"strings"
)

// ModelCapabilities describes known capabilities and limits for a specific model.
type ModelCapabilities struct {
	// ContextWindowTokens is the maximum input context window size.
	ContextWindowTokens int
	// MaxOutputTokens is the maximum output tokens the model can generate.
	MaxOutputTokens int
	// DefaultMaxOutputTokens is the default output token limit if not explicitly set.
	// API providers may use their own default if this is 0.
	DefaultMaxOutputTokens int
	// SupportsImages indicates whether the model accepts image inputs.
	SupportsImages bool
	// SupportsToolCalls indicates whether the model supports function/tool calling.
	SupportsToolCalls bool
	// SupportsReasoning indicates whether the model supports thinking/reasoning mode.
	SupportsReasoning bool
	// ReasoningMode describes how reasoning is controlled: none|toggle|effort.
	ReasoningMode string
	// ReasoningEfforts lists supported reasoning effort levels (for example:
	// low|medium|high|xhigh). Empty means the model uses toggle/budget-only
	// reasoning or the effort set is unknown.
	ReasoningEfforts []string
	// DefaultReasoningEffort is the recommended default effort when the model
	// uses effort-based reasoning.
	DefaultReasoningEffort string
	// SupportsJSONOutput indicates whether the model supports structured JSON output.
	SupportsJSONOutput bool
}

const (
	ReasoningModeNone   = "none"
	ReasoningModeToggle = "toggle"
	ReasoningModeEffort = "effort"
)

// DefaultModelCapabilities returns conservative defaults for unknown models.
func DefaultModelCapabilities() ModelCapabilities {
	return ModelCapabilities{
		ContextWindowTokens:    128000,
		MaxOutputTokens:        4096,
		DefaultMaxOutputTokens: 4096,
		SupportsToolCalls:      true,
		ReasoningMode:          ReasoningModeNone,
		SupportsJSONOutput:     true,
	}
}

// catalogEntry maps a provider+model pattern to capabilities.
type catalogEntry struct {
	provider string // provider name (e.g. "deepseek", "openai")
	pattern  string // model name prefix or exact match (e.g. "deepseek-chat", "gpt-4o")
	caps     ModelCapabilities
}

// builtinCatalog is the static registry of known model capabilities.
// Add new SOTA models here as they become available.
var builtinCatalog = []catalogEntry{
	// ── DeepSeek ──────────────────────────────────────────────────────────
	// deepseek-chat supports thinking mode via `thinking: {type: "enabled"}` in
	// extra_body – this is identical to using deepseek-reasoner. When thinking is
	// enabled the API defaults max_tokens to 32K (max 64K); applyThinkingReasoning
	// bumps the request limit automatically. DefaultMaxOutputTokens stays at 8K
	// so non-thinking requests don't over-allocate.
	{
		provider: "deepseek",
		pattern:  "deepseek-chat",
		caps: ModelCapabilities{
			ContextWindowTokens:    128000,
			MaxOutputTokens:        65536,
			DefaultMaxOutputTokens: 8192,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			SupportsJSONOutput:     true,
			SupportsImages:         false,
		},
	},
	{
		provider: "deepseek",
		pattern:  "deepseek-reasoner",
		caps: ModelCapabilities{
			ContextWindowTokens:    128000,
			MaxOutputTokens:        65536,
			DefaultMaxOutputTokens: 32768,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			SupportsJSONOutput:     true,
			SupportsImages:         false,
		},
	},
	// ── OpenAI ────────────────────────────────────────────────────────────
	{
		provider: "openai",
		pattern:  "gpt-4o",
		caps: ModelCapabilities{
			ContextWindowTokens:    128000,
			MaxOutputTokens:        16384,
			DefaultMaxOutputTokens: 4096,
			SupportsToolCalls:      true,
			SupportsReasoning:      false,
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "openai",
		pattern:  "gpt-4o-mini",
		caps: ModelCapabilities{
			ContextWindowTokens:    128000,
			MaxOutputTokens:        16384,
			DefaultMaxOutputTokens: 4096,
			SupportsToolCalls:      true,
			SupportsReasoning:      false,
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "openai",
		pattern:  "o1",
		caps: ModelCapabilities{
			ContextWindowTokens:    200000,
			MaxOutputTokens:        100000,
			DefaultMaxOutputTokens: 32768,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			ReasoningEfforts:       []string{"low", "medium", "high"},
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "openai",
		pattern:  "o1-mini",
		caps: ModelCapabilities{
			ContextWindowTokens:    128000,
			MaxOutputTokens:        65536,
			DefaultMaxOutputTokens: 16384,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			ReasoningEfforts:       []string{"low", "medium", "high"},
			SupportsJSONOutput:     true,
			SupportsImages:         false,
		},
	},
	{
		provider: "openai",
		pattern:  "o3",
		caps: ModelCapabilities{
			ContextWindowTokens:    200000,
			MaxOutputTokens:        100000,
			DefaultMaxOutputTokens: 32768,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh"},
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "openai",
		pattern:  "o3-mini",
		caps: ModelCapabilities{
			ContextWindowTokens:    200000,
			MaxOutputTokens:        100000,
			DefaultMaxOutputTokens: 16384,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh"},
			SupportsJSONOutput:     true,
			SupportsImages:         false,
		},
	},
	{
		provider: "openai",
		pattern:  "o4-mini",
		caps: ModelCapabilities{
			ContextWindowTokens:    200000,
			MaxOutputTokens:        100000,
			DefaultMaxOutputTokens: 16384,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh"},
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "openai",
		pattern:  "gpt-4.1",
		caps: ModelCapabilities{
			ContextWindowTokens:    1047576,
			MaxOutputTokens:        32768,
			DefaultMaxOutputTokens: 16384,
			SupportsToolCalls:      true,
			SupportsReasoning:      false,
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "openai",
		pattern:  "gpt-4.1-mini",
		caps: ModelCapabilities{
			ContextWindowTokens:    1047576,
			MaxOutputTokens:        32768,
			DefaultMaxOutputTokens: 16384,
			SupportsToolCalls:      true,
			SupportsReasoning:      false,
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "openai",
		pattern:  "gpt-4.1-nano",
		caps: ModelCapabilities{
			ContextWindowTokens:    1047576,
			MaxOutputTokens:        32768,
			DefaultMaxOutputTokens: 16384,
			SupportsToolCalls:      true,
			SupportsReasoning:      false,
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	// ── Anthropic ─────────────────────────────────────────────────────────
	{
		provider: "anthropic",
		pattern:  "claude-sonnet-4",
		caps: ModelCapabilities{
			ContextWindowTokens:    200000,
			MaxOutputTokens:        64000,
			DefaultMaxOutputTokens: 16384,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			ReasoningEfforts:       []string{"low", "medium", "high"},
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "anthropic",
		pattern:  "claude-3-7-sonnet",
		caps: ModelCapabilities{
			ContextWindowTokens:    200000,
			MaxOutputTokens:        64000,
			DefaultMaxOutputTokens: 16384,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			ReasoningEfforts:       []string{"low", "medium", "high"},
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "anthropic",
		pattern:  "claude-3-5-sonnet",
		caps: ModelCapabilities{
			ContextWindowTokens:    200000,
			MaxOutputTokens:        8192,
			DefaultMaxOutputTokens: 4096,
			SupportsToolCalls:      true,
			SupportsReasoning:      false,
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "anthropic",
		pattern:  "claude-3-5-haiku",
		caps: ModelCapabilities{
			ContextWindowTokens:    200000,
			MaxOutputTokens:        8192,
			DefaultMaxOutputTokens: 4096,
			SupportsToolCalls:      true,
			SupportsReasoning:      false,
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "anthropic",
		pattern:  "claude-opus-4",
		caps: ModelCapabilities{
			ContextWindowTokens:    200000,
			MaxOutputTokens:        64000,
			DefaultMaxOutputTokens: 16384,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			ReasoningEfforts:       []string{"low", "medium", "high"},
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	// ── Gemini ────────────────────────────────────────────────────────────
	{
		provider: "gemini",
		pattern:  "gemini-2.5-pro",
		caps: ModelCapabilities{
			ContextWindowTokens:    1048576,
			MaxOutputTokens:        65536,
			DefaultMaxOutputTokens: 8192,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			ReasoningEfforts:       []string{"low", "medium", "high"},
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "gemini",
		pattern:  "gemini-2.5-flash",
		caps: ModelCapabilities{
			ContextWindowTokens:    1048576,
			MaxOutputTokens:        65536,
			DefaultMaxOutputTokens: 8192,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			ReasoningEfforts:       []string{"low", "medium", "high"},
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	{
		provider: "gemini",
		pattern:  "gemini-2.0-flash",
		caps: ModelCapabilities{
			ContextWindowTokens:    1048576,
			MaxOutputTokens:        8192,
			DefaultMaxOutputTokens: 8192,
			SupportsToolCalls:      true,
			SupportsReasoning:      false,
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
	// ── Xiaomi ────────────────────────────────────────────────────────────
	{
		provider: "xiaomi",
		pattern:  "MiMo-VL-7B-RL",
		caps: ModelCapabilities{
			ContextWindowTokens:    32000,
			MaxOutputTokens:        16384,
			DefaultMaxOutputTokens: 4096,
			SupportsToolCalls:      true,
			SupportsReasoning:      true,
			SupportsJSONOutput:     true,
			SupportsImages:         true,
		},
	},
}

// LookupModelCapabilities searches the built-in catalog for capabilities
// matching the given provider and model name. It uses prefix matching:
// a catalog entry with pattern "gpt-4o" matches "gpt-4o-2024-08-06".
// More specific (longer) patterns take priority over shorter ones.
//
// Lookup priority (highest to lowest):
//  1. Local user override file  (loaded by InitModelCatalog)
//  2. Remote models.dev data / embedded snapshot  (loaded by InitModelCatalog)
//  3. Hard-coded builtinCatalog below
//
// Returns the matched capabilities and true, or DefaultModelCapabilities()
// and false if no match is found.
func LookupBaseCatalogCapabilities(provider, modelName string) (ModelCapabilities, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if provider == "" || modelName == "" {
		return DefaultModelCapabilities(), false
	}

	// 1 & 2 – dynamic catalog (local overrides → remote/snapshot).
	if caps, ok := lookupDynamic(provider, modelName); ok {
		// If the dynamic source didn't include DefaultMaxOutputTokens,
		// prefer the tuned value from the builtin catalog if one exists.
		if caps.DefaultMaxOutputTokens <= 0 {
			if builtin, found := lookupBuiltin(provider, modelName); found {
				caps.DefaultMaxOutputTokens = builtin.DefaultMaxOutputTokens
			}
		}
		return caps, true
	}
	return ModelCapabilities{}, false
}

// LookupModelCapabilities resolves model capabilities from the layered catalog:
// local override -> remote -> embedded -> provider overlay -> builtin.
func LookupModelCapabilities(provider, modelName string) (ModelCapabilities, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if provider == "" || modelName == "" {
		return DefaultModelCapabilities(), false
	}
	if caps, ok := LookupBaseCatalogCapabilities(provider, modelName); ok {
		if overlay, overlayOK := searchOverlay(provider, modelName); overlayOK {
			caps = mergeCapabilities(caps, overlay)
		}
		return caps, true
	}
	if caps, ok := lookupBuiltin(provider, modelName); ok {
		if overlay, overlayOK := searchOverlay(provider, modelName); overlayOK {
			caps = mergeCapabilities(caps, overlay)
		}
		return caps, true
	}
	return DefaultModelCapabilities(), false
}

// LookupSuggestedModelCapabilities returns the best-effort suggested defaults
// for one provider/model pair, including provider overlay fallbacks for models
// that are not present in the base catalog.
func LookupSuggestedModelCapabilities(provider, modelName string) (ModelCapabilities, bool) {
	if caps, ok := LookupModelCapabilities(provider, modelName); ok {
		return caps, true
	}
	return lookupOverlayModelCapabilities(provider, modelName)
}

// lookupBuiltin searches only the hard-coded builtinCatalog.
func lookupBuiltin(provider, modelName string) (ModelCapabilities, bool) {
	var best *catalogEntry
	bestLen := 0

	for i := range builtinCatalog {
		entry := &builtinCatalog[i]
		entryProvider := strings.ToLower(entry.provider)
		entryPattern := strings.ToLower(entry.pattern)

		// Provider must match exactly, or the config provider contains the catalog provider.
		if entryProvider != provider && !strings.Contains(provider, entryProvider) {
			continue
		}
		// Model name must match exactly or start with the pattern.
		if modelName != entryPattern && !strings.HasPrefix(modelName, entryPattern) {
			continue
		}
		// Prefer the longest (most specific) pattern match.
		if len(entryPattern) > bestLen {
			best = entry
			bestLen = len(entryPattern)
		}
	}

	if best == nil {
		return DefaultModelCapabilities(), false
	}
	out := best.caps
	normalizeModelCapabilitiesReasoning(&out)
	return out, true
}

// ApplyModelCatalog enriches the given Config with capabilities from the
// built-in catalog when the config does not already have explicit values.
// This is called when registering a provider config so that runtime parameters
// are automatically filled in for known models.
func ApplyModelCatalog(cfg *Config) {
	if cfg == nil {
		return
	}
	caps, found := LookupModelCapabilities(cfg.Provider, cfg.Model)
	if !found {
		if suggested, ok := LookupSuggestedModelCapabilities(cfg.Provider, cfg.Model); ok {
			caps = suggested
			found = true
		} else {
			// Apply conservative defaults for completely unknown models.
			defaults := DefaultModelCapabilities()
			if cfg.ContextWindowTokens <= 0 {
				cfg.ContextWindowTokens = defaults.ContextWindowTokens
			}
			if cfg.MaxOutputTok <= 0 {
				cfg.MaxOutputTok = defaults.DefaultMaxOutputTokens
			}
			return
		}
	}
	if cfg.ContextWindowTokens <= 0 {
		cfg.ContextWindowTokens = caps.ContextWindowTokens
	}
	if cfg.MaxOutputTok <= 0 {
		cfg.MaxOutputTok = caps.DefaultMaxOutputTokens
	}
	if strings.TrimSpace(cfg.ReasoningMode) == "" {
		cfg.ReasoningMode = caps.ReasoningMode
	}
	if len(cfg.SupportedReasoningEfforts) == 0 {
		cfg.SupportedReasoningEfforts = append([]string(nil), caps.ReasoningEfforts...)
	}
	if strings.TrimSpace(cfg.DefaultReasoningEffort) == "" {
		cfg.DefaultReasoningEffort = caps.DefaultReasoningEffort
	}
	if len(cfg.ReasoningLevels) == 0 {
		cfg.ReasoningLevels = reasoningLevelsFromCapabilities(caps)
	}
}

// NormalizeReasoningEffort canonicalizes one reasoning effort value.
// Known aliases:
//
//	very_high, very-high, veryhigh -> xhigh
func NormalizeReasoningEffort(input string) string {
	value := strings.ToLower(strings.TrimSpace(input))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case "":
		return ""
	case "very_high", "veryhigh":
		return "xhigh"
	default:
		return value
	}
}

// SupportedReasoningEfforts returns supported effort levels for the model.
// Empty means no effort levels are supported (toggle/budget-only) or unknown.
func SupportedReasoningEfforts(provider, modelName string) []string {
	caps, found := LookupModelCapabilities(provider, modelName)
	if found {
		if !caps.SupportsReasoning || NormalizeReasoningMode(caps.ReasoningMode) != ReasoningModeEffort {
			return nil
		}
		if normalized := normalizeReasoningEffortList(caps.ReasoningEfforts); len(normalized) > 0 {
			return normalized
		}
	}
	return inferReasoningEfforts(provider, modelName)
}

// SupportsReasoningEffort reports whether one model supports a specific effort.
func SupportsReasoningEffort(provider, modelName, effort string) bool {
	normalized := NormalizeReasoningEffort(effort)
	if normalized == "" {
		return false
	}
	levels := SupportedReasoningEfforts(provider, modelName)
	if len(levels) == 0 {
		return false
	}
	for _, one := range levels {
		if one == normalized {
			return true
		}
	}
	return false
}

func ReasoningModeForModel(provider, modelName string) string {
	caps, found := LookupModelCapabilities(provider, modelName)
	if !found {
		caps, found = LookupSuggestedModelCapabilities(provider, modelName)
	}
	if found {
		normalizeModelCapabilitiesReasoning(&caps)
		if mode := NormalizeReasoningMode(caps.ReasoningMode); mode != "" && (mode != ReasoningModeNone || caps.SupportsReasoning) {
			return mode
		}
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if strings.Contains(provider, "deepseek") || strings.HasPrefix(modelName, "deepseek-") {
		return ReasoningModeToggle
	}
	if provider == "xiaomi" || provider == "mimo" || strings.Contains(modelName, "mimo") {
		return ReasoningModeToggle
	}
	if len(inferReasoningEfforts(provider, modelName)) > 0 {
		return ReasoningModeEffort
	}
	return ReasoningModeNone
}

func DefaultReasoningEffortForModel(provider, modelName string) string {
	caps, found := LookupModelCapabilities(provider, modelName)
	if !found {
		caps, found = LookupSuggestedModelCapabilities(provider, modelName)
	}
	if !found {
		return defaultReasoningEffortFromList(inferReasoningEfforts(provider, modelName))
	}
	normalizeModelCapabilitiesReasoning(&caps)
	if NormalizeReasoningMode(caps.ReasoningMode) != ReasoningModeEffort {
		return defaultReasoningEffortFromList(inferReasoningEfforts(provider, modelName))
	}
	if normalized := NormalizeReasoningEffort(caps.DefaultReasoningEffort); normalized != "" {
		return normalized
	}
	return defaultReasoningEffortFromList(caps.ReasoningEfforts)
}

func NormalizeReasoningMode(input string) string {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case ReasoningModeNone:
		return ReasoningModeNone
	case ReasoningModeToggle, "boolean", "onoff":
		return ReasoningModeToggle
	case ReasoningModeEffort, "levels":
		return ReasoningModeEffort
	default:
		return ""
	}
}

func normalizeReasoningEffortList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, one := range in {
		normalized := NormalizeReasoningEffort(one)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func inferReasoningEfforts(provider, modelName string) []string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if provider == "" && modelName == "" {
		return nil
	}

	// DeepSeek/Xiaomi reasoning is modeled as thinking on/off toggles.
	if strings.Contains(provider, "deepseek") || strings.HasPrefix(modelName, "deepseek-") {
		return nil
	}
	if provider == "xiaomi" || provider == "mimo" || strings.Contains(modelName, "mimo") {
		return nil
	}

	if provider == "gemini" || strings.HasPrefix(modelName, "gemini-") {
		return []string{"low", "medium", "high"}
	}

	if strings.Contains(provider, "openai") || strings.HasPrefix(modelName, "o") {
		switch {
		case strings.HasPrefix(modelName, "o1"),
			strings.HasPrefix(modelName, "o3"),
			strings.HasPrefix(modelName, "o4"):
			return []string{"low", "medium", "high", "xhigh"}
		case strings.HasPrefix(modelName, "o"):
			return []string{"low", "medium", "high"}
		}
	}

	if provider == "anthropic" || strings.HasPrefix(modelName, "claude-") {
		return []string{"low", "medium", "high"}
	}

	return nil
}

func normalizeModelCapabilitiesReasoning(caps *ModelCapabilities) {
	if caps == nil {
		return
	}
	caps.ReasoningEfforts = normalizeReasoningEffortList(caps.ReasoningEfforts)
	caps.DefaultReasoningEffort = NormalizeReasoningEffort(caps.DefaultReasoningEffort)
	mode := NormalizeReasoningMode(caps.ReasoningMode)
	switch {
	case !caps.SupportsReasoning:
		caps.ReasoningMode = ReasoningModeNone
		caps.ReasoningEfforts = nil
		caps.DefaultReasoningEffort = ""
		return
	case mode != "":
		caps.ReasoningMode = mode
	case len(caps.ReasoningEfforts) > 0:
		caps.ReasoningMode = ReasoningModeEffort
	default:
		caps.ReasoningMode = ReasoningModeToggle
	}
	if caps.ReasoningMode != ReasoningModeEffort {
		caps.ReasoningEfforts = nil
		caps.DefaultReasoningEffort = ""
		return
	}
	if caps.DefaultReasoningEffort == "" || !SupportsReasoningEffortList(caps.ReasoningEfforts, caps.DefaultReasoningEffort) {
		caps.DefaultReasoningEffort = defaultReasoningEffortFromList(caps.ReasoningEfforts)
	}
}

func defaultReasoningEffortFromList(levels []string) string {
	levels = normalizeReasoningEffortList(levels)
	for _, preferred := range []string{"medium", "low", "minimal", "high", "xhigh"} {
		if SupportsReasoningEffortList(levels, preferred) {
			return preferred
		}
	}
	if len(levels) > 0 {
		return levels[0]
	}
	return ""
}

func SupportsReasoningEffortList(levels []string, effort string) bool {
	normalized := NormalizeReasoningEffort(effort)
	if normalized == "" {
		return false
	}
	for _, one := range normalizeReasoningEffortList(levels) {
		if one == normalized {
			return true
		}
	}
	return false
}

func reasoningLevelsFromCapabilities(caps ModelCapabilities) []string {
	normalizeModelCapabilitiesReasoning(&caps)
	switch caps.ReasoningMode {
	case ReasoningModeEffort:
		out := make([]string, 0, len(caps.ReasoningEfforts)+1)
		out = append(out, "none")
		out = append(out, caps.ReasoningEfforts...)
		return out
	case ReasoningModeToggle:
		return []string{"none"}
	default:
		return nil
	}
}
