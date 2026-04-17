package gatewayapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	ReasoningEffort        string
	DefaultReasoningEffort string
	MaxOutputTok           int
	Timeout                time.Duration
}

type Stack struct {
	Gateway   *appgateway.Gateway
	Sessions  sdksession.Service
	AppName   string
	UserID    string
	Workspace sdksession.WorkspaceRef
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
	})
}

type modelLookup struct {
	cfg           ModelConfig
	contextWindow int
	defaultAlias  string
	factory       *sdkproviders.Factory
}

func newModelLookup(cfg ModelConfig, contextWindow int) (*modelLookup, error) {
	cfg = normalizeModelConfig(cfg)
	if cfg.Provider == "minimax" {
		return &modelLookup{
			cfg:           cfg,
			contextWindow: contextWindow,
			defaultAlias:  cfg.Alias,
		}, nil
	}
	factory := sdkproviders.NewFactory()
	record := sdkproviders.Config{
		Alias:                  cfg.Alias,
		Provider:               cfg.Provider,
		API:                    cfg.API,
		Model:                  cfg.Model,
		BaseURL:                cfg.BaseURL,
		Timeout:                cfg.Timeout,
		MaxOutputTok:           cfg.MaxOutputTok,
		ContextWindowTokens:    contextWindow,
		ReasoningEffort:        cfg.ReasoningEffort,
		DefaultReasoningEffort: cfg.DefaultReasoningEffort,
		Auth: sdkproviders.AuthConfig{
			Type:      cfg.AuthType,
			Token:     cfg.Token,
			TokenEnv:  cfg.TokenEnv,
			HeaderKey: cfg.HeaderKey,
		},
	}
	if err := factory.Register(record); err != nil {
		return nil, err
	}
	return &modelLookup{
		cfg:           cfg,
		contextWindow: contextWindow,
		defaultAlias:  cfg.Alias,
		factory:       factory,
	}, nil
}

func (l *modelLookup) DefaultAlias() string {
	if l == nil {
		return ""
	}
	return l.defaultAlias
}

func (l *modelLookup) ResolveModel(_ context.Context, alias string, contextWindow int) (appgateway.ModelResolution, error) {
	if l == nil {
		return appgateway.ModelResolution{}, fmt.Errorf("gatewayapp: model lookup is nil")
	}
	alias = firstNonEmpty(strings.TrimSpace(alias), l.defaultAlias)
	if l.cfg.Provider == "minimax" {
		if alias != l.defaultAlias {
			return appgateway.ModelResolution{}, fmt.Errorf("gatewayapp: unknown model alias %q", alias)
		}
		return appgateway.ModelResolution{
			Model: sdkminimax.New(sdkminimax.Config{
				Model:           l.cfg.Model,
				BaseURL:         l.cfg.BaseURL,
				APIKey:          l.cfg.Token,
				HeaderKey:       l.cfg.HeaderKey,
				Timeout:         l.cfg.Timeout,
				MaxTokens:       l.cfg.MaxOutputTok,
				ReasoningEffort: l.cfg.ReasoningEffort,
			}),
			ReasoningEffort:        l.cfg.ReasoningEffort,
			DefaultReasoningEffort: l.cfg.DefaultReasoningEffort,
		}, nil
	}
	if contextWindow > 0 && contextWindow != l.contextWindow {
		next, err := newModelLookup(l.cfg, contextWindow)
		if err != nil {
			return appgateway.ModelResolution{}, err
		}
		return next.ResolveModel(context.Background(), alias, contextWindow)
	}
	llm, err := l.factory.NewByAlias(alias)
	if err != nil {
		return appgateway.ModelResolution{}, err
	}
	return appgateway.ModelResolution{
		Model:                  llm,
		ReasoningEffort:        l.cfg.ReasoningEffort,
		DefaultReasoningEffort: l.cfg.DefaultReasoningEffort,
	}, nil
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
