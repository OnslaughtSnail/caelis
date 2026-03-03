package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	launcherfull "github.com/OnslaughtSnail/caelis/cmd/launcher/full"
	"github.com/OnslaughtSnail/caelis/internal/version"
	"github.com/OnslaughtSnail/caelis/kernel/bootstrap"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	pluginbuiltin "github.com/OnslaughtSnail/caelis/kernel/plugin/builtin"
	"github.com/OnslaughtSnail/caelis/kernel/promptpipeline"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session/filestore"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolmcp "github.com/OnslaughtSnail/caelis/kernel/tool/mcptoolset"

	image "github.com/OnslaughtSnail/caelis/internal/cli/imageutil"
)

var conversationSessionCounter atomic.Uint64

func main() {
	launcher := launcherfull.NewLauncher(
		runCLI,
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
		toolProviders    = fs.String("tool-providers", pluginbuiltin.ProviderWorkspaceTools+","+pluginbuiltin.ProviderShellTools+","+providerLSPTools+","+pluginbuiltin.ProviderMCPTools, "Comma-separated tool providers")
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
		systemPrompt     = fs.String("system-prompt", "You are a helpful assistant.", "Base system prompt")
		promptConfigDir  = fs.String("prompt-config-dir", "", "Prompt config directory (default ~/.{app}/prompts)")
		skillsDirs       = fs.String("skills-dirs", "~/.agents/skills,.agents/skills", "Comma-separated skill directories")
		compactWatermark = fs.Float64("compact-watermark", 0.7, "Auto compaction watermark ratio (0.5-0.9)")
		contextWindow    = fs.Int("context-window", 0, "Model context window tokens override")
		permissionMode   = fs.String("permission-mode", configStore.PermissionMode(), "Permission mode: default|full_control")
		sandboxType      = fs.String("sandbox-type", configStore.SandboxType(), "Sandbox backend type when permission-mode=default")
		mcpConfigPath    = fs.String("mcp-config", defaultMCPConfigPath(), "MCP config JSON path (default ~/.agents/mcp_servers.json)")
		modelCapsPath    = fs.String("model-capabilities", defaultModelCapsOverridePath(), "Model capabilities override JSON path (default ~/.agents/model_capabilities.json)")
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
	// Initialise the dynamic model capability catalog (remote fetch + local overrides).
	// This runs concurrently with the rest of startup; errors fall back gracefully to
	// the embedded snapshot so we do not block or fail on network issues.
	modelproviders.InitModelCatalog(ctx, nil, *modelCapsPath)

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
	if err := mergeCredentialStoreProviderTokens(configStore, credentials); err != nil {
		return err
	}
	if err := configStore.SetRuntimeSettings(runtimeSettings{
		PermissionMode: *permissionMode,
		SandboxType:    *sandboxType,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warn: persist runtime settings failed: %v\n", err)
	}

	workspace, err := resolveWorkspaceContext()
	if err != nil {
		return err
	}
	skillDirList := splitCSV(*skillsDirs)
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

	execRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionMode(strings.TrimSpace(*permissionMode)),
		SandboxType:    strings.TrimSpace(*sandboxType),
	})
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := toolexec.Close(execRuntime); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warn: close execution runtime failed: %v\n", closeErr)
		}
	}()
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
		ExecutionRuntime: execRuntime,
		MCPToolManager:   mcpManager,
	}); err != nil {
		return err
	}
	if err := registerCLILSPToolProvider(pluginRegistry, workspace.CWD, execRuntime); err != nil {
		return err
	}

	resolved, err := bootstrap.Assemble(ctx, bootstrap.AssembleSpec{
		Registry:        pluginRegistry,
		ToolProviders:   splitCSV(*toolProviders),
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
	factory := modelproviders.NewFactory()
	for _, providerCfg := range configStore.ProviderConfigs() {
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

	eventStoreDir := filepath.Join(*storeDir, workspace.Key)
	storeImpl, err := filestore.NewWithOptions(eventStoreDir, filestore.Options{
		Layout: filestore.LayoutSessionOnly,
	})
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
		Store: store,
		Compaction: runtime.CompactionConfig{
			WatermarkRatio: *compactWatermark,
		},
	})
	if err != nil {
		return err
	}

	if singleShotMode {
		if llm == nil {
			return fmt.Errorf("no model configured, run /connect first or pass -model with a configured provider/model")
		}
		if strings.HasPrefix(strings.TrimSpace(singleInput), "/compact") {
			note := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(singleInput), "/compact"))
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
				fmt.Printf("compact: success, event_id=%s\n", ev.ID)
			}
			return nil
		}
		ag, err := buildAgent(buildAgentInput{
			AppName:                *appName,
			WorkspaceDir:           workspace.CWD,
			PromptConfigDir:        *promptConfigDir,
			EnableLSPRoutingPolicy: hasLSPTools(resolved.Tools),
			BasePrompt:             *systemPrompt,
			RuntimeHint:            buildRuntimePromptHint(execRuntime),
			SkillDirs:              skillDirList,
			StreamModel:            false,
			ThinkingMode:           modelRuntime.ThinkingMode,
			ThinkingBudget:         modelRuntime.ThinkingBudget,
			ReasoningEffort:        modelRuntime.ReasoningEffort,
			ModelProvider:          resolveProviderName(factory, alias),
			ModelName:              resolveModelName(factory, alias),
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
		BaseContext:            ctx,
		Runtime:                rt,
		AppName:                *appName,
		UserID:                 *userID,
		SessionID:              *sessionID,
		ContextWindow:          *contextWindow,
		Workspace:              workspace,
		Resolved:               resolved,
		ExecRuntime:            execRuntime,
		SandboxType:            strings.TrimSpace(*sandboxType),
		ModelAlias:             alias,
		Model:                  llm,
		ModelFactory:           factory,
		ConfigStore:            configStore,
		CredentialStore:        credentials,
		SessionIndex:           index,
		SystemPrompt:           *systemPrompt,
		PromptConfigDir:        *promptConfigDir,
		EnableLSPRoutingPolicy: hasLSPTools(resolved.Tools),
		SkillDirs:              skillDirList,
		ThinkingMode:           modelRuntime.ThinkingMode,
		ThinkingBudget:         modelRuntime.ThinkingBudget,
		ReasoningEffort:        modelRuntime.ReasoningEffort,
		InputRefs:              inputRefs,
		TUIDiagnostics:         newTUIDiagnostics(),
		HistoryFile:            historyPath,
		Version:                version.String(),
		NoColor:                *noColor,
		Verbose:                *verbose,
		UIMode:                 string(resolvedUIMode),
	})
	return console.loop()
}

