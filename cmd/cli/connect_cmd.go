package main

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

const connectCustomModelValue = "__custom_model__"

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

const (
	connectVolcengineStandardValue = "standard"
	connectVolcengineCodingValue   = "coding-plan"
)

type promptChoiceRequester interface {
	RequestChoicePrompt(prompt string, choices []tuievents.PromptChoice, defaultChoice string, filterable bool) (string, error)
	RequestMultiChoicePrompt(prompt string, choices []tuievents.PromptChoice, selectedChoices []string, filterable bool) (string, error)
}

type connectModelChoice struct {
	Name    string
	Display string
	Detail  string
}

type connectModelSelection struct {
	cfg             modelproviders.Config
	persistCfg      modelproviders.Config
	knownModel      bool
	requiresAdvance bool
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
		label:               "openrouter",
		api:                 modelproviders.APIOpenRouter,
		provider:            "openrouter",
		defaultBaseURL:      "https://openrouter.ai/api/v1",
		defaultContextToken: 262144,
		commonModels:        []string{"openai/gpt-4o-mini", "anthropic/claude-sonnet-4", "google/gemini-2.5-flash"},
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
		defaultBaseURL:      "https://api.anthropic.com",
		defaultContextToken: 200000,
		defaultMaxOutputTok: 1024,
		commonModels:        []string{"claude-sonnet-4-20250514", "claude-opus-4-20250514"},
	},
	{
		label:               "anthropic-compatible",
		api:                 modelproviders.APIAnthropicCompatible,
		provider:            "anthropic-compatible",
		defaultBaseURL:      "https://api.anthropic.com",
		defaultContextToken: 200000,
		defaultMaxOutputTok: 1024,
	},
	{
		label:               "deepseek",
		api:                 modelproviders.APIDeepSeek,
		provider:            "deepseek",
		defaultBaseURL:      "https://api.deepseek.com/v1",
		defaultContextToken: 128000,
		commonModels:        []string{"deepseek-chat", "deepseek-reasoner"},
	},
	{
		label:               "xiaomi",
		api:                 modelproviders.APIMimo,
		provider:            "xiaomi",
		defaultBaseURL:      "https://api.xiaomimimo.com/v1",
		defaultContextToken: 128000,
		commonModels:        []string{"mimo-v2-flash", "mimo-v2-reasoner"},
	},
	{
		label:               "minimax",
		api:                 modelproviders.APIAnthropicCompatible,
		provider:            "minimax",
		defaultBaseURL:      "https://api.minimaxi.com/anthropic",
		defaultContextToken: 204800,
		defaultMaxOutputTok: 8192,
		commonModels: []string{
			"MiniMax-M2.7",
			"MiniMax-M2.7-highspeed",
			"MiniMax-M2.5",
			"MiniMax-M2.5-highspeed",
			"MiniMax-M2.1",
			"MiniMax-M2.1-highspeed",
			"MiniMax-M2",
		},
	},
	{
		label:               "volcengine",
		api:                 modelproviders.APIVolcengine,
		provider:            "volcengine",
		defaultBaseURL:      "https://ark.cn-beijing.volces.com/api/v3",
		defaultContextToken: 128000,
	},
	{
		label:               "ollama",
		api:                 modelproviders.APIOllama,
		provider:            "ollama",
		defaultBaseURL:      "http://localhost:11434",
		defaultContextToken: 128000,
		noAuthRequired:      true,
		commonModels:        []string{"qwen2.5:7b", "llama3.1:8b", "deepseek-r1:7b", "gemma3:4b"},
	},
}

