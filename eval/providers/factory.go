package providers

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

func NewByAlias(alias string) (model.LLM, error) {
	factory, err := defaultFactory()
	if err != nil {
		return nil, err
	}
	return factory.NewByAlias(alias)
}

func ListModels() []string {
	factory, err := defaultFactory()
	if err != nil {
		return nil
	}
	return factory.ListModels()
}

func defaultFactory() (*modelproviders.Factory, error) {
	deepseekToken := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	geminiToken := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if deepseekToken == "" {
		return nil, fmt.Errorf("eval providers: DEEPSEEK_API_KEY is required")
	}
	if geminiToken == "" {
		return nil, fmt.Errorf("eval providers: GEMINI_API_KEY is required")
	}

	factory := modelproviders.NewFactory()
	configs := []modelproviders.Config{
		{
			Alias:               "deepseek-chat",
			Provider:            "deepseek",
			API:                 modelproviders.APIDeepSeek,
			Model:               "deepseek-chat",
			BaseURL:             "https://api.deepseek.com/v1",
			ContextWindowTokens: 64000,
			Auth: modelproviders.AuthConfig{
				Type:  modelproviders.AuthAPIKey,
				Token: deepseekToken,
			},
		},
		{
			Alias:               "deepseek/deepseek-chat",
			Provider:            "deepseek",
			API:                 modelproviders.APIDeepSeek,
			Model:               "deepseek-chat",
			BaseURL:             "https://api.deepseek.com/v1",
			ContextWindowTokens: 64000,
			Auth: modelproviders.AuthConfig{
				Type:  modelproviders.AuthAPIKey,
				Token: deepseekToken,
			},
		},
		{
			Alias:               "gemini-2.5-flash",
			Provider:            "gemini",
			API:                 modelproviders.APIGemini,
			Model:               "gemini-2.5-flash",
			BaseURL:             "https://generativelanguage.googleapis.com/v1beta",
			ContextWindowTokens: 128000,
			Auth: modelproviders.AuthConfig{
				Type:  modelproviders.AuthAPIKey,
				Token: geminiToken,
			},
		},
		{
			Alias:               "gemini/gemini-2.5-flash",
			Provider:            "gemini",
			API:                 modelproviders.APIGemini,
			Model:               "gemini-2.5-flash",
			BaseURL:             "https://generativelanguage.googleapis.com/v1beta",
			ContextWindowTokens: 128000,
			Auth: modelproviders.AuthConfig{
				Type:  modelproviders.AuthAPIKey,
				Token: geminiToken,
			},
		},
	}
	for _, cfg := range configs {
		if err := factory.Register(cfg); err != nil {
			return nil, fmt.Errorf("eval providers: register %q: %w", cfg.Alias, err)
		}
	}
	return factory, nil
}

func NormalizeModelAlias(alias string) string {
	value := strings.TrimSpace(strings.ToLower(alias))
	if value == "" {
		return ""
	}
	switch value {
	case "deepseek-chat":
		return "deepseek-chat"
	case "deepseek/deepseek-chat":
		return "deepseek/deepseek-chat"
	case "gemini-2.5-flash":
		return "gemini-2.5-flash"
	case "gemini/gemini-2.5-flash":
		return "gemini/gemini-2.5-flash"
	default:
		return value
	}
}

func DefaultModelAliases() []string {
	values := []string{"deepseek-chat", "gemini-2.5-flash"}
	sort.Strings(values)
	return values
}
