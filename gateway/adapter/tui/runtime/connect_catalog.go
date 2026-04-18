package runtime

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdkproviders "github.com/OnslaughtSnail/caelis/sdk/model/providers"
	"github.com/OnslaughtSnail/caelis/tui/modelcatalog"
)

const (
	connectVolcengineStandardValue = "standard"
	connectVolcengineCodingValue   = "coding-plan"
	connectCustomModelValue        = "__custom_model__"
	modelCatalogCacheTTL           = 24 * time.Hour
)

type providerTemplate struct {
	label               string
	api                 sdkproviders.APIType
	provider            string
	defaultBaseURL      string
	defaultContextToken int
	defaultMaxOutputTok int
	noAuthRequired      bool
	commonModels        []string
}

type connectModelChoice struct {
	Name    string
	Display string
	Detail  string
}

type connectModelDefaults struct {
	ContextWindow   int
	MaxOutput       int
	ReasoningLevels []string
}

type connectWizardPayload struct {
	Provider string
	BaseURL  string
	Timeout  string
	APIKey   string
	Model    string
}

var providerTemplates = []providerTemplate{
	{label: "openai", api: sdkproviders.APIOpenAI, provider: "openai", defaultBaseURL: "https://api.openai.com/v1", defaultContextToken: 128000, commonModels: []string{"gpt-4o", "gpt-4o-mini", "o3", "o4-mini"}},
	{label: "openai-compatible", api: sdkproviders.APIOpenAICompatible, provider: "openai-compatible", defaultBaseURL: "https://api.openai.com/v1", defaultContextToken: 128000, commonModels: []string{"gpt-4o", "gpt-4o-mini", "o3", "o4-mini"}},
	{label: "openrouter", api: sdkproviders.APIOpenRouter, provider: "openrouter", defaultBaseURL: "https://openrouter.ai/api/v1", defaultContextToken: 262144, commonModels: []string{"openai/gpt-4o-mini", "anthropic/claude-sonnet-4", "google/gemini-2.5-flash"}},
	{label: "gemini", api: sdkproviders.APIGemini, provider: "gemini", defaultBaseURL: "https://generativelanguage.googleapis.com/v1beta", defaultContextToken: 128000, commonModels: []string{"gemini-2.5-flash", "gemini-2.5-pro"}},
	{label: "anthropic", api: sdkproviders.APIAnthropic, provider: "anthropic", defaultBaseURL: "https://api.anthropic.com", defaultContextToken: 200000, defaultMaxOutputTok: 1024, commonModels: []string{"claude-sonnet-4-20250514", "claude-opus-4-20250514"}},
	{label: "anthropic-compatible", api: sdkproviders.APIAnthropicCompatible, provider: "anthropic-compatible", defaultBaseURL: "https://api.anthropic.com", defaultContextToken: 200000, defaultMaxOutputTok: 1024},
	{label: "deepseek", api: sdkproviders.APIDeepSeek, provider: "deepseek", defaultBaseURL: "https://api.deepseek.com/v1", defaultContextToken: 128000, commonModels: []string{"deepseek-chat", "deepseek-reasoner"}},
	{label: "xiaomi", api: sdkproviders.APIMimo, provider: "xiaomi", defaultBaseURL: "https://api.xiaomimimo.com/v1", defaultContextToken: 128000, commonModels: []string{"mimo-v2-flash", "mimo-v2-reasoner"}},
	{label: "minimax", api: sdkproviders.APIAnthropicCompatible, provider: "minimax", defaultBaseURL: "https://api.minimaxi.com/anthropic", defaultContextToken: 204800, defaultMaxOutputTok: 8192, commonModels: []string{"MiniMax-M2.7", "MiniMax-M2.7-highspeed", "MiniMax-M2.5", "MiniMax-M2.5-highspeed", "MiniMax-M2.1", "MiniMax-M2.1-highspeed", "MiniMax-M2"}},
	{label: "volcengine", api: sdkproviders.APIVolcengine, provider: "volcengine", defaultBaseURL: "https://ark.cn-beijing.volces.com/api/v3", defaultContextToken: 128000},
	{label: "ollama", api: sdkproviders.APIOllama, provider: "ollama", defaultBaseURL: "http://localhost:11434", defaultContextToken: 128000, noAuthRequired: true, commonModels: []string{"qwen2.5:7b", "llama3.1:8b", "deepseek-r1:7b", "gemma3:4b"}},
}

