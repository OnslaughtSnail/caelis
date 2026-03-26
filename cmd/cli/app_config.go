package main

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	stdruntime "runtime"
	"sort"
	"strings"
	"time"
	"unicode"

	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	"github.com/OnslaughtSnail/caelis/internal/envload"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

const (
	configVersion    = 1
	defaultModel     = ""
	configFileSuffix = "_config.json"

	defaultThinkingMode    = "auto"
	defaultThinkingBudget  = 1024
	defaultReasoningEffort = ""

	defaultPermissionMode = "default"
)

var configEnvPlaceholderPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type appConfig struct {
	Version                   int                    `json:"version"`
	DefaultModel              string                 `json:"default_model"`
	PermissionMode            string                 `json:"permission_mode,omitempty"`
	SandboxType               string                 `json:"sandbox_type,omitempty"`
	DefaultAgent              string                 `json:"defaultAgent,omitempty"`
	DefaultPermissions        string                 `json:"defaultPermissions,omitempty"`
	NonInteractivePermissions string                 `json:"nonInteractivePermissions,omitempty"`
	AuthPolicy                string                 `json:"authPolicy,omitempty"`
	TTL                       int                    `json:"ttl,omitempty"`
	Timeout                   *int                   `json:"timeout,omitempty"`
	Format                    string                 `json:"format,omitempty"`
	Providers                 []providerRecord       `json:"providers,omitempty"`
	Agents                    map[string]agentRecord `json:"agents,omitempty"`
	Auth                      map[string]string      `json:"auth,omitempty"`
}

type agentRecord struct {
	Name        string            `json:"-"`
	Description string            `json:"description,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	WorkDir     string            `json:"workDir,omitempty"`
	Stability   string            `json:"stability,omitempty"`
}

type runtimeSettings struct {
	PermissionMode string
	SandboxType    string
}

type modelRuntimeSettings struct {
	ThinkingBudget  int
	ReasoningEffort string
}

type providerRecord struct {
	Alias                     string            `json:"alias"`
	Provider                  string            `json:"provider"`
	API                       string            `json:"api"`
	Model                     string            `json:"model"`
	BaseURL                   string            `json:"base_url"`
	Headers                   map[string]string `json:"headers,omitempty"`
	TimeoutSeconds            int               `json:"timeout_seconds,omitempty"`
	MaxOutputTok              int               `json:"max_output_tokens,omitempty"`
	ContextWindowTokens       int               `json:"context_window_tokens,omitempty"`
	ReasoningLevels           []string          `json:"reasoning_levels,omitempty"`
	ReasoningMode             string            `json:"reasoning_mode,omitempty"`
	SupportedReasoningEfforts []string          `json:"supported_reasoning_efforts,omitempty"`
	DefaultReasoningEffort    string            `json:"default_reasoning_effort,omitempty"`
	ThinkingMode              string            `json:"thinking_mode,omitempty"`
	ThinkingBudget            int               `json:"thinking_budget,omitempty"`
	ReasoningEffort           string            `json:"reasoning_effort,omitempty"`
	OpenRouter                *openRouterRecord `json:"openrouter,omitempty"`
	Auth                      authRecord        `json:"auth"`
}

type openRouterRecord struct {
	Models     []string         `json:"models,omitempty"`
	Route      string           `json:"route,omitempty"`
	Provider   map[string]any   `json:"provider,omitempty"`
	Transforms []string         `json:"transforms,omitempty"`
	Plugins    []map[string]any `json:"plugins,omitempty"`
}

type authRecord struct {
	Type          string `json:"type"`
	TokenEnv      string `json:"token_env,omitempty"`
	Token         string `json:"token,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
	HeaderKey     string `json:"header_key,omitempty"`
	Prefix        string `json:"prefix,omitempty"`
}

type appConfigStore struct {
	path string
	data appConfig
}

func loadOrInitAppConfig(appName string) (*appConfigStore, error) {
	path, err := configPath(appName)
	if err != nil {
		return nil, err
	}
	if _, err := loadConfigEnvFiles(path); err != nil {
		return nil, err
	}
	store := &appConfigStore{
		path: path,
		data: defaultAppConfig(),
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("cli config: read %q: %w", path, err)
		}
		if err := store.save(); err != nil {
			return nil, err
		}
		return store, nil
	}

	var rawTop map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawTop); err != nil {
		return nil, fmt.Errorf("cli config: parse %q: %w", path, err)
	}
	if _, ok := rawTop["agent_servers"]; ok {
		return nil, fmt.Errorf("cli config: invalid config %q: legacy key %q is no longer supported; migrate to top-level %q map using acpx-style agent definitions", path, "agent_servers", "agents")
	}

	var loaded appConfig
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return nil, fmt.Errorf("cli config: parse %q: %w", path, err)
	}
	if err := resolveAppConfigEnvPlaceholders(&loaded, path); err != nil {
		return nil, err
	}
	mergeAppConfigDefaults(&loaded)
	store.data = loaded
	return store, nil
}

