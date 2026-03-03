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
	commonModels        []string
}

var providerTemplates = []providerTemplate{
	{
		label:               "openai",
		api:                 modelproviders.APIOpenAI,
		provider:            "openai",
		defaultBaseURL:      "https://api.openai.com/v1",
		defaultContextToken: 128000,
		commonModels:        []string{"gpt-4o", "gpt-4o-mini", "o3", "o4-mini"},
	},
	{
		label:               "openai-compatible",
		api:                 modelproviders.APIOpenAICompatible,
		provider:            "openai-compatible",
		defaultBaseURL:      "https://api.openai.com/v1",
		defaultContextToken: 128000,
		commonModels:        []string{"gpt-4o", "gpt-4o-mini", "o3", "o4-mini"},
	},
	{
		label:               "gemini",
		api:                 modelproviders.APIGemini,
		provider:            "gemini",
		defaultBaseURL:      "https://generativelanguage.googleapis.com/v1beta",
		defaultContextToken: 128000,
		commonModels:        []string{"gemini-2.5-flash", "gemini-2.5-pro"},
	},
	{
		label:               "anthropic",
		api:                 modelproviders.APIAnthropic,
		provider:            "anthropic",
		defaultBaseURL:      "https://api.anthropic.com/v1",
		defaultContextToken: 200000,
		defaultMaxOutputTok: 1024,
		commonModels:        []string{"claude-sonnet-4-20250514", "claude-opus-4-20250514"},
	},
	{
		label:               "deepseek",
		api:                 modelproviders.APIDeepSeek,
		provider:            "deepseek",
		defaultBaseURL:      "https://api.deepseek.com/v1",
		defaultContextToken: 64000,
		commonModels:        []string{"deepseek-chat", "deepseek-reasoner"},
	},
	{
		label:               "xiaomi",
		api:                 modelproviders.APIOpenAICompatible,
		provider:            "xiaomi",
		defaultBaseURL:      "https://api.xiaomimimo.com/v1",
		defaultContextToken: 64000,
		commonModels:        []string{"mimo-v2-flash", "mimo-v2-reasoner"},
	},
}

