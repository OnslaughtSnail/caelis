package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"sort"
	"strings"
	"time"
	"unicode"

	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

const (
	configVersion    = 1
	defaultModel     = ""
	configFileSuffix = "_config.json"
	defaultStream    = false

	defaultThinkingMode    = "auto"
	defaultThinkingBudget  = 1024
	defaultReasoningEffort = ""
	defaultShowReasoning   = true

	defaultPermissionMode = "default"
)

type appConfig struct {
	Version             int              `json:"version"`
	DefaultModel        string           `json:"default_model"`
	CredentialStoreMode string           `json:"credential_store_mode,omitempty"`
	StreamModel         *bool            `json:"stream_model,omitempty"`
	ThinkingMode        string           `json:"thinking_mode,omitempty"`
	ThinkingBudget      *int             `json:"thinking_budget,omitempty"`
	ReasoningEffort     string           `json:"reasoning_effort,omitempty"`
	ShowReasoning       *bool            `json:"show_reasoning,omitempty"`
	PermissionMode      string           `json:"permission_mode,omitempty"`
	SandboxType         string           `json:"sandbox_type,omitempty"`
	Providers           []providerRecord `json:"providers,omitempty"`
}

type runtimeSettings struct {
	StreamModel     bool
	ThinkingMode    string
	ThinkingBudget  int
	ReasoningEffort string
	ShowReasoning   bool
	PermissionMode  string
	SandboxType     string
}

type providerRecord struct {
	Alias               string            `json:"alias"`
	Provider            string            `json:"provider"`
	API                 string            `json:"api"`
	Model               string            `json:"model"`
	BaseURL             string            `json:"base_url"`
	Headers             map[string]string `json:"headers,omitempty"`
	TimeoutSeconds      int               `json:"timeout_seconds,omitempty"`
	MaxOutputTok        int               `json:"max_output_tokens,omitempty"`
	ContextWindowTokens int               `json:"context_window_tokens,omitempty"`
	Auth                authRecord        `json:"auth"`
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

	var loaded appConfig
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return nil, fmt.Errorf("cli config: parse %q: %w", path, err)
	}
	mergeAppConfigDefaults(&loaded)
	store.data = loaded
	return store, nil
}

func (s *appConfigStore) DefaultModel() string {
	if s == nil {
		return defaultModel
	}
	value := strings.TrimSpace(s.data.DefaultModel)
	return strings.ToLower(value)
}

func (s *appConfigStore) CredentialStoreMode() string {
	if s == nil {
		return defaultCredentialStoreMode
	}
	return normalizeCredentialStoreMode(s.data.CredentialStoreMode)
}

func (s *appConfigStore) StreamModel() bool {
	if s == nil || s.data.StreamModel == nil {
		return defaultStream
	}
	return *s.data.StreamModel
}

func (s *appConfigStore) ThinkingMode() string {
	if s == nil {
		return defaultThinkingMode
	}
	return normalizeThinkingMode(s.data.ThinkingMode)
}

func (s *appConfigStore) ThinkingBudget() int {
	if s == nil || s.data.ThinkingBudget == nil {
		return defaultThinkingBudget
	}
	value := *s.data.ThinkingBudget
	if value <= 0 {
		return defaultThinkingBudget
	}
	return value
}

func (s *appConfigStore) ReasoningEffort() string {
	if s == nil {
		return defaultReasoningEffort
	}
	return normalizeReasoningEffort(s.data.ReasoningEffort)
}

func (s *appConfigStore) ShowReasoning() bool {
	if s == nil || s.data.ShowReasoning == nil {
		return defaultShowReasoning
	}
	return *s.data.ShowReasoning
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
		cfg := modelproviders.Config{
			Alias:               alias,
			Provider:            strings.TrimSpace(rec.Provider),
			API:                 modelproviders.APIType(strings.TrimSpace(rec.API)),
			Model:               strings.TrimSpace(rec.Model),
			BaseURL:             strings.TrimSpace(rec.BaseURL),
			Headers:             copyHeaders(rec.Headers),
			ContextWindowTokens: rec.ContextWindowTokens,
			MaxOutputTok:        rec.MaxOutputTok,
			Auth: modelproviders.AuthConfig{
				Type:          modelproviders.AuthType(strings.TrimSpace(rec.Auth.Type)),
				TokenEnv:      strings.TrimSpace(rec.Auth.TokenEnv),
				Token:         strings.TrimSpace(rec.Auth.Token),
				CredentialRef: strings.TrimSpace(rec.Auth.CredentialRef),
				HeaderKey:     strings.TrimSpace(rec.Auth.HeaderKey),
				Prefix:        strings.TrimSpace(rec.Auth.Prefix),
			},
		}
		if rec.TimeoutSeconds > 0 {
			cfg.Timeout = time.Duration(rec.TimeoutSeconds) * time.Second
		}
		out = append(out, cfg)
	}
	return out
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

