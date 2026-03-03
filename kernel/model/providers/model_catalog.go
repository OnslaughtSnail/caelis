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
	// SupportsJSONOutput indicates whether the model supports structured JSON output.
	SupportsJSONOutput bool
}

// DefaultModelCapabilities returns conservative defaults for unknown models.
func DefaultModelCapabilities() ModelCapabilities {
	return ModelCapabilities{
		ContextWindowTokens:    32000,
		MaxOutputTokens:        4096,
		DefaultMaxOutputTokens: 4096,
		SupportsToolCalls:      true,
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
func LookupModelCapabilities(provider, modelName string) (ModelCapabilities, bool) {
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

	// 3 – static builtin catalog fallback.
	return lookupBuiltin(provider, modelName)
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
	return best.caps, true
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
	if cfg.ContextWindowTokens <= 0 {
		cfg.ContextWindowTokens = caps.ContextWindowTokens
	}
	if cfg.MaxOutputTok <= 0 {
		cfg.MaxOutputTok = caps.DefaultMaxOutputTokens
	}
}