func handleConnect(c *cliConsole, args []string) (bool, error) {
	if len(args) > 5 {
		return false, fmt.Errorf("usage: /connect [provider] [model] [base_url] [timeout_seconds] [api_key]")
	}
	if c.modelFactory == nil {
		return false, fmt.Errorf("model factory is not configured")
	}
	quickMode := len(args) >= 1
	tpl := providerTemplate{}
	if len(args) >= 1 {
		one, ok := findProviderTemplate(args[0])
		if !ok {
			return false, fmt.Errorf("unknown provider %q", strings.TrimSpace(args[0]))
		}
		tpl = one
	} else {
		c.ui.Section("Select provider type:")
		for i, item := range providerTemplates {
			c.ui.Numbered(i+1, item.label)
		}
		picked, err := promptIntInRange(c, "provider", 1, len(providerTemplates), 1)
		if err != nil {
			return false, err
		}
		tpl = providerTemplates[picked-1]
	}

	baseURL := strings.TrimSpace(tpl.defaultBaseURL)
	timeoutSeconds := 60
	if len(args) >= 3 {
		baseURL = strings.TrimSpace(args[2])
	}
	if len(args) >= 4 {
		parsedTimeout, parseErr := strconv.Atoi(strings.TrimSpace(args[3]))
		if parseErr != nil {
			return false, fmt.Errorf("invalid timeout_seconds %q", strings.TrimSpace(args[3]))
		}
		timeoutSeconds = parsedTimeout
		if timeoutSeconds < 0 {
			return false, fmt.Errorf("invalid timeout_seconds: must be >= 0")
		}
	}
	var err error
	if !quickMode {
		baseURL, err = c.promptText("base_url", tpl.defaultBaseURL, false)
		if err != nil {
			return false, err
		}
		timeoutSeconds, err = promptInt(c, "timeout_seconds", 60)
		if err != nil {
			return false, err
		}
	} else {
		c.ui.Note("using base_url=%s timeout_seconds=%d (quick mode)\n", baseURL, timeoutSeconds)
	}

	token := ""
	if len(args) >= 5 {
		token = strings.TrimSpace(args[4])
	} else {
		c.printf("auth: api_key\n")
		input, err := c.promptText("api_key", "", true)
		if err != nil {
			return false, err
		}
		token = strings.TrimSpace(input)
	}
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

	modelName := ""
	if len(args) >= 2 {
		modelName = strings.TrimSpace(args[1])
		if modelName == "" {
			return false, fmt.Errorf("model is required")
		}
	}
	models, discoverErr := modelproviders.DiscoverModels(c.baseCtx, baseCfg)
	// Start at 0 so ApplyModelCatalog (called inside Register) can fill in the
	// correct catalog values. DiscoverModels overrides only when the API
	// explicitly returns a positive value.
	contextWindow := 0
	maxOutput := 0
	if modelName != "" {
		// Keep explicit model from command args.
	} else if discoverErr != nil {
		c.ui.Warn("list_models failed: %v\n", discoverErr)
	} else if len(models) == 0 {
		c.ui.Warn("provider returned empty model list, fallback to manual input\n")
	} else if len(models) == 1 {
		chosen := models[0]
		modelName = strings.TrimSpace(chosen.Name)
		c.ui.Note("auto-selected model: %s\n", describeRemoteModel(tpl.provider, chosen))
		if chosen.ContextWindowTokens > 0 {
			contextWindow = chosen.ContextWindowTokens
		}
		if chosen.MaxOutputTokens > 0 {
			maxOutput = chosen.MaxOutputTokens
		}
	} else {
		c.ui.Section("Available models:")
		for i, item := range models {
			c.ui.Numbered(i+1, describeRemoteModel(tpl.provider, item))
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
	cfg.ThinkingMode = defaultThinkingMode
	cfg.ThinkingBudget = defaultThinkingBudget
	cfg.ReasoningEffort = defaultReasoningEffort
	if c.configStore != nil {
		modelSettings := c.configStore.ModelRuntimeSettings(alias)
		cfg.ThinkingMode = modelSettings.ThinkingMode
		cfg.ThinkingBudget = modelSettings.ThinkingBudget
		cfg.ReasoningEffort = modelSettings.ReasoningEffort
	}
	cfg.Auth.CredentialRef = credentialRef
	// Enrich cfg with catalog values before capturing persistCfg so that the
	// record written to disk already contains the fully-resolved token limits.
	modelproviders.ApplyModelCatalog(&cfg)
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
			c.ui.Warn("update default model failed: %v\n", err)
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
	c.applyModelRuntimeSettings(alias)
	c.ui.Success("Connected: %s\n", alias)
	if c.configStore != nil {
		c.ui.Note("api_key saved in provider config.\n")
	}
	if c.credentialStore != nil {
		c.ui.Note("api_key also saved locally with owner-only permissions.\n")
	}
	return false, nil
}

func findProviderTemplate(input string) (providerTemplate, bool) {
	target := strings.ToLower(strings.TrimSpace(input))
	if target == "" {
		return providerTemplate{}, false
	}
	for _, one := range providerTemplates {
		if strings.EqualFold(strings.TrimSpace(one.label), target) {
			return one, true
		}
	}
	return providerTemplate{}, false
}

func commonModelsForProvider(provider string) []string {
	target := strings.ToLower(strings.TrimSpace(provider))
	if target == "" {
		return nil
	}
	for _, one := range providerTemplates {
		if strings.EqualFold(strings.TrimSpace(one.provider), target) || strings.EqualFold(strings.TrimSpace(one.label), target) {
			return append([]string(nil), one.commonModels...)
		}
	}
	return nil
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
	if c.prompter == nil {
		return "", fmt.Errorf("interactive prompt is unavailable")
	}
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
		line, err = c.prompter.ReadSecret(prompt)
	} else {
		line, err = c.prompter.ReadLine(prompt)
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