func loadConfigEnvFiles(configFilePath string) ([]string, error) {
	paths := make([]string, 0, 2)
	if cwd, err := os.Getwd(); err == nil && strings.TrimSpace(cwd) != "" {
		paths = append(paths, filepath.Join(cwd, ".env"))
	}
	configDir := strings.TrimSpace(filepath.Dir(configFilePath))
	if configDir != "" {
		paths = append(paths, filepath.Join(configDir, ".env"))
	}
	unique := dedupeStrings(paths)
	loaded, err := envload.LoadFilesIfExists(unique)
	if err != nil {
		return nil, fmt.Errorf("cli config: load .env failed: %w", err)
	}
	return loaded, nil
}

func resolveAppConfigEnvPlaceholders(cfg *appConfig, configPath string) error {
	if cfg == nil {
		return nil
	}
	resolveField := func(fieldPath string, value *string) error {
		if value == nil {
			return nil
		}
		resolved, err := resolveConfigStringPlaceholders(*value)
		if err != nil {
			return fmt.Errorf("cli config: invalid config %q: %s: %w", configPath, fieldPath, err)
		}
		*value = resolved
		return nil
	}

	if err := resolveField("default_model", &cfg.DefaultModel); err != nil {
		return err
	}
	if err := resolveField("permission_mode", &cfg.PermissionMode); err != nil {
		return err
	}
	if err := resolveField("sandbox_type", &cfg.SandboxType); err != nil {
		return err
	}
	if err := resolveField("defaultAgent", &cfg.DefaultAgent); err != nil {
		return err
	}
	if err := resolveField("defaultPermissions", &cfg.DefaultPermissions); err != nil {
		return err
	}
	if err := resolveField("nonInteractivePermissions", &cfg.NonInteractivePermissions); err != nil {
		return err
	}
	if err := resolveField("authPolicy", &cfg.AuthPolicy); err != nil {
		return err
	}
	if err := resolveField("format", &cfg.Format); err != nil {
		return err
	}

	for i := range cfg.Providers {
		rec := &cfg.Providers[i]
		prefix := fmt.Sprintf("providers[%d]", i)

		if err := resolveField(prefix+".alias", &rec.Alias); err != nil {
			return err
		}
		if err := resolveField(prefix+".provider", &rec.Provider); err != nil {
			return err
		}
		if err := resolveField(prefix+".api", &rec.API); err != nil {
			return err
		}
		if err := resolveField(prefix+".model", &rec.Model); err != nil {
			return err
		}
		if err := resolveField(prefix+".base_url", &rec.BaseURL); err != nil {
			return err
		}
		if err := resolveField(prefix+".thinking_mode", &rec.ThinkingMode); err != nil {
			return err
		}
		if err := resolveField(prefix+".reasoning_mode", &rec.ReasoningMode); err != nil {
			return err
		}
		if err := resolveField(prefix+".default_reasoning_effort", &rec.DefaultReasoningEffort); err != nil {
			return err
		}
		if err := resolveField(prefix+".reasoning_effort", &rec.ReasoningEffort); err != nil {
			return err
		}

		if len(rec.Headers) > 0 {
			keys := make([]string, 0, len(rec.Headers))
			for key := range rec.Headers {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				value := rec.Headers[key]
				if err := resolveField(prefix+".headers."+key, &value); err != nil {
					return err
				}
				rec.Headers[key] = value
			}
		}

		if err := resolveField(prefix+".auth.type", &rec.Auth.Type); err != nil {
			return err
		}
		if err := resolveField(prefix+".auth.token_env", &rec.Auth.TokenEnv); err != nil {
			return err
		}
		if err := resolveField(prefix+".auth.token", &rec.Auth.Token); err != nil {
			return err
		}
		if err := resolveField(prefix+".auth.credential_ref", &rec.Auth.CredentialRef); err != nil {
			return err
		}
		if err := resolveField(prefix+".auth.header_key", &rec.Auth.HeaderKey); err != nil {
			return err
		}
		if err := resolveField(prefix+".auth.prefix", &rec.Auth.Prefix); err != nil {
			return err
		}
	}
	if len(cfg.Agents) > 0 {
		keys := make([]string, 0, len(cfg.Agents))
		for key := range cfg.Agents {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rec := cfg.Agents[key]
			if err := resolveAppConfigAgentPlaceholders(&rec, "agents."+key, resolveField); err != nil {
				return err
			}
			cfg.Agents[key] = rec
		}
	}
	if len(cfg.Auth) > 0 {
		keys := make([]string, 0, len(cfg.Auth))
		for key := range cfg.Auth {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := cfg.Auth[key]
			if err := resolveField("auth."+key, &value); err != nil {
				return err
			}
			cfg.Auth[key] = value
		}
	}
	return nil
}

func resolveAppConfigAgentPlaceholders(rec *agentRecord, prefix string, resolveField func(string, *string) error) error {
	if rec == nil {
		return nil
	}
	if err := resolveField(prefix+".description", &rec.Description); err != nil {
		return err
	}
	if err := resolveField(prefix+".command", &rec.Command); err != nil {
		return err
	}
	if err := resolveField(prefix+".workDir", &rec.WorkDir); err != nil {
		return err
	}
	if err := resolveField(prefix+".stability", &rec.Stability); err != nil {
		return err
	}
	for i := range rec.Args {
		if err := resolveField(fmt.Sprintf("%s.args[%d]", prefix, i), &rec.Args[i]); err != nil {
			return err
		}
	}
	if len(rec.Env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(rec.Env))
	for key := range rec.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := rec.Env[key]
		if err := resolveField(prefix+".env."+key, &value); err != nil {
			return err
		}
		rec.Env[key] = value
	}
	return nil
}

