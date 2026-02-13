package providers

import (
	"fmt"
	"sort"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

// Factory builds model providers from alias configs.
type Factory struct {
	configs map[string]Config
}

// NewFactory returns an empty provider factory.
func NewFactory() *Factory {
	return &Factory{configs: map[string]Config{}}
}

// Register adds or overwrites one alias config.
func (f *Factory) Register(cfg Config) error {
	if f == nil {
		return fmt.Errorf("providers: factory is nil")
	}
	alias := strings.ToLower(strings.TrimSpace(cfg.Alias))
	if alias == "" {
		return fmt.Errorf("providers: alias is required")
	}
	if cfg.API != APIOpenAI && cfg.API != APIOpenAICompatible && cfg.API != APIGemini && cfg.API != APIAnthropic && cfg.API != APIDeepSeek {
		return fmt.Errorf("providers: unsupported api type %q", cfg.API)
	}
	authType := strings.TrimSpace(string(cfg.Auth.Type))
	if authType != "" && cfg.Auth.Type != AuthAPIKey {
		return fmt.Errorf("providers: unsupported auth type %q (only api_key is supported now)", cfg.Auth.Type)
	}
	if cfg.Auth.Type == "" {
		cfg.Auth.Type = AuthAPIKey
	}
	cfg.Alias = alias
	f.configs[alias] = cfg
	return nil
}

// NewByAlias creates a model provider by alias.
func (f *Factory) NewByAlias(alias string) (model.LLM, error) {
	if f == nil {
		return nil, fmt.Errorf("providers: factory is nil")
	}
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		return nil, fmt.Errorf("providers: model alias is required")
	}
	cfg, ok := f.configs[alias]
	if !ok {
		return nil, fmt.Errorf("providers: unknown model alias %q", alias)
	}
	token, err := resolveToken(cfg.Auth)
	if err != nil {
		return nil, err
	}

	switch cfg.API {
	case APIDeepSeek:
		return newDeepSeek(cfg, token), nil
	case APIOpenAICompatible:
		if isXiaomiProvider(cfg.Provider) {
			return newXiaomi(cfg, token), nil
		}
		return newOpenAICompat(cfg, token), nil
	case APIOpenAI:
		return newOpenAICompat(cfg, token), nil
	case APIAnthropic:
		return newAnthropic(cfg, token), nil
	case APIGemini:
		return newGemini(cfg, token), nil
	default:
		return nil, fmt.Errorf("providers: unsupported api type %q", cfg.API)
	}
}

// NewByAlias creates a model provider from a new empty factory.
func NewByAlias(alias string) (model.LLM, error) {
	return NewFactory().NewByAlias(alias)
}

// ListModels returns available aliases from current factory.
func (f *Factory) ListModels() []string {
	if f == nil {
		return nil
	}
	out := make([]string, 0, len(f.configs))
	for k := range f.configs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ListModels returns aliases from a new empty factory.
func ListModels() []string {
	return NewFactory().ListModels()
}

func resolveToken(cfg AuthConfig) (string, error) {
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return "", fmt.Errorf("providers: auth token is empty")
	}
	return token, nil
}
