package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	appassembly "github.com/OnslaughtSnail/caelis/internal/app/assembly"
	"github.com/OnslaughtSnail/caelis/internal/version"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	pluginbuiltin "github.com/OnslaughtSnail/caelis/kernel/plugin/builtin"
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
		sandboxType      = fs.String("sandbox-type", configStore.SandboxType(), "Sandbox backend type when permission-mode=default (Linux auto tries bwrap then landlock)")
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

	factory := buildModelFactory(configStore, credentials)
	alias := resolveModelAliasFromConfig(*modelAlias, configStore)
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

	sessionRT, err := setupSessionRuntime(*storeDir, workspace.Key, *appName, *userID, *sessionIndexFile, *compactWatermark, workspace)
	if err != nil {
		return err
	}
	store := sessionRT.Store
	index := sessionRT.Index
	defer func() {
		if closeErr := index.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warn: close session index failed: %v\n", closeErr)
		}
	}()
	rt := sessionRT.Runtime

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
			resolved, err := appassembly.Assemble(ctx, appassembly.AssembleSpec{
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
