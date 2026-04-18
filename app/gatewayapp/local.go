package gatewayapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	sdkproviders "github.com/OnslaughtSnail/caelis/sdk/model/providers"
	sdkminimax "github.com/OnslaughtSnail/caelis/sdk/model/providers/minimax"
	sdkplugin "github.com/OnslaughtSnail/caelis/sdk/plugin"
	sdkpolicy "github.com/OnslaughtSnail/caelis/sdk/policy/presets"
	"github.com/OnslaughtSnail/caelis/sdk/runtime/agents/chat"
	localruntime "github.com/OnslaughtSnail/caelis/sdk/runtime/local"
	"github.com/OnslaughtSnail/caelis/sdk/sandbox/host"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sessionfile "github.com/OnslaughtSnail/caelis/sdk/session/file"
	sdkbuiltin "github.com/OnslaughtSnail/caelis/sdk/tool/builtin"
)

type Config struct {
	AppName        string
	UserID         string
	StoreDir       string
	WorkspaceKey   string
	WorkspaceCWD   string
	PermissionMode string
	ContextWindow  int
	SystemPrompt   string
	Assembly       sdkplugin.ResolvedAssembly
	Model          ModelConfig
}

type ModelConfig struct {
	Alias                  string
	Provider               string
	API                    sdkproviders.APIType
	Model                  string
	BaseURL                string
	Token                  string
	TokenEnv               string
	AuthType               sdkproviders.AuthType
	HeaderKey              string
	ContextWindowTokens    int
	ReasoningEffort        string
	DefaultReasoningEffort string
	ReasoningLevels        []string
	ReasoningMode          string
	MaxOutputTok           int
	Timeout                time.Duration
}

type Stack struct {
	Gateway   *appgateway.Gateway
	Sessions  sdksession.Service
	AppName   string
	UserID    string
	Workspace sdksession.WorkspaceRef
	lookup    *modelLookup
}

type SessionRuntimeState struct {
	ModelAlias  string
	SandboxMode string
}

func NewLocalStack(cfg Config) (*Stack, error) {
	appName := firstNonEmpty(strings.TrimSpace(cfg.AppName), "caelis")
	userID := firstNonEmpty(strings.TrimSpace(cfg.UserID), "local-user")
	workspaceCWD := firstNonEmpty(strings.TrimSpace(cfg.WorkspaceCWD), mustGetwd())
	workspaceKey := firstNonEmpty(strings.TrimSpace(cfg.WorkspaceKey), "workspace")
	storeDir := strings.TrimSpace(cfg.StoreDir)
	if storeDir == "" {
		storeDir = filepath.Join(workspaceCWD, ".caelis")
	}

	sandboxRuntime, err := host.New(host.Config{CWD: workspaceCWD})
	if err != nil {
		return nil, err
	}
	tools, err := sdkbuiltin.BuildCoreTools(sdkbuiltin.CoreToolsConfig{Runtime: sandboxRuntime})
	if err != nil {
		return nil, err
	}
	sessions := sessionfile.NewService(sessionfile.NewStore(sessionfile.Config{
		RootDir: filepath.Join(storeDir, "sessions"),
	}))
	rt, err := localruntime.New(localruntime.Config{
		Sessions:          sessions,
		AgentFactory:      chat.Factory{},
		DefaultPolicyMode: policyMode(cfg.PermissionMode),
		Assembly:          cfg.Assembly,
	})
	if err != nil {
		return nil, err
	}
	lookup, err := newModelLookup(cfg.Model, cfg.ContextWindow)
	if err != nil {
		return nil, err
	}
	baseMetadata := map[string]any{}
	if prompt := strings.TrimSpace(cfg.SystemPrompt); prompt != "" {
		baseMetadata["system_prompt"] = prompt
	}
	if reasoning := strings.TrimSpace(cfg.Model.ReasoningEffort); reasoning != "" {
		baseMetadata["reasoning_effort"] = reasoning
	}
	resolver, err := appgateway.NewAssemblyResolver(appgateway.AssemblyResolverConfig{
		Sessions:          sessions,
		Assembly:          cfg.Assembly,
		DefaultModelAlias: lookup.DefaultAlias(),
		ContextWindow:     cfg.ContextWindow,
		ModelLookup:       lookup,
		Tools:             tools,
		BaseMetadata:      baseMetadata,
	})
	if err != nil {
		return nil, err
	}
	gw, err := appgateway.New(appgateway.Config{
		Sessions: sessions,
		Runtime:  rt,
		Resolver: resolver,
	})
	if err != nil {
		return nil, err
	}
	return &Stack{
		Gateway:  gw,
		Sessions: sessions,
		AppName:  appName,
		UserID:   userID,
		Workspace: sdksession.WorkspaceRef{
			Key: workspaceKey,
			CWD: workspaceCWD,
		},
		lookup: lookup,
	}, nil
}