func resolveConfigStringPlaceholders(raw string) (string, error) {
	matches := configEnvPlaceholderPattern.FindAllStringSubmatchIndex(raw, -1)
	if len(matches) == 0 {
		return raw, nil
	}
	var b strings.Builder
	last := 0
	for _, idx := range matches {
		if len(idx) < 4 {
			continue
		}
		b.WriteString(raw[last:idx[0]])
		name := raw[idx[2]:idx[3]]
		value, ok := os.LookupEnv(name)
		if !ok || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("unresolved env placeholder ${%s}", name)
		}
		b.WriteString(value)
		last = idx[1]
	}
	b.WriteString(raw[last:])
	return b.String(), nil
}

func (s *appConfigStore) DefaultModel() string {
	if s == nil {
		return defaultModel
	}
	value := strings.TrimSpace(s.data.DefaultModel)
	return strings.ToLower(value)
}

func (s *appConfigStore) DefaultAgent() string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(s.data.DefaultAgent))
}

// AgentDescriptors converts configured agent records into application agent descriptors.
func (s *appConfigStore) AgentDescriptors() []appagents.Descriptor {
	descs, _ := s.resolvedAgentDescriptors()
	return descs
}

func (s *appConfigStore) resolvedAgentDescriptors() ([]appagents.Descriptor, error) {
	if s == nil {
		return nil, nil
	}
	records := s.normalizedAgentRecords()
	out := make([]appagents.Descriptor, 0, len(records))
	for _, rec := range records {
		id := strings.TrimSpace(rec.Name)
		if id == "" {
			continue
		}
		desc, err := appagents.ResolveDescriptor(appagents.Descriptor{
			ID:          id,
			Name:        id,
			Description: strings.TrimSpace(rec.Description),
			Stability:   strings.TrimSpace(rec.Stability),
			Transport:   appagents.TransportACP,
			Command:     strings.TrimSpace(rec.Command),
			Args:        append([]string(nil), rec.Args...),
			Env:         copyStringMap(rec.Env),
			WorkDir:     strings.TrimSpace(rec.WorkDir),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, desc)
	}
	return out, nil
}

func (s *appConfigStore) AgentRegistry() (*appagents.Registry, error) {
	descs, err := s.resolvedAgentDescriptors()
	if err != nil {
		return nil, err
	}
	reg := appagents.NewRegistry(descs...)
	if err := reg.Validate(); err != nil {
		return nil, err
	}
	return reg, nil
}

func (s *appConfigStore) normalizedAgentRecords() []agentRecord {
	if s == nil {
		return nil
	}
	out := make([]agentRecord, 0, len(s.data.Agents))
	keys := make([]string, 0, len(s.data.Agents))
	for key := range s.data.Agents {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		rec := s.data.Agents[key]
		rec.Name = key
		normalizeAgentRecord(&rec)
		out = append(out, rec)
	}
	return out
}

func (s *appConfigStore) UpsertAgent(name string, rec agentRecord) error {
	if s == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("cli config: agent name is required")
	}
	if s.data.Agents == nil {
		s.data.Agents = map[string]agentRecord{}
	}
	rec.Name = name
	normalizeAgentRecord(&rec)
	s.data.Agents[name] = rec
	return s.save()
}

func (s *appConfigStore) DeleteAgent(name string) error {
	if s == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" || len(s.data.Agents) == 0 {
		return nil
	}
	if _, ok := s.data.Agents[name]; !ok {
		return nil
	}
	delete(s.data.Agents, name)
	if len(s.data.Agents) == 0 {
		s.data.Agents = nil
	}
	return s.save()
}

func (s *appConfigStore) CredentialStoreMode() string {
	return defaultCredentialStoreMode
}

func (s *appConfigStore) StreamModel() bool {
	return true
}

func (s *appConfigStore) ThinkingMode() string {
	return defaultThinkingMode
}

func (s *appConfigStore) ThinkingBudget() int {
	return defaultThinkingBudget
}

func (s *appConfigStore) ReasoningEffort() string {
	return defaultReasoningEffort
}

func (s *appConfigStore) ShowReasoning() bool {
	return true
}

func (s *appConfigStore) PermissionMode() string {
	if s == nil {
		return defaultPermissionMode
	}
	return normalizePermissionMode(s.data.PermissionMode)
}

func (s *appConfigStore) SandboxType() string {
	if s == nil {
		return platformDefaultSandboxType()
	}
	return normalizeSandboxType(s.data.SandboxType)
}

