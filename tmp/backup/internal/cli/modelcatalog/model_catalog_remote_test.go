package modelcatalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

// resetDynamicCatalog clears the package-level catalog state for test isolation.
func resetDynamicCatalog(t *testing.T) {
	t.Helper()
	dynamicMu.Lock()
	remoteCatalog = nil
	embeddedCatalog = nil
	localOverrides = nil
	dynamicMu.Unlock()
	t.Cleanup(func() {
		dynamicMu.Lock()
		remoteCatalog = nil
		embeddedCatalog = nil
		localOverrides = nil
		dynamicMu.Unlock()
	})
}

// writeTempOverride writes override JSON data to a temp file and returns its path.
func writeTempOverride(t *testing.T, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "override.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// minimalModelsDevPayload returns a small models.dev-format JSON payload for tests.
func minimalModelsDevPayload(t *testing.T) []byte {
	t.Helper()
	payload := map[string]interface{}{
		"openai": map[string]interface{}{
			"id": "openai",
			"models": map[string]interface{}{
				"gpt-4o": map[string]interface{}{
					"id":                "gpt-4o",
					"tool_call":         true,
					"reasoning":         false,
					"attachment":        true,
					"structured_output": true,
					"limit":             map[string]interface{}{"context": 128000, "output": 16384},
				},
			},
		},
		"google": map[string]interface{}{
			"id": "google",
			"models": map[string]interface{}{
				"gemini-2.0-flash": map[string]interface{}{
					"id":         "gemini-2.0-flash",
					"tool_call":  true,
					"attachment": true,
					"limit":      map[string]interface{}{"context": 1000000, "output": 8192},
				},
			},
		},
		"xiaomi": map[string]interface{}{
			"id": "xiaomi",
			"models": map[string]interface{}{
				"mimo-v2-flash": map[string]interface{}{
					"id":         "mimo-v2-flash",
					"tool_call":  true,
					"reasoning":  true,
					"attachment": false,
					"limit":      map[string]interface{}{"context": 262000, "output": 64000},
				},
			},
		},
		"empty-provider": map[string]interface{}{
			"id":     "empty-provider",
			"models": map[string]interface{}{},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// useTestServer redirects the package-level fetch URL to the given server and
// restores it when the test ends.
func useTestServer(t *testing.T, srv *httptest.Server) {
	t.Helper()
	modelsDevURLOverride = srv.URL
	t.Cleanup(func() { modelsDevURLOverride = "" })
}

// ---------------------------------------------------------------------------
// parseSnapshotBytes
// ---------------------------------------------------------------------------

func TestParseSnapshotBytes_ParsesValidEntries(t *testing.T) {
	raw := []byte(`{
		"_comment": "ignored",
		"openai:gpt-4o": {
			"context_window": 128000, "max_output": 16384,
			"default_max_output": 8192,
			"tool_calls": true, "reasoning": false, "images": true, "json_output": true
		},
		"deepseek:deepseek-reasoner": {
			"context_window": 128000, "max_output": 65536,
			"default_max_output": 32768,
			"tool_calls": true, "reasoning": true, "images": false, "json_output": true
		}
	}`)
	snap := parseSnapshotBytes(raw)
	if len(snap) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(snap))
	}
	e := snap["openai:gpt-4o"]
	if e.ContextWindow != 128000 {
		t.Errorf("ContextWindow: want 128000, got %d", e.ContextWindow)
	}
	if !e.Images {
		t.Error("expected Images=true")
	}
}

func TestParseSnapshotBytes_StripsCommentKey(t *testing.T) {
	raw := []byte(`{"_comment": "doc", "openai:gpt-4": {"context_window": 8192, "max_output": 4096}}`)
	snap := parseSnapshotBytes(raw)
	if _, ok := snap["_comment"]; ok {
		t.Error("_comment key should be stripped")
	}
	if _, ok := snap["openai:gpt-4"]; !ok {
		t.Error("expected openai:gpt-4")
	}
}

func TestParseSnapshotBytes_InvalidJSONReturnsNil(t *testing.T) {
	snap := parseSnapshotBytes([]byte("{bad json"))
	if snap != nil {
		t.Error("expected nil for invalid JSON")
	}
}

// ---------------------------------------------------------------------------
// parseModelsDevJSON
// ---------------------------------------------------------------------------

func TestParseModelsDevJSON_ParsesOpenAI(t *testing.T) {
	snap, err := parseModelsDevJSON(minimalModelsDevPayload(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e, ok := snap["openai:gpt-4o"]
	if !ok {
		t.Fatal("expected openai:gpt-4o")
	}
	if e.ContextWindow != 128000 {
		t.Errorf("ContextWindow: want 128000, got %d", e.ContextWindow)
	}
	if !e.ToolCalls {
		t.Error("expected ToolCalls=true")
	}
}

func TestParseModelsDevJSON_MapsGoogleToGemini(t *testing.T) {
	snap, err := parseModelsDevJSON(minimalModelsDevPayload(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap["gemini:gemini-2.0-flash"]; !ok {
		t.Error("expected google→gemini alias: gemini:gemini-2.0-flash")
	}
	if _, ok := snap["google:gemini-2.0-flash"]; ok {
		t.Error("raw 'google:' key should not exist after alias mapping")
	}
}

func TestParseModelsDevJSON_LeavesXiaomiUnderCanonicalProvider(t *testing.T) {
	snap, err := parseModelsDevJSON(minimalModelsDevPayload(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap["xiaomi:mimo-v2-flash"]; !ok {
		t.Fatal("expected xiaomi:mimo-v2-flash")
	}
	if _, ok := snap["mimo:mimo-v2-flash"]; ok {
		t.Fatal("did not expect xiaomi models to be rewritten to mimo")
	}
}

func TestParseModelsDevJSON_SkipsEmptyProvider(t *testing.T) {
	snap, err := parseModelsDevJSON(minimalModelsDevPayload(t))
	if err != nil {
		t.Fatal(err)
	}
	for k := range snap {
		if len(k) > 15 && k[:15] == "empty-provider:" {
			t.Errorf("empty-provider should be skipped, found key %q", k)
		}
	}
}

func TestParseModelsDevJSON_InvalidJSON(t *testing.T) {
	if _, err := parseModelsDevJSON([]byte("not json")); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ---------------------------------------------------------------------------
// searchCapSnapshot – prefix / longest-match
// ---------------------------------------------------------------------------

func TestSearchCapSnapshot_ExactMatch(t *testing.T) {
	snap := capSnapshot{
		"openai:gpt-4o": {ContextWindow: 128000, MaxOutput: 16384, ToolCalls: true},
	}
	caps, ok := searchCapSnapshot(snap, "openai", "gpt-4o")
	if !ok {
		t.Fatal("expected match")
	}
	if caps.ContextWindowTokens != 128000 {
		t.Errorf("want 128000, got %d", caps.ContextWindowTokens)
	}
	if caps.DefaultMaxOutputTokens != 8192 {
		t.Errorf("want default max output 8192, got %d", caps.DefaultMaxOutputTokens)
	}
}

func TestSearchCapSnapshot_PrefixMatch(t *testing.T) {
	snap := capSnapshot{
		"openai:gpt-4o": {ContextWindow: 128000, MaxOutput: 16384, ToolCalls: true},
	}
	caps, ok := searchCapSnapshot(snap, "openai", "gpt-4o-mini")
	if !ok {
		t.Fatal("expected prefix match gpt-4o-mini → gpt-4o")
	}
	if !caps.SupportsToolCalls {
		t.Error("expected SupportsToolCalls=true")
	}
}

func TestSearchCapSnapshot_LongestPrefixWins(t *testing.T) {
	snap := capSnapshot{
		"openai:gpt-4":    {ContextWindow: 8192},
		"openai:gpt-4o":   {ContextWindow: 128000},
		"openai:gpt-4o-m": {ContextWindow: 256000, DefaultMaxOutput: 256000},
	}
	caps, ok := searchCapSnapshot(snap, "openai", "gpt-4o-mini")
	if !ok {
		t.Fatal("expected match")
	}
	// "gpt-4o-m" is the longest prefix of "gpt-4o-mini"
	if caps.ContextWindowTokens != 256000 {
		t.Errorf("expected longest-prefix (256000), got %d", caps.ContextWindowTokens)
	}
}

func TestSearchCapSnapshot_ProviderMismatch(t *testing.T) {
	snap := capSnapshot{
		"anthropic:claude-3": {ContextWindow: 200000},
	}
	if _, ok := searchCapSnapshot(snap, "openai", "claude-3"); ok {
		t.Error("should not match different provider")
	}
}

func TestSearchCapSnapshot_GeminiAliasLookup(t *testing.T) {
	// Key stored as "google:" (models.dev native), queried with internal "gemini".
	snap := capSnapshot{
		"google:gemini-2.0-flash": {ContextWindow: 1000000, MaxOutput: 8192, ToolCalls: true},
	}
	caps, ok := searchCapSnapshot(snap, "gemini", "gemini-2.0-flash-exp")
	if !ok {
		t.Fatal("expected match via google→gemini alias")
	}
	if caps.ContextWindowTokens != 1000000 {
		t.Errorf("want 1000000, got %d", caps.ContextWindowTokens)
	}
}

func TestSearchCapSnapshot_XiaomiLookup(t *testing.T) {
	snap := capSnapshot{
		"xiaomi:mimo-v2-flash": {ContextWindow: 262000, MaxOutput: 64000, ToolCalls: true, Reasoning: true},
	}
	caps, ok := searchCapSnapshot(snap, "xiaomi", "mimo-v2-flash")
	if !ok {
		t.Fatal("expected xiaomi lookup to match xiaomi snapshot key")
	}
	if caps.ContextWindowTokens != 262000 {
		t.Fatalf("want 262000, got %d", caps.ContextWindowTokens)
	}
}

func TestSearchCapSnapshot_NilSnapshot(t *testing.T) {
	if _, ok := searchCapSnapshot(nil, "openai", "gpt-4o"); ok {
		t.Error("nil snapshot should return false")
	}
}

// ---------------------------------------------------------------------------
// defaultMaxOutputHeuristic
// ---------------------------------------------------------------------------

func TestDefaultMaxOutputHeuristic_ReasoningLargeModel(t *testing.T) {
	if got := defaultMaxOutputHeuristic(65536, 128000, true); got != 32768 {
		t.Errorf("want 32768, got %d", got)
	}
}

func TestDefaultMaxOutputHeuristic_NonReasoningLargeModel(t *testing.T) {
	if got := defaultMaxOutputHeuristic(65536, 128000, false); got != 8192 {
		t.Errorf("want 8192, got %d", got)
	}
}

func TestDefaultMaxOutputHeuristic_NonReasoningSmallModel(t *testing.T) {
	if got := defaultMaxOutputHeuristic(4096, 128000, false); got != 4096 {
		t.Errorf("want 4096 (pass-through), got %d", got)
	}
}

func TestDefaultMaxOutputHeuristic_ZeroMaxOut(t *testing.T) {
	if got := defaultMaxOutputHeuristic(0, 128000, false); got != 8192 {
		t.Errorf("want fallback 8192, got %d", got)
	}
}

func TestDefaultMaxOutputHeuristic_ReasoningSmallModel(t *testing.T) {
	if got := defaultMaxOutputHeuristic(16384, 128000, true); got != 16384 {
		t.Errorf("want 16384 (reasoning < 32768 passes through), got %d", got)
	}
}

func TestDefaultMaxOutputHeuristic_CapsNonReasoningByContext(t *testing.T) {
	if got := defaultMaxOutputHeuristic(65536, 16000, false); got != 2000 {
		t.Errorf("want context/8 cap 2000, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// InitModelCatalog – remote fetch via local HTTP test server
// ---------------------------------------------------------------------------

func TestInitModelCatalog_LoadsRemoteCatalog(t *testing.T) {
	resetDynamicCatalog(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(minimalModelsDevPayload(t)) //nolint:errcheck
	}))
	defer srv.Close()
	useTestServer(t, srv)

	InitModelCatalog(context.Background(), srv.Client(), "")

	dynamicMu.RLock()
	snap := remoteCatalog
	dynamicMu.RUnlock()

	if len(snap) == 0 {
		t.Fatal("expected remote catalog to be loaded")
	}
	if _, ok := snap["openai:gpt-4o"]; !ok {
		t.Error("expected openai:gpt-4o after remote fetch")
	}
	if _, ok := snap["gemini:gemini-2.0-flash"]; !ok {
		t.Error("expected gemini:gemini-2.0-flash after alias mapping")
	}
}

func TestInitModelCatalog_FallsBackToEmbeddedSnapshot(t *testing.T) {
	resetDynamicCatalog(t)

	// Server returns 500 – remote fetch must fail.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusInternalServerError)
	}))
	defer srv.Close()
	useTestServer(t, srv)

	InitModelCatalog(context.Background(), srv.Client(), "")

	dynamicMu.RLock()
	remote := remoteCatalog
	embedded := embeddedCatalog
	dynamicMu.RUnlock()

	if len(remote) != 0 {
		t.Error("expected remote catalog empty after remote failure")
	}
	if len(embedded) == 0 {
		t.Error("expected embedded snapshot to be loaded after remote failure")
	}
}

func TestInitModelCatalog_LocalOverrideLoaded(t *testing.T) {
	resetDynamicCatalog(t)

	// Remote fails so we know the interesting data only comes from overrides.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusInternalServerError)
	}))
	defer srv.Close()
	useTestServer(t, srv)

	overridePath := writeTempOverride(t, []byte(`{
		"openai:test-model-x": {
			"context_window": 99999, "max_output": 9999,
			"default_max_output": 5000,
			"tool_calls": true, "reasoning": false, "images": false, "json_output": true
		}
	}`))

	InitModelCatalog(context.Background(), srv.Client(), overridePath)

	dynamicMu.RLock()
	ov := localOverrides
	dynamicMu.RUnlock()

	e, ok := ov["openai:test-model-x"]
	if !ok {
		t.Fatal("override entry not found in localOverrides")
	}
	if e.ContextWindow != 99999 {
		t.Errorf("want context_window=99999, got %d", e.ContextWindow)
	}
}

func TestInitModelCatalog_MissingOverrideFileSilent(t *testing.T) {
	resetDynamicCatalog(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusInternalServerError)
	}))
	defer srv.Close()
	useTestServer(t, srv)

	// Non-existent path – must not panic or error.
	InitModelCatalog(context.Background(), srv.Client(), "/tmp/no-such-file-caelis-test.json")
}

// ---------------------------------------------------------------------------
// LookupModelCapabilities priority chain
// ---------------------------------------------------------------------------

func TestLookupModelCapabilities_LocalOverrideTakesPriority(t *testing.T) {
	resetDynamicCatalog(t)

	dynamicMu.Lock()
	remoteCatalog = capSnapshot{
		"openai:gpt-4o": {ContextWindow: 111, MaxOutput: 111, ToolCalls: true, DefaultMaxOutput: 111},
	}
	localOverrides = capSnapshot{
		"openai:gpt-4o": {ContextWindow: 999, MaxOutput: 999, ToolCalls: true, DefaultMaxOutput: 999},
	}
	dynamicMu.Unlock()

	caps, ok := LookupModelCapabilities("openai", "gpt-4o")
	if !ok {
		t.Fatal("expected match")
	}
	if caps.ContextWindowTokens != 999 {
		t.Errorf("local override should win: want 999, got %d", caps.ContextWindowTokens)
	}
}

func TestLookupModelCapabilities_DynamicOverridesBuiltin(t *testing.T) {
	resetDynamicCatalog(t)

	// Put a different value for deepseek-chat in the dynamic catalog.
	dynamicMu.Lock()
	remoteCatalog = capSnapshot{
		"deepseek:deepseek-chat": {
			ContextWindow: 200000, MaxOutput: 65536, DefaultMaxOutput: 8192,
			ToolCalls: true, Reasoning: true, JSONOutput: true,
		},
	}
	dynamicMu.Unlock()

	caps, ok := LookupModelCapabilities("deepseek", "deepseek-chat")
	if !ok {
		t.Fatal("expected match")
	}
	if caps.ContextWindowTokens != 200000 {
		t.Errorf("dynamic catalog should override builtin: want 200000, got %d", caps.ContextWindowTokens)
	}
}

func TestLookupModelCapabilities_RemoteMissFallsBackToEmbedded(t *testing.T) {
	resetDynamicCatalog(t)

	dynamicMu.Lock()
	remoteCatalog = capSnapshot{
		"openai:gpt-4o": {ContextWindow: 128000, MaxOutput: 16384, ToolCalls: true},
	}
	embeddedCatalog = capSnapshot{
		"deepseek:deepseek-chat": {
			ContextWindow: 128000, MaxOutput: 65536, DefaultMaxOutput: 8192,
			ToolCalls: true, Reasoning: true, JSONOutput: true,
		},
	}
	dynamicMu.Unlock()

	caps, ok := LookupModelCapabilities("deepseek", "deepseek-chat")
	if !ok {
		t.Fatal("expected embedded fallback when remote catalog misses model")
	}
	if caps.ContextWindowTokens != 128000 {
		t.Fatalf("expected embedded context 128000, got %d", caps.ContextWindowTokens)
	}
}

func TestLookupModelCapabilities_FallsBackToBuiltin(t *testing.T) {
	resetDynamicCatalog(t)
	// Dynamic catalog is empty – must fall through to builtinCatalog.
	caps, ok := LookupModelCapabilities("deepseek", "deepseek-reasoner")
	if !ok {
		t.Fatal("expected builtin fallback")
	}
	if !caps.SupportsReasoning {
		t.Error("deepseek-reasoner must have SupportsReasoning=true")
	}
}

func TestLookupModelCapabilities_DefaultMaxFilledFromBuiltin(t *testing.T) {
	resetDynamicCatalog(t)

	// Dynamic entry omits DefaultMaxOutput (0) → should be backfilled from builtin.
	dynamicMu.Lock()
	remoteCatalog = capSnapshot{
		"deepseek:deepseek-chat": {
			ContextWindow: 128000, MaxOutput: 65536, // DefaultMaxOutput intentionally 0
			ToolCalls: true, Reasoning: true,
		},
	}
	dynamicMu.Unlock()

	caps, ok := LookupModelCapabilities("deepseek", "deepseek-chat")
	if !ok {
		t.Fatal("expected match")
	}
	if caps.DefaultMaxOutputTokens <= 0 {
		t.Errorf("DefaultMaxOutputTokens must be backfilled from builtin, got %d", caps.DefaultMaxOutputTokens)
	}
}

// ---------------------------------------------------------------------------
// Concurrency safety
// ---------------------------------------------------------------------------

func TestInitModelCatalog_ConcurrentCallsSafe(t *testing.T) {
	defer func() {
		dynamicMu.Lock()
		remoteCatalog = nil
		embeddedCatalog = nil
		localOverrides = nil
		dynamicMu.Unlock()
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusInternalServerError)
	}))
	defer srv.Close()
	useTestServer(t, srv)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			InitModelCatalog(context.Background(), srv.Client(), "")
		}()
	}
	wg.Wait()
	// If the race detector is enabled (-race), any data race will cause failure.
}