func handleConnect(c *cliConsole, args []string) (bool, error) {
	if c.modelFactory == nil {
		return false, fmt.Errorf("model factory is not configured")
	}
	if len(args) != 0 {
		return false, fmt.Errorf("usage: /connect")
	}

	tpl, err := promptProviderTemplate(c)
	if err != nil {
		return false, err
	}
	if tpl.provider == "volcengine" {
		tpl, err = promptVolcengineEndpointTemplate(c, tpl)
		if err != nil {
			return false, err
		}
	}

	baseURL := strings.TrimSpace(tpl.defaultBaseURL)
	if tpl.provider == "openai-compatible" || tpl.provider == "anthropic-compatible" {
		baseURL, err = c.promptText("base_url", tpl.defaultBaseURL, false)
		if err != nil {
			return false, err
		}
		baseURL = strings.TrimSpace(baseURL)
		if baseURL == "" {
			return false, fmt.Errorf("base_url is required")
		}
	}

	timeoutSeconds := 60
	token := ""
	authType := modelproviders.AuthAPIKey
	if tpl.noAuthRequired {
		authType = modelproviders.AuthNone
	} else {
		token, err = c.promptText("api_key", "", true)
		if err != nil {
			return false, err
		}
		token = strings.TrimSpace(token)
		if token == "" {
			return false, fmt.Errorf("api_key is required")
		}
	}
	credentialRef := normalizeCredentialRef(defaultCredentialRef(tpl.provider, baseURL))
	if credentialRef == "" {
		credentialRef = normalizeCredentialRef(tpl.provider)
	}

	baseCfg := modelproviders.Config{
		Provider: strings.TrimSpace(tpl.provider),
		API:      tpl.api,
		BaseURL:  baseURL,
		Timeout:  time.Duration(timeoutSeconds) * time.Second,
		Auth: modelproviders.AuthConfig{
			Type:          authType,
			Token:         token,
			CredentialRef: credentialRef,
		},
	}

	if c.tuiSender != nil && modelCatalogRefreshDue() {
		c.setPromptLoading(true)
		defer c.setPromptLoading(false)
	}
	status, refreshed := connectModelCatalogRefreshFn(c.baseCtx)
	if refreshed && status.RemoteError != nil {
		reportConnectCatalogFallback(c, status.RemoteError)
	}

	remoteModels := []modelproviders.RemoteModel(nil)
	if shouldDiscoverConnectModels(tpl) {
		var discoverErr error
		remoteModels, discoverErr = discoverModelsFn(c.baseCtx, baseCfg)
		if discoverErr != nil && !shouldSuppressDiscoverModelsError(baseCfg, discoverErr) {
			c.ui.Warn("list_models failed: %v\n", discoverErr)
		}
	}

	var modelNames []string
	if useConnectModelChoices(tpl) {
		fallbackModels := fallbackConnectModels(tpl, remoteModels)
		choices := buildConnectModelChoices(tpl.provider, remoteModels, fallbackModels)
		if hasSelectableConnectModels(choices) {
			modelNames, err = promptConnectModelChoices(c, tpl.provider, choices)
			if err != nil {
				return false, err
			}
		} else {
			modelNames, err = promptConnectManualModelNames(c, tpl.provider)
			if err != nil {
				return false, err
			}
		}
	} else {
		modelNames, err = promptConnectManualModelNames(c, tpl.provider)
		if err != nil {
			return false, err
		}
	}
	selected := make([]connectModelSelection, 0, len(modelNames))
	for _, modelName := range modelNames {
		meta, _ := findRemoteModelByName(remoteModels, modelName)
		selection, err := buildConnectModelSelection(c, tpl, baseCfg, credentialRef, modelName, meta)
		if err != nil {
			return false, err
		}
		selected = append(selected, selection)
	}

	if len(selected) == 0 {
		return false, fmt.Errorf("no model selected")
	}

	if c.credentialStore != nil && credentialRef != "" {
		if err := c.credentialStore.Upsert(credentialRef, credentialRecord{
			Type:  string(authType),
			Token: token,
		}); err != nil {
			return false, err
		}
	}

	for _, selection := range selected {
		if err := c.modelFactory.Register(selection.cfg); err != nil {
			return false, err
		}
		if c.configStore != nil {
			if err := c.configStore.UpsertProvider(selection.persistCfg); err != nil {
				return false, err
			}
		}
	}

	firstAlias := selected[0].cfg.Alias
	llm, err := c.modelFactory.NewByAlias(firstAlias)
	if err != nil {
		return false, err
	}
	if c.configStore != nil {
		if err := c.configStore.SetDefaultModel(firstAlias); err != nil {
			c.ui.Warn("update default model failed: %v\n", err)
		}
	}
	c.modelAlias = firstAlias
	c.llm = llm
	c.applyModelRuntimeSettings(firstAlias)
	for _, selection := range selected {
		c.ui.Success("Connected: %s\n", selection.cfg.Alias)
	}
	return false, nil
}

func promptProviderTemplate(c *cliConsole) (providerTemplate, error) {
	choices := make([]promptChoiceItem, 0, len(providerTemplates))
	for _, tpl := range providerTemplates {
		detail := tpl.defaultBaseURL
		if tpl.noAuthRequired {
			detail += " · no auth"
		}
		choices = append(choices, promptChoiceItem{
			Label:  tpl.label,
			Value:  tpl.label,
			Detail: detail,
		})
	}
	value, err := c.promptChoice("Select provider", choices, providerTemplates[0].label, false)
	if err != nil {
		return providerTemplate{}, err
	}
	tpl, ok := findProviderTemplate(value)
	if !ok {
		return providerTemplate{}, fmt.Errorf("unknown provider %q", value)
	}
	return tpl, nil
}

func promptVolcengineEndpointTemplate(c *cliConsole, tpl providerTemplate) (providerTemplate, error) {
	value, err := c.promptChoice("Select endpoint", []promptChoiceItem{
		{
			Label:  "standard api",
			Value:  connectVolcengineStandardValue,
			Detail: "https://ark.cn-beijing.volces.com/api/v3",
		},
		{
			Label:  "coding plan",
			Value:  connectVolcengineCodingValue,
			Detail: "https://ark.cn-beijing.volces.com/api/coding/v3",
		},
	}, connectVolcengineStandardValue, false)
	if err != nil {
		return providerTemplate{}, err
	}
	switch value {
	case connectVolcengineCodingValue:
		tpl.api = modelproviders.APIVolcengineCoding
		tpl.defaultBaseURL = "https://ark.cn-beijing.volces.com/api/coding/v3"
		tpl.commonModels = []string{
			"doubao-seed-2.0-code",
			"doubao-seed-2.0-pro",
			"doubao-seed-2.0-lite",
			"doubao-seed-code",
			"minimax-m2.5",
			"glm-4.7",
			"deepseek-v3.2",
			"kimi-k2.5",
		}
	default:
		tpl.api = modelproviders.APIVolcengine
		tpl.defaultBaseURL = "https://ark.cn-beijing.volces.com/api/v3"
		tpl.commonModels = nil
	}
	return tpl, nil
}

