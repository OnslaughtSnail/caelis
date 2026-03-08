package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/OnslaughtSnail/caelis/internal/cli/modelcatalog"
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

	baseURL := strings.TrimSpace(tpl.defaultBaseURL)
	if tpl.provider == "openai-compatible" {
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

	remoteModels, discoverErr := discoverModelsFn(c.baseCtx, baseCfg)
	if discoverErr != nil && !shouldSuppressDiscoverModelsError(baseCfg, discoverErr) {
		c.ui.Warn("list_models failed: %v\n", discoverErr)
	}

	choices := buildConnectModelChoices(tpl.provider, remoteModels)
	if len(choices) == 0 {
		choices = append(choices, connectModelChoice{
			Name:    connectCustomModelValue,
			Display: "输入自定义模型名",
			Detail:  "provider 未返回模型目录，手动输入",
		})
	}

	modelNames, err := promptConnectModelChoices(c, tpl.provider, choices)
	if err != nil {
		return false, err
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
	if c.credentialStore != nil {
		c.ui.Note("api_key 已保存到本地凭据库，config 仅保存 credential_ref。\n")
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
	value, err := c.promptChoice("选择 provider", choices, providerTemplates[0].label, false)
	if err != nil {
		return providerTemplate{}, err
	}
	tpl, ok := findProviderTemplate(value)
	if !ok {
		return providerTemplate{}, fmt.Errorf("unknown provider %q", value)
	}
	return tpl, nil
}

func buildConnectModelChoices(provider string, remoteModels []modelproviders.RemoteModel) []connectModelChoice {
	seen := map[string]struct{}{}
	out := make([]connectModelChoice, 0, len(remoteModels)+16)
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
			Display: canonicalModelRef(provider, name),
			Detail:  strings.TrimSpace(detail),
		})
	}
	for _, item := range remoteModels {
		add(item.Name, describeRemoteModelDetail(item))
	}
	for _, name := range listProviderCatalogModels(provider) {
		add(name, "")
	}
	for _, name := range commonModelsForProvider(provider) {
		add(name, "")
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Display) < strings.ToLower(out[j].Display)
	})
	out = append(out, connectModelChoice{
		Name:    connectCustomModelValue,
		Display: "输入自定义模型名",
		Detail:  "手动输入 provider/model",
	})
	return out
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
	values, err := c.promptMultiChoice("选择模型", promptChoices, true)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == connectCustomModelValue {
			customValue, err := c.promptText("model", "", false)
			if err != nil {
				return nil, err
			}
			customValue = strings.TrimSpace(customValue)
			if customValue == "" {
				return nil, fmt.Errorf("model is required")
			}
			out = append(out, customValue)
			continue
		}
		out = append(out, strings.TrimSpace(value))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no model selected")
	}
	return out, nil
}

func buildConnectModelSelection(c *cliConsole, tpl providerTemplate, baseCfg modelproviders.Config, credentialRef string, modelName string, remote *modelproviders.RemoteModel) (connectModelSelection, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return connectModelSelection{}, fmt.Errorf("model is required")
	}
	alias := canonicalModelRef(baseCfg.Provider, modelName)
	if alias == "" {
		return connectModelSelection{}, fmt.Errorf("invalid provider/model")
	}

	_, baseKnown := lookupBaseCatalogModelCapabilities(baseCfg.Provider, modelName)
	caps, mergedKnown := lookupSuggestedCatalogModelCapabilities(baseCfg.Provider, modelName)
	if !mergedKnown {
		caps = defaultCatalogModelCapabilities()
	}
	if caps.ContextWindowTokens <= 0 {
		caps.ContextWindowTokens = defaultContextWindowForTemplate(tpl)
	}
	if caps.DefaultMaxOutputTokens <= 0 {
		caps.DefaultMaxOutputTokens = defaultMaxOutputForTemplate(tpl)
	}
	if caps.MaxOutputTokens <= 0 {
		caps.MaxOutputTokens = caps.DefaultMaxOutputTokens
	}
	requiresAdvanced := !baseKnown || !reasoningDefinitionKnown(baseCfg.Provider, caps)
	if remote != nil {
		if remote.ContextWindowTokens > 0 {
			caps.ContextWindowTokens = remote.ContextWindowTokens
		}
		if remote.MaxOutputTokens > 0 {
			caps.MaxOutputTokens = remote.MaxOutputTokens
			if caps.DefaultMaxOutputTokens <= 0 {
				caps.DefaultMaxOutputTokens = remote.MaxOutputTokens
			}
		}
	}

	reasoningMode := normalizeCatalogReasoningMode(caps.ReasoningMode)
	reasoningEfforts := normalizeReasoningLevels(caps.ReasoningEfforts)
	defaultReasoningEffort := normalizeReasoningEffort(caps.DefaultReasoningEffort)
	contextWindow := caps.ContextWindowTokens
	maxOutput := caps.DefaultMaxOutputTokens
	if maxOutput <= 0 {
		maxOutput = caps.MaxOutputTokens
	}
	if maxOutput <= 0 {
		maxOutput = defaultMaxOutputForTemplate(tpl)
	}
	if contextWindow <= 0 {
		contextWindow = defaultContextWindowForTemplate(tpl)
	}

	if requiresAdvanced {
		c.ui.Note("模型 %s 缺少完整能力定义，进入补充配置。\n", alias)
		var err error
		contextWindow, err = promptInt(c, "context_window_tokens", contextWindow)
		if err != nil {
			return connectModelSelection{}, err
		}
		maxOutput, err = promptInt(c, "max_output_tokens", maxOutput)
		if err != nil {
			return connectModelSelection{}, err
		}
		reasoningMode, reasoningEfforts, defaultReasoningEffort, err = promptReasoningDefinition(c, reasoningMode, reasoningEfforts, defaultReasoningEffort)
		if err != nil {
			return connectModelSelection{}, err
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
		requiresAdvance: requiresAdvanced,
	}, nil
}

