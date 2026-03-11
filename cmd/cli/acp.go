package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/version"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/bootstrap"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	pluginbuiltin "github.com/OnslaughtSnail/caelis/kernel/plugin/builtin"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session/filestore"
	taskfilestore "github.com/OnslaughtSnail/caelis/kernel/task/filestore"
	toolmcp "github.com/OnslaughtSnail/caelis/kernel/tool/mcptoolset"
)

func runACP(ctx context.Context, args []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	initialAppName := appNameFromArgs(args, "caelis")
	configStore, err := loadOrInitAppConfig(initialAppName)
	if err != nil {
		return err
	}
	defaultStoreDir, err := sessionStoreDir(initialAppName)
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("acp", flag.ContinueOnError)
	var (
		toolProviders    = fs.String("tool-providers", pluginbuiltin.ProviderWorkspaceTools+","+pluginbuiltin.ProviderShellTools+","+pluginbuiltin.ProviderMCPTools, "Comma-separated tool providers")
		policyProviders  = fs.String("policy-providers", pluginbuiltin.ProviderDefaultPolicy, "Comma-separated policy providers")
		modelAlias       = fs.String("model", configStore.DefaultModel(), "Model alias")
		appName          = fs.String("app", initialAppName, "App name")
		userID           = fs.String("user", "local-user", "User id")
		storeDir         = fs.String("store-dir", defaultStoreDir, "Local event store directory")
		systemPrompt     = fs.String("system-prompt", "You are a helpful assistant.", "Base system prompt")
		promptConfigDir  = fs.String("prompt-config-dir", "", "Prompt config directory (default ~/.{app}/prompts)")
		skillsDirs       = fs.String("skills-dirs", "~/.agents/skills,.agents/skills", "Comma-separated skill directories")
		compactWatermark = fs.Float64("compact-watermark", 0.7, "Auto compaction watermark ratio (0.5-0.9)")
		permissionMode   = fs.String("permission-mode", configStore.PermissionMode(), "Permission mode: default|full_control")
		sandboxType      = fs.String("sandbox-type", configStore.SandboxType(), "Sandbox backend type when permission-mode=default")
		workspaceRoot    = fs.String("workspace-root", "", "Workspace root for ACP session cwd validation (default: git root or current directory)")
		experimentalLSP  = fs.Bool("experimental-lsp", false, "Enable experimental CLI LSP tools plugin")
		mcpConfigPath    = fs.String("mcp-config", defaultMCPConfigPath(), "MCP config JSON path (default ~/.agents/mcp_servers.json)")
		authMethodID     = fs.String("auth-method-id", "", "Optional ACP auth method id; when set, clients must authenticate before using session methods")
		authMethodName   = fs.String("auth-method-name", "Local token", "ACP auth method display name")
		authTokenEnv     = fs.String("auth-token-env", "", "Optional env var containing the expected ACP auth token for the configured auth method")
		showVersion      = fs.Bool("version", false, "Show version and exit")
	)
	if err := rejectRemovedExecutionFlags(args); err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(version.String())
		return nil
	}
	if len(fs.Args()) > 0 {
		return fmt.Errorf("unknown arguments: %v", fs.Args())
	}

	credentials, err := loadOrInitCredentialStore(initialAppName, credentialStoreModeAuto)
	if err != nil {
		return err
	}
	workspace, err := resolveWorkspaceContext()
	if err != nil {
		return err
	}
	resolvedWorkspaceRoot, err := resolveWorkspaceRoot(workspace.CWD, *workspaceRoot)
	if err != nil {
		return err
	}
	skillDirList := splitCSV(*skillsDirs)

	sandboxHelperPath, err := resolveSandboxHelperPath()
	if err != nil {
		return err
	}
	baseRuntime, err := newExecutionRuntime(
		toolexec.PermissionMode(strings.TrimSpace(*permissionMode)),
		strings.TrimSpace(*sandboxType),
		sandboxHelperPath,
	)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := toolexec.Close(baseRuntime); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warn: close execution runtime failed: %v\n", closeErr)
		}
	}()

	baseMCPConfig, err := loadMCPToolConfig(*mcpConfigPath)
	if err != nil {
		return err
	}

	factory := modelproviders.NewFactory()
	for _, providerCfg := range configStore.ProviderConfigs() {
		providerCfg = hydrateProviderAuthToken(providerCfg, credentials)
		modelcatalogApplyConfigDefaults(&providerCfg)
		if registerErr := factory.Register(providerCfg); registerErr != nil {
			fmt.Fprintf(os.Stderr, "warn: skip provider %q: %v\n", providerCfg.Alias, registerErr)
		}
	}
	alias := strings.TrimSpace(strings.ToLower(*modelAlias))
	if alias == "" {
		alias = configStore.DefaultModel()
	}
	if configStore != nil && alias != "" {
		alias = configStore.ResolveModelAlias(alias)
	}
	if alias == "" {
		return fmt.Errorf("no model configured, run /connect first or pass -model with a configured provider/model")
	}
	modelRuntime := defaultModelRuntimeSettings()
	if configStore != nil {
		modelRuntime = configStore.ModelRuntimeSettings(alias)
	}
	authMethods, authValidator, err := buildACPAuth(strings.TrimSpace(*authMethodID), strings.TrimSpace(*authMethodName), strings.TrimSpace(*authTokenEnv))
	if err != nil {
		return err
	}
	sessionModes := []internalacp.SessionMode{
		{ID: "default", Name: "Default", Description: "Normal coding mode with execution enabled."},
		{ID: "plan", Name: "Plan", Description: "Planning-first mode that focuses on analysis before making changes."},
		{ID: "full_access", Name: "Full Access", Description: "Execute changes directly without interactive approval, while still blocking dangerous destructive commands."},
	}
	sessionConfig := buildACPSessionConfigOptions(resolveProviderName(factory, alias), resolveModelName(factory, alias), modelRuntime)
	llm, err := factory.NewByAlias(alias)
	if err != nil {
		return err
	}

	eventStoreDir := filepath.Join(*storeDir, workspace.Key)
	storeImpl, err := filestore.NewWithOptions(eventStoreDir, filestore.Options{
		Layout: filestore.LayoutSessionOnly,
	})
	if err != nil {
		return err
	}
	taskStoreImpl, err := taskfilestore.New(filepath.Join(eventStoreDir, ".tasks"))
	if err != nil {
		return err
	}
	rt, err := runtime.New(runtime.Config{
		Store:     storeImpl,
		TaskStore: taskStoreImpl,
		Compaction: runtime.CompactionConfig{
			WatermarkRatio: *compactWatermark,
		},
	})
	if err != nil {
		return err
	}

	conn := internalacp.NewConn(os.Stdin, os.Stdout)
	server, err := internalacp.NewServer(internalacp.ServerConfig{
		Conn:            conn,
		Runtime:         rt,
		Store:           storeImpl,
		Model:           llm,
		AppName:         *appName,
		UserID:          *userID,
		WorkspaceRoot:   resolvedWorkspaceRoot,
		ProtocolVersion: "0.2.0",
		AgentInfo: &internalacp.Implementation{
			Name:    *appName,
			Title:   "caelis",
			Version: version.String(),
		},
		AuthMethods:   authMethods,
		Authenticate:  authValidator,
		SessionModes:  sessionModes,
		DefaultModeID: "default",
		SessionConfig: sessionConfig,
		NewAgent: func(stream bool, sessionCWD string, sessionCfg internalacp.AgentSessionConfig) (agent.Agent, error) {
			resolvedThinkingMode, resolvedReasoningEffort := resolveACPSessionReasoning(modelRuntime, sessionCfg.ConfigValues)
			return buildAgent(buildAgentInput{
				AppName:                     *appName,
				WorkspaceDir:                sessionCWD,
				PromptConfigDir:             *promptConfigDir,
				EnableExperimentalLSPPrompt: *experimentalLSP,
				BasePrompt:                  *systemPrompt,
				SkillDirs:                   skillDirList,
				StreamModel:                 stream,
				ThinkingMode:                resolvedThinkingMode,
				ThinkingBudget:              modelRuntime.ThinkingBudget,
				ReasoningEffort:             resolvedReasoningEffort,
				ModelProvider:               resolveProviderName(factory, alias),
				ModelName:                   resolveModelName(factory, alias),
			})
		},
		NewSessionResources: func(ctx context.Context, sessionID string, sessionCWD string, caps internalacp.ClientCapabilities, mcpServers []internalacp.MCPServer, modeResolver func() string) (*internalacp.SessionResources, error) {
			execRuntime := internalacp.NewRuntime(baseRuntime, conn, sessionID, resolvedWorkspaceRoot, sessionCWD, caps, modeResolver)
			mcpCfg, err := buildACPMCPConfig(baseMCPConfig, mcpServers)
			if err != nil {
				return nil, err
			}
			var manager *toolmcp.Manager
			if len(mcpCfg.Servers) > 0 {
				manager, err = toolmcp.NewManager(mcpCfg)
				if err != nil {
					return nil, err
				}
			}
			registry := plugin.NewRegistry()
			if err := pluginbuiltin.RegisterAll(registry, pluginbuiltin.RegisterOptions{
				ExecutionRuntime: execRuntime,
				MCPToolManager:   manager,
			}); err != nil {
				if manager != nil {
					_ = manager.Close()
				}
				return nil, err
			}
			resolvedToolProviders := splitCSV(*toolProviders)
			if *experimentalLSP {
				resolvedToolProviders = appendProviderIfMissing(resolvedToolProviders, providerLSPTools)
			}
			if includesProvider(resolvedToolProviders, providerLSPTools) {
				if err := registerCLILSPToolProvider(registry, sessionCWD, execRuntime); err != nil {
					if manager != nil {
						_ = manager.Close()
					}
					return nil, err
				}
			}
			resolved, err := bootstrap.Assemble(ctx, bootstrap.AssembleSpec{
				Registry:        registry,
				ToolProviders:   resolvedToolProviders,
				PolicyProviders: splitCSV(*policyProviders),
			})
			if err != nil {
				if manager != nil {
					_ = manager.Close()
				}
				return nil, err
			}
			resolved.Policies = append(resolved.Policies, policy.NewSecurityBaseline(policy.SecurityBaselineConfig{
				GuardedTools: []string{"WRITE", "PATCH", "BASH"},
			}))
			return &internalacp.SessionResources{
				Runtime:  execRuntime,
				Tools:    resolved.Tools,
				Policies: resolved.Policies,
				Close: func(closeCtx context.Context) error {
					var firstErr error
					if err := resolved.Close(closeCtx); err != nil {
						firstErr = err
					}
					if manager != nil {
						if err := manager.Close(); err != nil && firstErr == nil {
							firstErr = err
						}
					}
					return firstErr
				},
			}, nil
		},
	})
	if err != nil {
		return err
	}
	return server.Serve(ctx)
}