func (s *appConfigStore) ProviderConfigs() []modelproviders.Config {
	if s == nil || len(s.data.Providers) == 0 {
		return nil
	}
	out := make([]modelproviders.Config, 0, len(s.data.Providers))
	for _, rec := range s.data.Providers {
		alias := strings.TrimSpace(strings.ToLower(rec.Alias))
		if alias == "" {
			continue
		}
		auth := rec.Auth
		normalizeProviderAuthRecord(rec.Provider, rec.BaseURL, &auth)
		cfg := modelproviders.Config{
			Alias:                     alias,
			Provider:                  strings.TrimSpace(rec.Provider),
			API:                       modelproviders.APIType(strings.TrimSpace(rec.API)),
			Model:                     strings.TrimSpace(rec.Model),
			BaseURL:                   strings.TrimSpace(rec.BaseURL),
			Headers:                   copyHeaders(rec.Headers),
			ContextWindowTokens:       rec.ContextWindowTokens,
			MaxOutputTok:              rec.MaxOutputTok,
			ReasoningLevels:           normalizeReasoningLevels(rec.ReasoningLevels),
			ReasoningMode:             normalizeCatalogReasoningMode(rec.ReasoningMode),
			SupportedReasoningEfforts: normalizeReasoningLevels(rec.SupportedReasoningEfforts),
			DefaultReasoningEffort:    normalizeReasoningEffort(rec.DefaultReasoningEffort),
			ThinkingBudget:            normalizeThinkingBudget(rec.ThinkingBudget),
			ReasoningEffort:           resolvedProviderRecordReasoningEffort(rec),
			OpenRouter:                providerRecordOpenRouterConfig(rec.OpenRouter),
			Auth: modelproviders.AuthConfig{
				Type:          modelproviders.AuthType(strings.TrimSpace(auth.Type)),
				TokenEnv:      "",
				Token:         strings.TrimSpace(auth.Token),
				CredentialRef: normalizeCredentialRef(auth.CredentialRef),
				HeaderKey:     strings.TrimSpace(auth.HeaderKey),
				Prefix:        strings.TrimSpace(auth.Prefix),
			},
		}
		if rec.TimeoutSeconds > 0 {
			cfg.Timeout = time.Duration(rec.TimeoutSeconds) * time.Second
		}
		out = append(out, cfg)
	}
	return out
}

func defaultModelRuntimeSettings() modelRuntimeSettings {
	return modelRuntimeSettings{
		ThinkingBudget:  defaultThinkingBudget,
		ReasoningEffort: defaultReasoningEffort,
	}
}

func (s *appConfigStore) ModelRuntimeSettings(alias string) modelRuntimeSettings {
	settings := defaultModelRuntimeSettings()
	target := strings.ToLower(strings.TrimSpace(alias))
	if target == "" || s == nil {
		return settings
	}
	for _, rec := range s.data.Providers {
		recAlias := strings.ToLower(strings.TrimSpace(rec.Alias))
		if recAlias == "" {
			continue
		}
		recRef := canonicalModelRef(rec.Provider, rec.Model)
		if recAlias != target && recRef != target {
			continue
		}
		settings.ThinkingBudget = normalizeThinkingBudget(rec.ThinkingBudget)
		settings.ReasoningEffort = resolvedProviderRecordReasoningEffort(rec)
		return settings
	}
	return settings
}

func (s *appConfigStore) ConfiguredModelRefs() []string {
	if s == nil || len(s.data.Providers) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(s.data.Providers))
	for _, rec := range s.data.Providers {
		ref := canonicalModelRef(rec.Provider, rec.Model)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

func (s *appConfigStore) ConfiguredModelAliases() []string {
	if s == nil || len(s.data.Providers) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.data.Providers))
	for _, rec := range s.data.Providers {
		alias := strings.ToLower(strings.TrimSpace(rec.Alias))
		if alias == "" {
			continue
		}
		out = append(out, alias)
	}
	sort.Strings(out)
	return out
}

