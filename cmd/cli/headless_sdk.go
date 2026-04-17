package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	kernelmodel "github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdkproviders "github.com/OnslaughtSnail/caelis/sdk/model/providers"
	sdkplugin "github.com/OnslaughtSnail/caelis/sdk/plugin"
	sdkpolicy "github.com/OnslaughtSnail/caelis/sdk/policy/presets"
	"github.com/OnslaughtSnail/caelis/sdk/runtime/agents/chat"
	localruntime "github.com/OnslaughtSnail/caelis/sdk/runtime/local"
	"github.com/OnslaughtSnail/caelis/sdk/sandbox/host"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
	sessionfile "github.com/OnslaughtSnail/caelis/sdk/session/file"
	sdkbuiltin "github.com/OnslaughtSnail/caelis/sdk/tool/builtin"
)

type cliSDKHeadlessConfig struct {
	StoreDir       string
	AppName        string
	UserID         string
	SessionID      string
	WorkspaceKey   string
	WorkspaceCWD   string
	ModelAlias     string
	ContextWindow  int
	PermissionMode string
	ModelFactory   *modelproviders.Factory
	Assembly       sdkplugin.ResolvedAssembly
	AgentInput     buildAgentInput
}

func runCLIHeadlessOnce(
	ctx context.Context,
	cfg cliSDKHeadlessConfig,
	input string,
	contentParts []kernelmodel.ContentPart,
) (headlessRunResult, error) {
	gw, err := buildSDKHeadlessGateway(cfg)
	if err != nil {
		return headlessRunResult{}, err
	}
	return runHeadlessOnce(ctx, gw, headlessGatewayRunRequest{
		AppName:   cfg.AppName,
		UserID:    cfg.UserID,
		SessionID: cfg.SessionID,
		Workspace: sdksession.WorkspaceRef{
			Key: cfg.WorkspaceKey,
			CWD: cfg.WorkspaceCWD,
		},
		Input:        input,
		ContentParts: sdkContentPartsFromLegacy(contentParts),
	})
}

func buildSDKHeadlessGateway(cfg cliSDKHeadlessConfig) (*appgateway.Gateway, error) {
	if cfg.ModelFactory == nil {
		return nil, fmt.Errorf("cli: sdk headless model factory is required")
	}
	if strings.TrimSpace(cfg.ModelAlias) == "" {
		return nil, fmt.Errorf("cli: sdk headless model alias is required")
	}
	if _, usesACP, err := resolveMainSessionAgentDescriptor(cfg.AgentInput); err != nil {
		return nil, err
	} else if usesACP {
		return nil, fmt.Errorf("cli: sdk headless gateway does not support ACP main agents yet")
	}
	systemPrompt, err := resolveSystemPrompt(cfg.AgentInput)
	if err != nil {
		return nil, err
	}

	sandboxRuntime, err := host.New(host.Config{CWD: cfg.WorkspaceCWD})
	if err != nil {
		return nil, err
	}
	tools, err := sdkbuiltin.BuildCoreTools(sdkbuiltin.CoreToolsConfig{Runtime: sandboxRuntime})
	if err != nil {
		return nil, err
	}
	sessions := sessionfile.NewService(sessionfile.NewStore(sessionfile.Config{
		RootDir: filepath.Join(strings.TrimSpace(cfg.StoreDir), "sdk-headless"),
	}))
	rt, err := localruntime.New(localruntime.Config{
		Sessions:          sessions,
		AgentFactory:      chat.Factory{},
		DefaultPolicyMode: headlessPolicyMode(cfg.PermissionMode),
	})
	if err != nil {
		return nil, err
	}
	baseMetadata := map[string]any{
		"system_prompt": systemPrompt,
	}
	if reasoning := strings.TrimSpace(cfg.AgentInput.ReasoningEffort); reasoning != "" {
		baseMetadata["reasoning_effort"] = reasoning
	}
	if cfg.AgentInput.ThinkingBudget > 0 {
		baseMetadata["reasoning_budget_tokens"] = cfg.AgentInput.ThinkingBudget
	}
	resolver, err := appgateway.NewAssemblyResolver(appgateway.AssemblyResolverConfig{
		Sessions:          sessions,
		Assembly:          cfg.Assembly,
		DefaultModelAlias: cfg.ModelAlias,
		ContextWindow:     cfg.ContextWindow,
		ModelLookup: cliModelLookup{
			factory: cfg.ModelFactory,
		},
		Tools:        tools,
		BaseMetadata: baseMetadata,
	})
	if err != nil {
		return nil, err
	}
	return appgateway.New(appgateway.Config{
		Sessions: sessions,
		Runtime:  rt,
		Resolver: resolver,
	})
}

