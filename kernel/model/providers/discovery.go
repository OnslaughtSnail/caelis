package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RemoteModel describes one model discovered from provider list APIs.
type RemoteModel struct {
	Name                string
	ContextWindowTokens int
	MaxOutputTokens     int
	Capabilities        []string
}

// DiscoverModels queries provider list-model APIs using one provider config.
// It returns an error when provider does not expose list APIs or auth is invalid.
func DiscoverModels(ctx context.Context, cfg Config) ([]RemoteModel, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	token, err := resolveToken(cfg.Auth)
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	switch cfg.API {
	case APIOpenAI, APIOpenAICompatible, APIDeepSeek:
		return discoverOpenAIModels(ctx, client, cfg, token)
	case APIGemini:
		return discoverGeminiModels(ctx, client, cfg, token)
	case APIAnthropic:
		return discoverAnthropicModels(ctx, client, cfg, token)
	default:
		return nil, fmt.Errorf("providers: unsupported api type %q for list_models", cfg.API)
	}
}

func discoverOpenAIModels(ctx context.Context, client *http.Client, cfg Config, token string) ([]RemoteModel, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	applyDefaultAuthHeader(req, cfg, token, false)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, statusError(resp)
	}
	var payload struct {
		Data []struct {
			ID                 string `json:"id"`
			ContextWindow      any    `json:"context_window"`
			MaxOutputTokens    any    `json:"max_output_tokens"`
			InputTokenLimit    any    `json:"input_token_limit"`
			OutputTokenLimit   any    `json:"output_token_limit"`
			Capabilities       any    `json:"capabilities"`
			SupportedMethods   any    `json:"supported_generation_methods"`
			SupportsReasoning  any    `json:"supports_reasoning"`
			ReasoningSupported any    `json:"reasoning_supported"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	models := make([]RemoteModel, 0, len(payload.Data))
	for _, item := range payload.Data {
		name := strings.TrimSpace(item.ID)
		if name == "" {
			continue
		}
		ctxWindow := firstPositiveInt(
			toInt(item.ContextWindow),
			toInt(item.InputTokenLimit),
		)
		maxOutput := firstPositiveInt(
			toInt(item.MaxOutputTokens),
			toInt(item.OutputTokenLimit),
		)
		caps := appendUniqueStrings(nil, toStringSlice(item.Capabilities)...)
		caps = appendUniqueStrings(caps, toStringSlice(item.SupportedMethods)...)
		if toBool(item.SupportsReasoning) || toBool(item.ReasoningSupported) {
			caps = appendUniqueStrings(caps, "reasoning")
		}
		models = append(models, RemoteModel{
			Name:                name,
			ContextWindowTokens: ctxWindow,
			MaxOutputTokens:     maxOutput,
			Capabilities:        caps,
		})
	}
	return normalizeRemoteModels(models), nil
}

func discoverGeminiModels(ctx context.Context, client *http.Client, cfg Config, token string) ([]RemoteModel, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/") + "/models"
	all := make([]RemoteModel, 0, 16)
	pageToken := ""
	for i := 0; i < 5; i++ {
		query := url.Values{}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		if cfg.Auth.Type == AuthAPIKey || cfg.Auth.Type == "" {
			query.Set("key", token)
		}
		endpoint := base
		if encoded := query.Encode(); encoded != "" {
			endpoint += "?" + encoded
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		if cfg.Auth.Type != AuthAPIKey && cfg.Auth.Type != "" {
			applyDefaultAuthHeader(req, cfg, token, true)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		var payload struct {
			Models []struct {
				Name                       string   `json:"name"`
				InputTokenLimit            int      `json:"inputTokenLimit"`
				OutputTokenLimit           int      `json:"outputTokenLimit"`
				SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, statusError(resp)
		}
		err = json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, item := range payload.Models {
			name := strings.TrimSpace(strings.TrimPrefix(item.Name, "models/"))
			if name == "" {
				continue
			}
			all = append(all, RemoteModel{
				Name:                name,
				ContextWindowTokens: item.InputTokenLimit,
				MaxOutputTokens:     item.OutputTokenLimit,
				Capabilities:        appendUniqueStrings(nil, item.SupportedGenerationMethods...),
			})
		}
		pageToken = strings.TrimSpace(payload.NextPageToken)
		if pageToken == "" {
			break
		}
	}
	return normalizeRemoteModels(all), nil
}

func discoverAnthropicModels(ctx context.Context, client *http.Client, cfg Config, token string) ([]RemoteModel, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	applyDefaultAuthHeader(req, cfg, token, false)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, statusError(resp)
	}
	var payload struct {
		Data []struct {
			ID               string `json:"id"`
			ContextWindow    any    `json:"context_window"`
			MaxOutputTokens  any    `json:"max_output_tokens"`
			InputTokenLimit  any    `json:"input_token_limit"`
			OutputTokenLimit any    `json:"output_token_limit"`
			Capabilities     any    `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	models := make([]RemoteModel, 0, len(payload.Data))
	for _, item := range payload.Data {
		name := strings.TrimSpace(item.ID)
		if name == "" {
			continue
		}
		models = append(models, RemoteModel{
			Name:                name,
			ContextWindowTokens: firstPositiveInt(toInt(item.ContextWindow), toInt(item.InputTokenLimit)),
			MaxOutputTokens:     firstPositiveInt(toInt(item.MaxOutputTokens), toInt(item.OutputTokenLimit)),
			Capabilities:        toStringSlice(item.Capabilities),
		})
	}
	return normalizeRemoteModels(models), nil
}

func applyDefaultAuthHeader(req *http.Request, cfg Config, token string, geminiBearerOnly bool) {
	if req == nil {
		return
	}
	auth := cfg.Auth
	if key := strings.TrimSpace(auth.HeaderKey); key != "" {
		prefix := strings.TrimSpace(auth.Prefix)
		value := token
		if prefix != "" {
			value = prefix + " " + token
		}
		req.Header.Set(key, value)
		return
	}

	switch cfg.API {
	case APIGemini:
		if geminiBearerOnly {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return
	case APIAnthropic:
		if auth.Type == AuthOAuthToken || auth.Type == AuthBearerToken {
			req.Header.Set("Authorization", "Bearer "+token)
			return
		}
		req.Header.Set("x-api-key", token)
		return
	default:
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func normalizeRemoteModels(in []RemoteModel) []RemoteModel {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]RemoteModel, len(in))
	for _, item := range in {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		item.Name = name
		item.Capabilities = appendUniqueStrings(nil, item.Capabilities...)
		existing, ok := seen[name]
		if !ok {
			seen[name] = item
			continue
		}
		if existing.ContextWindowTokens <= 0 && item.ContextWindowTokens > 0 {
			existing.ContextWindowTokens = item.ContextWindowTokens
		}
		if existing.MaxOutputTokens <= 0 && item.MaxOutputTokens > 0 {
			existing.MaxOutputTokens = item.MaxOutputTokens
		}
		existing.Capabilities = appendUniqueStrings(existing.Capabilities, item.Capabilities...)
		seen[name] = existing
	}
	out := make([]RemoteModel, 0, len(seen))
	for _, item := range seen {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func appendUniqueStrings(base []string, values ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(values))
	out := make([]string, 0, len(base)+len(values))
	for _, item := range base {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func toStringSlice(raw any) []string {
	switch value := raw.(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, one := range value {
			text := strings.TrimSpace(fmt.Sprint(one))
			if text != "" && text != "<nil>" {
				out = append(out, text)
			}
		}
		return out
	case map[string]any:
		out := make([]string, 0, len(value))
		for k, v := range value {
			if toBool(v) {
				out = append(out, k)
			}
		}
		return out
	default:
		text := strings.TrimSpace(fmt.Sprint(raw))
		if text == "" || text == "<nil>" || text == "map[]" {
			return nil
		}
		return []string{text}
	}
}

func toInt(raw any) int {
	switch value := raw.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float32:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		i, _ := value.Int64()
		return int(i)
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return 0
		}
		i, _ := strconv.Atoi(value)
		return i
	default:
		return 0
	}
}

func toBool(raw any) bool {
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		value = strings.TrimSpace(strings.ToLower(value))
		return value == "1" || value == "true" || value == "yes" || value == "on"
	case int:
		return value != 0
	case int64:
		return value != 0
	case float64:
		return value != 0
	default:
		return false
	}
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