func (s *appConfigStore) ResolveModelAlias(input string) string {
	target := strings.ToLower(strings.TrimSpace(input))
	if target == "" {
		return ""
	}
	if s == nil {
		return target
	}
	matches := make([]string, 0, 2)
	for _, rec := range s.data.Providers {
		alias := strings.ToLower(strings.TrimSpace(rec.Alias))
		if alias == target {
			return alias
		}
		ref := canonicalModelRef(rec.Provider, rec.Model)
		if ref != "" && ref == target {
			matches = append(matches, alias)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return target
}

func (s *appConfigStore) SetDefaultModel(alias string) error {
	if s == nil {
		return nil
	}
	alias = strings.TrimSpace(strings.ToLower(alias))
	if alias == "" {
		return nil
	}
	if s.data.DefaultModel == alias {
		return nil
	}
	s.data.DefaultModel = alias
	return s.save()
}

func (s *appConfigStore) SetCredentialStoreMode(mode string) error {
	_ = s
	_ = mode
	return nil
}

func (s *appConfigStore) SetRuntimeSettings(settings runtimeSettings) error {
	if s == nil {
		return nil
	}
	permissionMode := normalizePermissionMode(settings.PermissionMode)
	sandboxType := normalizeSandboxType(settings.SandboxType)

	changed := false
	if s.data.PermissionMode != permissionMode {
		s.data.PermissionMode = permissionMode
		changed = true
	}
	if s.data.SandboxType != sandboxType {
		s.data.SandboxType = sandboxType
		changed = true
	}
	if !changed {
		return nil
	}
	return s.save()
}

func (s *appConfigStore) SetModelRuntimeSettings(alias string, settings modelRuntimeSettings) error {
	if s == nil {
		return nil
	}
	target := strings.ToLower(strings.TrimSpace(alias))
	if target == "" {
		return nil
	}
	normalized := modelRuntimeSettings{
		ThinkingBudget:  normalizeThinkingBudget(settings.ThinkingBudget),
		ReasoningEffort: normalizeReasoningEffort(settings.ReasoningEffort),
	}

	changed := false
	for i := range s.data.Providers {
		rec := &s.data.Providers[i]
		recAlias := strings.ToLower(strings.TrimSpace(rec.Alias))
		recRef := canonicalModelRef(rec.Provider, rec.Model)
		if recAlias != target && recRef != target {
			continue
		}
		if rec.ThinkingBudget != normalized.ThinkingBudget {
			rec.ThinkingBudget = normalized.ThinkingBudget
			changed = true
		}
		if rec.ThinkingMode != "" {
			rec.ThinkingMode = ""
			changed = true
		}
		if rec.ReasoningEffort != normalized.ReasoningEffort {
			rec.ReasoningEffort = normalized.ReasoningEffort
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.save()
}

func (s *appConfigStore) UpsertProvider(cfg modelproviders.Config) error {
	if s == nil {
		return nil
	}
	alias := strings.TrimSpace(strings.ToLower(cfg.Alias))
	if alias == "" {
		return fmt.Errorf("cli config: provider alias is required")
	}
	record := providerRecord{
		Alias:                     alias,
		Provider:                  strings.TrimSpace(cfg.Provider),
		API:                       string(cfg.API),
		Model:                     strings.TrimSpace(cfg.Model),
		BaseURL:                   strings.TrimSpace(cfg.BaseURL),
		Headers:                   copyHeaders(cfg.Headers),
		ContextWindowTokens:       cfg.ContextWindowTokens,
		MaxOutputTok:              cfg.MaxOutputTok,
		ReasoningLevels:           normalizeReasoningLevels(cfg.ReasoningLevels),
		ReasoningMode:             normalizeCatalogReasoningMode(cfg.ReasoningMode),
		SupportedReasoningEfforts: normalizeReasoningLevels(cfg.SupportedReasoningEfforts),
		DefaultReasoningEffort:    normalizeReasoningEffort(cfg.DefaultReasoningEffort),
		ThinkingBudget:            normalizeThinkingBudget(cfg.ThinkingBudget),
		ReasoningEffort:           resolveProviderConfigReasoningEffort(cfg),
		OpenRouter:                openRouterRecordForConfig(cfg.OpenRouter),
		Auth: authRecord{
			Type:          string(cfg.Auth.Type),
			TokenEnv:      "",
			Token:         strings.TrimSpace(cfg.Auth.Token),
			CredentialRef: normalizeCredentialRef(cfg.Auth.CredentialRef),
			HeaderKey:     strings.TrimSpace(cfg.Auth.HeaderKey),
			Prefix:        strings.TrimSpace(cfg.Auth.Prefix),
		},
	}
	normalizeProviderAuthRecord(record.Provider, record.BaseURL, &record.Auth)
	if cfg.Timeout > 0 {
		record.TimeoutSeconds = int(cfg.Timeout.Seconds())
	}

	found := false
	for i := range s.data.Providers {
		if strings.EqualFold(strings.TrimSpace(s.data.Providers[i].Alias), alias) {
			s.data.Providers[i] = record
			found = true
			break
		}
	}
	if !found {
		s.data.Providers = append(s.data.Providers, record)
	}
	return s.save()
}

func (s *appConfigStore) ResolveOrAllocateModelAlias(provider string, modelName string, baseURL string) string {
	ref := canonicalModelRef(provider, modelName)
	if ref == "" {
		return ""
	}
	if s == nil {
		return ref
	}
	targetEndpoint := normalizedProviderEndpoint(baseURL)
	candidateUsed := false
	for _, rec := range s.data.Providers {
		if canonicalModelRef(rec.Provider, rec.Model) != ref {
			continue
		}
		alias := strings.ToLower(strings.TrimSpace(rec.Alias))
		if alias == "" {
			continue
		}
		if normalizedProviderEndpoint(rec.BaseURL) == targetEndpoint {
			return alias
		}
		if alias == ref {
			candidateUsed = true
		}
	}
	if !candidateUsed {
		return ref
	}
	suffix := endpointAliasSuffix(baseURL)
	alias := ref + "@" + suffix
	if !s.providerAliasExists(alias) {
		return alias
	}
	base := alias
	for i := 2; ; i++ {
		alias = fmt.Sprintf("%s-%d", base, i)
		if !s.providerAliasExists(alias) {
			return alias
		}
	}
}

func (s *appConfigStore) providerAliasExists(alias string) bool {
	target := strings.ToLower(strings.TrimSpace(alias))
	if target == "" || s == nil {
		return false
	}
	for _, rec := range s.data.Providers {
		if strings.ToLower(strings.TrimSpace(rec.Alias)) == target {
			return true
		}
	}
	return false
}

func (s *appConfigStore) RemoveProvider(alias string) (providerRecord, bool, error) {
	target := strings.ToLower(strings.TrimSpace(alias))
	if s == nil || target == "" {
		return providerRecord{}, false, nil
	}
	for i := range s.data.Providers {
		if strings.ToLower(strings.TrimSpace(s.data.Providers[i].Alias)) != target {
			continue
		}
		removed := s.data.Providers[i]
		s.data.Providers = append(s.data.Providers[:i], s.data.Providers[i+1:]...)
		if strings.EqualFold(strings.TrimSpace(s.data.DefaultModel), target) {
			s.data.DefaultModel = ""
			if len(s.data.Providers) > 0 {
				aliases := s.ConfiguredModelAliases()
				if len(aliases) > 0 {
					s.data.DefaultModel = aliases[0]
				}
			}
		}
		return removed, true, s.save()
	}
	return providerRecord{}, false, nil
}

func (s *appConfigStore) CredentialRefInUse(ref string, exceptAlias string) bool {
	key := normalizeCredentialRef(ref)
	skip := strings.ToLower(strings.TrimSpace(exceptAlias))
	if s == nil || key == "" {
		return false
	}
	for _, rec := range s.data.Providers {
		alias := strings.ToLower(strings.TrimSpace(rec.Alias))
		if alias == "" || alias == skip {
			continue
		}
		recRef := normalizeCredentialRef(rec.Auth.CredentialRef)
		if recRef == "" {
			recRef = defaultCredentialRef(rec.Provider, rec.BaseURL)
		}
		if recRef == key {
			return true
		}
	}
	return false
}

func (s *appConfigStore) save() error {
	if s == nil {
		return nil
	}
	mergeAppConfigDefaults(&s.data)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("cli config: create dir: %w", err)
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("cli config: marshal: %w", err)
	}
	raw = append(raw, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("cli config: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("cli config: rename: %w", err)
	}
	return nil
}

func defaultAppConfig() appConfig {
	return appConfig{
		Version:        configVersion,
		DefaultModel:   defaultModel,
		PermissionMode: defaultPermissionMode,
		SandboxType:    platformDefaultSandboxType(),
		Providers:      nil,
	}
}

func mergeAppConfigDefaults(cfg *appConfig) {
	if cfg == nil {
		return
	}
	if cfg.Version <= 0 {
		cfg.Version = configVersion
	}
	cfg.DefaultModel = strings.TrimSpace(strings.ToLower(cfg.DefaultModel))
	cfg.DefaultAgent = strings.TrimSpace(strings.ToLower(cfg.DefaultAgent))
	if cfg.DefaultModel == "fake" {
		cfg.DefaultModel = ""
	}
	cfg.PermissionMode = normalizePermissionMode(cfg.PermissionMode)
	cfg.SandboxType = normalizeSandboxType(cfg.SandboxType)
	if len(cfg.Agents) > 0 {
		keys := make([]string, 0, len(cfg.Agents))
		for key := range cfg.Agents {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rec := cfg.Agents[key]
			rec.Name = key
			normalizeAgentRecord(&rec)
			cfg.Agents[key] = rec
		}
	}
	cfg.Auth = normalizeStringMap(cfg.Auth)
	for i := range cfg.Providers {
		oldAlias := strings.TrimSpace(strings.ToLower(cfg.Providers[i].Alias))
		normalizeLegacyProviderRecord(&cfg.Providers[i])
		cfg.Providers[i].ReasoningLevels = normalizeReasoningLevels(cfg.Providers[i].ReasoningLevels)
		cfg.Providers[i].ReasoningMode = normalizeCatalogReasoningMode(cfg.Providers[i].ReasoningMode)
		cfg.Providers[i].SupportedReasoningEfforts = normalizeReasoningLevels(cfg.Providers[i].SupportedReasoningEfforts)
		cfg.Providers[i].DefaultReasoningEffort = normalizeReasoningEffort(cfg.Providers[i].DefaultReasoningEffort)
		cfg.Providers[i].ThinkingMode = normalizeThinkingMode(cfg.Providers[i].ThinkingMode)
		cfg.Providers[i].ThinkingBudget = normalizeThinkingBudget(cfg.Providers[i].ThinkingBudget)
		cfg.Providers[i].ReasoningEffort = resolvedProviderRecordReasoningEffort(cfg.Providers[i])
		normalizeProviderAuthRecord(cfg.Providers[i].Provider, cfg.Providers[i].BaseURL, &cfg.Providers[i].Auth)
		newAlias := strings.TrimSpace(strings.ToLower(cfg.Providers[i].Alias))
		if oldAlias != "" && newAlias != "" && cfg.DefaultModel == oldAlias {
			cfg.DefaultModel = newAlias
		}
	}
}

func normalizeAgentRecord(rec *agentRecord) {
	if rec == nil {
		return
	}
	rec.Name = strings.TrimSpace(rec.Name)
	rec.Description = strings.TrimSpace(rec.Description)
	rec.Command = strings.TrimSpace(rec.Command)
	rec.WorkDir = strings.TrimSpace(rec.WorkDir)
	rec.Stability = appagents.NormalizeStability(rec.Stability)
	rec.Args = normalizeStringSlice(rec.Args)
	rec.Env = normalizeStringMap(rec.Env)
}

func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		out[trimmedKey] = strings.TrimSpace(values[key])
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func normalizeLegacyProviderRecord(rec *providerRecord) {
	if rec == nil {
		return
	}
	provider := strings.TrimSpace(strings.ToLower(rec.Provider))
	api := strings.TrimSpace(strings.ToLower(rec.API))
	switch provider {
	case "mimo":
		rec.Provider = "xiaomi"
		if api == string(modelproviders.APIOpenAICompatible) || api == "" || api == string(modelproviders.APIMimo) {
			rec.API = string(modelproviders.APIMimo)
		}
		if alias := strings.TrimSpace(strings.ToLower(rec.Alias)); strings.HasPrefix(alias, "mimo/") {
			rec.Alias = "xiaomi/" + strings.TrimPrefix(alias, "mimo/")
		}
	case "volcengine-coding-plan":
		rec.Provider = "volcengine"
		if api == string(modelproviders.APIOpenAICompatible) || api == "" || api == string(modelproviders.APIVolcengineCoding) {
			rec.API = string(modelproviders.APIVolcengineCoding)
		}
		if alias := strings.TrimSpace(strings.ToLower(rec.Alias)); strings.HasPrefix(alias, "volcengine-coding-plan/") {
			rec.Alias = "volcengine/" + strings.TrimPrefix(alias, "volcengine-coding-plan/")
		}
	}
}

func resolvedProviderRecordReasoningEffort(rec providerRecord) string {
	cfg := modelproviders.Config{
		Provider:                  strings.TrimSpace(rec.Provider),
		API:                       modelproviders.APIType(strings.TrimSpace(rec.API)),
		Model:                     strings.TrimSpace(rec.Model),
		ReasoningMode:             normalizeCatalogReasoningMode(rec.ReasoningMode),
		SupportedReasoningEfforts: normalizeReasoningLevels(rec.SupportedReasoningEfforts),
		DefaultReasoningEffort:    normalizeReasoningEffort(rec.DefaultReasoningEffort),
	}
	raw := strings.TrimSpace(rec.ReasoningEffort)
	if raw == "" {
		raw = strings.TrimSpace(rec.ThinkingMode)
	}
	return normalizeStoredReasoningEffort(cfg, raw)
}

func resolveProviderConfigReasoningEffort(cfg modelproviders.Config) string {
	raw := strings.TrimSpace(cfg.ReasoningEffort)
	if raw == "" {
		raw = strings.TrimSpace(cfg.ThinkingMode)
	}
	return normalizeStoredReasoningEffort(cfg, raw)
}

func normalizeStoredReasoningEffort(cfg modelproviders.Config, raw string) string {
	opt, err := resolveModelReasoningOption(cfg, raw)
	if err == nil {
		return normalizeReasoningEffort(opt.ReasoningEffort)
	}
	switch normalizeReasoningSelection(raw) {
	case "", "auto":
		return ""
	case "off":
		return "none"
	case "on":
		profile := reasoningProfileForConfig(cfg)
		if profile.Mode == reasoningModeNone {
			return ""
		}
		return reasoningProfileDefaultEffort(profile)
	default:
		return normalizeReasoningEffort(raw)
	}
}

func normalizeThinkingMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on", "true", "enabled", "enable", "1":
		return "on"
	case "off", "false", "disabled", "disable", "0":
		return "off"
	default:
		return defaultThinkingMode
	}
}

func normalizeThinkingBudget(budget int) int {
	if budget <= 0 {
		return defaultThinkingBudget
	}
	return budget
}

func normalizeReasoningEffort(effort string) string {
	return normalizeCatalogReasoningEffort(effort)
}

func normalizeReasoningLevel(level string) string {
	value := strings.ToLower(strings.TrimSpace(level))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	switch value {
	case "mimimal":
		return "minimal"
	case "very_high", "veryhigh", "x_high":
		return "xhigh"
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return value
	default:
		return ""
	}
}

func normalizeReasoningLevels(levels []string) []string {
	if len(levels) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(levels))
	for _, one := range levels {
		normalized := normalizeReasoningLevel(one)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeProviderAuthRecord(provider string, baseURL string, auth *authRecord) {
	if auth == nil {
		return
	}
	auth.Type = strings.TrimSpace(auth.Type)
	auth.TokenEnv = ""
	auth.Token = strings.TrimSpace(auth.Token)
	auth.CredentialRef = normalizeCredentialRef(auth.CredentialRef)
	auth.HeaderKey = strings.TrimSpace(auth.HeaderKey)
	auth.Prefix = strings.TrimSpace(auth.Prefix)

	// Strategy: prefer credential_ref (credential store). Keep plaintext token
	// for backward compatibility fallback when credential store is missing.
	if auth.CredentialRef != "" {
		return
	}
	if auth.Token != "" {
		return
	}
	auth.CredentialRef = normalizeCredentialRef(defaultCredentialRef(provider, baseURL))
}

func normalizePermissionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "full_control":
		return "full_control"
	default:
		return defaultPermissionMode
	}
}

func normalizeSandboxType(sandboxType string) string {
	value := strings.TrimSpace(strings.ToLower(sandboxType))
	switch value {
	case "", "auto", "default":
		return platformDefaultSandboxType()
	default:
		return value
	}
}

func platformDefaultSandboxType() string {
	if stdruntime.GOOS == "darwin" {
		return "seatbelt"
	}
	if stdruntime.GOOS == "linux" {
		return ""
	}
	return "bwrap"
}

func availableSandboxTypesForPlatform(goos string) []string {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "darwin":
		return []string{"seatbelt"}
	case "linux":
		return []string{"bwrap", "landlock"}
	default:
		return []string{"bwrap"}
	}
}