func buildConnectModelChoices(provider string, remoteModels []modelproviders.RemoteModel, fallbackModels []string) []connectModelChoice {
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

func fallbackConnectModels(tpl providerTemplate, remoteModels []modelproviders.RemoteModel) []string {
	if len(remoteModels) > 0 {
		return nil
	}
	if (tpl.api == modelproviders.APIVolcengineCoding || tpl.provider == "minimax") && len(tpl.commonModels) > 0 {
		return append([]string(nil), tpl.commonModels...)
	}
	if models := listCatalogModels(tpl.provider); len(models) > 0 {
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

func useConnectModelChoices(tpl providerTemplate) bool {
	return tpl.api != modelproviders.APIVolcengine
}

func hasSelectableConnectModels(choices []connectModelChoice) bool {
	for _, choice := range choices {
		if choice.Name != connectCustomModelValue {
			return true
		}
	}
	return false
}

func promptConnectManualModelNames(c *cliConsole, provider string) ([]string, error) {
	raw, err := c.promptText("model", "", false)
	if err != nil {
		return nil, err
	}
	parts := splitArrayInput(raw)
	if len(parts) == 0 {
		return nil, fmt.Errorf("model is required")
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		name, normalizeErr := normalizeConnectModelName(provider, part)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		out = append(out, name)
	}
	out = dedupeOrderedStrings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("model is required")
	}
	return out, nil
}

func promptConnectModelChoices(c *cliConsole, provider string, choices []connectModelChoice) ([]string, error) {
	promptChoices := make([]promptChoiceItem, 0, len(choices))
	for _, choice := range choices {
		promptChoices = append(promptChoices, promptChoiceItem{
			Label:  choice.Display,
			Value:  choice.Name,
			Detail: choice.Detail,
		})
	}
	values, err := c.promptMultiChoice("Select model", promptChoices, true)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == connectCustomModelValue {
			customValues, err := promptConnectManualModelNames(c, provider)
			if err != nil {
				return nil, err
			}
			out = append(out, customValues...)
			continue
		}
		name, err := normalizeConnectModelName(provider, value)
		if err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	out = dedupeOrderedStrings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("no model selected")
	}
	return out, nil
}

func normalizeConnectModelName(provider string, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("model is required")
	}
	providerPrefix := strings.ToLower(strings.TrimSpace(provider)) + "/"
	if providerPrefix != "/" && strings.HasPrefix(strings.ToLower(value), providerPrefix) {
		remainder := strings.TrimSpace(value[len(providerPrefix):])
		if strings.EqualFold(strings.TrimSpace(provider), "openrouter") && !strings.Contains(remainder, "/") {
			remainder = value
		}
		value = remainder
	}
	if value == "" {
		return "", fmt.Errorf("model is required")
	}
	return value, nil
}