type buildAgentInput struct {
	AppName                string
	WorkspaceDir           string
	PromptConfigDir        string
	EnableLSPRoutingPolicy bool
	BasePrompt             string
	RuntimeHint            string
	SkillDirs              []string
	StreamModel            bool
	ThinkingMode           string
	ThinkingBudget         int
	ReasoningEffort        string
	ModelProvider          string
	ModelName              string
}

func buildAgent(in buildAgentInput) (*llmagent.Agent, error) {
	promptInput, err := buildPromptAssembleSpec(in)
	if err != nil {
		return nil, err
	}
	assembled, err := promptpipeline.Assemble(promptInput.Spec)
	if err != nil {
		return nil, err
	}
	for _, warn := range promptInput.Warnings {
		fmt.Fprintf(os.Stderr, "warn: %v\n", warn)
	}

	reasoning, err := parseReasoning(in.ThinkingMode, in.ThinkingBudget, in.ReasoningEffort, in.ModelProvider, in.ModelName)
	if err != nil {
		return nil, err
	}

	return llmagent.New(llmagent.Config{
		Name:              "main",
		SystemPrompt:      assembled.Prompt,
		StreamModel:       in.StreamModel,
		Reasoning:         reasoning,
		EmitPartialEvents: in.StreamModel,
	})
}