type cliModelLookup struct {
	factory *modelproviders.Factory
}

func (r cliModelLookup) ResolveModel(_ context.Context, alias string, contextWindow int) (appgateway.ModelResolution, error) {
	llm, providerCfg, err := newSDKModelFromLegacyFactory(r.factory, alias, contextWindow)
	if err != nil {
		return appgateway.ModelResolution{}, err
	}
	return appgateway.ModelResolution{
		Model:                  llm,
		ReasoningEffort:        providerCfg.ReasoningEffort,
		DefaultReasoningEffort: providerCfg.DefaultReasoningEffort,
	}, nil
}

func newSDKModelFromLegacyFactory(factory *modelproviders.Factory, alias string, contextWindow int) (sdkmodel.LLM, sdkproviders.Config, error) {
	if factory == nil {
		return nil, sdkproviders.Config{}, fmt.Errorf("cli: legacy model factory is nil")
	}
	legacyCfg, ok := factory.ConfigForAlias(alias)
	if !ok {
		return nil, sdkproviders.Config{}, fmt.Errorf("cli: unknown model alias %q", alias)
	}
	sdkCfg := sdkProviderConfigFromLegacy(legacyCfg)
	if contextWindow > 0 {
		sdkCfg.ContextWindowTokens = contextWindow
	}
	sdkFactory := sdkproviders.NewFactory()
	if err := sdkFactory.Register(sdkCfg); err != nil {
		return nil, sdkproviders.Config{}, err
	}
	llm, err := sdkFactory.NewByAlias(sdkCfg.Alias)
	if err != nil {
		return nil, sdkproviders.Config{}, err
	}
	return llm, sdkCfg, nil
}

func sdkProviderConfigFromLegacy(in modelproviders.Config) sdkproviders.Config {
	return sdkproviders.Config{
		Alias:                     in.Alias,
		Provider:                  in.Provider,
		API:                       sdkproviders.APIType(in.API),
		Model:                     in.Model,
		BaseURL:                   in.BaseURL,
		Headers:                   cloneHeadlessStringMap(in.Headers),
		Timeout:                   in.Timeout,
		MaxOutputTok:              in.MaxOutputTok,
		ContextWindowTokens:       in.ContextWindowTokens,
		ReasoningLevels:           append([]string(nil), in.ReasoningLevels...),
		ReasoningMode:             in.ReasoningMode,
		SupportedReasoningEfforts: append([]string(nil), in.SupportedReasoningEfforts...),
		DefaultReasoningEffort:    in.DefaultReasoningEffort,
		ThinkingMode:              in.ThinkingMode,
		ThinkingBudget:            in.ThinkingBudget,
		ReasoningEffort:           in.ReasoningEffort,
		OpenRouter: sdkproviders.OpenRouterConfig{
			Models:     append([]string(nil), in.OpenRouter.Models...),
			Route:      in.OpenRouter.Route,
			Provider:   cloneHeadlessAnyMap(in.OpenRouter.Provider),
			Transforms: append([]string(nil), in.OpenRouter.Transforms...),
			Plugins:    cloneHeadlessAnyMaps(in.OpenRouter.Plugins),
		},
		Auth: sdkproviders.AuthConfig{
			Type:          sdkproviders.AuthType(in.Auth.Type),
			TokenEnv:      in.Auth.TokenEnv,
			Token:         in.Auth.Token,
			CredentialRef: in.Auth.CredentialRef,
			HeaderKey:     in.Auth.HeaderKey,
			Prefix:        in.Auth.Prefix,
		},
	}
}

func sdkContentPartsFromLegacy(parts []kernelmodel.ContentPart) []sdkmodel.ContentPart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]sdkmodel.ContentPart, 0, len(parts))
	for _, part := range parts {
		out = append(out, sdkmodel.ContentPart{
			Type:     sdkmodel.ContentPartType(part.Type),
			Text:     part.Text,
			MimeType: part.MimeType,
			Data:     part.Data,
			FileName: part.FileName,
		})
	}
	return out
}

func headlessPolicyMode(permissionMode string) string {
	if strings.EqualFold(strings.TrimSpace(permissionMode), "full_control") {
		return sdkpolicy.ModeFullAccess
	}
	return sdkpolicy.ModeDefault
}

func cloneHeadlessStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneHeadlessAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneHeadlessAnyMaps(in []map[string]any) []map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(in))
	for _, one := range in {
		out = append(out, cloneHeadlessAnyMap(one))
	}
	return out
}