func buildConnectModelSelection(c *cliConsole, tpl providerTemplate, baseCfg modelproviders.Config, credentialRef string, modelName string, remote *modelproviders.RemoteModel) (connectModelSelection, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return connectModelSelection{}, fmt.Errorf("model is required")
	}
	alias := canonicalModelRef(baseCfg.Provider, modelName)
	if c != nil && c.configStore != nil {
		alias = c.configStore.ResolveOrAllocateModelAlias(baseCfg.Provider, modelName, baseCfg.BaseURL)
	}
	if alias == "" {
		return connectModelSelection{}, fmt.Errorf("invalid provider/model")
	}
	subjectCfg := baseCfg
	subjectCfg.Model = modelName

	baseCaps, baseKnown := lookupDynamicCatalogCapabilities(baseCfg.Provider, modelName)
	if !baseKnown {
		baseCaps, baseKnown = lookupCatalogModelCapabilities(baseCfg.Provider, modelName)
	}
	if !baseKnown {
		baseCaps = defaultCatalogModelCapabilities()
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
	overlayCaps, overlayKnown := lookupOverlayCatalogCapabilities(baseCfg.Provider, modelName)
	catalogKnown := baseKnown
	if remote != nil {
		if remote.ContextWindowTokens > 0 {
			baseCaps.ContextWindowTokens = remote.ContextWindowTokens
		}
		if remote.MaxOutputTokens > 0 {
			baseCaps.MaxOutputTokens = remote.MaxOutputTokens
			baseCaps.DefaultMaxOutputTokens = remote.MaxOutputTokens
		}
		if _, supportsReasoning, _, _, remoteKnown := connectRemoteCapabilities(remote); remoteKnown {
			baseKnown = true
			if supportsReasoning {
				baseCaps.SupportsReasoning = true
			}
		}
	}

	reasoningMode := normalizeCatalogReasoningMode(baseCaps.ReasoningMode)
	if overlayMode := normalizeCatalogReasoningMode(overlayCaps.ReasoningMode); overlayMode != "" {
		reasoningMode = overlayMode
	}
	reasoningEfforts := normalizeReasoningLevels(baseCaps.ReasoningEfforts)
	if overlayEfforts := normalizeReasoningLevels(overlayCaps.ReasoningEfforts); len(overlayEfforts) > 0 {
		reasoningEfforts = overlayEfforts
	}
	defaultReasoningEffort := normalizeReasoningEffort(baseCaps.DefaultReasoningEffort)
	if overlayDefault := normalizeReasoningEffort(overlayCaps.DefaultReasoningEffort); overlayDefault != "" {
		defaultReasoningEffort = overlayDefault
	}
	contextWindow := baseCaps.ContextWindowTokens
	maxOutput := baseCaps.DefaultMaxOutputTokens
	if maxOutput <= 0 {
		maxOutput = baseCaps.MaxOutputTokens
	}
	if maxOutput <= 0 {
		maxOutput = defaultMaxOutputForTemplate(tpl)
	}
	if contextWindow <= 0 {
		contextWindow = defaultContextWindowForTemplate(tpl)
	}
	switch {
	case !baseKnown:
		maxOutput = recommendedCatalogFallbackMaxOutputTokens(contextWindow, maxOutput, baseCaps.SupportsReasoning)
		var err error
		contextWindow, err = promptTokenCount(c, "context_window_tokens", contextWindow, subjectCfg)
		if err != nil {
			return connectModelSelection{}, err
		}
		maxOutput, err = promptTokenCount(c, "max_output_tokens", maxOutput, subjectCfg)
		if err != nil {
			return connectModelSelection{}, err
		}
		reasoningMode, reasoningEfforts, defaultReasoningEffort, err = promptUnknownModelReasoningDefinition(c, subjectCfg, reasoningMode, reasoningEfforts, defaultReasoningEffort)
		if err != nil {
			return connectModelSelection{}, err
		}
	case shouldFallbackManualReasoning(baseCfg.Provider, catalogKnown, remote):
		var err error
		reasoningMode, reasoningEfforts, defaultReasoningEffort, err = promptUnknownModelReasoningDefinition(c, subjectCfg, reasoningMode, reasoningEfforts, defaultReasoningEffort)
		if err != nil {
			return connectModelSelection{}, err
		}
	case !baseCaps.SupportsReasoning:
		reasoningMode = reasoningModeNone
		reasoningEfforts = nil
		defaultReasoningEffort = ""
	case reasoningMode == reasoningModeFixed:
		reasoningEfforts = nil
		defaultReasoningEffort = ""
	case reasoningMode == "":
		profile := connectProviderReasoningProfile(baseCfg)
		switch profile.Mode {
		case reasoningModeFixed:
			reasoningMode = reasoningModeFixed
			reasoningEfforts = nil
			defaultReasoningEffort = ""
		case reasoningModeToggle:
			reasoningMode = reasoningModeToggle
			reasoningEfforts = nil
			defaultReasoningEffort = ""
		case reasoningModeEffort:
			reasoningEfforts = append([]string(nil), profile.SupportedEfforts...)
			defaultReasoningEffort = profile.DefaultEffort
			var err error
			reasoningMode, reasoningEfforts, defaultReasoningEffort, err = promptProviderEffortDefinition(c, subjectCfg, profile.SupportedEfforts, reasoningEfforts, defaultReasoningEffort)
			if err != nil {
				return connectModelSelection{}, err
			}
		default:
			var err error
			reasoningMode, reasoningEfforts, defaultReasoningEffort, err = promptReasoningDefinition(c, subjectCfg, reasoningModeNone, nil, "")
			if err != nil {
				return connectModelSelection{}, err
			}
		}
	}

	cfg := baseCfg
	cfg.Alias = alias
	cfg.Model = modelName
	cfg.ContextWindowTokens = contextWindow
	cfg.MaxOutputTok = maxOutput
	cfg.ReasoningMode = reasoningMode
	cfg.SupportedReasoningEfforts = reasoningEfforts
	cfg.DefaultReasoningEffort = defaultReasoningEffort
	cfg.ReasoningLevels = reasoningLevelsForMode(reasoningMode, reasoningEfforts)
	cfg.Auth.CredentialRef = credentialRef
	applyConnectRuntimeDefaults(c, &cfg)
	persistCfg := cfg
	persistCfg.Auth.Token = ""
	return connectModelSelection{
		cfg:             cfg,
		persistCfg:      persistCfg,
		knownModel:      baseKnown,
		requiresAdvance: !baseKnown || (!overlayKnown && baseCaps.SupportsReasoning && reasoningMode == reasoningModeEffort),
	}, nil
}

