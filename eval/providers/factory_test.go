package providers

import "testing"

func TestListModelsContainsDefaultAliases(t *testing.T) {
	models := ListModels()
	if len(models) == 0 {
		t.Fatalf("expected non-empty model aliases")
	}
	assertContains(t, models, "deepseek-chat")
	assertContains(t, models, "gemini-2.5-flash")
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