func splitCSV(input string) []string {
	parts := strings.Split(input, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func resolveProviderName(factory *modelproviders.Factory, alias string) string {
	if factory == nil || alias == "" {
		return ""
	}
	cfg, ok := factory.ConfigForAlias(alias)
	if !ok {
		return ""
	}
	return cfg.Provider
}

func resolveModelName(factory *modelproviders.Factory, alias string) string {
	if factory == nil || alias == "" {
		return ""
	}
	cfg, ok := factory.ConfigForAlias(alias)
	if !ok {
		return ""
	}
	return cfg.Model
}

func buildRuntimePromptHint(execRuntime toolexec.Runtime) string {
	if execRuntime == nil {
		return ""
	}
	mode := strings.TrimSpace(string(execRuntime.PermissionMode()))
	if mode == "" {
		return ""
	}
	lines := []string{
		"## Runtime Execution",
		"- Informational runtime hints; higher-priority instructions may override.",
	}
	if policyHint := runtimePolicyHint(execRuntime.SandboxPolicy()); policyHint != "" {
		lines = append(lines, "- "+policyHint)
	}
	switch execRuntime.PermissionMode() {
	case toolexec.PermissionModeFullControl:
		lines = append(lines, "- permission_mode=full_control route=host")
		lines = append(lines, "- Rule: BASH commands run on host directly with no approval gate.")
	default:
		lines = append(lines, fmt.Sprintf("- permission_mode=default sandbox_type=%s", execRuntime.SandboxType()))
		if execRuntime.FallbackToHost() {
			lines = append(lines, "- Rule: sandbox is unavailable; all BASH commands require approval then run on host.")
			if reason := strings.TrimSpace(execRuntime.FallbackReason()); reason != "" {
				lines = append(lines, fmt.Sprintf("- Fallback reason: %s", truncateInline(reason, 160)))
			}
			lines = append(lines, "- Approval UX: y=allow once, a=allow this command for current session, n=cancel current run.")
		} else {
			lines = append(lines, "- Rule: commands run in sandbox by default; only require_escalated requests need host approval.")
			lines = append(lines, "- Approval UX: host escalation uses y/a/n; denied approval stops current run and returns control to user.")
		}
	}
	return strings.Join(lines, "\n")
}

func runtimePolicyHint(policy toolexec.SandboxPolicy) string {
	policyType := strings.TrimSpace(string(policy.Type))
	if policyType == "" {
		return ""
	}
	network := "off"
	if policy.NetworkAccess {
		network = "on"
	}
	return fmt.Sprintf(
		"sandbox_policy=%s network=%s writable_roots=%s read_only_subpaths=%s",
		policyType,
		network,
		csvOrDash(policy.WritableRoots),
		csvOrDash(policy.ReadOnlySubpaths),
	)
}

func csvOrDash(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	filtered := make([]string, 0, len(items))
	for _, one := range items {
		trimmed := strings.TrimSpace(one)
		if trimmed == "" {
			continue
		}
		filtered = append(filtered, trimmed)
	}
	if len(filtered) == 0 {
		return "-"
	}
	return strings.Join(filtered, ",")
}

func nextConversationSessionID() string {
	seq := conversationSessionCounter.Add(1)
	return fmt.Sprintf("s-%d-%d", time.Now().UTC().UnixNano(), seq)
}

func flagProvided(args []string, flagName string) bool {
	flagName = strings.TrimSpace(flagName)
	if flagName == "" {
		return false
	}
	short := "-" + flagName
	long := "--" + flagName
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == short || trimmed == long {
			return true
		}
		if strings.HasPrefix(trimmed, short+"=") || strings.HasPrefix(trimmed, long+"=") {
			return true
		}
	}
	return false
}

func rejectRemovedExecutionFlags(args []string) error {
	removed := map[string]string{
		"exec-mode":      "-permission-mode",
		"bash-strategy":  "-permission-mode",
		"bash-allowlist": "sandbox policy and host escalation approval flow",
		"bash-deny-meta": "-permission-mode",
	}
	for _, arg := range args {
		for flagName, replacement := range removed {
			short := "-" + flagName
			long := "--" + flagName
			if arg == short || arg == long || strings.HasPrefix(arg, short+"=") || strings.HasPrefix(arg, long+"=") {
				return fmt.Errorf("flag %q has been removed, use %s instead", flagName, replacement)
			}
		}
	}
	return nil
}

func hasLSPTools(tools []tool.Tool) bool {
	for _, one := range tools {
		if one == nil {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(one.Name())), "LSP_") {
			return true
		}
	}
	return false
}

func parseReasoning(mode string, budget int, effort string, provider string, modelName string) (model.ReasoningConfig, error) {
	cfg := model.ReasoningConfig{Effort: strings.TrimSpace(effort)}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
		// For "auto" mode, look up the model catalog to decide.
		// If the model is known to support reasoning, enable it.
		caps, found := modelproviders.LookupModelCapabilities(provider, modelName)
		if found && caps.SupportsReasoning {
			enabled := true
			cfg.Enabled = &enabled
			if budget > 0 {
				cfg.BudgetTokens = budget
			}
		}
		return cfg, nil
	case "on":
		enabled := true
		cfg.Enabled = &enabled
		if budget > 0 {
			cfg.BudgetTokens = budget
		}
		return cfg, nil
	case "off":
		enabled := false
		cfg.Enabled = &enabled
		cfg.BudgetTokens = 0
		return cfg, nil
	default:
		return model.ReasoningConfig{}, fmt.Errorf("invalid thinking-mode %q, expected auto|on|off", mode)
	}
}

// defaultModelCapsOverridePath returns the default path for the user's local
// model capability override file, following the same ~/.agents/ convention
// as the MCP config file.
func defaultModelCapsOverridePath() string {
	return "~/.agents/model_capabilities.json"
}

func exitErr(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