func promptUnknownModelReasoningDefinition(c *cliConsole, baseCfg modelproviders.Config, defaultMode string, defaultEfforts []string, defaultEffort string) (string, []string, string, error) {
	profile := connectProviderReasoningProfile(baseCfg)
	switch profile.Mode {
	case reasoningModeToggle:
		value, err := c.promptChoice(connectPromptLabel("Select reasoning support", baseCfg), []promptChoiceItem{
			{Label: "none", Value: reasoningModeNone, Detail: "This model does not expose provider reasoning controls."},
			{Label: "toggle", Value: reasoningModeToggle, Detail: "This model supports provider thinking on/off controls."},
		}, defaultMode, false)
		if err != nil {
			return "", nil, "", err
		}
		mode := normalizeCatalogReasoningMode(value)
		if mode == reasoningModeToggle {
			return reasoningModeToggle, nil, "", nil
		}
		return reasoningModeNone, nil, "", nil
	case reasoningModeEffort:
		value, err := c.promptChoice(connectPromptLabel("Does this model support reasoning?", baseCfg), []promptChoiceItem{
			{Label: "no", Value: "no", Detail: "Do not configure reasoning controls for this model."},
			{Label: "yes", Value: "yes", Detail: "Configure supported reasoning_effort values."},
		}, "yes", false)
		if err != nil {
			return "", nil, "", err
		}
		if value != "yes" {
			return reasoningModeNone, nil, "", nil
		}
		return promptProviderEffortDefinition(c, baseCfg, profile.SupportedEfforts, defaultEfforts, defaultEffort)
	default:
		return promptReasoningDefinition(c, baseCfg, defaultMode, defaultEfforts, defaultEffort)
	}
}

func applyConnectRuntimeDefaults(_ *cliConsole, cfg *modelproviders.Config) {
	if cfg == nil {
		return
	}
	cfg.ThinkingBudget = defaultThinkingBudget
	cfg.ReasoningEffort = defaultReasoningEffort
	profile := reasoningProfileForConfig(*cfg)
	switch profile.Mode {
	case reasoningModeFixed:
		cfg.ReasoningEffort = ""
	case reasoningModeEffort:
		cfg.ReasoningEffort = profile.DefaultEffort
	case reasoningModeToggle:
		cfg.ReasoningEffort = reasoningProfileDefaultEffort(profile)
	}
}

