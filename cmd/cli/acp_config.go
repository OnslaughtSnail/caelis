package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

func resolveACPSessionReasoning(defaults modelRuntimeSettings, values map[string]string) string {
	reasoningEffort := normalizeReasoningEffort(defaults.ReasoningEffort)
	if len(values) == 0 {
		return reasoningEffort
	}
	if raw := strings.TrimSpace(values[acpConfigReasoningEffort]); raw != "" {
		switch normalizeReasoningSelection(raw) {
		case "off", "none":
			reasoningEffort = "none"
		case "on":
			reasoningEffort = normalizeReasoningEffort(defaults.ReasoningEffort)
		default:
			reasoningEffort = normalizeReasoningEffort(raw)
		}
	}
	return reasoningEffort
}

func buildACPSessionConfigOptions(sessionModes []internalacp.SessionMode, factory *modelproviders.Factory, configStore *appConfigStore, defaultAlias string) []internalacp.SessionConfigOptionTemplate {
	defaults := defaultModelRuntimeSettings()
	if configStore != nil {
		defaults = configStore.ModelRuntimeSettings(defaultAlias)
	}
	reasoningOptions, reasoningDefault := buildACPReasoningOptionsForAlias(factory, configStore, defaultAlias, defaults)
	return []internalacp.SessionConfigOptionTemplate{
		{
			ID:           acpConfigMode,
			Name:         "Approval Preset",
			Description:  "Choose an approval and sandboxing preset for your session",
			Category:     "mode",
			DefaultValue: "default",
			Options:      buildACPSessionModeOptions(sessionModes),
		},
		{
			ID:           acpConfigModel,
			Name:         "Model",
			Description:  "Choose which model caelis should use",
			Category:     "model",
			DefaultValue: defaultAlias,
			Options:      buildACPModelSelectOptions(factory, configStore),
		},
		{
			ID:           acpConfigReasoningEffort,
			Name:         "Reasoning Effort",
			Description:  "Choose how much reasoning effort the model should use",
			Category:     "thought_level",
			DefaultValue: reasoningDefault,
			Options:      reasoningOptions,
		},
	}
}

func buildACPAuth(methodID string, methodName string, tokenEnv string) ([]internalacp.AuthMethod, internalacp.AuthValidator, error) {
	if methodID == "" {
		return nil, nil, nil
	}
	methodName = strings.TrimSpace(methodName)
	if methodName == "" {
		methodName = "Local token"
	}
	description := "Lightweight local ACP handshake for stdio agents."
	if tokenEnv != "" {
		if strings.TrimSpace(os.Getenv(tokenEnv)) == "" {
			return nil, nil, fmt.Errorf("auth token env %q is empty", tokenEnv)
		}
		description = fmt.Sprintf("Lightweight local ACP handshake validated against %s.", tokenEnv)
	}
	methods := []internalacp.AuthMethod{{
		ID:          methodID,
		Name:        methodName,
		Description: description,
	}}
	validator := func(ctx context.Context, req internalacp.AuthenticateRequest) error {
		_ = ctx
		methodID := strings.TrimSpace(req.MethodID)
		if methodID == "" {
			return fmt.Errorf("authentication method is required")
		}
		presented := lookupACPAuthCredential(methodID)
		if strings.TrimSpace(presented) == "" {
			return fmt.Errorf("authentication credential for %q is unavailable in the agent environment", methodID)
		}
		if tokenEnv == "" {
			return nil
		}
		expected := strings.TrimSpace(os.Getenv(tokenEnv))
		if expected == "" {
			return fmt.Errorf("auth token env %q is empty", tokenEnv)
		}
		if presented != expected {
			return fmt.Errorf("authentication failed for %q", methodID)
		}
		return nil
	}
	return methods, validator, nil
}

func lookupACPAuthCredential(methodID string) string {
	for _, key := range acpAuthEnvKeys(methodID) {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return ""
}

func acpAuthEnvKeys(methodID string) []string {
	methodID = strings.TrimSpace(methodID)
	if methodID == "" {
		return nil
	}
	normalized := strings.ToUpper(methodID)
	normalized = strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, normalized)
	normalized = strings.Trim(normalized, "_")
	keys := []string{methodID}
	if normalized != "" {
		keys = append(keys, normalized, "ACPX_AUTH_"+normalized)
	}
	sort.Strings(keys)
	return dedupeStrings(keys)
}