func applyConnectRuntimeDefaults(c *cliConsole, cfg *modelproviders.Config) {
	if cfg == nil {
		return
	}
	cfg.ThinkingMode = defaultThinkingMode
	cfg.ThinkingBudget = defaultThinkingBudget
	cfg.ReasoningEffort = defaultReasoningEffort
	if c != nil && c.configStore != nil {
		settings := c.configStore.ModelRuntimeSettings(cfg.Alias)
		cfg.ThinkingMode = settings.ThinkingMode
		cfg.ThinkingBudget = settings.ThinkingBudget
		cfg.ReasoningEffort = settings.ReasoningEffort
	}
	if len(cfg.ReasoningLevels) > 0 && cfg.ThinkingMode == defaultThinkingMode && cfg.ReasoningEffort == defaultReasoningEffort {
		profile := reasoningProfileForConfig(*cfg)
		switch profile.Mode {
		case reasoningModeEffort:
			cfg.ThinkingMode = "on"
			cfg.ReasoningEffort = profile.DefaultEffort
		case reasoningModeToggle:
			cfg.ThinkingMode = defaultThinkingMode
			cfg.ReasoningEffort = defaultReasoningEffort
		}
	}
}

func reasoningDefinitionKnown(provider string, caps modelcatalog.ModelCapabilities) bool {
	switch caps.ReasoningMode {
	case reasoningModeNone:
		return true
	case reasoningModeToggle:
		return true
	case reasoningModeEffort:
		return len(caps.ReasoningEfforts) > 0
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "deepseek", "xiaomi", "mimo":
		return true
	default:
		return false
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

func promptReasoningDefinition(c *cliConsole, defaultMode string, defaultEfforts []string, defaultEffort string) (string, []string, string, error) {
	defaultMode = normalizeCatalogReasoningMode(defaultMode)
	if defaultMode == "" {
		defaultMode = reasoningModeNone
	}
	mode, err := c.promptChoice("选择 reasoning 模式", []promptChoiceItem{
		{Label: "none", Value: reasoningModeNone},
		{Label: "toggle", Value: reasoningModeToggle},
		{Label: "effort", Value: reasoningModeEffort},
	}, defaultMode, false)
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
		selected, selErr := c.promptMultiChoiceWithDefaults("选择 reasoning efforts", []promptChoiceItem{
			{Label: "low", Value: "low"},
			{Label: "medium", Value: "medium"},
			{Label: "high", Value: "high"},
		}, selectedDefaults, false)
		if selErr != nil {
			return "", nil, "", selErr
		}
		extraDefault := reasoningExtrasDefault(selectedDefaults)
		extraRaw, extraErr := c.promptText("额外 reasoning efforts(optional, comma/space/tab)", extraDefault, false)
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

func reasoningLevelsForMode(mode string, efforts []string) []string {
	mode = normalizeCatalogReasoningMode(mode)
	switch mode {
	case reasoningModeToggle:
		return []string{"none"}
	case reasoningModeEffort:
		out := []string{"none"}
		return append(out, normalizeReasoningLevels(efforts)...)
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
	for _, one := range items {
		if one == target {
			return true
		}
	}
	return false
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
				Label:  choice.Label,
				Value:  choice.Value,
				Detail: choice.Detail,
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
	ref := canonicalModelRef(provider, item.Name)
	if detail := describeRemoteModelDetail(item); detail != "" {
		return fmt.Sprintf("%s (%s)", ref, detail)
	}
	return ref
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