func (s *Stack) StartSession(ctx context.Context, preferredSessionID string, bindingKey string) (sdksession.Session, error) {
	if s == nil || s.Gateway == nil {
		return sdksession.Session{}, fmt.Errorf("gatewayapp: stack is unavailable")
	}
	return s.Gateway.StartSession(ctx, appgateway.StartSessionRequest{
		AppName:            s.AppName,
		UserID:             s.UserID,
		Workspace:          s.Workspace,
		PreferredSessionID: strings.TrimSpace(preferredSessionID),
		BindingKey:         strings.TrimSpace(bindingKey),
		Binding: appgateway.BindingDescriptor{
			Surface: strings.TrimSpace(bindingKey),
			Owner:   s.AppName,
		},
	})
}

// Connect reconfigures the model provider on the live stack. The new config
// takes effect for subsequent turns.
func (s *Stack) Connect(cfg ModelConfig) (string, error) {
	if s == nil || s.Gateway == nil {
		return "", fmt.Errorf("gatewayapp: stack is unavailable")
	}
	if s.lookup == nil {
		return "", fmt.Errorf("gatewayapp: model lookup unavailable")
	}
	resolver := s.Gateway.Resolver()
	if resolver == nil {
		return "", fmt.Errorf("gatewayapp: resolver not available")
	}
	alias, err := s.lookup.Upsert(cfg)
	if err != nil {
		return "", fmt.Errorf("gatewayapp: invalid model config: %w", err)
	}
	resolver.SetModelLookup(s.lookup, s.lookup.DefaultAlias())
	return alias, nil
}

// UseModel persists one per-session model alias override for subsequent turns.
func (s *Stack) UseModel(ctx context.Context, ref sdksession.SessionRef, alias string) error {
	if s == nil || s.Sessions == nil {
		return fmt.Errorf("gatewayapp: sessions service unavailable")
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("gatewayapp: model alias is required")
	}
	if s.lookup != nil && !s.lookup.HasAlias(alias) {
		return fmt.Errorf("gatewayapp: unknown model alias %q", alias)
	}
	if s.lookup != nil {
		s.lookup.SetDefault(alias)
		if resolver := s.Gateway.Resolver(); resolver != nil {
			resolver.SetModelLookup(s.lookup, s.lookup.DefaultAlias())
		}
	}
	return s.Sessions.UpdateState(ctx, ref, func(state map[string]any) (map[string]any, error) {
		next := sdksession.CloneState(state)
		if next == nil {
			next = map[string]any{}
		}
		next[appgateway.StateCurrentModelAlias] = alias
		return next, nil
	})
}