const (
	acpConfigMode            = "mode"
	acpConfigModel           = "model"
	acpConfigReasoningEffort = "reasoning_effort"
)

func buildACPSessionModeOptions(modes []internalacp.SessionMode) []internalacp.SessionConfigSelectOption {
	out := make([]internalacp.SessionConfigSelectOption, 0, len(modes))
	for _, item := range modes {
		out = append(out, internalacp.SessionConfigSelectOption{
			Value:       strings.TrimSpace(item.ID),
			Name:        strings.TrimSpace(item.Name),
			Description: strings.TrimSpace(item.Description),
		})
	}
	return out
}

func buildACPModelSelectOptions(factory *modelproviders.Factory, configStore *appConfigStore) []internalacp.SessionConfigSelectOption {
	aliases := configuredACPModelAliases(factory, configStore)
	out := make([]internalacp.SessionConfigSelectOption, 0, len(aliases))
	for _, alias := range aliases {
		cfg, _ := factory.ConfigForAlias(alias)
		out = append(out, internalacp.SessionConfigSelectOption{
			Value:       alias,
			Name:        alias,
			Description: formatACPModelDescription(cfg),
		})
	}
	return out
}

func configuredACPModelAliases(factory *modelproviders.Factory, configStore *appConfigStore) []string {
	if configStore != nil {
		aliases := configStore.ConfiguredModelAliases()
		if len(aliases) > 0 {
			return aliases
		}
	}
	if factory == nil {
		return nil
	}
	return factory.ListModels()
}

func resolveACPSelectedModelAlias(defaultAlias string, values map[string]string, configStore *appConfigStore) string {
	selectedAlias := strings.TrimSpace(defaultAlias)
	if raw := strings.TrimSpace(values[acpConfigModel]); raw != "" {
		selectedAlias = raw
	}
	if configStore != nil {
		if resolved := configStore.ResolveModelAlias(selectedAlias); resolved != "" {
			selectedAlias = resolved
		}
	}
	return strings.ToLower(strings.TrimSpace(selectedAlias))
}

func defaultACPReasoningSelection(factory *modelproviders.Factory, alias string, defaults modelRuntimeSettings) string {
	if effort := normalizeReasoningEffort(defaults.ReasoningEffort); effort != "" {
		return effort
	}
	if factory != nil {
		if cfg, ok := factory.ConfigForAlias(alias); ok {
			profile := reasoningProfileForConfig(cfg)
			switch profile.Mode {
			case reasoningModeNone:
				return "none"
			case reasoningModeFixed:
				return "none"
			case reasoningModeToggle:
				return reasoningProfileDefaultEffort(profile)
			case reasoningModeEffort:
				if profile.DefaultEffort != "" {
					return profile.DefaultEffort
				}
			}
		}
	}
	return "none"
}

func buildACPReasoningOptionsForAlias(factory *modelproviders.Factory, configStore *appConfigStore, alias string, defaults modelRuntimeSettings) ([]internalacp.SessionConfigSelectOption, string) {
	cfg := acpReasoningConfigForAlias(factory, configStore, alias)
	modelOptions := modelReasoningOptionsForConfig(cfg)
	if len(modelOptions) == 0 {
		return []internalacp.SessionConfigSelectOption{{
			Value:       "none",
			Name:        "None",
			Description: "Reasoning is unavailable for this model.",
		}}, "none"
	}
	options := make([]internalacp.SessionConfigSelectOption, 0, len(modelOptions))
	for _, one := range modelOptions {
		name := strings.TrimSpace(one.Display)
		if name == "" {
			name = strings.TrimSpace(one.Value)
		}
		options = append(options, internalacp.SessionConfigSelectOption{
			Value:       one.Value,
			Name:        titleizeACPOptionName(name),
			Description: acpReasoningOptionDescription(one),
		})
	}
	return options, normalizeACPReasoningSelection(cfg, defaultACPReasoningSelection(factory, alias, defaults))
}