func buildACPMCPConfig(base toolmcp.Config, servers []internalacp.MCPServer) (toolmcp.Config, error) {
	if len(servers) == 0 {
		return base, nil
	}
	cfg := toolmcp.Config{
		CacheTTL: base.CacheTTL,
		Servers:  append(make([]toolmcp.ServerConfig, 0, len(base.Servers)+len(servers)), base.Servers...),
	}
	for _, item := range servers {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return toolmcp.Config{}, fmt.Errorf("acp mcp server name is required")
		}
		server := toolmcp.ServerConfig{Name: name, Prefix: name}
		switch strings.TrimSpace(strings.ToLower(item.Type)) {
		case "", "stdio":
			server.Transport = toolmcp.TransportStdio
			server.Command = strings.TrimSpace(item.Command)
			server.Args = append([]string(nil), item.Args...)
			if server.Command == "" {
				return toolmcp.Config{}, fmt.Errorf("acp mcp server %q command is required", name)
			}
			if len(item.Env) > 0 {
				server.Env = map[string]string{}
				for _, env := range item.Env {
					server.Env[strings.TrimSpace(env.Name)] = env.Value
				}
			}
		case "http":
			server.Transport = toolmcp.TransportStreamable
			server.URL = strings.TrimSpace(item.URL)
		case "sse":
			server.Transport = toolmcp.TransportSSE
			server.URL = strings.TrimSpace(item.URL)
		default:
			return toolmcp.Config{}, fmt.Errorf("unsupported acp mcp transport %q", item.Type)
		}
		if len(item.Headers) > 0 {
			return toolmcp.Config{}, fmt.Errorf("acp mcp headers are not supported yet for %q", name)
		}
		cfg.Servers = append(cfg.Servers, server)
	}
	return cfg, nil
}