func (s *appConfigStore) ResolveModelAlias(input string) string {
	target := strings.ToLower(strings.TrimSpace(input))
	if target == "" {
		return ""
	}
	if s == nil {
		return target
	}
	for _, rec := range s.data.Providers {
		alias := strings.ToLower(strings.TrimSpace(rec.Alias))
		if alias == target {
			return alias
		}
		ref := canonicalModelRef(rec.Provider, rec.Model)
		if ref != "" && ref == target {
			return alias
		}
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
	if s == nil {
		return nil
	}
	mode = normalizeCredentialStoreMode(mode)
	if s.data.CredentialStoreMode == mode {
		return nil
	}
	s.data.CredentialStoreMode = mode
	return s.save()
}

func (s *appConfigStore) SetRuntimeSettings(settings runtimeSettings) error {
	if s == nil {
		return nil
	}
	thinkingMode := normalizeThinkingMode(settings.ThinkingMode)
	thinkingBudget := settings.ThinkingBudget
	if thinkingBudget <= 0 {
		thinkingBudget = defaultThinkingBudget
	}
	reasoningEffort := normalizeReasoningEffort(settings.ReasoningEffort)
	permissionMode := normalizePermissionMode(settings.PermissionMode)
	sandboxType := normalizeSandboxType(settings.SandboxType)

	changed := false
	if s.data.StreamModel == nil || *s.data.StreamModel != settings.StreamModel {
		v := settings.StreamModel
		s.data.StreamModel = &v
		changed = true
	}
	if s.data.ThinkingMode != thinkingMode {
		s.data.ThinkingMode = thinkingMode
		changed = true
	}
	if s.data.ThinkingBudget == nil || *s.data.ThinkingBudget != thinkingBudget {
		v := thinkingBudget
		s.data.ThinkingBudget = &v
		changed = true
	}
	if s.data.ReasoningEffort != reasoningEffort {
		s.data.ReasoningEffort = reasoningEffort
		changed = true
	}
	if s.data.ShowReasoning == nil || *s.data.ShowReasoning != settings.ShowReasoning {
		v := settings.ShowReasoning
		s.data.ShowReasoning = &v
		changed = true
	}
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

func (s *appConfigStore) UpsertProvider(cfg modelproviders.Config) error {
	if s == nil {
		return nil
	}
	alias := strings.TrimSpace(strings.ToLower(cfg.Alias))
	if alias == "" {
		return fmt.Errorf("cli config: provider alias is required")
	}
	record := providerRecord{
		Alias:               alias,
		Provider:            strings.TrimSpace(cfg.Provider),
		API:                 string(cfg.API),
		Model:               strings.TrimSpace(cfg.Model),
		BaseURL:             strings.TrimSpace(cfg.BaseURL),
		Headers:             copyHeaders(cfg.Headers),
		ContextWindowTokens: cfg.ContextWindowTokens,
		MaxOutputTok:        cfg.MaxOutputTok,
		Auth: authRecord{
			Type:          string(cfg.Auth.Type),
			TokenEnv:      strings.TrimSpace(cfg.Auth.TokenEnv),
			Token:         strings.TrimSpace(cfg.Auth.Token),
			CredentialRef: strings.TrimSpace(cfg.Auth.CredentialRef),
			HeaderKey:     strings.TrimSpace(cfg.Auth.HeaderKey),
			Prefix:        strings.TrimSpace(cfg.Auth.Prefix),
		},
	}
	if record.Auth.CredentialRef == "" {
		record.Auth.CredentialRef = normalizeCredentialRef(defaultCredentialRef(record.Provider, record.BaseURL))
	}
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
	streamModel := defaultStream
	thinkingBudget := defaultThinkingBudget
	showReasoning := defaultShowReasoning
	return appConfig{
		Version:             configVersion,
		DefaultModel:        defaultModel,
		CredentialStoreMode: defaultCredentialStoreMode,
		StreamModel:         &streamModel,
		ThinkingMode:        defaultThinkingMode,
		ThinkingBudget:      &thinkingBudget,
		ReasoningEffort:     defaultReasoningEffort,
		ShowReasoning:       &showReasoning,
		PermissionMode:      defaultPermissionMode,
		SandboxType:         platformDefaultSandboxType(),
		Providers:           nil,
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
	if cfg.DefaultModel == "fake" {
		cfg.DefaultModel = ""
	}
	cfg.CredentialStoreMode = normalizeCredentialStoreMode(cfg.CredentialStoreMode)
	if cfg.StreamModel == nil {
		v := defaultStream
		cfg.StreamModel = &v
	}
	cfg.ThinkingMode = normalizeThinkingMode(cfg.ThinkingMode)
	if cfg.ThinkingBudget == nil || *cfg.ThinkingBudget <= 0 {
		v := defaultThinkingBudget
		cfg.ThinkingBudget = &v
	}
	cfg.ReasoningEffort = normalizeReasoningEffort(cfg.ReasoningEffort)
	if cfg.ShowReasoning == nil {
		v := defaultShowReasoning
		cfg.ShowReasoning = &v
	}
	cfg.PermissionMode = normalizePermissionMode(cfg.PermissionMode)
	cfg.SandboxType = normalizeSandboxType(cfg.SandboxType)
}

func normalizeThinkingMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "on":
		return "on"
	case "off":
		return "off"
	default:
		return defaultThinkingMode
	}
}

func normalizeReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	default:
		return defaultReasoningEffort
	}
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
	if value == "" {
		return platformDefaultSandboxType()
	}
	return value
}

func platformDefaultSandboxType() string {
	if stdruntime.GOOS == "darwin" {
		return "seatbelt"
	}
	return "docker"
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
	storeDir, err := sessionStoreDir(appName)
	if err != nil {
		return "", err
	}
	return filepath.Join(storeDir, "session_index.db"), nil
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

func canonicalModelRef(provider, modelName string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	if provider == "" || modelName == "" {
		return ""
	}
	return provider + "/" + modelName
}