var (
	connectDiscoverModelsFn = sdkproviders.DiscoverModels
	connectCatalogInitMu    sync.Mutex
	connectCatalogLastSync  time.Time
)

func completeConnectArgs(ctx context.Context, command string, query string, limit int) ([]SlashArgCandidate, error) {
	switch {
	case command == "connect":
		return completeConnectProviders(query, limit), nil
	case strings.HasPrefix(command, "connect-baseurl:"):
		return completeConnectBaseURL(strings.TrimPrefix(command, "connect-baseurl:"), query, limit), nil
	case strings.HasPrefix(command, "connect-timeout:"):
		return completeConnectTimeout(strings.TrimPrefix(command, "connect-timeout:"), query, limit), nil
	case strings.HasPrefix(command, "connect-apikey:"):
		return nil, nil
	case strings.HasPrefix(command, "connect-model:"):
		return completeConnectModels(ctx, parseConnectWizardPayload(strings.TrimPrefix(command, "connect-model:")), query, limit)
	case strings.HasPrefix(command, "connect-context:"):
		return completeConnectContext(ctx, parseConnectWizardPayload(strings.TrimPrefix(command, "connect-context:")), query, limit)
	case strings.HasPrefix(command, "connect-maxout:"):
		return completeConnectMaxOutput(ctx, parseConnectWizardPayload(strings.TrimPrefix(command, "connect-maxout:")), query, limit)
	case strings.HasPrefix(command, "connect-reasoning-levels:"):
		return completeConnectReasoningLevels(ctx, parseConnectWizardPayload(strings.TrimPrefix(command, "connect-reasoning-levels:")), query, limit)
	default:
		return nil, nil
	}
}