// DeleteModel clears one per-session model alias override when it matches the
// supplied alias. This reverts the session back to the resolver default.
func (s *Stack) DeleteModel(ctx context.Context, ref sdksession.SessionRef, alias string) error {
	if s == nil || s.Sessions == nil {
		return fmt.Errorf("gatewayapp: sessions service unavailable")
	}
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("gatewayapp: model alias is required")
	}
	if s.lookup == nil {
		return fmt.Errorf("gatewayapp: model lookup unavailable")
	}
	if err := s.lookup.Delete(alias); err != nil {
		return err
	}
	if resolver := s.Gateway.Resolver(); resolver != nil {
		resolver.SetModelLookup(s.lookup, s.lookup.DefaultAlias())
	}
	return s.Sessions.UpdateState(ctx, ref, func(state map[string]any) (map[string]any, error) {
		next := sdksession.CloneState(state)
		if next == nil {
			next = map[string]any{}
		}
		current, _ := next[appgateway.StateCurrentModelAlias].(string)
		if alias == "" || strings.EqualFold(strings.TrimSpace(current), alias) {
			delete(next, appgateway.StateCurrentModelAlias)
		}
		return next, nil
	})
}

// SetSandboxMode persists one per-session sandbox mode override for
// subsequent turns and returns the normalized display label.
func (s *Stack) SetSandboxMode(ctx context.Context, ref sdksession.SessionRef, mode string) (string, error) {
	if s == nil || s.Sessions == nil {
		return "", fmt.Errorf("gatewayapp: sessions service unavailable")
	}
	normalized, err := normalizeSandboxMode(mode)
	if err != nil {
		return "", err
	}
	err = s.Sessions.UpdateState(ctx, ref, func(state map[string]any) (map[string]any, error) {
		next := sdksession.CloneState(state)
		if next == nil {
			next = map[string]any{}
		}
		next[appgateway.StateCurrentSandboxMode] = normalized
		return next, nil
	})
	if err != nil {
		return "", err
	}
	return normalized, nil
}

// SessionRuntimeState returns the current per-session runtime overrides backed
// by session state.
func (s *Stack) SessionRuntimeState(ctx context.Context, ref sdksession.SessionRef) (SessionRuntimeState, error) {
	if s == nil || s.Sessions == nil {
		return SessionRuntimeState{}, fmt.Errorf("gatewayapp: sessions service unavailable")
	}
	state, err := s.Sessions.SnapshotState(ctx, ref)
	if err != nil {
		return SessionRuntimeState{}, err
	}
	return SessionRuntimeState{
		ModelAlias:  appgateway.CurrentModelAlias(state),
		SandboxMode: appgateway.CurrentSandboxMode(state),
	}, nil
}

// ListModelAliases returns the current session override plus resolver-known
// model aliases for picker surfaces such as the TUI `/model` command.
func (s *Stack) ListModelAliases(ctx context.Context, ref sdksession.SessionRef) ([]string, error) {
	if s == nil || s.Gateway == nil {
		return nil, fmt.Errorf("gatewayapp: stack is unavailable")
	}
	resolver := s.Gateway.Resolver()
	if resolver == nil {
		return nil, fmt.Errorf("gatewayapp: resolver not available")
	}
	return resolver.ListModelAliases(ctx, ref)
}

func (s *Stack) DefaultModelAlias() string {
	if s == nil || s.lookup == nil {
		return ""
	}
	return s.lookup.DefaultAlias()
}

// CompactSession appends a compaction event to the given session. The note is
// stored as the compact summary text.
func (s *Stack) CompactSession(ctx context.Context, ref sdksession.SessionRef, note string) error {
	if s == nil || s.Sessions == nil {
		return fmt.Errorf("gatewayapp: sessions service unavailable")
	}
	note = strings.TrimSpace(note)
	if note == "" {
		note = "manual compaction"
	}
	compactEvent := &sdksession.Event{
		Type:       sdksession.EventTypeCompact,
		Visibility: sdksession.VisibilityCanonical,
		Time:       time.Now(),
		Text:       note,
		Meta: map[string]any{
			"trigger": "manual",
		},
	}
	_, err := s.Sessions.AppendEvent(ctx, sdksession.AppendEventRequest{
		SessionRef: ref,
		Event:      compactEvent,
	})
	return err
}

type modelLookup struct {
	mu            sync.RWMutex
	configs       map[string]ModelConfig
	contextWindow int
	defaultAlias  string
}

