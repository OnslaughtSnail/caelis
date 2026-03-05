package providers

import (
	"strings"
	"testing"
)

func TestListModelsContainsDefaultAliases(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-deepseek-token")
	t.Setenv("GEMINI_API_KEY", "test-gemini-token")
	models := ListModels()
	if len(models) == 0 {
		t.Fatalf("expected non-empty model aliases")
	}
	assertContains(t, models, "deepseek-chat")
	assertContains(t, models, "gemini-3.1-flash-lite-preview")
}

func TestDefaultFactoryRequiresAtLeastOneToken(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	_, err := defaultFactory()
	if err == nil {
		t.Fatal("expected missing token error")
	}
	if !strings.Contains(err.Error(), "no model credentials configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDefaultFactory_RegistersOnlyConfiguredModels(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-deepseek-token")
	t.Setenv("GEMINI_API_KEY", "")
	models := ListModels()
	assertContains(t, models, "deepseek-chat")
	for _, one := range models {
		if strings.HasPrefix(one, "gemini") {
			t.Fatalf("did not expect gemini aliases when GEMINI_API_KEY is empty, got %v", models)
		}
	}
}

func assertContains(t *testing.T, values []string, target string) {
	t.Helper()
	for _, one := range values {
		if one == target {
			return
		}
	}
	t.Fatalf("expected %q in %#v", target, values)
}
