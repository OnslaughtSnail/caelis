package providers

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

const (
	deepSeekAPIKeyEnv = "DEEPSEEK_API_KEY"
	geminiAPIKeyEnv   = "GEMINI_API_KEY"
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
	factory := modelproviders.NewFactory()
	configs := make([]modelproviders.Config, 0, 4)
	deepseekToken := strings.TrimSpace(os.Getenv(deepSeekAPIKeyEnv))
	if deepseekToken != "" {
		configs = append(configs,
			modelproviders.Config{
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
			modelproviders.Config{
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
		)
	}
	geminiToken := strings.TrimSpace(os.Getenv(geminiAPIKeyEnv))
	if geminiToken != "" {
		configs = append(configs,
			modelproviders.Config{
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
			modelproviders.Config{
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
		)
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("eval providers: no model credentials configured; set %s and/or %s", deepSeekAPIKeyEnv, geminiAPIKeyEnv)
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
	values := make([]string, 0, 2)
	if strings.TrimSpace(os.Getenv(deepSeekAPIKeyEnv)) != "" {
		values = append(values, "deepseek-chat")
	}
	if strings.TrimSpace(os.Getenv(geminiAPIKeyEnv)) != "" {
		values = append(values, "gemini-2.5-flash")
	}
	sort.Strings(values)
	return values
}