func newModelLookup(cfg ModelConfig, contextWindow int) (*modelLookup, error) {
	cfg = normalizeModelConfig(cfg)
	lookup := &modelLookup{
		configs:       map[string]ModelConfig{},
		contextWindow: contextWindow,
		defaultAlias:  cfg.Alias,
	}
	if _, err := lookup.Upsert(cfg); err != nil {
		return nil, err
	}
	return lookup, nil
}

func (l *modelLookup) DefaultAlias() string {
	if l == nil {
		return ""
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.defaultAlias
}

func (l *modelLookup) ListModelAliases() []string {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	aliases := make([]string, 0, len(l.configs)+1)
	if l.defaultAlias != "" {
		aliases = append(aliases, l.defaultAlias)
	}
	rest := make([]string, 0, len(l.configs))
	for alias := range l.configs {
		if !strings.EqualFold(alias, l.defaultAlias) {
			rest = append(rest, alias)
		}
	}
	sort.Strings(rest)
	aliases = append(aliases, rest...)
	return dedupeNonEmptyStrings(aliases)
}

func (l *modelLookup) ResolveModel(ctx context.Context, alias string, contextWindow int) (appgateway.ModelResolution, error) {
	if l == nil {
		return appgateway.ModelResolution{}, fmt.Errorf("gatewayapp: model lookup is nil")
	}
	l.mu.RLock()
	alias = firstNonEmpty(strings.TrimSpace(alias), l.defaultAlias)
	cfg, ok := l.configs[strings.ToLower(alias)]
	fallbackContextWindow := l.contextWindow
	l.mu.RUnlock()
	if !ok {
		return appgateway.ModelResolution{}, fmt.Errorf("gatewayapp: unknown model alias %q", alias)
	}
	if cfg.Provider == "minimax" {
		if alias != l.defaultAlias {
			// MiniMax aliases are still resolved through one concrete config.
		}
		return appgateway.ModelResolution{
			Model: sdkminimax.New(sdkminimax.Config{
				Model:           cfg.Model,
				BaseURL:         cfg.BaseURL,
				APIKey:          cfg.Token,
				HeaderKey:       cfg.HeaderKey,
				Timeout:         cfg.Timeout,
				MaxTokens:       cfg.MaxOutputTok,
				ReasoningEffort: cfg.ReasoningEffort,
			}),
			ReasoningEffort:        cfg.ReasoningEffort,
			DefaultReasoningEffort: cfg.DefaultReasoningEffort,
		}, nil
	}
	effectiveContextWindow := fallbackContextWindow
	if cfg.ContextWindowTokens > 0 {
		effectiveContextWindow = cfg.ContextWindowTokens
	}
	if contextWindow > 0 {
		effectiveContextWindow = contextWindow
	}
	factory := sdkproviders.NewFactory()
	record := sdkproviders.Config{
		Alias:                     cfg.Alias,
		Provider:                  cfg.Provider,
		API:                       cfg.API,
		Model:                     cfg.Model,
		BaseURL:                   cfg.BaseURL,
		Timeout:                   cfg.Timeout,
		MaxOutputTok:              cfg.MaxOutputTok,
		ContextWindowTokens:       effectiveContextWindow,
		ReasoningLevels:           append([]string(nil), cfg.ReasoningLevels...),
		ReasoningMode:             cfg.ReasoningMode,
		DefaultReasoningEffort:    cfg.DefaultReasoningEffort,
		ReasoningEffort:           cfg.ReasoningEffort,
		SupportedReasoningEfforts: append([]string(nil), cfg.ReasoningLevels...),
		Auth: sdkproviders.AuthConfig{
			Type:      cfg.AuthType,
			Token:     cfg.Token,
			TokenEnv:  cfg.TokenEnv,
			HeaderKey: cfg.HeaderKey,
		},
	}
	if err := factory.Register(record); err != nil {
		return appgateway.ModelResolution{}, err
	}
	llm, err := factory.NewByAlias(alias)
	if err != nil {
		return appgateway.ModelResolution{}, err
	}
	return appgateway.ModelResolution{
		Model:                  llm,
		ReasoningEffort:        cfg.ReasoningEffort,
		DefaultReasoningEffort: cfg.DefaultReasoningEffort,
	}, nil
}

func (l *modelLookup) HasAlias(alias string) bool {
	if l == nil {
		return false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, ok := l.configs[strings.ToLower(strings.TrimSpace(alias))]
	return ok
}

func (l *modelLookup) Upsert(cfg ModelConfig) (string, error) {
	if l == nil {
		return "", fmt.Errorf("gatewayapp: model lookup is nil")
	}
	cfg = normalizeModelConfig(cfg)
	if cfg.Provider == "" || cfg.Model == "" {
		return "", fmt.Errorf("gatewayapp: provider and model are required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.configs == nil {
		l.configs = map[string]ModelConfig{}
	}
	l.configs[strings.ToLower(cfg.Alias)] = cfg
	l.defaultAlias = cfg.Alias
	if cfg.ContextWindowTokens > 0 {
		l.contextWindow = cfg.ContextWindowTokens
	}
	return cfg.Alias, nil
}

func (l *modelLookup) Delete(alias string) error {
	if l == nil {
		return fmt.Errorf("gatewayapp: model lookup is nil")
	}
	key := strings.ToLower(strings.TrimSpace(alias))
	if key == "" {
		return fmt.Errorf("gatewayapp: model alias is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.configs[key]; !ok {
		return fmt.Errorf("gatewayapp: unknown model alias %q", alias)
	}
	delete(l.configs, key)
	if strings.EqualFold(l.defaultAlias, alias) {
		l.defaultAlias = ""
		aliases := make([]string, 0, len(l.configs))
		for one := range l.configs {
			aliases = append(aliases, one)
		}
		sort.Strings(aliases)
		if len(aliases) > 0 {
			l.defaultAlias = aliases[0]
		}
	}
	return nil
}

func (l *modelLookup) SetDefault(alias string) {
	if l == nil {
		return
	}
	key := strings.ToLower(strings.TrimSpace(alias))
	if key == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if cfg, ok := l.configs[key]; ok {
		l.defaultAlias = cfg.Alias
	}
}

func normalizeModelConfig(cfg ModelConfig) ModelConfig {
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.Alias = strings.ToLower(strings.TrimSpace(cfg.Alias))
	if cfg.Alias == "" {
		cfg.Alias = buildAlias(cfg.Provider, cfg.Model)
	}
	if cfg.AuthType == "" {
		if cfg.Provider == "ollama" {
			cfg.AuthType = sdkproviders.AuthNone
		} else {
			cfg.AuthType = sdkproviders.AuthAPIKey
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.MaxOutputTok <= 0 {
		cfg.MaxOutputTok = 4096
	}
	if cfg.ContextWindowTokens < 0 {
		cfg.ContextWindowTokens = 0
	}
	cfg.ReasoningLevels = dedupeNonEmptyStrings(cfg.ReasoningLevels)
	if cfg.Token == "" && strings.TrimSpace(cfg.TokenEnv) != "" {
		cfg.Token = strings.TrimSpace(os.Getenv(strings.TrimSpace(cfg.TokenEnv)))
	}
	return cfg
}

func buildAlias(provider string, modelName string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelName = strings.TrimSpace(modelName)
	if provider == "" {
		return strings.ToLower(modelName)
	}
	if modelName == "" {
		return provider
	}
	return strings.ToLower(provider + "/" + modelName)
}

func normalizeSandboxMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "auto":
		return "auto", nil
	case "default":
		return "default", nil
	case "full_control", "full_access":
		return "full_control", nil
	default:
		return "", fmt.Errorf("gatewayapp: unknown sandbox mode %q", mode)
	}
}

func dedupeNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func policyMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "full_control") {
		return sdkpolicy.ModeFullAccess
	}
	return sdkpolicy.ModeDefault
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func mustGetwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
