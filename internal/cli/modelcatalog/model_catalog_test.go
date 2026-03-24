package modelcatalog

import (
	"testing"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

func TestLookupModelCapabilities_ExactMatch(t *testing.T) {
	caps, found := LookupModelCapabilities("deepseek", "deepseek-chat")
	if !found {
		t.Fatal("expected to find deepseek-chat")
	}
	if caps.ContextWindowTokens != 128000 {
		t.Fatalf("expected context 128000, got %d", caps.ContextWindowTokens)
	}
	if caps.MaxOutputTokens != 8192 {
		t.Fatalf("expected max output 8192, got %d", caps.MaxOutputTokens)
	}
	if caps.SupportsReasoning {
		t.Fatal("deepseek-chat should not support configurable reasoning")
	}
	if !caps.SupportsToolCalls {
		t.Fatal("deepseek-chat should support tool calls")
	}
}

func TestLookupModelCapabilities_DeepSeekReasoner(t *testing.T) {
	caps, found := LookupModelCapabilities("deepseek", "deepseek-reasoner")
	if !found {
		t.Fatal("expected to find deepseek-reasoner")
	}
	if caps.ContextWindowTokens != 128000 {
		t.Fatalf("expected context 128000, got %d", caps.ContextWindowTokens)
	}
	if caps.MaxOutputTokens != 64000 {
		t.Fatalf("expected max output 64000, got %d", caps.MaxOutputTokens)
	}
	if caps.DefaultMaxOutputTokens != 32768 {
		t.Fatalf("expected default max output 32768, got %d", caps.DefaultMaxOutputTokens)
	}
	if !caps.SupportsReasoning {
		t.Fatal("deepseek-reasoner should support reasoning")
	}
	if caps.ReasoningMode != ReasoningModeFixed {
		t.Fatalf("expected deepseek-reasoner fixed reasoning mode, got %q", caps.ReasoningMode)
	}
}

func TestLookupModelCapabilities_PrefixMatch(t *testing.T) {
	caps, found := LookupModelCapabilities("openai", "gpt-4o-2024-11-20")
	if !found {
		t.Fatal("expected prefix match for gpt-4o-*")
	}
	if caps.ContextWindowTokens != 128000 {
		t.Fatalf("expected context 128000, got %d", caps.ContextWindowTokens)
	}
	if !caps.SupportsImages {
		t.Fatal("gpt-4o should support images")
	}
}

func TestLookupModelCapabilities_MoreSpecificWins(t *testing.T) {
	// "gpt-4o-mini" should match the specific "gpt-4o-mini" entry, not "gpt-4o"
	caps, found := LookupModelCapabilities("openai", "gpt-4o-mini-2024-07-18")
	if !found {
		t.Fatal("expected match for gpt-4o-mini variant")
	}
	// Both gpt-4o and gpt-4o-mini have same context, but the match should be the longer pattern
	if caps.ContextWindowTokens != 128000 {
		t.Fatalf("expected context 128000, got %d", caps.ContextWindowTokens)
	}
}

func TestLookupModelCapabilities_UnknownModel(t *testing.T) {
	caps, found := LookupModelCapabilities("openai", "some-unknown-model")
	if found {
		t.Fatal("did not expect match for unknown model")
	}
	defaults := DefaultModelCapabilities()
	if caps.ContextWindowTokens != defaults.ContextWindowTokens {
		t.Fatalf("expected default context %d, got %d", defaults.ContextWindowTokens, caps.ContextWindowTokens)
	}
	if caps.DefaultMaxOutputTokens != defaults.DefaultMaxOutputTokens {
		t.Fatalf("expected default max output %d, got %d", defaults.DefaultMaxOutputTokens, caps.DefaultMaxOutputTokens)
	}
}

func TestLookupModelCapabilities_CaseInsensitive(t *testing.T) {
	caps, found := LookupModelCapabilities("DeepSeek", "DeepSeek-Chat")
	if !found {
		t.Fatal("expected case-insensitive match")
	}
	if caps.ContextWindowTokens != 128000 {
		t.Fatalf("expected context 128000, got %d", caps.ContextWindowTokens)
	}
}

func TestLookupModelCapabilities_EmptyInputs(t *testing.T) {
	_, found := LookupModelCapabilities("", "deepseek-chat")
	if found {
		t.Fatal("should not match with empty provider")
	}
	_, found = LookupModelCapabilities("deepseek", "")
	if found {
		t.Fatal("should not match with empty model")
	}
}

func TestLookupModelCapabilities_Anthropic(t *testing.T) {
	caps, found := LookupModelCapabilities("anthropic", "claude-sonnet-4-20250514")
	if !found {
		t.Fatal("expected match for claude-sonnet-4 variant")
	}
	if caps.ContextWindowTokens != 200000 {
		t.Fatalf("expected context 200000, got %d", caps.ContextWindowTokens)
	}
	if !caps.SupportsReasoning {
		t.Fatal("claude-sonnet-4 should support reasoning")
	}
	if !caps.SupportsImages {
		t.Fatal("claude-sonnet-4 should support images")
	}
}

func TestLookupModelCapabilities_Gemini(t *testing.T) {
	caps, found := LookupModelCapabilities("gemini", "gemini-2.5-pro-preview")
	if !found {
		t.Fatal("expected match for gemini-2.5-pro variant")
	}
	if caps.ContextWindowTokens != 1048576 {
		t.Fatalf("expected context 1048576, got %d", caps.ContextWindowTokens)
	}
	if !caps.SupportsReasoning {
		t.Fatal("gemini-2.5-pro should support reasoning")
	}
}

func TestLookupModelCapabilities_MiniMax(t *testing.T) {
	caps, found := LookupModelCapabilities("minimax", "MiniMax-M2.5-highspeed")
	if !found {
		t.Fatal("expected match for minimax model")
	}
	if caps.ContextWindowTokens != 204800 {
		t.Fatalf("expected context 204800, got %d", caps.ContextWindowTokens)
	}
	if !caps.SupportsToolCalls {
		t.Fatal("minimax should support tool calls")
	}
	if !caps.SupportsJSONOutput {
		t.Fatal("minimax should support json output")
	}
	if caps.SupportsImages {
		t.Fatal("minimax should not be treated as an image model")
	}
	if !caps.SupportsReasoning || caps.ReasoningMode != ReasoningModeFixed {
		t.Fatalf("expected fixed reasoning support, got %+v", caps)
	}
}

func TestLookupModelCapabilities_MimoPro(t *testing.T) {
	caps, found := LookupModelCapabilities("xiaomi", "mimo-v2-pro")
	if !found {
		t.Fatal("expected match for mimo-v2-pro")
	}
	if caps.ContextWindowTokens != 1048576 {
		t.Fatalf("expected context 1048576, got %d", caps.ContextWindowTokens)
	}
	if caps.MaxOutputTokens != 131072 {
		t.Fatalf("expected max output 131072, got %d", caps.MaxOutputTokens)
	}
	if caps.DefaultMaxOutputTokens != 32768 {
		t.Fatalf("expected default max output 32768, got %d", caps.DefaultMaxOutputTokens)
	}
	if !caps.SupportsToolCalls {
		t.Fatal("mimo-v2-pro should support tool calls")
	}
	if !caps.SupportsReasoning {
		t.Fatal("mimo-v2-pro should support reasoning")
	}
	if caps.SupportsImages {
		t.Fatal("mimo-v2-pro should not be treated as an image model")
	}
	if caps.ReasoningMode != ReasoningModeToggle {
		t.Fatalf("expected toggle reasoning mode, got %q", caps.ReasoningMode)
	}
}

func TestApplyConfigDefaults_FillsDefaults(t *testing.T) {
	cfg := &modelproviders.Config{
		Provider: "deepseek",
		Model:    "deepseek-chat",
	}
	ApplyConfigDefaults(cfg)
	if cfg.ContextWindowTokens != 128000 {
		t.Fatalf("expected context 128000, got %d", cfg.ContextWindowTokens)
	}
	if cfg.MaxOutputTok != 4096 {
		t.Fatalf("expected max_output 4096 (default), got %d", cfg.MaxOutputTok)
	}
}

func TestApplyConfigDefaults_DoesNotOverrideExplicit(t *testing.T) {
	cfg := &modelproviders.Config{
		Provider:            "deepseek",
		Model:               "deepseek-chat",
		ContextWindowTokens: 64000,
		MaxOutputTok:        8192,
	}
	ApplyConfigDefaults(cfg)
	if cfg.ContextWindowTokens != 64000 {
		t.Fatalf("should not override explicit context, got %d", cfg.ContextWindowTokens)
	}
	if cfg.MaxOutputTok != 8192 {
		t.Fatalf("should not override explicit max_output, got %d", cfg.MaxOutputTok)
	}
}

func TestApplyConfigDefaults_UnknownModelGetsDefaults(t *testing.T) {
	cfg := &modelproviders.Config{
		Provider: "some-provider",
		Model:    "unknown-model",
	}
	ApplyConfigDefaults(cfg)
	defaults := DefaultModelCapabilities()
	if cfg.ContextWindowTokens != defaults.ContextWindowTokens {
		t.Fatalf("expected default context %d, got %d", defaults.ContextWindowTokens, cfg.ContextWindowTokens)
	}
	if cfg.MaxOutputTok != defaults.DefaultMaxOutputTokens {
		t.Fatalf("expected default max_output %d, got %d", defaults.DefaultMaxOutputTokens, cfg.MaxOutputTok)
	}
}

func TestApplyConfigDefaults_UnknownModelCapsDefaultOutputByContext(t *testing.T) {
	cfg := &modelproviders.Config{
		Provider:            "some-provider",
		Model:               "unknown-model",
		ContextWindowTokens: 16000,
	}
	ApplyConfigDefaults(cfg)
	if cfg.MaxOutputTok != 2000 {
		t.Fatalf("expected max_output capped to context/8 = 2000, got %d", cfg.MaxOutputTok)
	}
}

func TestApplyConfigDefaults_DeepSeekReasoner(t *testing.T) {
	cfg := &modelproviders.Config{
		Provider: "deepseek",
		Model:    "deepseek-reasoner",
	}
	ApplyConfigDefaults(cfg)
	if cfg.ContextWindowTokens != 128000 {
		t.Fatalf("expected context 128000, got %d", cfg.ContextWindowTokens)
	}
	if cfg.MaxOutputTok != 32768 {
		t.Fatalf("expected default max output 32768, got %d", cfg.MaxOutputTok)
	}
}

func TestSupportedReasoningEfforts_Gemini(t *testing.T) {
	got := SupportedReasoningEfforts("gemini", "gemini-2.5-pro")
	if len(got) != 3 || got[0] != "low" || got[1] != "medium" || got[2] != "high" {
		t.Fatalf("unexpected gemini efforts: %v", got)
	}
	if mode := ReasoningModeForModel("gemini", "gemini-2.5-pro"); mode != ReasoningModeEffort {
		t.Fatalf("expected gemini reasoning mode effort, got %q", mode)
	}
	if def := DefaultReasoningEffortForModel("gemini", "gemini-2.5-pro"); def != "medium" {
		t.Fatalf("expected gemini default effort medium, got %q", def)
	}
}

func TestSupportedReasoningEfforts_OpenAIO3IncludesXHigh(t *testing.T) {
	got := SupportedReasoningEfforts("openai", "o3")
	if len(got) < 4 || got[3] != "xhigh" {
		t.Fatalf("expected xhigh for o3, got %v", got)
	}
	if !SupportsReasoningEffort("openai", "o3", "very-high") {
		t.Fatalf("expected very-high alias to map to xhigh")
	}
}

func TestLookupSuggestedModelCapabilities_UsesOverlayForKnownReasoningPrefix(t *testing.T) {
	got, ok := LookupSuggestedModelCapabilities("openai", "o3-custom-build")
	if !ok {
		t.Fatal("expected model-specific overlay fallback")
	}
	if got.ReasoningMode != ReasoningModeEffort {
		t.Fatalf("expected overlay reasoning mode effort, got %+v", got)
	}
	if !SupportsReasoningEffortList(got.ReasoningEfforts, "xhigh") {
		t.Fatalf("expected overlay xhigh support, got %+v", got.ReasoningEfforts)
	}
}

func TestSupportedReasoningEfforts_OpenRouterUsesCanonicalFallback(t *testing.T) {
	got := SupportedReasoningEfforts("openrouter", "unknown-model")
	want := []string{"none", "minimal", "low", "medium", "high", "xhigh"}
	if len(got) != len(want) {
		t.Fatalf("unexpected openrouter efforts: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected openrouter efforts: %v", got)
		}
	}
	if mode := ReasoningModeForModel("openrouter", "unknown-model"); mode != ReasoningModeEffort {
		t.Fatalf("expected openrouter reasoning mode effort, got %q", mode)
	}
	if def := DefaultReasoningEffortForModel("openrouter", "unknown-model"); def != "medium" {
		t.Fatalf("expected openrouter default effort medium, got %q", def)
	}
}

