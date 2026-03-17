package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	launcherfull "github.com/OnslaughtSnail/caelis/cmd/launcher/full"
	appassembly "github.com/OnslaughtSnail/caelis/internal/app/assembly"
	"github.com/OnslaughtSnail/caelis/internal/version"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	pluginbuiltin "github.com/OnslaughtSnail/caelis/kernel/plugin/builtin"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolmcp "github.com/OnslaughtSnail/caelis/kernel/tool/mcptoolset"

	image "github.com/OnslaughtSnail/caelis/internal/cli/imageutil"
	"github.com/OnslaughtSnail/caelis/internal/sandboxhelper"
)

func main() {
	if sandboxhelper.MaybeRun(os.Args[1:]) {
		return
	}
	launcher := launcherfull.NewLauncher(
		runCLI,
		runACP,
		notImplementedLauncher("api"),
		notImplementedLauncher("web"),
	)
	if err := launcher.Execute(context.Background(), os.Args[1:]); err != nil {
		exitErr(err)
	}
}

func notImplementedLauncher(mode string) func(context.Context, []string) error {
	return func(ctx context.Context, args []string) error {
		_ = ctx
		_ = args
		return fmt.Errorf("%s launcher is not implemented yet", mode)
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
		toolProviders    = fs.String("tool-providers", pluginbuiltin.ProviderWorkspaceTools+","+pluginbuiltin.ProviderShellTools+","+pluginbuiltin.ProviderMCPTools, "Comma-separated tool providers")
		policyProviders  = fs.String("policy-providers", pluginbuiltin.ProviderDefaultPolicy, "Comma-separated policy providers")
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
		mcpConfigPath    = fs.String("mcp-config", defaultMCPConfigPath(), "MCP config JSON path (default ~/.agents/mcp_servers.json)")
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
	var mcpManager *toolmcp.Manager
	mcpManager, err = loadMCPToolManager(*mcpConfigPath)
	if err != nil {
		return err
	}
	if mcpManager != nil {
		defer func() {
			if closeErr := mcpManager.Close(); closeErr != nil {
				fmt.Fprintf(os.Stderr, "warn: close mcp manager failed: %v\n", closeErr)
			}
		}()
	}
	pluginRegistry := plugin.NewRegistry()
	if err := pluginbuiltin.RegisterAll(pluginRegistry, pluginbuiltin.RegisterOptions{
		ExecutionRuntime: execRuntimeView,
		MCPToolManager:   mcpManager,
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
	defer func() {
		if closeErr := resolved.Close(context.Background()); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warn: close assembled providers failed: %v\n", closeErr)
		}
	}()
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
	}()
	rt := sessionRT.Runtime

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
		headlessResult, runErr := runHeadlessOnce(ctx, rt, runtime.RunRequest{
			AppName:             *appName,
			UserID:              *userID,
			SessionID:           *sessionID,
			Input:               resolvedInput,
			ContentParts:        contentParts,
			Agent:               ag,
			Model:               llm,
			Tools:               resolved.Tools,
			CoreTools:           tool.CoreToolsConfig{Runtime: execRuntime},
			Policies:            resolved.Policies,
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
	})
	return console.loop()
}
