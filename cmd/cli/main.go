package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	launcherfull "github.com/OnslaughtSnail/caelis/cmd/launcher/full"
	"github.com/OnslaughtSnail/caelis/internal/idutil"
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
	taskfilestore "github.com/OnslaughtSnail/caelis/kernel/task/filestore"
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
		sandboxType      = fs.String("sandbox-type", configStore.SandboxType(), "Sandbox backend type when permission-mode=default")
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
		_ = toolexec.Close(execRuntime)
		return fmt.Errorf("sandbox type %q is unavailable: %s", strings.TrimSpace(*sandboxType), execRuntime.FallbackReason())
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
	resolved, err := bootstrap.Assemble(ctx, bootstrap.AssembleSpec{
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

	if singleShotMode {
		if llm == nil {
			return fmt.Errorf("no model configured, run /connect first or pass -model with a configured provider/model")
		}
		if strings.HasPrefix(strings.TrimSpace(singleInput), "/compact") {
			note := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(singleInput), "/compact"))
			fmt.Println("note: 正在压缩上下文...")
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

type buildAgentInput struct {
	AppName                     string
	WorkspaceDir                string
	EnableExperimentalLSPPrompt bool
	BasePrompt                  string
	SkillDirs                   []string
	StreamModel                 bool
	ThinkingBudget              int
	ReasoningEffort             string
	ModelProvider               string
	ModelName                   string
	ModelConfig                 modelproviders.Config
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

	reasoning, err := parseReasoningEffortForConfig(in.ReasoningEffort, in.ThinkingBudget, in.ModelProvider, in.ModelName, in.ModelConfig)
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

func appendProviderIfMissing(providers []string, name string) []string {
	if includesProvider(providers, name) {
		return providers
	}
	return append(providers, name)
}

func includesProvider(providers []string, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, provider := range providers {
		if strings.TrimSpace(provider) == name {
			return true
		}
	}
	return false
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
			lines = append(lines, "- Escalation: use require_escalated=true only when sandbox limits are blocking a necessary next step.")
		} else {
			lines = append(lines, "- Rule: commands run in sandbox by default; use require_escalated=true only when sandbox limits are blocking a necessary next step.")
			lines = append(lines, "- Escalate for cases like browser/GUI launch, downloads that sandbox blocks, or writes/access outside sandbox; do not escalate preemptively.")
			lines = append(lines, "- Safe inspection commands may auto-pass host escalation without user approval.")
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
	return idutil.NewSessionID()
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
	return parseReasoningForConfig(mode, budget, effort, provider, modelName, modelproviders.Config{})
}

func parseReasoningForConfig(mode string, budget int, effort string, provider string, modelName string, providerCfg modelproviders.Config) (model.ReasoningConfig, error) {
	rawEffort := normalizeReasoningLevel(effort)
	selection := normalizeReasoningSelection(mode)
	selectionCfg := providerCfg
	if strings.TrimSpace(selectionCfg.Provider) == "" {
		selectionCfg.Provider = provider
	}
	if strings.TrimSpace(selectionCfg.Model) == "" {
		selectionCfg.Model = modelName
	}
	switch selection {
	case "", "auto":
	case "on":
		if rawEffort == "" {
			if opt, err := resolveModelReasoningOption(selectionCfg, "on"); err == nil {
				rawEffort = opt.ReasoningEffort
			}
		}
	case "off":
		rawEffort = "none"
	default:
		return model.ReasoningConfig{}, fmt.Errorf("invalid thinking-mode %q, expected auto|on|off", mode)
	}
	return parseReasoningEffortForConfig(rawEffort, budget, provider, modelName, providerCfg)
}

func parseReasoningEffortForConfig(effort string, budget int, provider string, modelName string, providerCfg modelproviders.Config) (model.ReasoningConfig, error) {
	cfg := model.ReasoningConfig{Effort: normalizeReasoningLevel(effort)}
	if budget > 0 {
		cfg.BudgetTokens = budget
	}
	profile := reasoningProfileForConfig(providerCfg)
	if profile.Mode == reasoningModeNone {
		profile = reasoningProfileForModel(provider, modelName)
	}
	switch profile.Mode {
	case reasoningModeNone:
		cfg.Effort = ""
		cfg.BudgetTokens = 0
	case reasoningModeFixed:
		cfg.Effort = ""
		cfg.BudgetTokens = 0
	case reasoningModeToggle:
		switch cfg.Effort {
		case "":
			if cfg.Effort == "" {
				cfg.BudgetTokens = 0
			}
		case "none":
			cfg.BudgetTokens = 0
		default:
			if profile.DefaultEffort != "" {
				cfg.Effort = profile.DefaultEffort
			} else {
				cfg.Effort = "medium"
			}
		}
	case reasoningModeEffort:
		if cfg.Effort == "none" {
			if len(profile.SupportedEfforts) > 0 && !catalogSupportsReasoningEffortList(profile.SupportedEfforts, "none") {
				cfg.Effort = profile.DefaultEffort
			} else {
				cfg.BudgetTokens = 0
				break
			}
		}
		if cfg.Effort != "" && !catalogSupportsReasoningEffortList(profile.SupportedEfforts, cfg.Effort) {
			cfg.Effort = profile.DefaultEffort
		}
	}
	return cfg, nil
}

func exitErr(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