func availableSandboxTypes() []string {
	return availableSandboxTypesForPlatform(stdruntime.GOOS)
}

func sandboxTypeDisplayLabel(sandboxType string) string {
	raw := strings.TrimSpace(strings.ToLower(sandboxType))
	if raw == "" || raw == "auto" || raw == "default" {
		return autoSandboxTypeDisplayLabel(stdruntime.GOOS)
	}
	value := normalizeSandboxType(raw)
	if sandboxTypeIsExperimental(value) {
		return value + " (experimental)"
	}
	return value
}

func autoSandboxTypeDisplayLabel(goos string) string {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "linux":
		return "auto (bwrap -> landlock)"
	case "darwin":
		return "auto (seatbelt)"
	default:
		return "auto (bwrap)"
	}
}

func sandboxTypeIsExperimental(sandboxType string) bool {
	switch strings.TrimSpace(strings.ToLower(sandboxType)) {
	case "landlock":
		return true
	default:
		return false
	}
}

func configPath(appName string) (string, error) {
	root, err := appDataDir(appName)
	if err != nil {
		return "", err
	}
	name := normalizedAppName(appName)
	return filepath.Join(root, name+configFileSuffix), nil
}

func sessionStoreDir(appName string) (string, error) {
	root, err := appDataDir(appName)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "sessions"), nil
}

