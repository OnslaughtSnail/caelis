package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

type providerTemplate struct {
	label               string
	api                 modelproviders.APIType
	provider            string
	defaultBaseURL      string
	defaultContextToken int
	defaultMaxOutputTok int
}

var providerTemplates = []providerTemplate{
	{
		label:               "openai",
		api:                 modelproviders.APIOpenAI,
		provider:            "openai",
		defaultBaseURL:      "https://api.openai.com/v1",
		defaultContextToken: 128000,
	},
	{
		label:               "openai-compatible",
		api:                 modelproviders.APIOpenAICompatible,
		provider:            "openai-compatible",
		defaultBaseURL:      "https://api.openai.com/v1",
		defaultContextToken: 128000,
	},
	{
		label:               "gemini",
		api:                 modelproviders.APIGemini,
		provider:            "gemini",
		defaultBaseURL:      "https://generativelanguage.googleapis.com/v1beta",
		defaultContextToken: 128000,
	},
	{
		label:               "anthropic",
		api:                 modelproviders.APIAnthropic,
		provider:            "anthropic",
		defaultBaseURL:      "https://api.anthropic.com/v1",
		defaultContextToken: 200000,
		defaultMaxOutputTok: 1024,
	},
	{
		label:               "deepseek",
		api:                 modelproviders.APIDeepSeek,
		provider:            "deepseek",
		defaultBaseURL:      "https://api.deepseek.com/v1",
		defaultContextToken: 64000,
	},
	{
		label:               "xiaomi",
		api:                 modelproviders.APIOpenAICompatible,
		provider:            "xiaomi",
		defaultBaseURL:      "https://api.xiaomimimo.com/v1",
		defaultContextToken: 64000,
	},
}

func handleConnect(c *cliConsole, args []string) (bool, error) {
	if len(args) != 0 {
		return false, fmt.Errorf("usage: /connect")
	}
	if c.modelFactory == nil {
		return false, fmt.Errorf("model factory is not configured")
	}
	fmt.Println("选择 provider 类型:")
	for i, item := range providerTemplates {
		fmt.Printf("  %d) %s\n", i+1, item.label)
	}
	picked, err := promptIntInRange(c, "provider", 1, len(providerTemplates), 1)
	if err != nil {
		return false, err
	}
	tpl := providerTemplates[picked-1]

	baseURL, err := c.promptText("base_url", tpl.defaultBaseURL, false)
	if err != nil {
		return false, err
	}
	timeoutSeconds, err := promptInt(c, "timeout_seconds", 60)
	if err != nil {
		return false, err
	}

	c.printf("auth: api_key\n")
	token, err := c.promptText("api_key", "", true)
	if err != nil {
		return false, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false, fmt.Errorf("api_key is required")
	}
	credentialRef := defaultCredentialRef(tpl.provider, baseURL)

	baseCfg := modelproviders.Config{
		Provider: strings.TrimSpace(tpl.provider),
		API:      tpl.api,
		BaseURL:  strings.TrimSpace(baseURL),
		Timeout:  time.Duration(timeoutSeconds) * time.Second,
		Auth: modelproviders.AuthConfig{
			Type:          modelproviders.AuthAPIKey,
			Token:         token,
			CredentialRef: credentialRef,
		},
	}
	if baseCfg.BaseURL == "" {
		return false, fmt.Errorf("base_url is required")
	}

	models, discoverErr := modelproviders.DiscoverModels(c.baseCtx, baseCfg)
	modelName := ""
	contextWindow := tpl.defaultContextToken
	maxOutput := tpl.defaultMaxOutputTok
	if discoverErr != nil {
		c.printf("warn: list_models failed: %v\n", discoverErr)
	} else if len(models) == 0 {
		c.printf("warn: provider returned empty model list, fallback to manual input\n")
	} else {
		c.printf("可用模型:\n")
		for i, item := range models {
			c.printf("  %d) %s\n", i+1, describeRemoteModel(tpl.provider, item))
		}
		index, pickErr := promptIntInRange(c, "model", 1, len(models), 1)
		if pickErr != nil {
			return false, pickErr
		}
		chosen := models[index-1]
		modelName = strings.TrimSpace(chosen.Name)
		if chosen.ContextWindowTokens > 0 {
			contextWindow = chosen.ContextWindowTokens
		}
		if chosen.MaxOutputTokens > 0 {
			maxOutput = chosen.MaxOutputTokens
		}
	}
	if modelName == "" {
		modelName, err = c.promptText("model", "", false)
		if err != nil {
			return false, err
		}
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			return false, fmt.Errorf("model is required")
		}
	}
	alias := canonicalModelRef(baseCfg.Provider, modelName)
	if alias == "" {
		return false, fmt.Errorf("invalid provider/model")
	}
	if cfgRef := normalizeCredentialRef(credentialRef); cfgRef != "" {
		credentialRef = cfgRef
	} else {
		credentialRef = normalizeCredentialRef(alias)
	}
	cfg := baseCfg
	cfg.Alias = alias
	cfg.Model = modelName
	cfg.ContextWindowTokens = contextWindow
	cfg.MaxOutputTok = maxOutput
	cfg.Auth.CredentialRef = credentialRef
	persistCfg := cfg

	if err := c.modelFactory.Register(cfg); err != nil {
		return false, err
	}
	llm, err := c.modelFactory.NewByAlias(alias)
	if err != nil {
		return false, err
	}

	if c.configStore != nil {
		if err := c.configStore.UpsertProvider(persistCfg); err != nil {
			return false, err
		}
		if err := c.configStore.SetDefaultModel(alias); err != nil {
			fmt.Fprintf(c.out, "warn: update default model failed: %v\n", err)
		}
	}
	if c.credentialStore != nil && credentialRef != "" {
		if err := c.credentialStore.Upsert(credentialRef, credentialRecord{
			Type:  string(cfg.Auth.Type),
			Token: token,
		}); err != nil {
			return false, err
		}
	}
	c.modelAlias = alias
	c.llm = llm
	c.printf("connected: %s\n", alias)
	if c.configStore != nil {
		c.printf("note: api_key saved in provider config.\n")
	}
	if c.credentialStore != nil {
		c.printf("note: api_key also saved locally with owner-only permissions.\n")
	}
	return false, nil
}

