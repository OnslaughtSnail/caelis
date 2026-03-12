package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/version"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/bootstrap"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
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
	defaultSessionIndexPath, err := sessionIndexPath(initialAppName)
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
		sessionIndexFile = fs.String("session-index", defaultSessionIndexPath, "Session index sqlite file path")
		systemPrompt     = fs.String("system-prompt", "", "Base system prompt")
		skillsDirs       = fs.String("skills-dirs", "~/.agents/skills", "Ignored; skills are loaded from ~/.agents/skills")
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
	_ = skillsDirs
	skillDirList := activeSkillDirs()

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
	sessionConfig := buildACPSessionConfigOptions(sessionModes, factory, configStore, alias)

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
	index, err := newSessionIndex(*sessionIndexFile)
	if err != nil {
		return err
	}
	if err := index.SyncWorkspaceFromStoreDir(workspace, *appName, *userID, eventStoreDir); err != nil {
		fmt.Fprintf(os.Stderr, "warn: sync session index failed: %v\n", err)
	}
	defer func() {
		if closeErr := index.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warn: close session index failed: %v\n", closeErr)
		}
	}()
	store := newIndexedSessionStore(storeImpl, index, workspace)
	rt, err := runtime.New(runtime.Config{
		Store:     store,
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
		Store:           store,
		AppName:         *appName,
		UserID:          *userID,
		WorkspaceRoot:   resolvedWorkspaceRoot,
		ProtocolVersion: internalacp.CurrentProtocolVersion,
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
		SessionConfigState: func(sessionCfg internalacp.AgentSessionConfig, templates []internalacp.SessionConfigOptionTemplate) []internalacp.SessionConfigOption {
			return buildACPSessionConfigState(templates, factory, configStore, alias, sessionCfg)
		},
		NormalizeConfig: func(sessionCfg internalacp.AgentSessionConfig) internalacp.AgentSessionConfig {
			return normalizeACPSessionConfig(factory, configStore, alias, sessionCfg)
		},
		NewModel: func(sessionCfg internalacp.AgentSessionConfig) (model.LLM, error) {
			selectedAlias := resolveACPSelectedModelAlias(alias, sessionCfg.ConfigValues, configStore)
			return factory.NewByAlias(selectedAlias)
		},
		PromptImageEnabled: func() bool {
			return true
		},
		SupportsPromptImage: func(sessionCfg internalacp.AgentSessionConfig) bool {
			selectedAlias := resolveACPSelectedModelAlias(alias, sessionCfg.ConfigValues, configStore)
			return acpModelSupportsImages(factory, selectedAlias)
		},
		SessionModels: func(sessionCfg internalacp.AgentSessionConfig) *internalacp.SessionModelState {
			selectedAlias := resolveACPSelectedModelAlias(alias, sessionCfg.ConfigValues, configStore)
			return buildACPSessionModelState(factory, configStore, selectedAlias)
		},
		ListSessions: func(ctx context.Context, req internalacp.SessionListRequest) (internalacp.SessionListResponse, error) {
			_ = ctx
			return buildACPSessionList(index, workspace, req), nil
		},
		NewAgent: func(stream bool, sessionCWD string, sessionCfg internalacp.AgentSessionConfig) (agent.Agent, error) {
			selectedAlias := resolveACPSelectedModelAlias(alias, sessionCfg.ConfigValues, configStore)
			sessionRuntime := modelRuntime
			if configStore != nil {
				sessionRuntime = configStore.ModelRuntimeSettings(selectedAlias)
			}
			resolvedReasoningEffort := resolveACPSessionReasoning(sessionRuntime, sessionCfg.ConfigValues)
			return buildAgent(buildAgentInput{
				AppName:                     *appName,
				WorkspaceDir:                sessionCWD,
				EnableExperimentalLSPPrompt: *experimentalLSP,
				BasePrompt:                  *systemPrompt,
				SkillDirs:                   skillDirList,
				StreamModel:                 stream,
				ThinkingBudget:              sessionRuntime.ThinkingBudget,
				ReasoningEffort:             resolvedReasoningEffort,
				ModelProvider:               resolveProviderName(factory, selectedAlias),
				ModelName:                   resolveModelName(factory, selectedAlias),
				ModelConfig: func() modelproviders.Config {
					if factory == nil {
						return modelproviders.Config{}
					}
					cfg, _ := factory.ConfigForAlias(selectedAlias)
					return cfg
				}(),
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
				GuardedTools: []string{"WRITE", "PATCH"},
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

func buildACPSessionModelState(factory *modelproviders.Factory, configStore *appConfigStore, selectedAlias string) *internalacp.SessionModelState {
	aliases := configuredACPModelAliases(factory, configStore)
	if len(aliases) == 0 {
		return nil
	}
	models := make([]internalacp.SessionModel, 0, len(aliases))
	for _, alias := range aliases {
		cfg, _ := factory.ConfigForAlias(alias)
		models = append(models, internalacp.SessionModel{
			ModelID:     alias,
			Name:        alias,
			Description: formatACPModelDescription(cfg),
		})
	}
	return &internalacp.SessionModelState{
		CurrentModelID:  selectedAlias,
		AvailableModels: models,
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