func buildACPSessionConfigState(templates []internalacp.SessionConfigOptionTemplate, factory *modelproviders.Factory, configStore *appConfigStore, defaultAlias string, sessionCfg internalacp.AgentSessionConfig) []internalacp.SessionConfigOption {
	normalized := normalizeACPSessionConfig(factory, configStore, defaultAlias, sessionCfg)
	selectedAlias := resolveACPSelectedModelAlias(defaultAlias, normalized.ConfigValues, configStore)
	defaults := defaultModelRuntimeSettings()
	if configStore != nil {
		defaults = configStore.ModelRuntimeSettings(selectedAlias)
	}
	reasoningOptions, reasoningDefault := buildACPReasoningOptionsForAlias(factory, configStore, selectedAlias, defaults)
	values := normalized.ConfigValues
	out := make([]internalacp.SessionConfigOption, 0, len(templates))
	for _, item := range templates {
		current := strings.TrimSpace(values[item.ID])
		options := append([]internalacp.SessionConfigSelectOption(nil), item.Options...)
		switch strings.TrimSpace(item.ID) {
		case acpConfigMode:
			current = strings.TrimSpace(normalized.ModeID)
		case acpConfigModel:
			current = selectedAlias
		case acpConfigReasoningEffort:
			current = strings.TrimSpace(values[item.ID])
			if current == "" {
				current = reasoningDefault
			}
			options = reasoningOptions
		}
		if current == "" {
			current = strings.TrimSpace(item.DefaultValue)
		}
		out = append(out, internalacp.SessionConfigOption{
			Type:         "select",
			ID:           item.ID,
			Name:         item.Name,
			Description:  item.Description,
			Category:     item.Category,
			CurrentValue: current,
			Options:      options,
		})
	}
	return out
}

func normalizeACPSessionConfig(factory *modelproviders.Factory, configStore *appConfigStore, defaultAlias string, sessionCfg internalacp.AgentSessionConfig) internalacp.AgentSessionConfig {
	cfg := internalacp.AgentSessionConfig{
		ModeID:       strings.TrimSpace(sessionCfg.ModeID),
		ConfigValues: cloneACPConfigMap(sessionCfg.ConfigValues),
	}
	if cfg.ConfigValues == nil {
		cfg.ConfigValues = map[string]string{}
	}
	if cfg.ModeID == "" {
		if modeValue := strings.TrimSpace(cfg.ConfigValues[acpConfigMode]); modeValue != "" {
			cfg.ModeID = modeValue
		} else {
			cfg.ModeID = "default"
		}
	}
	cfg.ConfigValues[acpConfigMode] = cfg.ModeID
	selectedAlias := resolveACPSelectedModelAlias(defaultAlias, cfg.ConfigValues, configStore)
	if selectedAlias != "" {
		cfg.ConfigValues[acpConfigModel] = selectedAlias
	}
	reasoningCfg := acpReasoningConfigForAlias(factory, configStore, selectedAlias)
	rawReasoning := strings.TrimSpace(cfg.ConfigValues[acpConfigReasoningEffort])
	if rawReasoning == "" {
		defaults := defaultModelRuntimeSettings()
		if configStore != nil {
			defaults = configStore.ModelRuntimeSettings(selectedAlias)
		}
		rawReasoning = defaultACPReasoningSelection(factory, selectedAlias, defaults)
	}
	cfg.ConfigValues[acpConfigReasoningEffort] = normalizeACPReasoningSelection(reasoningCfg, rawReasoning)
	return cfg
}

func cloneACPConfigMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func acpReasoningConfigForAlias(factory *modelproviders.Factory, configStore *appConfigStore, alias string) modelproviders.Config {
	alias = resolveACPSelectedModelAlias(alias, map[string]string{acpConfigModel: alias}, configStore)
	cfg := modelproviders.Config{Alias: alias}
	if factory != nil {
		if foundCfg, ok := factory.ConfigForAlias(alias); ok {
			return foundCfg
		}
	}
	if strings.TrimSpace(cfg.Provider) == "" || strings.TrimSpace(cfg.Model) == "" {
		parts := strings.SplitN(alias, "/", 2)
		if len(parts) == 2 {
			cfg.Provider = strings.TrimSpace(parts[0])
			cfg.Model = strings.TrimSpace(parts[1])
		}
	}
	return cfg
}