func TestListCatalogModels_IncludesDynamicAndBuiltin(t *testing.T) {
	got := ListCatalogModels("deepseek")
	if len(got) == 0 {
		t.Fatal("expected catalog models for deepseek")
	}
	foundBuiltin := false
	for _, one := range got {
		if one == "deepseek-chat" {
			foundBuiltin = true
			break
		}
	}
	if !foundBuiltin {
		t.Fatalf("expected deepseek-chat in catalog models: %v", got)
	}
}

func TestListCatalogModels_XiaomiIncludesDynamicEntries(t *testing.T) {
	resetDynamicCatalog(t)
	dynamicMu.Lock()
	remoteCatalog = capSnapshot{
		"xiaomi:mimo-v2-flash": {ContextWindow: 262000, MaxOutput: 64000, ToolCalls: true, Reasoning: true},
	}
	dynamicMu.Unlock()

	got := ListCatalogModels("xiaomi")
	found := false
	for _, one := range got {
		if one == "mimo-v2-flash" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected xiaomi dynamic model in catalog list, got %v", got)
	}
}

func TestReasoningModeForDeepSeekModels(t *testing.T) {
	if mode := ReasoningModeForModel("deepseek", "deepseek-chat"); mode != ReasoningModeNone {
		t.Fatalf("expected deepseek-chat no reasoning mode, got %q", mode)
	}
	if efforts := SupportedReasoningEfforts("deepseek", "deepseek-chat"); len(efforts) != 0 {
		t.Fatalf("expected no effort list for deepseek-chat, got %v", efforts)
	}
	if mode := ReasoningModeForModel("deepseek", "deepseek-reasoner"); mode != ReasoningModeFixed {
		t.Fatalf("expected deepseek-reasoner fixed reasoning mode, got %q", mode)
	}
}