func defaultContextWindowForTemplate(tpl providerTemplate) int {
	if tpl.defaultContextToken > 0 {
		return tpl.defaultContextToken
	}
	return 128000
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
	if models := listCatalogModels(target); len(models) > 0 {
		return models
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
	return 8192
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

func promptReasoningDefinition(c *cliConsole, baseCfg modelproviders.Config, defaultMode string, defaultEfforts []string, defaultEffort string) (string, []string, string, error) {
	defaultMode = normalizeCatalogReasoningMode(defaultMode)
	if defaultMode == "" {
		defaultMode = reasoningModeNone
	}
	if connectUsesCanonicalEffortSelection(baseCfg) {
		return promptSupportedReasoningEfforts(c, baseCfg, openAICompatibleStandardEfforts, defaultEfforts, defaultEffort)
	}
	mode, err := c.promptChoice(connectPromptLabel("Select reasoning capability", baseCfg), connectReasoningCapabilityChoices(), defaultMode, false)
	if err != nil {
		return "", nil, "", err
	}
	mode = normalizeCatalogReasoningMode(mode)
	switch mode {
	case reasoningModeNone:
		return mode, nil, "", nil
	case reasoningModeToggle:
		return mode, nil, "", nil
	case reasoningModeEffort:
		selectedDefaults := normalizeReasoningLevels(defaultEfforts)
		if len(selectedDefaults) == 0 {
			selectedDefaults = []string{"low", "medium", "high"}
		}
		selected, selErr := c.promptMultiChoiceWithDefaults(connectPromptLabel("Select reasoning efforts", baseCfg), []promptChoiceItem{
			{Label: "low", Value: "low"},
			{Label: "medium", Value: "medium"},
			{Label: "high", Value: "high"},
		}, selectedDefaults, false)
		if selErr != nil {
			return "", nil, "", selErr
		}
		extraDefault := reasoningExtrasDefault(selectedDefaults)
		extraRaw, extraErr := c.promptText(connectPromptLabel("additional_reasoning_efforts(optional, comma/space/tab)", baseCfg), extraDefault, false)
		if extraErr != nil {
			return "", nil, "", extraErr
		}
		extra, extraParseErr := parseReasoningLevelsInput(extraRaw)
		if extraParseErr != nil {
			return "", nil, "", extraParseErr
		}
		combined := mergeReasoningEffortLists(selected, extra)
		if len(combined) == 0 {
			combined = []string{"low", "medium", "high"}
		}
		resolvedDefault := normalizeReasoningEffort(defaultEffort)
		if !containsString(combined, resolvedDefault) {
			if containsString(combined, "medium") {
				resolvedDefault = "medium"
			} else {
				resolvedDefault = combined[0]
			}
		}
		return mode, combined, resolvedDefault, nil
	default:
		return reasoningModeNone, nil, "", nil
	}
}

func connectProviderReasoningProfile(baseCfg modelproviders.Config) reasoningProfile {
	switch baseCfg.API {
	case modelproviders.APIDeepSeek, modelproviders.APIMimo, modelproviders.APIVolcengine, modelproviders.APIVolcengineCoding:
		return reasoningProfile{Mode: reasoningModeToggle}
	case modelproviders.APIOpenAI, modelproviders.APIOpenAICompatible, modelproviders.APIOpenRouter:
		return reasoningProfile{Mode: reasoningModeEffort, SupportedEfforts: append([]string(nil), openAICompatibleStandardEfforts...), DefaultEffort: "medium"}
	case modelproviders.APIGemini, modelproviders.APIAnthropic:
		return reasoningProfile{Mode: reasoningModeEffort, SupportedEfforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"}
	default:
		return reasoningProfile{Mode: reasoningModeNone}
	}
}

func promptProviderEffortDefinition(c *cliConsole, baseCfg modelproviders.Config, supportedEfforts []string, defaultEfforts []string, defaultEffort string) (string, []string, string, error) {
	if len(supportedEfforts) == 0 {
		return reasoningModeNone, nil, "", nil
	}
	return promptSupportedReasoningEfforts(c, baseCfg, supportedEfforts, defaultEfforts, defaultEffort)
}

func connectUsesCanonicalEffortSelection(baseCfg modelproviders.Config) bool {
	if usesCanonicalOpenAICompatibleEfforts(baseCfg.Provider) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(baseCfg.Provider)) {
	case "openai":
		return true
	default:
		return false
	}
}

func promptSupportedReasoningEfforts(c *cliConsole, baseCfg modelproviders.Config, supportedEfforts []string, defaultEfforts []string, defaultEffort string) (string, []string, string, error) {
	supportedEfforts = normalizeReasoningLevels(supportedEfforts)
	if len(supportedEfforts) == 0 {
		return reasoningModeNone, nil, "", nil
	}
	selectedDefaults := normalizeReasoningLevels(defaultEfforts)
	if len(selectedDefaults) == 0 {
		selectedDefaults = append([]string(nil), supportedEfforts...)
	}
	selected, err := c.promptMultiChoiceWithDefaults(connectPromptLabel("Select supported reasoning_effort values", baseCfg), connectReasoningEffortChoices(supportedEfforts), selectedDefaults, false)
	if err != nil {
		return "", nil, "", err
	}
	selected = normalizeReasoningLevels(selected)
	if len(selected) == 0 {
		return reasoningModeNone, nil, "", nil
	}
	if len(selected) == 1 && selected[0] == "none" {
		return reasoningModeNone, []string{"none"}, "", nil
	}
	resolvedDefault := normalizeReasoningEffort(defaultEffort)
	if resolvedDefault == "none" {
		resolvedDefault = ""
	}
	if !containsString(selected, resolvedDefault) || resolvedDefault == "" {
		for _, preferred := range []string{"medium", "low", "minimal", "high", "xhigh"} {
			if containsString(selected, preferred) {
				resolvedDefault = preferred
				break
			}
		}
		if resolvedDefault == "" {
			for _, one := range selected {
				if one != "none" {
					resolvedDefault = one
					break
				}
			}
		}
	}
	return reasoningModeEffort, selected, resolvedDefault, nil
}

func connectPromptLabel(prompt string, cfg modelproviders.Config) string {
	prompt = strings.TrimSpace(prompt)
	ref := connectDisplayModelRef(cfg.Provider, cfg.Model)
	if prompt == "" || ref == "" {
		return prompt
	}
	return prompt + " for " + ref
}

func connectRemoteCapabilities(remote *modelproviders.RemoteModel) (supportsToolCalls bool, supportsReasoning bool, supportsImages bool, supportsJSON bool, known bool) {
	if remote == nil {
		return false, false, false, false, false
	}
	if remote.ContextWindowTokens > 0 || remote.MaxOutputTokens > 0 || len(remote.Capabilities) > 0 {
		known = true
	}
	for _, one := range remote.Capabilities {
		switch strings.ToLower(strings.TrimSpace(one)) {
		case "tools", "tool_choice":
			supportsToolCalls = true
		case "reasoning", "include_reasoning":
			supportsReasoning = true
		case "image":
			supportsImages = true
		case "response_format", "structured_outputs":
			supportsJSON = true
		}
	}
	return supportsToolCalls, supportsReasoning, supportsImages, supportsJSON, known
}

func shouldFallbackManualReasoning(provider string, catalogKnown bool, remote *modelproviders.RemoteModel) bool {
	if !strings.EqualFold(strings.TrimSpace(provider), "openrouter") {
		return false
	}
	if catalogKnown || remote == nil {
		return false
	}
	_, supportsReasoning, _, _, _ := connectRemoteCapabilities(remote)
	return !supportsReasoning
}

func connectReasoningEffortChoices(supportedEfforts []string) []promptChoiceItem {
	choices := make([]promptChoiceItem, 0, len(supportedEfforts))
	for _, one := range normalizeReasoningLevels(supportedEfforts) {
		switch one {
		case "none":
			choices = append(choices, promptChoiceItem{Label: "none", Value: "none", Detail: "Explicitly disable reasoning when the model supports it."})
		case "minimal":
			choices = append(choices, promptChoiceItem{Label: "minimal", Value: "minimal", Detail: "The lightest reasoning effort."})
		case "low":
			choices = append(choices, promptChoiceItem{Label: "low", Value: "low", Detail: "Faster responses with lower reasoning overhead."})
		case "medium":
			choices = append(choices, promptChoiceItem{Label: "medium", Value: "medium", Detail: "Balanced reasoning depth and speed."})
		case "high":
			choices = append(choices, promptChoiceItem{Label: "high", Value: "high", Detail: "Deeper reasoning."})
		case "xhigh":
			choices = append(choices, promptChoiceItem{Label: "xhigh", Value: "xhigh", Detail: "The highest reasoning effort."})
		}
	}
	return choices
}

func connectReasoningCapabilityChoices() []promptChoiceItem {
	return []promptChoiceItem{
		{
			Label:  "None",
			Value:  reasoningModeNone,
			Detail: "The model does not provide an extra reasoning mode.",
		},
		{
			Label:  "Toggle",
			Value:  reasoningModeToggle,
			Detail: "The model only supports enable or disable, without effort levels.",
		},
		{
			Label:  "Effort levels",
			Value:  reasoningModeEffort,
			Detail: "The model supports reasoning levels such as low, medium, and high.",
		},
	}
}

func reasoningLevelsForMode(mode string, efforts []string) []string {
	mode = normalizeCatalogReasoningMode(mode)
	switch mode {
	case reasoningModeFixed:
		return nil
	case reasoningModeToggle:
		return []string{"none"}
	case reasoningModeEffort:
		return normalizeReasoningLevels(efforts)
	default:
		return nil
	}
}

func reasoningExtrasDefault(levels []string) string {
	levels = normalizeReasoningLevels(levels)
	extras := make([]string, 0, len(levels))
	for _, level := range levels {
		if level == "low" || level == "medium" || level == "high" {
			continue
		}
		extras = append(extras, level)
	}
	return strings.Join(extras, ",")
}

func mergeReasoningEffortLists(parts ...[]string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)
	for _, list := range parts {
		for _, one := range normalizeReasoningLevels(list) {
			if one == "none" {
				continue
			}
			if _, ok := seen[one]; ok {
				continue
			}
			seen[one] = struct{}{}
			out = append(out, one)
		}
	}
	return out
}

func containsString(items []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	return slices.Contains(items, target)
}

type promptChoiceItem struct {
	Label  string
	Value  string
	Detail string
}

func (c *cliConsole) promptChoice(prompt string, choices []promptChoiceItem, defaultValue string, filterable bool) (string, error) {
	if c.prompter == nil {
		return "", fmt.Errorf("interactive prompt is unavailable")
	}
	if len(choices) == 0 {
		return "", fmt.Errorf("no choices available")
	}
	if requester, ok := c.prompter.(promptChoiceRequester); ok {
		promptChoices := make([]tuievents.PromptChoice, 0, len(choices))
		for _, choice := range choices {
			promptChoices = append(promptChoices, tuievents.PromptChoice{
				Label:         choice.Label,
				Value:         choice.Value,
				Detail:        choice.Detail,
				AlwaysVisible: choice.Value == connectCustomModelValue,
			})
		}
		return requester.RequestChoicePrompt(prompt, promptChoices, defaultValue, filterable)
	}
	c.ui.Section(prompt)
	defaultIndex := 0
	for i, choice := range choices {
		text := choice.Label
		if choice.Detail != "" {
			text += " (" + choice.Detail + ")"
		}
		if strings.TrimSpace(defaultValue) != "" && choice.Value == defaultValue {
			defaultIndex = i
		}
		c.ui.Numbered(i+1, text)
	}
	index, err := promptIntInRange(c, "choice", 1, len(choices), defaultIndex+1)
	if err != nil {
		return "", err
	}
	return choices[index-1].Value, nil
}

func (c *cliConsole) promptMultiChoice(prompt string, choices []promptChoiceItem, filterable bool) ([]string, error) {
	return c.promptMultiChoiceWithDefaults(prompt, choices, nil, filterable)
}

func (c *cliConsole) promptMultiChoiceWithDefaults(prompt string, choices []promptChoiceItem, selectedChoices []string, filterable bool) ([]string, error) {
	if c.prompter == nil {
		return nil, fmt.Errorf("interactive prompt is unavailable")
	}
	if len(choices) == 0 {
		return nil, fmt.Errorf("no choices available")
	}
	if requester, ok := c.prompter.(promptChoiceRequester); ok {
		promptChoices := make([]tuievents.PromptChoice, 0, len(choices))
		for _, choice := range choices {
			promptChoices = append(promptChoices, tuievents.PromptChoice{
				Label:  choice.Label,
				Value:  choice.Value,
				Detail: choice.Detail,
			})
		}
		raw, err := requester.RequestMultiChoicePrompt(prompt, promptChoices, selectedChoices, filterable)
		if err != nil {
			return nil, err
		}
		return dedupeOrderedStrings(splitArrayInput(raw)), nil
	}
	c.ui.Section(prompt)
	for i, choice := range choices {
		text := choice.Label
		if choice.Detail != "" {
			text += " (" + choice.Detail + ")"
		}
		c.ui.Numbered(i+1, text)
	}
	raw, err := c.promptText("choices(space separated)", "", false)
	if err != nil {
		return nil, err
	}
	parts := splitArrayInput(raw)
	if len(parts) == 0 {
		return nil, fmt.Errorf("no model selected")
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		index, convErr := strconv.Atoi(strings.TrimSpace(part))
		if convErr != nil || index < 1 || index > len(choices) {
			return nil, fmt.Errorf("invalid choice %q", part)
		}
		value := choices[index-1].Value
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func findRemoteModelByName(models []modelproviders.RemoteModel, name string) (*modelproviders.RemoteModel, bool) {
	target := strings.ToLower(strings.TrimSpace(name))
	for i := range models {
		if strings.ToLower(strings.TrimSpace(models[i].Name)) == target {
			return &models[i], true
		}
	}
	return nil, false
}

func splitArrayInput(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
}

func dedupeOrderedStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
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
	return out
}

func describeRemoteModel(provider string, item modelproviders.RemoteModel) string {
	ref := connectDisplayModelRef(provider, item.Name)
	if detail := describeRemoteModelDetail(item); detail != "" {
		return fmt.Sprintf("%s (%s)", ref, detail)
	}
	return ref
}

func connectDisplayModelRef(provider, modelName string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.TrimSpace(modelName)
	if provider == "" || modelName == "" {
		return ""
	}
	if provider == "openrouter" {
		return modelName
	}
	return canonicalModelRef(provider, modelName)
}

func describeRemoteModelDetail(item modelproviders.RemoteModel) string {
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
	return strings.Join(parts, ", ")
}

func shouldSuppressDiscoverModelsError(cfg modelproviders.Config, err error) bool {
	if err == nil {
		return false
	}
	if cfg.API == modelproviders.APIGemini && strings.Contains(err.Error(), "http status 400") {
		return true
	}
	if cfg.Provider == "minimax" {
		return true
	}
	if cfg.API == modelproviders.APIAnthropicCompatible {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "http status 404") || strings.Contains(lower, "http status 405") || strings.Contains(lower, "http status 501") || strings.Contains(lower, "not implemented") {
			return true
		}
	}
	if cfg.API == modelproviders.APIVolcengineCoding {
		return true
	}
	return false
}

func reportConnectCatalogFallback(c *cliConsole, err error) {
	if c == nil || err == nil {
		return
	}
	const hint = "models.dev unavailable; using bundled model snapshot"
	if c.tuiSender != nil {
		c.tuiSender.Send(tuievents.SetHintMsg{
			Hint:       hint,
			ClearAfter: transientHintDuration,
		})
		return
	}
	if c.ui != nil {
		c.ui.Note("%s: %v\n", hint, err)
	}
}

func (c *cliConsole) setPromptLoading(running bool) {
	if c == nil || c.tuiSender == nil {
		return
	}
	c.tuiSender.Send(tuievents.SetRunningMsg{Running: running})
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
	return strings.TrimSpace(line), nil
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

func promptTokenCount(c *cliConsole, name string, defaultValue int, cfg modelproviders.Config) (int, error) {
	text, err := c.promptText(connectPromptLabel(name, cfg)+"(k)", formatTokenCountDefault(defaultValue), false)
	if err != nil {
		return 0, err
	}
	value, err := parseTokenCountInput(text)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %q", name, text)
	}
	if value < 0 {
		return 0, fmt.Errorf("invalid %s: must be >= 0", name)
	}
	return value, nil
}

func formatTokenCountDefault(value int) string {
	if value <= 0 {
		return "0"
	}
	if value%1024 == 0 {
		return strconv.Itoa(value/1024) + "k"
	}
	return strconv.Itoa(value)
}

func parseTokenCountInput(raw string) (int, error) {
	text := strings.ToLower(strings.TrimSpace(raw))
	if text == "" {
		return 0, fmt.Errorf("empty token count")
	}
	multiplier := 1
	switch {
	case strings.HasSuffix(text, "k"):
		multiplier = 1024
		text = strings.TrimSpace(strings.TrimSuffix(text, "k"))
	case strings.HasSuffix(text, "m"):
		multiplier = 1024 * 1024
		text = strings.TrimSpace(strings.TrimSuffix(text, "m"))
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		return 0, err
	}
	if multiplier == 1 && value > 0 && value <= 4096 {
		multiplier = 1024
	}
	return value * multiplier, nil
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