func resolveACPSessionReasoning(defaults modelRuntimeSettings, values map[string]string) (string, string) {
	thinkingMode := normalizeThinkingMode(defaults.ThinkingMode)
	reasoningEffort := normalizeReasoningEffort(defaults.ReasoningEffort)
	if len(values) == 0 {
		return thinkingMode, reasoningEffort
	}
	if raw := strings.TrimSpace(values[acpConfigThinkingMode]); raw != "" {
		thinkingMode = normalizeThinkingMode(raw)
	}
	if raw := strings.TrimSpace(values[acpConfigReasoningEffort]); raw != "" {
		switch raw {
		case acpConfigValueDefault:
			reasoningEffort = normalizeReasoningEffort(defaults.ReasoningEffort)
		default:
			reasoningEffort = normalizeReasoningEffort(raw)
		}
	}
	return thinkingMode, reasoningEffort
}

func buildACPSessionConfigOptions(provider string, modelName string, defaults modelRuntimeSettings) []internalacp.SessionConfigOptionTemplate {
	profile := reasoningProfileForModel(provider, modelName)
	options := []internalacp.SessionConfigOptionTemplate{{
		ID:           acpConfigThinkingMode,
		Name:         "Thinking Mode",
		Description:  "Controls whether the model should use additional reasoning by default.",
		Category:     "thought_level",
		DefaultValue: normalizeThinkingMode(defaults.ThinkingMode),
		Options: []internalacp.SessionConfigSelectOption{
			{Value: "auto", Name: "auto", Description: "Let the agent choose automatically."},
			{Value: "off", Name: "off", Description: "Disable extra reasoning."},
			{Value: "on", Name: "on", Description: "Prefer extra reasoning when supported."},
		},
	}}
	if profile.Mode != reasoningModeEffort {
		return options
	}
	effortOptions := []internalacp.SessionConfigSelectOption{
		{Value: acpConfigValueDefault, Name: "default", Description: "Use the model's configured default effort."},
		{Value: "none", Name: "none", Description: "Disable reasoning effort for this session."},
	}
	for _, effort := range profile.SupportedEfforts {
		if effort == "" {
			continue
		}
		effortOptions = append(effortOptions, internalacp.SessionConfigSelectOption{
			Value:       effort,
			Name:        effort,
			Description: fmt.Sprintf("Use %s reasoning effort for this session.", effort),
		})
	}
	return append(options, internalacp.SessionConfigOptionTemplate{
		ID:           acpConfigReasoningEffort,
		Name:         "Reasoning Effort",
		Description:  "Overrides the reasoning effort for this ACP session.",
		Category:     "thought_level",
		DefaultValue: acpDefaultReasoningConfigValue(defaults.ReasoningEffort),
		Options:      effortOptions,
	})
}

func acpDefaultReasoningConfigValue(value string) string {
	value = normalizeReasoningEffort(value)
	if value == "" {
		return acpConfigValueDefault
	}
	return value
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
	acpConfigThinkingMode    = "thinking_mode"
	acpConfigReasoningEffort = "reasoning_effort"
	acpConfigValueDefault    = "default"
)
