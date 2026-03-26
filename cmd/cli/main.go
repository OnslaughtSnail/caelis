package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	launcherfull "github.com/OnslaughtSnail/caelis/cmd/launcher/full"
	internalacp "github.com/OnslaughtSnail/caelis/internal/acp"
	"github.com/OnslaughtSnail/caelis/internal/app/acpext"
	appassembly "github.com/OnslaughtSnail/caelis/internal/app/assembly"
	appbootstrap "github.com/OnslaughtSnail/caelis/internal/app/bootstrap"
	"github.com/OnslaughtSnail/caelis/internal/app/sessionsvc"
	"github.com/OnslaughtSnail/caelis/internal/version"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"

	image "github.com/OnslaughtSnail/caelis/internal/cli/imageutil"
	"github.com/OnslaughtSnail/caelis/internal/sandboxhelper"
)

func main() {
	if sandboxhelper.MaybeRun(os.Args[1:]) {
		return
	}
	launcher := launcherfull.NewLauncher(runCLI, runACP)
	if err := launcher.Execute(context.Background(), os.Args[1:]); err != nil {
		exitErr(err)
	}
}

func runCLI(ctx context.Context, args []string) error {
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

	fs := flag.NewFlagSet("console", flag.ContinueOnError)
	var (
		toolProviders    = fs.String("tool-providers", appassembly.ProviderWorkspaceTools+","+appassembly.ProviderShellTools, "Comma-separated tool providers")
		policyProviders  = fs.String("policy-providers", appassembly.ProviderDefaultPolicy, "Comma-separated policy providers")
		modelAlias       = fs.String("model", configStore.DefaultModel(), "Model alias")
		uiMode           = fs.String("ui", string(uiModeAuto), "Interactive UI mode: auto|tui")
		appName          = fs.String("app", initialAppName, "App name")
		userID           = fs.String("user", "local-user", "User id")
		sessionID        = fs.String("session", "default", "Session id")
		prompt           = fs.String("p", "", "Single-shot prompt text (headless mode)")
		input            = fs.String("input", "", "Input text")
		outputFormat     = fs.String("format", string(headlessFormatText), "Output format for headless mode: text|json")
		storeDir         = fs.String("store-dir", defaultStoreDir, "Local event store directory")
		sessionIndexFile = fs.String("session-index", defaultSessionIndexPath, "Session index sqlite file path")
		systemPrompt     = fs.String("system-prompt", "", "Base system prompt")
		skillsDirs       = fs.String("skills-dirs", "~/.agents/skills", "Ignored; skills are loaded from ~/.agents/skills")
		compactWatermark = fs.Float64("compact-watermark", 0.7, "Auto compaction watermark ratio (0.5-0.9)")
		contextWindow    = fs.Int("context-window", 0, "Model context window tokens override")
		permissionMode   = fs.String("permission-mode", configStore.PermissionMode(), "Permission mode: default|full_control")
		sandboxType      = fs.String("sandbox-type", configStore.SandboxType(), "Sandbox backend type when permission-mode=default (Linux auto tries bwrap then landlock)")
		experimentalLSP  = fs.Bool("experimental-lsp", false, "Enable experimental CLI LSP tools plugin")
		showVersion      = fs.Bool("version", false, "Show version and exit")
		verbose          = fs.Bool("verbose", false, "Enable verbose output with debug details")
		noColor          = fs.Bool("no-color", false, "Disable colored output")
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
	stdinTTY := isTTY(os.Stdin)
	stdoutTTY := isTTY(os.Stdout)
	singleInput, singleShotMode, err := resolveSingleShotInput(*prompt, *input, os.Stdin, stdinTTY, stdoutTTY)
	if err != nil {
		return err
	}
	outFormat, err := parseHeadlessOutputFormat(*outputFormat)
	if err != nil {
		return err
	}
	resolvedUIMode := uiModeTUI
	if !singleShotMode {
		mode, err := resolveInteractiveUIMode(*uiMode, stdinTTY, stdoutTTY)
		if err != nil {
			return err
		}
		resolvedUIMode = mode
	}
	if !flagProvided(args, "session") {
		*sessionID = nextConversationSessionID()
	}
	credentials, err := loadOrInitCredentialStore(initialAppName, credentialStoreModeAuto)
	if err != nil {
		return err
	}
	workspace, err := resolveWorkspaceContext()
	if err != nil {
		return err
	}
	resolvedWorkspaceRoot, err := resolveWorkspaceRoot(workspace.CWD, "")
	if err != nil {
		return err
	}
	_ = skillsDirs
	skillDirList := activeSkillDirs()
	inputRefs, inputRefWarnings, err := newInputReferenceResolver(workspace.CWD, skillDirList)
	if err != nil {
		return err
	}
	for _, warn := range inputRefWarnings {
		fmt.Fprintf(os.Stderr, "warn: %v\n", warn)
	}
	historyPath, err := historyFilePath(initialAppName, workspace.Key)
	if err != nil {
		return err
	}

	sandboxHelperPath, err := resolveSandboxHelperPath()
	if err != nil {
		return err
	}
	execRuntime, err := newExecutionRuntime(
		toolexec.PermissionMode(strings.TrimSpace(*permissionMode)),
		strings.TrimSpace(*sandboxType),
		sandboxHelperPath,
	)
	if err != nil {
		return err
	}
	if flagProvided(args, "sandbox-type") &&
		execRuntime.PermissionMode() == toolexec.PermissionModeDefault &&
		execRuntime.FallbackToHost() {
		requestedSandbox := sandboxTypeDisplayLabel(normalizeSandboxType(strings.TrimSpace(*sandboxType)))
		if requestedSandbox == "" {
			requestedSandbox = "auto"
		}
		_ = toolexec.Close(execRuntime)
		return fmt.Errorf("sandbox type %q is unavailable: %s", requestedSandbox, execRuntime.FallbackReason())
	}
	defer func() {
		if closeErr := toolexec.Close(execRuntime); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warn: close execution runtime failed: %v\n", closeErr)
		}
	}()
	execRuntimeView := newSwappableRuntime(execRuntime)
	if err := configStore.SetRuntimeSettings(runtimeSettings{
		PermissionMode: *permissionMode,
		SandboxType:    *sandboxType,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warn: persist runtime settings failed: %v\n", err)
	}
	if execRuntime.FallbackToHost() {
		fmt.Fprintf(os.Stderr, "warn: sandbox unavailable, fallback to host+approval: %s\n", execRuntime.FallbackReason())
	}
	pluginRegistry := plugin.NewRegistry()
	if err := appassembly.RegisterBuiltinProviders(pluginRegistry, appassembly.RegisterOptions{
		ExecutionRuntime: execRuntimeView,
	}); err != nil {
		return err
	}
	resolvedToolProviders := splitCSV(*toolProviders)
	if *experimentalLSP {
		resolvedToolProviders = appendProviderIfMissing(resolvedToolProviders, providerLSPTools)
	}
	if includesProvider(resolvedToolProviders, providerLSPTools) {
		if err := registerCLILSPToolProvider(pluginRegistry, workspace.CWD, execRuntimeView); err != nil {
			return err
		}
	}
	resolved, err := appassembly.Assemble(ctx, appassembly.AssembleSpec{
		Registry:        pluginRegistry,
		ToolProviders:   resolvedToolProviders,
		PolicyProviders: splitCSV(*policyProviders),
	})
	if err != nil {
		return err
	}
	factory := buildModelFactory(configStore, credentials)

	alias := resolveModelAliasFromConfig(*modelAlias, configStore)
	modelRuntime := defaultModelRuntimeSettings()
	if configStore != nil {
		modelRuntime = configStore.ModelRuntimeSettings(alias)
	}
	var llm model.LLM
	if alias != "" {
		llm, err = factory.NewByAlias(alias)
		if err != nil {
			return err
		}
		if err := configStore.SetDefaultModel(alias); err != nil {
			fmt.Fprintf(os.Stderr, "warn: update default model failed: %v\n", err)
		}
	}

	sessionRT, err := setupSessionRuntime(*storeDir, workspace.Key, *appName, *userID, *sessionIndexFile, *compactWatermark, workspace)
	if err != nil {
		return err
	}
	store := sessionRT.Store
	index := sessionRT.Index
	if flagProvided(args, "session") {
		if resolvedSessionID, ok, resolveErr := index.ResolveWorkspaceSessionID(workspace.Key, *sessionID); resolveErr != nil {
			return resolveErr
		} else if ok {
			*sessionID = resolvedSessionID
		}
	}
	defer func() {
		if closeErr := index.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warn: close session index failed: %v\n", closeErr)
		}
		if sessionRT.DB != nil {
			if closeErr := sessionRT.DB.Close(); closeErr != nil {
				fmt.Fprintf(os.Stderr, "warn: close local store db failed: %v\n", closeErr)
			}
		}
	}()
	rt := sessionRT.Runtime
	sessionModes := []internalacp.SessionMode{
		{ID: "default", Name: "Default", Description: "Normal coding mode with execution enabled."},
		{ID: "plan", Name: "Plan", Description: "Planning-first mode that focuses on analysis before making changes."},
		{ID: "full_access", Name: "Full Access", Description: "Execute changes directly without interactive approval, while still blocking dangerous destructive commands."},
	}
	sessionConfig := buildACPSessionConfigOptions(sessionModes, factory, configStore, alias)
	agentReg, err := configStore.AgentRegistry()
	if err != nil {
		return fmt.Errorf("invalid agent config: %w", err)
	}
	var newACPAdapter appbootstrap.ACPAdapterFactory
	subagentRunnerFactory := acpext.NewACPSubagentRunnerFactory(acpext.Config{
		Store:                store,
		WorkspaceRoot:        resolvedWorkspaceRoot,
		WorkspaceCWD:         workspace.CWD,
		ClientRuntime:        execRuntime,
		ResolveAgentRegistry: configStore.AgentRegistry,
		NewAdapter: func(conn *internalacp.Conn) (internalacp.Adapter, error) {
			if newACPAdapter == nil {
				return nil, fmt.Errorf("self acp adapter is not initialized")
			}
			return newACPAdapter(conn)
		},
	})
	serviceSet, err := appbootstrap.Build(appbootstrap.Config{
		Runtime:               rt,
		Store:                 store,
		ACPRuntime:            sessionRT.ACPRuntime,
		ACPStore:              sessionRT.ACPStore,
		AppName:               *appName,
		UserID:                *userID,
		DefaultAgent:          configStore.DefaultAgent(),
		WorkspaceCWD:          workspace.CWD,
		Execution:             execRuntime,
		Tools:                 resolved.Tools,
		Policies:              resolved.Policies,
		Resolved:              resolved,
		EnablePlan:            true,
		EnableSelfSpawn:       true,
		Index:                 &cliSessionIndexAdapter{index: index},
		SubagentRunnerFactory: subagentRunnerFactory,
		ACP: &appbootstrap.ACPConfig{
			WorkspaceRoot: resolvedWorkspaceRoot,
			SessionModes:  sessionModes,
			DefaultModeID: "default",
			SessionConfig: sessionConfig,
			BuildSystemPrompt: func(sessionCWD string) (string, error) {
				return resolveSystemPrompt(buildAgentInput{
					AppName:                     *appName,
					WorkspaceDir:                sessionCWD,
					EnableExperimentalLSPPrompt: hasLSPTools(resolved.Tools),
					BasePrompt:                  *systemPrompt,
					SkillDirs:                   skillDirList,
					DefaultAgent:                configStore.DefaultAgent(),
					AgentDescriptors:            configStore.AgentDescriptors(),
				})
			},
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
			NewAgent: func(stream bool, sessionCWD string, frozenPrompt string, sessionCfg internalacp.AgentSessionConfig) (agent.Agent, error) {
				selectedAlias := resolveACPSelectedModelAlias(alias, sessionCfg.ConfigValues, configStore)
				sessionRuntime := modelRuntime
				if configStore != nil {
					sessionRuntime = configStore.ModelRuntimeSettings(selectedAlias)
				}
				resolvedReasoningEffort := resolveACPSessionReasoning(sessionRuntime, sessionCfg.ConfigValues)
				return buildAgent(buildAgentInput{
					AppName:                     *appName,
					WorkspaceDir:                sessionCWD,
					EnableExperimentalLSPPrompt: hasLSPTools(resolved.Tools),
					BasePrompt:                  *systemPrompt,
					FrozenPrompt:                frozenPrompt,
					SkillDirs:                   skillDirList,
					DefaultAgent:                configStore.DefaultAgent(),
					AgentDescriptors:            configStore.AgentDescriptors(),
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
			NewSessionResources: func(ctx context.Context, acpConn *internalacp.Conn, sessionID string, sessionCWD string, caps internalacp.ClientCapabilities, modeResolver func() string) (*internalacp.SessionResources, error) {
				execRuntimeACP := internalacp.NewRuntime(execRuntime, acpConn, sessionID, resolvedWorkspaceRoot, sessionCWD, caps, modeResolver)
				registry := plugin.NewRegistry()
				if err := appassembly.RegisterBuiltinProviders(registry, appassembly.RegisterOptions{
					ExecutionRuntime: execRuntimeACP,
				}); err != nil {
					return nil, err
				}
				resolvedACPProviders := append([]string(nil), resolvedToolProviders...)
				if includesProvider(resolvedACPProviders, providerLSPTools) {
					if err := registerCLILSPToolProvider(registry, sessionCWD, execRuntimeACP); err != nil {
						return nil, err
					}
				}
				assembled, err := appassembly.Assemble(ctx, appassembly.AssembleSpec{
					Registry:        registry,
					ToolProviders:   resolvedACPProviders,
					PolicyProviders: splitCSV(*policyProviders),
				})
				if err != nil {
					return nil, err
				}
				return &internalacp.SessionResources{
					Runtime:  execRuntimeACP,
					Tools:    assembled.Tools,
					Policies: assembled.Policies,
					Close: func(closeCtx context.Context) error {
						return assembled.Close(closeCtx)
					},
				}, nil
			},
		},
	})
	if err != nil {
		return err
	}
	newACPAdapter = serviceSet.NewACPAdapter
	defer func() {
		if closeErr := serviceSet.Close(context.Background()); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warn: close assembled providers failed: %v\n", closeErr)
		}
	}()

	if singleShotMode {
		if llm == nil {
			return fmt.Errorf("no model configured, run /connect first or pass -model with a configured provider/model")
		}
		if strings.HasPrefix(strings.TrimSpace(singleInput), "/compact") {
			note := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(singleInput), "/compact"))
			beforeUsage, _ := rt.ContextUsage(ctx, runtime.UsageRequest{
				AppName:             *appName,
				UserID:              *userID,
				SessionID:           *sessionID,
				Model:               llm,
				ContextWindowTokens: *contextWindow,
			})
			ev, compactErr := rt.Compact(ctx, runtime.CompactRequest{
				AppName:             *appName,
				UserID:              *userID,
				SessionID:           *sessionID,
				Model:               llm,
				Note:                note,
				ContextWindowTokens: *contextWindow,
			})
			if compactErr != nil {
				return compactErr
			}
			if ev == nil {
				fmt.Println("compact: skipped (not enough history to compact)")
			} else {
				afterUsage, _ := rt.ContextUsage(ctx, runtime.UsageRequest{
					AppName:             *appName,
					UserID:              *userID,
					SessionID:           *sessionID,
					Model:               llm,
					ContextWindowTokens: *contextWindow,
				})
				fmt.Printf("compact: success, %s -> %s tokens\n", formatCompactTokenUsage(beforeUsage.CurrentTokens), formatCompactTokenUsage(afterUsage.CurrentTokens))
			}
			return nil
		}
		ag, err := buildAgent(buildAgentInput{
			AppName:                     *appName,
			WorkspaceDir:                workspace.CWD,
			EnableExperimentalLSPPrompt: hasLSPTools(resolved.Tools),
			BasePrompt:                  *systemPrompt,
			SkillDirs:                   skillDirList,
			DefaultAgent:                configStore.DefaultAgent(),
			AgentDescriptors:            configStore.AgentDescriptors(),
			StreamModel:                 false,
			ThinkingBudget:              modelRuntime.ThinkingBudget,
			ReasoningEffort:             modelRuntime.ReasoningEffort,
			ModelProvider:               resolveProviderName(factory, alias),
			ModelName:                   resolveModelName(factory, alias),
			ModelConfig: func() modelproviders.Config {
				if factory == nil {
					return modelproviders.Config{}
				}
				cfg, _ := factory.ConfigForAlias(alias)
				return cfg
			}(),
		})
		if err != nil {
			return err
		}
		resolvedInput := singleInput
		var contentParts []model.ContentPart
		if inputRefs != nil {
			result, rewriteErr := inputRefs.RewriteInput(singleInput)
			if rewriteErr != nil {
				fmt.Fprintf(os.Stderr, "warn: input reference resolution skipped: %v\n", rewriteErr)
			} else {
				resolvedInput = result.Text
				for _, note := range result.Notes {
					fmt.Fprintf(os.Stderr, "note: %s\n", note)
				}
				for _, relPath := range result.ResolvedPaths {
					if !image.IsImagePath(relPath) {
						continue
					}
					absPath := inputRefs.AbsPath(relPath)
					part, loadErr := image.LoadAsContentPart(absPath)
					if loadErr != nil {
						fmt.Fprintf(os.Stderr, "warn: image load skipped: %s: %v\n", relPath, loadErr)
						continue
					}
					contentParts = append(contentParts, part)
					fmt.Fprintf(os.Stderr, "note: attached image: %s\n", relPath)
				}
			}
		}
		headlessResult, runErr := runHeadlessOnce(ctx, serviceSet.SessionService, sessionsvc.RunTurnRequest{
			SessionRef: sessionsvc.SessionRef{
				AppName:      *appName,
				UserID:       *userID,
				SessionID:    *sessionID,
				WorkspaceKey: workspace.Key,
			},
			Input:               resolvedInput,
			ContentParts:        contentParts,
			Agent:               ag,
			Model:               llm,
			ContextWindowTokens: *contextWindow,
		})
		if runErr != nil {
			return runErr
		}
		return writeHeadlessResult(os.Stdout, outFormat, headlessResult)
	}

	console := newCLIConsole(cliConsoleConfig{
		BaseContext:           ctx,
		Runtime:               rt,
		AppName:               *appName,
		UserID:                *userID,
		SessionID:             *sessionID,
		ContextWindow:         *contextWindow,
		Workspace:             workspace,
		WorkspaceLine:         workspaceStatusLine(workspace.CWD),
		Resolved:              resolved,
		SessionStore:          store,
		ExecRuntime:           execRuntime,
		ExecRuntimeView:       execRuntimeView,
		SandboxType:           strings.TrimSpace(*sandboxType),
		SandboxHelperPath:     sandboxHelperPath,
		ModelAlias:            alias,
		Model:                 llm,
		ModelFactory:          factory,
		ConfigStore:           configStore,
		CredentialStore:       credentials,
		SessionIndex:          index,
		SystemPrompt:          *systemPrompt,
		EnableExperimentalLSP: hasLSPTools(resolved.Tools),
		SkillDirs:             skillDirList,
		ThinkingBudget:        modelRuntime.ThinkingBudget,
		ReasoningEffort:       modelRuntime.ReasoningEffort,
		InputRefs:             inputRefs,
		TUIDiagnostics:        newTUIDiagnostics(),
		HistoryFile:           historyPath,
		Version:               version.String(),
		NoColor:               *noColor,
		Verbose:               *verbose,
		UIMode:                string(resolvedUIMode),
		AgentRegistry:         agentReg,
		SessionService:        serviceSet.SessionService,
		Gateway:               serviceSet.Gateway,
		NewACPAdapter: func(conn *internalacp.Conn) (internalacp.Adapter, error) {
			return serviceSet.NewACPAdapter(conn)
		},
	})
	return console.loop()
}