func completeConnectProviders(query string, limit int) []SlashArgCandidate {
	out := make([]SlashArgCandidate, 0, len(providerTemplates))
	for _, tpl := range providerTemplates {
		if query != "" && !strings.Contains(strings.ToLower(tpl.label+" "+tpl.defaultBaseURL), strings.ToLower(strings.TrimSpace(query))) {
			continue
		}
		detail := strings.TrimSpace(tpl.defaultBaseURL)
		if tpl.noAuthRequired {
			detail = strings.TrimSpace(detail + " · no auth")
		}
		out = append(out, SlashArgCandidate{
			Value:   tpl.label,
			Display: tpl.label,
			Detail:  detail,
			NoAuth:  tpl.noAuthRequired,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func completeConnectBaseURL(provider string, query string, limit int) []SlashArgCandidate {
	tpl, ok := findProviderTemplate(provider)
	if !ok {
		return nil
	}
	var candidates []SlashArgCandidate
	if tpl.provider == "volcengine" {
		candidates = append(candidates,
			SlashArgCandidate{Value: "https://ark.cn-beijing.volces.com/api/v3", Display: "standard api", Detail: connectVolcengineStandardValue},
			SlashArgCandidate{Value: "https://ark.cn-beijing.volces.com/api/coding/v3", Display: "coding plan", Detail: connectVolcengineCodingValue},
		)
	} else {
		candidates = append(candidates, SlashArgCandidate{Value: tpl.defaultBaseURL, Display: tpl.defaultBaseURL, Detail: "default"})
	}
	return filterSlashArgCandidates(candidates, query, limit)
}

func completeConnectTimeout(provider string, query string, limit int) []SlashArgCandidate {
	values := []string{"60", "120", "180"}
	out := make([]SlashArgCandidate, 0, len(values))
	for _, value := range values {
		out = append(out, SlashArgCandidate{Value: value, Display: value, Detail: fmt.Sprintf("%ss", value)})
	}
	_ = provider
	return filterSlashArgCandidates(out, query, limit)
}

func completeConnectModels(ctx context.Context, payload connectWizardPayload, query string, limit int) ([]SlashArgCandidate, error) {
	tpl, ok := findProviderTemplate(payload.Provider)
	if !ok {
		return nil, nil
	}
	_, _ = ensureConnectCatalog(ctx)
	cfg := buildDiscoveryConfig(tpl, payload)
	var remoteModels []sdkproviders.RemoteModel
	if shouldDiscoverConnectModels(tpl) {
		models, err := connectDiscoverModelsFn(ctx, cfg)
		if err == nil {
			remoteModels = models
		}
	}
	fallbackModels := fallbackConnectModels(tpl, remoteModels)
	choices := buildConnectModelChoices(tpl.provider, remoteModels, fallbackModels)
	out := make([]SlashArgCandidate, 0, len(choices))
	for _, choice := range choices {
		if query != "" && !strings.Contains(strings.ToLower(choice.Name+" "+choice.Display+" "+choice.Detail), strings.ToLower(strings.TrimSpace(query))) {
			continue
		}
		out = append(out, SlashArgCandidate{
			Value:   choice.Name,
			Display: choice.Display,
			Detail:  choice.Detail,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func completeConnectContext(ctx context.Context, payload connectWizardPayload, query string, limit int) ([]SlashArgCandidate, error) {
	defaults, err := connectDefaultsForPayload(ctx, payload)
	if err != nil {
		return nil, err
	}
	return filterSlashArgCandidates([]SlashArgCandidate{{Value: strconv.Itoa(defaults.ContextWindow), Display: strconv.Itoa(defaults.ContextWindow), Detail: "context window tokens"}}, query, limit), nil
}

func completeConnectMaxOutput(ctx context.Context, payload connectWizardPayload, query string, limit int) ([]SlashArgCandidate, error) {
	defaults, err := connectDefaultsForPayload(ctx, payload)
	if err != nil {
		return nil, err
	}
	return filterSlashArgCandidates([]SlashArgCandidate{{Value: strconv.Itoa(defaults.MaxOutput), Display: strconv.Itoa(defaults.MaxOutput), Detail: "max output tokens"}}, query, limit), nil
}

func completeConnectReasoningLevels(ctx context.Context, payload connectWizardPayload, query string, limit int) ([]SlashArgCandidate, error) {
	defaults, err := connectDefaultsForPayload(ctx, payload)
	if err != nil {
		return nil, err
	}
	value := "-"
	detail := "no reasoning levels"
	if len(defaults.ReasoningLevels) > 0 {
		value = strings.Join(defaults.ReasoningLevels, ",")
		detail = "suggested reasoning levels"
	}
	return filterSlashArgCandidates([]SlashArgCandidate{{Value: value, Display: value, Detail: detail}}, query, limit), nil
}

func connectDefaultsForPayload(ctx context.Context, payload connectWizardPayload) (connectModelDefaults, error) {
	tpl, ok := findProviderTemplate(payload.Provider)
	if !ok {
		return connectModelDefaults{}, nil
	}
	_, _ = ensureConnectCatalog(ctx)
	cfg := buildDiscoveryConfig(tpl, payload)
	var remote *sdkproviders.RemoteModel
	if shouldDiscoverConnectModels(tpl) {
		models, err := connectDiscoverModelsFn(ctx, cfg)
		if err == nil {
			remote = findRemoteModelByName(models, payload.Model)
		}
	}
	baseCaps, baseKnown := modelcatalog.LookupDynamicModelCapabilities(tpl.provider, payload.Model)
	if !baseKnown {
		baseCaps, baseKnown = modelcatalog.LookupModelCapabilities(tpl.provider, payload.Model)
	}
	if !baseKnown {
		baseCaps = modelcatalog.DefaultModelCapabilities()
	}
	if baseCaps.ContextWindowTokens <= 0 {
		baseCaps.ContextWindowTokens = defaultContextWindowForTemplate(tpl)
	}
	if baseCaps.DefaultMaxOutputTokens <= 0 {
		baseCaps.DefaultMaxOutputTokens = defaultMaxOutputForTemplate(tpl)
	}
	if baseCaps.MaxOutputTokens <= 0 {
		baseCaps.MaxOutputTokens = baseCaps.DefaultMaxOutputTokens
	}
	if overlayCaps, ok := modelcatalog.LookupOverlayModelCapabilities(tpl.provider, payload.Model); ok {
		if overlayCaps.ContextWindowTokens > 0 {
			baseCaps.ContextWindowTokens = overlayCaps.ContextWindowTokens
		}
		if overlayCaps.DefaultMaxOutputTokens > 0 {
			baseCaps.DefaultMaxOutputTokens = overlayCaps.DefaultMaxOutputTokens
		}
		if len(normalizeReasoningLevels(overlayCaps.ReasoningEfforts)) > 0 {
			baseCaps.ReasoningEfforts = normalizeReasoningLevels(overlayCaps.ReasoningEfforts)
		}
	}
	if remote != nil {
		if remote.ContextWindowTokens > 0 {
			baseCaps.ContextWindowTokens = remote.ContextWindowTokens
		}
		if remote.MaxOutputTokens > 0 {
			baseCaps.MaxOutputTokens = remote.MaxOutputTokens
			baseCaps.DefaultMaxOutputTokens = remote.MaxOutputTokens
		}
		if _, supportsReasoning, _, _, known := connectRemoteCapabilities(remote); known && supportsReasoning && len(baseCaps.ReasoningEfforts) == 0 {
			baseCaps.ReasoningEfforts = []string{"low", "medium", "high"}
		}
	}
	contextWindow := baseCaps.ContextWindowTokens
	if contextWindow <= 0 {
		contextWindow = defaultContextWindowForTemplate(tpl)
	}
	maxOutput := baseCaps.DefaultMaxOutputTokens
	if maxOutput <= 0 {
		maxOutput = baseCaps.MaxOutputTokens
	}
	if maxOutput <= 0 {
		maxOutput = defaultMaxOutputForTemplate(tpl)
	}
	return connectModelDefaults{
		ContextWindow:   contextWindow,
		MaxOutput:       maxOutput,
		ReasoningLevels: normalizeReasoningLevels(baseCaps.ReasoningEfforts),
	}, nil
}

func ensureConnectCatalog(ctx context.Context) (modelcatalog.CatalogInitStatus, bool) {
	connectCatalogInitMu.Lock()
	defer connectCatalogInitMu.Unlock()
	if !connectCatalogLastSync.IsZero() && time.Since(connectCatalogLastSync) < modelCatalogCacheTTL {
		return modelcatalog.CatalogInitStatus{}, false
	}
	status := modelcatalog.InitModelCatalogWithStatus(ctx, nil, defaultModelCatalogOverridePath())
	if status.RemoteFetched || status.RemoteError == nil {
		connectCatalogLastSync = time.Now()
	}
	return status, true
}

func defaultModelCatalogOverridePath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".agents", "model_capabilities.json")
}

func buildDiscoveryConfig(tpl providerTemplate, payload connectWizardPayload) sdkproviders.Config {
	baseURL := strings.TrimSpace(payload.BaseURL)
	if baseURL == "" {
		baseURL = tpl.defaultBaseURL
	}
	timeoutSeconds, _ := strconv.Atoi(strings.TrimSpace(payload.Timeout))
	if timeoutSeconds <= 0 {
		timeoutSeconds = 60
	}
	authType := sdkproviders.AuthAPIKey
	if tpl.noAuthRequired {
		authType = sdkproviders.AuthNone
	}
	return sdkproviders.Config{
		Provider: tpl.provider,
		API:      tpl.api,
		BaseURL:  baseURL,
		Timeout:  time.Duration(timeoutSeconds) * time.Second,
		Auth: sdkproviders.AuthConfig{
			Type:  authType,
			Token: strings.TrimSpace(payload.APIKey),
		},
	}
}

func parseConnectWizardPayload(raw string) connectWizardPayload {
	parts := strings.SplitN(raw, "|", 5)
	for len(parts) < 5 {
		parts = append(parts, "")
	}
	return connectWizardPayload{
		Provider: strings.TrimSpace(parts[0]),
		BaseURL:  decodeConnectWizardPart(parts[1]),
		Timeout:  strings.TrimSpace(parts[2]),
		APIKey:   decodeConnectWizardPart(parts[3]),
		Model:    decodeConnectWizardPart(parts[4]),
	}
}

func decodeConnectWizardPart(value string) string {
	decoded, err := url.QueryUnescape(strings.TrimSpace(value))
	if err != nil {
		return strings.TrimSpace(value)
	}
	return decoded
}

func filterSlashArgCandidates(candidates []SlashArgCandidate, query string, limit int) []SlashArgCandidate {
	query = strings.ToLower(strings.TrimSpace(query))
	out := make([]SlashArgCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if query != "" && !strings.Contains(strings.ToLower(candidate.Value+" "+candidate.Display+" "+candidate.Detail), query) {
			continue
		}
		out = append(out, candidate)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func findProviderTemplate(value string) (providerTemplate, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, tpl := range providerTemplates {
		if tpl.label == value || tpl.provider == value {
			return tpl, true
		}
	}
	return providerTemplate{}, false
}

func buildConnectModelChoices(provider string, remoteModels []sdkproviders.RemoteModel, fallbackModels []string) []connectModelChoice {
	seen := map[string]struct{}{}
	out := []connectModelChoice{{
		Name:    connectCustomModelValue,
		Display: "custom model",
		Detail:  "Enter a model name manually.",
	}}
	add := func(name string, detail string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, connectModelChoice{
			Name:    name,
			Display: connectDisplayModelRef(provider, name),
			Detail:  strings.TrimSpace(detail),
		})
	}
	for _, item := range fallbackModels {
		add(item, "")
	}
	for _, item := range remoteModels {
		add(item.Name, describeRemoteModelDetail(item))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name == connectCustomModelValue {
			return true
		}
		if out[j].Name == connectCustomModelValue {
			return false
		}
		return strings.ToLower(out[i].Display) < strings.ToLower(out[j].Display)
	})
	return out
}

func fallbackConnectModels(tpl providerTemplate, remoteModels []sdkproviders.RemoteModel) []string {
	if len(remoteModels) > 0 {
		return nil
	}
	if (tpl.api == sdkproviders.APIVolcengineCoding || tpl.provider == "minimax") && len(tpl.commonModels) > 0 {
		return append([]string(nil), tpl.commonModels...)
	}
	if models := modelcatalog.ListCatalogModels(tpl.provider); len(models) > 0 {
		return models
	}
	if len(tpl.commonModels) > 0 {
		return append([]string(nil), tpl.commonModels...)
	}
	return commonModelsForProvider(tpl.provider)
}

func shouldDiscoverConnectModels(tpl providerTemplate) bool {
	return tpl.provider != "volcengine"
}

func findRemoteModelByName(models []sdkproviders.RemoteModel, name string) *sdkproviders.RemoteModel {
	for i := range models {
		if strings.EqualFold(strings.TrimSpace(models[i].Name), strings.TrimSpace(name)) {
			return &models[i]
		}
	}
	return nil
}

func connectDisplayModelRef(provider, modelName string) string {
	provider = strings.TrimSpace(provider)
	modelName = strings.TrimSpace(modelName)
	if provider == "" {
		return modelName
	}
	if modelName == "" {
		return provider
	}
	if strings.HasPrefix(strings.ToLower(modelName), strings.ToLower(provider)+"/") {
		return modelName
	}
	return provider + "/" + modelName
}

func describeRemoteModelDetail(item sdkproviders.RemoteModel) string {
	parts := make([]string, 0, 3)
	if item.ContextWindowTokens > 0 {
		parts = append(parts, fmt.Sprintf("ctx %d", item.ContextWindowTokens))
	}
	if item.MaxOutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("out %d", item.MaxOutputTokens))
	}
	if len(item.Capabilities) > 0 {
		parts = append(parts, strings.Join(item.Capabilities, ","))
	}
	return strings.Join(parts, " · ")
}

func defaultContextWindowForTemplate(tpl providerTemplate) int {
	if tpl.defaultContextToken > 0 {
		return tpl.defaultContextToken
	}
	return 128000
}

func defaultMaxOutputForTemplate(tpl providerTemplate) int {
	if tpl.defaultMaxOutputTok > 0 {
		return tpl.defaultMaxOutputTok
	}
	return 4096
}

func commonModelsForProvider(provider string) []string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	for _, tpl := range providerTemplates {
		if tpl.provider == provider || tpl.label == provider {
			return append([]string(nil), tpl.commonModels...)
		}
	}
	return nil
}

func connectRemoteCapabilities(remote *sdkproviders.RemoteModel) (supportsToolCalls bool, supportsReasoning bool, supportsImages bool, supportsJSON bool, known bool) {
	if remote == nil {
		return false, false, false, false, false
	}
	for _, cap := range remote.Capabilities {
		switch strings.ToLower(strings.TrimSpace(cap)) {
		case "tools", "tool_call", "tool_calls", "function_calling", "function-calling":
			supportsToolCalls = true
			known = true
		case "reasoning", "thinking":
			supportsReasoning = true
			known = true
		case "image", "images", "vision":
			supportsImages = true
			known = true
		case "json", "structured_output", "structured-output":
			supportsJSON = true
			known = true
		}
	}
	return
}

func normalizeReasoningLevels(levels []string) []string {
	if len(levels) == 0 {
		return nil
	}
	out := make([]string, 0, len(levels))
	seen := map[string]struct{}{}
	for _, level := range levels {
		trimmed := strings.ToLower(strings.TrimSpace(level))
		if trimmed == "" || trimmed == "-" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