func normalizeACPReasoningSelection(cfg modelproviders.Config, raw string) string {
	opt, err := resolveModelReasoningOption(cfg, raw)
	if err == nil && strings.TrimSpace(opt.Value) != "" {
		return strings.TrimSpace(opt.Value)
	}
	options := modelReasoningOptionsForConfig(cfg)
	if len(options) > 0 {
		return strings.TrimSpace(options[0].Value)
	}
	return "none"
}

func titleizeACPOptionName(value string) string {
	if value == "" {
		return ""
	}
	if len(value) == 1 {
		return strings.ToUpper(value)
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func acpReasoningOptionDescription(option modelReasoningOption) string {
	switch strings.TrimSpace(option.Value) {
	case "off", "none":
		return "Disable extra reasoning."
	case "on":
		return "Enable extra reasoning."
	case "minimal":
		return "Use minimal reasoning effort."
	case "low":
		return "Fast responses with lighter reasoning."
	case "medium":
		return "Balance speed and reasoning depth."
	case "high":
		return "Greater reasoning depth for complex problems."
	case "xhigh":
		return "Extra high reasoning depth for complex problems."
	default:
		return ""
	}
}

func acpModelSupportsImages(factory *modelproviders.Factory, alias string) bool {
	if factory == nil {
		return false
	}
	cfg, ok := factory.ConfigForAlias(strings.TrimSpace(alias))
	if !ok {
		return false
	}
	caps, found := lookupCatalogModelCapabilities(cfg.Provider, cfg.Model)
	return found && caps.SupportsImages
}

func formatACPModelDescription(cfg modelproviders.Config) string {
	parts := make([]string, 0, 3)
	if provider := strings.TrimSpace(cfg.Provider); provider != "" {
		parts = append(parts, provider)
	}
	if modelName := strings.TrimSpace(cfg.Model); modelName != "" {
		parts = append(parts, modelName)
	}
	profile := reasoningProfileForConfig(cfg)
	switch profile.Mode {
	case reasoningModeFixed:
		parts = append(parts, "reasoning is fixed")
	case reasoningModeToggle:
		parts = append(parts, "supports on/off reasoning")
	case reasoningModeEffort:
		if len(profile.SupportedEfforts) > 0 {
			parts = append(parts, "supports "+strings.Join(profile.SupportedEfforts, "/")+" reasoning")
		}
	}
	return strings.Join(parts, " ")
}

func buildACPSessionList(index *sessionIndex, workspace workspaceContext, req internalacp.SessionListRequest) internalacp.SessionListResponse {
	if index == nil {
		return internalacp.SessionListResponse{Sessions: []internalacp.SessionSummary{}}
	}
	records, err := index.ListWorkspaceSessionsPage(workspace.Key, 1, 200)
	if err != nil {
		return internalacp.SessionListResponse{Sessions: []internalacp.SessionSummary{}}
	}
	filtered := make([]sessionIndexRecord, 0, len(records))
	for _, rec := range records {
		if rec.EventCount <= 0 {
			continue
		}
		filtered = append(filtered, rec)
	}
	start := 0
	cursor := strings.TrimSpace(req.Cursor)
	if cursor != "" {
		for i, rec := range filtered {
			if buildACPSessionCursor(rec) == cursor {
				start = i + 1
				break
			}
		}
	}
	limit := 20
	if req.Limit != nil && *req.Limit > 0 {
		limit = *req.Limit
	}
	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	items := make([]internalacp.SessionSummary, 0, end-start)
	for _, rec := range filtered[start:end] {
		items = append(items, internalacp.SessionSummary{
			SessionID: rec.SessionID,
			CWD:       rec.WorkspaceCWD,
			Title:     acpSessionTitle(rec),
			UpdatedAt: rec.LastEventAt.UTC().Format(time.RFC3339),
		})
	}
	resp := internalacp.SessionListResponse{Sessions: items}
	if end < len(filtered) && end > start {
		resp.NextCursor = buildACPSessionCursor(filtered[end-1])
	}
	return resp
}

func buildACPSessionCursor(rec sessionIndexRecord) string {
	return rec.LastEventAt.UTC().Format(time.RFC3339) + "|" + rec.SessionID
}

func acpSessionTitle(rec sessionIndexRecord) string {
	return sessionIndexPreview(rec, 120)
}
