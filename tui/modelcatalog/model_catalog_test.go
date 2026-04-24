package modelcatalog

import "testing"

func TestLookupModelCapabilitiesFallsBackToBuiltinWhenDynamicCatalogUnavailable(t *testing.T) {
	t.Parallel()

	dynamicMu.Lock()
	savedRemote := remoteCatalog
	savedEmbedded := embeddedCatalog
	savedLocal := localOverrides
	remoteCatalog = nil
	embeddedCatalog = nil
	localOverrides = nil
	dynamicMu.Unlock()
	defer func() {
		dynamicMu.Lock()
		remoteCatalog = savedRemote
		embeddedCatalog = savedEmbedded
		localOverrides = savedLocal
		dynamicMu.Unlock()
	}()

	caps, ok := LookupModelCapabilities("openai", "gpt-4o")
	if !ok {
		t.Fatal("LookupModelCapabilities(openai, gpt-4o) = false, want builtin fallback")
	}
	if caps.ContextWindowTokens <= 0 || caps.DefaultMaxOutputTokens <= 0 {
		t.Fatalf("caps = %#v, want populated builtin fallback", caps)
	}
}

func TestLookupSuggestedModelCapabilitiesSupportsOpenAICompatible(t *testing.T) {
	t.Parallel()

	caps, ok := LookupSuggestedModelCapabilities("openai-compatible", "gpt-4o-mini")
	if !ok {
		t.Fatal("LookupSuggestedModelCapabilities(openai-compatible, gpt-4o-mini) = false, want true")
	}
	if caps.ContextWindowTokens <= 0 {
		t.Fatalf("ContextWindowTokens = %d, want > 0", caps.ContextWindowTokens)
	}
}

func TestLookupSuggestedModelCapabilitiesUsesCodeFreeOverlayForGLM51(t *testing.T) {
	t.Parallel()

	caps, ok := LookupSuggestedModelCapabilities("codefree", "GLM-5.1")
	if !ok {
		t.Fatal("LookupSuggestedModelCapabilities(codefree, GLM-5.1) = false, want true")
	}
	if caps.ContextWindowTokens != 128000 {
		t.Fatalf("ContextWindowTokens = %d, want 128000", caps.ContextWindowTokens)
	}
}

func TestListCatalogModelsIncludesBuiltinDefaults(t *testing.T) {
	t.Parallel()

	models := ListCatalogModels("deepseek")
	if len(models) == 0 {
		t.Fatal("ListCatalogModels(deepseek) returned no models")
	}
	foundChat := false
	foundReasoner := false
	for _, model := range models {
		switch model {
		case "deepseek-chat":
			foundChat = true
		case "deepseek-reasoner":
			foundReasoner = true
		}
	}
	if !foundChat || !foundReasoner {
		t.Fatalf("ListCatalogModels(deepseek) = %#v, want deepseek-chat and deepseek-reasoner", models)
	}
}

func TestParseSnapshotBytesInvalidJSONGracefullyDegrades(t *testing.T) {
	t.Parallel()

	if snap := parseSnapshotBytes([]byte("{not-json")); snap != nil {
		t.Fatalf("parseSnapshotBytes(invalid) = %#v, want nil", snap)
	}
}