func sessionIndexPath(appName string) (string, error) {
	root, err := appDataDir(appName)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "state.db"), nil
}

func historyFilePath(appName, workspaceKey string) (string, error) {
	root, err := appDataDir(appName)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(workspaceKey)
	if key == "" {
		key = "default"
	}
	return filepath.Join(root, "history", key+".history"), nil
}

func appDataDir(appName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cli config: resolve user home: %w", err)
	}
	return filepath.Join(home, "."+normalizedAppName(appName)), nil
}

func normalizedAppName(appName string) string {
	name := sanitizeAppName(appName)
	if name == "" {
		return "caelis"
	}
	return name
}

func sanitizeAppName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return strings.ToLower(strings.Trim(b.String(), "_"))
}

func appNameFromArgs(args []string, fallback string) string {
	name := strings.TrimSpace(fallback)
	for i := 0; i < len(args); i++ {
		token := strings.TrimSpace(args[i])
		if token == "" {
			continue
		}
		if token == "-app" || token == "--app" {
			if i+1 < len(args) {
				return strings.TrimSpace(args[i+1])
			}
			continue
		}
		if strings.HasPrefix(token, "-app=") {
			return strings.TrimSpace(strings.TrimPrefix(token, "-app="))
		}
		if strings.HasPrefix(token, "--app=") {
			return strings.TrimSpace(strings.TrimPrefix(token, "--app="))
		}
	}
	return name
}

func copyHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		kk := strings.TrimSpace(k)
		if kk == "" {
			continue
		}
		out[kk] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func providerRecordOpenRouterConfig(in *openRouterRecord) modelproviders.OpenRouterConfig {
	if in == nil {
		return modelproviders.OpenRouterConfig{}
	}
	return modelproviders.OpenRouterConfig{
		Models:     dedupeStrings(in.Models),
		Route:      strings.TrimSpace(in.Route),
		Provider:   copyAnyMap(in.Provider),
		Transforms: dedupeStrings(in.Transforms),
		Plugins:    copyMapSlice(in.Plugins),
	}
}

func openRouterRecordForConfig(in modelproviders.OpenRouterConfig) *openRouterRecord {
	record := &openRouterRecord{
		Models:     dedupeStrings(in.Models),
		Route:      strings.TrimSpace(in.Route),
		Provider:   copyAnyMap(in.Provider),
		Transforms: dedupeStrings(in.Transforms),
		Plugins:    copyMapSlice(in.Plugins),
	}
	if len(record.Models) == 0 && record.Route == "" && len(record.Provider) == 0 && len(record.Transforms) == 0 && len(record.Plugins) == 0 {
		return nil
	}
	return record
}

func copyAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		kk := strings.TrimSpace(k)
		if kk == "" {
			continue
		}
		out[kk] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func copyMapSlice(in []map[string]any) []map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(in))
	for _, item := range in {
		if copied := copyAnyMap(item); len(copied) > 0 {
			out = append(out, copied)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, one := range values {
		value := strings.TrimSpace(one)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func canonicalModelRef(provider, modelName string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if provider == "" || modelName == "" {
		return ""
	}
	if strings.HasPrefix(modelName, provider+"/") {
		return modelName
	}
	return provider + "/" + modelName
}

func normalizedProviderEndpoint(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return strings.TrimRight(strings.ToLower(value), "/")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	normalized := strings.TrimRight(parsed.String(), "/")
	return normalized
}

func endpointAliasSuffix(baseURL string) string {
	normalized := normalizedProviderEndpoint(baseURL)
	if normalized == "" {
		return "alt"
	}
	parsed, err := url.Parse(normalized)
	value := ""
	if err == nil {
		value = normalizeCredentialRef(parsed.Host)
		pathPart := normalizeCredentialRef(parsed.Path)
		if pathPart != "" {
			if value != "" {
				value += "_"
			}
			value += pathPart
		}
	}
	if value == "" {
		value = normalizeCredentialRef(normalized)
	}
	if len(value) > 48 {
		sum := sha1.Sum([]byte(normalized))
		value = value[:36] + "_" + fmt.Sprintf("%x", sum[:4])
	}
	return value
}
