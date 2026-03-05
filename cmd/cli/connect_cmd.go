package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

type providerTemplate struct {
	label               string
	api                 modelproviders.APIType
	provider            string
	defaultBaseURL      string
	defaultContextToken int
	defaultMaxOutputTok int
	noAuthRequired      bool
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
	{
		label:               "ollama",
		api:                 modelproviders.APIOllama,
		provider:            "ollama",
		defaultBaseURL:      "http://localhost:11434",
		defaultContextToken: 32000,
		noAuthRequired:      true,
		commonModels:        []string{"qwen2.5:7b", "llama3.1:8b", "deepseek-r1:7b", "gemma3:4b"},
	},
}

func handleConnect(c *cliConsole, args []string) (bool, error) {
	if c.modelFactory == nil {
		return false, fmt.Errorf("model factory is not configured")
	}
	connectArgs, err := parseConnectCLIArgs(args)
	if err != nil {
		return false, err
	}
	quickMode := connectArgs.quickMode
	tpl := providerTemplate{}
	if quickMode {
		one, ok := findProviderTemplate(connectArgs.provider)
		if !ok {
			return false, fmt.Errorf("unknown provider %q", strings.TrimSpace(connectArgs.provider))
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
	if connectArgs.baseURL != "" {
		baseURL = connectArgs.baseURL
	}
	if connectArgs.hasTimeout {
		timeoutSeconds = connectArgs.timeoutSeconds
	}
	if !quickMode {
		var err error
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

	token := strings.TrimSpace(connectArgs.apiKey)
	authType := modelproviders.AuthAPIKey
	if tpl.noAuthRequired {
		authType = modelproviders.AuthNone
	} else if !quickMode && token == "" {
		c.printf("auth: api_key\n")
		input, err := c.promptText("api_key", "", true)
		if err != nil {
			return false, err
		}
		token = strings.TrimSpace(input)
	}
	if !tpl.noAuthRequired && token == "" {
		return false, fmt.Errorf("api_key is required")
	}
	credentialRef := defaultCredentialRef(tpl.provider, baseURL)

	baseCfg := modelproviders.Config{
		Provider: strings.TrimSpace(tpl.provider),
		API:      tpl.api,
		BaseURL:  strings.TrimSpace(baseURL),
		Timeout:  time.Duration(timeoutSeconds) * time.Second,
		Auth: modelproviders.AuthConfig{
			Type:          authType,
			Token:         token,
			CredentialRef: credentialRef,
		},
	}
	if baseCfg.BaseURL == "" {
		return false, fmt.Errorf("base_url is required")
	}
	c.ui.Note("正在从 models.dev 拉取最新模型配置...\n")
	catalogStatus := refreshModelCatalogForConnect(c.baseCtx)
	if catalogStatus.RemoteFetched {
		c.ui.Note("models.dev 模型配置拉取完成。\n")
	} else {
		c.ui.Warn("models.dev 拉取失败或超时，已回退到内置模型配置。\n")
	}

	modelName := strings.TrimSpace(connectArgs.model)
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
		var chosen modelproviders.RemoteModel
		if quickMode {
			chosen = models[0]
			c.ui.Note("auto-selected model (quick mode): %s\n", describeRemoteModel(tpl.provider, chosen))
		} else {
			c.ui.Section("Available models:")
			for i, item := range models {
				c.ui.Numbered(i+1, describeRemoteModel(tpl.provider, item))
			}
			index, pickErr := promptIntInRange(c, "model", 1, len(models), 1)
			if pickErr != nil {
				return false, pickErr
			}
			chosen = models[index-1]
		}
		modelName = strings.TrimSpace(chosen.Name)
		if chosen.ContextWindowTokens > 0 {
			contextWindow = chosen.ContextWindowTokens
		}
		if chosen.MaxOutputTokens > 0 {
			maxOutput = chosen.MaxOutputTokens
		}
	}
	if modelName == "" {
		if quickMode {
			return false, fmt.Errorf("model is required in quick mode")
		}
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
	catalogCaps, hasDynamicCaps := modelproviders.LookupDynamicModelCapabilities(baseCfg.Provider, modelName)
	defaultCtx := tpl.defaultContextToken
	if defaultCtx <= 0 {
		defaultCtx = 32000
	}
	defaultOut := defaultMaxOutputForTemplate(tpl)
	defaultReasoningLevels := []string(nil)
	if hasDynamicCaps {
		if catalogCaps.ContextWindowTokens > 0 {
			defaultCtx = catalogCaps.ContextWindowTokens
		}
		if catalogCaps.DefaultMaxOutputTokens > 0 {
			defaultOut = catalogCaps.DefaultMaxOutputTokens
		} else if catalogCaps.MaxOutputTokens > 0 {
			defaultOut = catalogCaps.MaxOutputTokens
		}
		defaultReasoningLevels = normalizeReasoningLevels(catalogCaps.ReasoningEfforts)
		if len(defaultReasoningLevels) == 0 && !catalogCaps.SupportsReasoning {
			defaultReasoningLevels = []string{"none"}
		}
	}
	if contextWindow <= 0 {
		contextWindow = defaultCtx
	}
	if maxOutput <= 0 {
		maxOutput = defaultOut
	}
	reasoningLevels := append([]string(nil), defaultReasoningLevels...)
	if connectArgs.hasContextWindow {
		contextWindow = connectArgs.contextWindowTokens
	}
	if connectArgs.hasMaxOutput {
		maxOutput = connectArgs.maxOutputTokens
	}
	if connectArgs.hasReasoningLevels {
		reasoningLevels, err = parseReasoningLevelsInput(connectArgs.reasoningLevelsRaw)
		if err != nil {
			return false, err
		}
	}
	if !hasDynamicCaps {
		c.ui.Note("该模型缺少在线/内置能力定义，进入手动配置模式。\n")
		if !quickMode && !connectArgs.hasContextWindow {
			contextWindow, err = promptInt(c, "context_window_tokens", contextWindow)
			if err != nil {
				return false, err
			}
		}
		if !quickMode && !connectArgs.hasMaxOutput {
			maxOutput, err = promptInt(c, "max_output_tokens", maxOutput)
			if err != nil {
				return false, err
			}
		}
		if !quickMode && !connectArgs.hasReasoningLevels {
			reasoningLevels, err = promptReasoningLevels(c, reasoningLevels)
			if err != nil {
				return false, err
			}
		}
	} else if catalogCaps.SupportsReasoning && len(reasoningLevels) == 0 && !quickMode && !connectArgs.hasReasoningLevels {
		// Dynamic catalog marks reasoning support but does not provide explicit levels.
		// Ask user to configure levels explicitly to avoid hidden inference.
		reasoningLevels, err = promptReasoningLevels(c, reasoningLevels)
		if err != nil {
			return false, err
		}
	}
	cfg := baseCfg
	cfg.Alias = alias
	cfg.Model = modelName
	cfg.ContextWindowTokens = contextWindow
	cfg.MaxOutputTok = maxOutput
	cfg.ReasoningLevels = normalizeReasoningLevels(reasoningLevels)
	cfg.ThinkingMode = defaultThinkingMode
	cfg.ThinkingBudget = defaultThinkingBudget
	cfg.ReasoningEffort = defaultReasoningEffort
	if c.configStore != nil {
		modelSettings := c.configStore.ModelRuntimeSettings(alias)
		cfg.ThinkingMode = modelSettings.ThinkingMode
		cfg.ThinkingBudget = modelSettings.ThinkingBudget
		cfg.ReasoningEffort = modelSettings.ReasoningEffort
	}
	if len(cfg.ReasoningLevels) > 0 && cfg.ThinkingMode == defaultThinkingMode && cfg.ReasoningEffort == defaultReasoningEffort {
		defaultLevel := cfg.ReasoningLevels[0]
		if defaultLevel == "none" {
			cfg.ThinkingMode = "off"
			cfg.ReasoningEffort = ""
		} else {
			cfg.ThinkingMode = "on"
			cfg.ReasoningEffort = defaultLevel
		}
	}
	cfg.Auth.CredentialRef = credentialRef
	persistCfg := cfg
	persistCfg.Auth.Token = ""

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
	if c.credentialStore != nil {
		c.ui.Note("api_key 已保存到本地凭据库，config 仅保存 credential_ref。\n")
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

func defaultMaxOutputForTemplate(tpl providerTemplate) int {
	if tpl.defaultMaxOutputTok > 0 {
		return tpl.defaultMaxOutputTok
	}
	return 4096
}

func refreshModelCatalogForConnect(baseCtx context.Context) modelproviders.CatalogInitStatus {
	ctx := baseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return modelproviders.InitModelCatalogWithStatus(timeoutCtx, nil, "")
}

func promptReasoningLevels(c *cliConsole, defaults []string) ([]string, error) {
	defaultText := strings.Join(defaults, ",")
	text, err := c.promptText("reasoning_levels(comma/space/tab)", defaultText, false)
	if err != nil {
		return nil, err
	}
	return parseReasoningLevelsInput(text)
}

func parseReasoningLevelsInput(raw string) ([]string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || value == "-" {
		return nil, nil
	}
	parts := splitArrayInput(value)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		level := normalizeReasoningLevel(part)
		if level == "" {
			return nil, fmt.Errorf("invalid reasoning level %q, expected one of none|minimal|low|medium|high|xhigh", strings.TrimSpace(part))
		}
		if _, ok := seen[level]; ok {
			continue
		}
		seen[level] = struct{}{}
		out = append(out, level)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

type connectCLIArgs struct {
	quickMode           bool
	provider            string
	model               string
	baseURL             string
	timeoutSeconds      int
	hasTimeout          bool
	apiKey              string
	contextWindowTokens int
	hasContextWindow    bool
	maxOutputTokens     int
	hasMaxOutput        bool
	reasoningLevelsRaw  string
	hasReasoningLevels  bool
}

func parseConnectCLIArgs(args []string) (connectCLIArgs, error) {
	parsed := connectCLIArgs{
		quickMode: len(args) > 0,
	}
	if len(args) == 0 {
		return parsed, nil
	}
	parsed.provider = strings.TrimSpace(args[0])
	if parsed.provider == "" {
		return connectCLIArgs{}, fmt.Errorf("usage: /connect [provider] [model] [base_url] [timeout_seconds] [api_key] [context_window_tokens] [max_output_tokens] [reasoning_levels]")
	}
	if len(args) >= 2 {
		parsed.model = strings.TrimSpace(args[1])
	}
	if len(args) >= 3 {
		parsed.baseURL = strings.TrimSpace(args[2])
	}
	if len(args) >= 4 {
		timeout, err := parseConnectIntArg("timeout_seconds", args[3])
		if err != nil {
			return connectCLIArgs{}, err
		}
		parsed.timeoutSeconds = timeout
		parsed.hasTimeout = true
	}

	tail := make([]string, 0, len(args))
	if len(args) > 4 {
		tail = append(tail, args[4:]...)
	}
	if len(tail) == 0 {
		return parsed, nil
	}

	first := strings.TrimSpace(tail[0])
	if first == "-" {
		tail = tail[1:]
	} else if _, err := strconv.Atoi(first); err != nil {
		parsed.apiKey = first
		tail = tail[1:]
	}

	if len(tail) > 0 {
		contextTokens, err := parseConnectIntArg("context_window_tokens", tail[0])
		if err != nil {
			return connectCLIArgs{}, err
		}
		parsed.contextWindowTokens = contextTokens
		parsed.hasContextWindow = true
		tail = tail[1:]
	}
	if len(tail) > 0 {
		maxTokens, err := parseConnectIntArg("max_output_tokens", tail[0])
		if err != nil {
			return connectCLIArgs{}, err
		}
		parsed.maxOutputTokens = maxTokens
		parsed.hasMaxOutput = true
		tail = tail[1:]
	}
	if len(tail) > 0 {
		parsed.reasoningLevelsRaw = strings.TrimSpace(strings.Join(tail, " "))
		parsed.hasReasoningLevels = true
	}
	return parsed, nil
}

func parseConnectIntArg(name string, raw string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q", name, strings.TrimSpace(raw))
	}
	if value < 0 {
		return 0, fmt.Errorf("invalid %s: must be >= 0", name)
	}
	return value, nil
}

func splitArrayInput(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
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
