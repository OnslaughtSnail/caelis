package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoverGeminiModels_UsesAPIKeyHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-goog-api-key"); got != "token" {
			t.Fatalf("expected x-goog-api-key header, got %q", got)
		}
		if got := r.URL.Query().Get("key"); got != "" {
			t.Fatalf("did not expect key query param, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"models":[{"name":"models/gemini-2.5-flash","inputTokenLimit":1048576,"outputTokenLimit":65536,"supportedGenerationMethods":["generateContent"]}]}`)
	}))
	defer server.Close()

	got, err := discoverGeminiModels(context.Background(), server.Client(), Config{
		Provider: "gemini",
		API:      APIGemini,
		BaseURL:  server.URL,
		Auth: AuthConfig{
			Type:  AuthAPIKey,
			Token: "token",
		},
	}, "token")
	if err != nil {
		t.Fatalf("discoverGeminiModels failed: %v", err)
	}
	if len(got) != 1 || got[0].Name != "gemini-2.5-flash" {
		t.Fatalf("unexpected models: %+v", got)
	}
}