func describeRemoteModel(provider string, item modelproviders.RemoteModel) string {
	ref := canonicalModelRef(provider, item.Name)
	parts := make([]string, 0, 3)
	if item.ContextWindowTokens > 0 {
		parts = append(parts, fmt.Sprintf("ctx=%d", item.ContextWindowTokens))
	}
	if item.MaxOutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("out=%d", item.MaxOutputTokens))
	}
	if len(item.Capabilities) > 0 {
		parts = append(parts, "cap="+strings.Join(item.Capabilities, "|"))
	}
	if len(parts) == 0 {
		return ref
	}
	return fmt.Sprintf("%s (%s)", ref, strings.Join(parts, ", "))
}

func (c *cliConsole) promptText(name, defaultValue string, secret bool) (string, error) {
	prompt := name
	if strings.TrimSpace(defaultValue) != "" {
		prompt += fmt.Sprintf(" [%s]", defaultValue)
	}
	prompt += ": "
	var (
		line string
		err  error
	)
	if secret {
		line, err = c.editor.ReadSecret(prompt)
	} else {
		line, err = c.editor.ReadLine(prompt)
	}
	if err != nil {
		return "", err
	}
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

func promptInt(c *cliConsole, name string, defaultValue int) (int, error) {
	text, err := c.promptText(name, strconv.Itoa(defaultValue), false)
	if err != nil {
		return 0, err
	}
	value, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %q", name, text)
	}
	if value < 0 {
		return 0, fmt.Errorf("invalid %s: must be >= 0", name)
	}
	return value, nil
}

func promptIntInRange(c *cliConsole, name string, minValue, maxValue, defaultValue int) (int, error) {
	value, err := promptInt(c, name, defaultValue)
	if err != nil {
		return 0, err
	}
	if value < minValue || value > maxValue {
		return 0, fmt.Errorf("invalid %s: %d (expected %d..%d)", name, value, minValue, maxValue)
	}
	return value, nil
}
