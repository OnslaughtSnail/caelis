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
	"github.com/OnslaughtSnail/caelis/kernel/lspadapter/gopls"
	"github.com/OnslaughtSnail/caelis/kernel/lspbroker"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	"github.com/OnslaughtSnail/caelis/kernel/plugin"
	pluginbuiltin "github.com/OnslaughtSnail/caelis/kernel/plugin/builtin"
	"github.com/OnslaughtSnail/caelis/kernel/promptpipeline"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session/filestore"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolmcp "github.com/OnslaughtSnail/caelis/kernel/tool/mcptoolset"
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
		toolProviders    = fs.String("tool-providers", pluginbuiltin.ProviderLocalTools+","+pluginbuiltin.ProviderWorkspaceTools+","+pluginbuiltin.ProviderShellTools+","+pluginbuiltin.ProviderLSPActivation+","+pluginbuiltin.ProviderMCPTools, "Comma-separated tool providers")
		policyProviders  = fs.String("policy-providers", pluginbuiltin.ProviderDefaultPolicy, "Comma-separated policy providers")
		modelAlias       = fs.String("model", configStore.DefaultModel(), "Model alias")
		appName          = fs.String("app", initialAppName, "App name")
		userID           = fs.String("user", "local-user", "User id")
		sessionID        = fs.String("session", "default", "Session id")
		input            = fs.String("input", "", "Input text")
		storeDir         = fs.String("store-dir", defaultStoreDir, "Local event store directory")
		sessionIndexFile = fs.String("session-index", defaultSessionIndexPath, "Session index sqlite file path")
		systemPrompt     = fs.String("system-prompt", "You are a helpful assistant.", "Base system prompt")
		promptConfigDir  = fs.String("prompt-config-dir", "", "Prompt config directory (default ~/.{app}/prompts)")
		credentialStore  = fs.String("credential-store", configStore.CredentialStoreMode(), "Credential store mode: auto|file|ephemeral")
		skillsDirs       = fs.String("skills-dirs", "~/.agents/skills,.agents/skills", "Comma-separated skill directories")
		streamModel      = fs.Bool("stream", configStore.StreamModel(), "Enable model streaming mode")
		thinkingMode     = fs.String("thinking-mode", configStore.ThinkingMode(), "Thinking mode: auto|on|off")
		thinkingBudget   = fs.Int("thinking-budget", configStore.ThinkingBudget(), "Thinking token budget when thinking-mode=on")
		reasoningEffort  = fs.String("reasoning-effort", configStore.ReasoningEffort(), "Reasoning effort hint: low|medium|high")
		compactWatermark = fs.Float64("compact-watermark", 0.7, "Auto compaction watermark ratio (0.5-0.9)")
		contextWindow    = fs.Int("context-window", 0, "Model context window tokens override")
		permissionMode   = fs.String("permission-mode", configStore.PermissionMode(), "Permission mode: default|full_control")
		sandboxType      = fs.String("sandbox-type", configStore.SandboxType(), "Sandbox backend type when permission-mode=default")
		safeCommands     = fs.String("safe-commands", "", "Comma-separated safe base commands for sandbox execution")
		mcpConfigPath    = fs.String("mcp-config", defaultMCPConfigPath(), "MCP config JSON path (default ~/.agents/mcp_servers.json)")
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
	if !flagProvided(args, "session") {
		*sessionID = nextConversationSessionID()
	}
	if err := configStore.SetCredentialStoreMode(*credentialStore); err != nil {
		return err
	}
	credentials, err := loadOrInitCredentialStore(initialAppName, *credentialStore)
	if err != nil {
		return err
	}
	if err := mergeCredentialStoreProviderTokens(configStore, credentials); err != nil {
		return err
	}
	if err := configStore.SetRuntimeSettings(runtimeSettings{
		StreamModel:     *streamModel,
		ThinkingMode:    *thinkingMode,
		ThinkingBudget:  *thinkingBudget,
		ReasoningEffort: *reasoningEffort,
		ShowReasoning:   configStore.ShowReasoning(),
		PermissionMode:  *permissionMode,
		SandboxType:     *sandboxType,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warn: persist runtime settings failed: %v\n", err)
	}

	workspace, err := resolveWorkspaceContext()
	if err != nil {
		return err
	}
	historyPath, err := historyFilePath(initialAppName, workspace.Key)
	if err != nil {
		return err
	}

	execRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionMode(strings.TrimSpace(*permissionMode)),
		SandboxType:    strings.TrimSpace(*sandboxType),
		SafeCommands:   splitCSV(*safeCommands),
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
	lspBroker := lspbroker.New()
	goAdapter, err := gopls.New(gopls.Config{Runtime: execRuntime})
	if err != nil {
		return err
	}
	if err := lspBroker.RegisterAdapter(goAdapter); err != nil {
		return err
	}
	pluginRegistry := plugin.NewRegistry()
	if err := pluginbuiltin.RegisterAll(pluginRegistry, pluginbuiltin.RegisterOptions{
		ExecutionRuntime: execRuntime,
		MCPToolManager:   mcpManager,
	}); err != nil {
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
	lspActivationTools := lspActivationToolNames(resolved.Tools)

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
			Enabled:        true,
			WatermarkRatio: *compactWatermark,
		},
	})
	if err != nil {
		return err
	}

	if strings.TrimSpace(*input) != "" {
		if llm == nil {
			return fmt.Errorf("no model configured, run /connect first or pass -model with a configured provider/model")
		}
		if strings.HasPrefix(strings.TrimSpace(*input), "/compact") {
			note := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(*input), "/compact"))
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
			EnableLSPRoutingPolicy: hasToolName(resolved.Tools, "LSP_ACTIVATE"),
			BasePrompt:             *systemPrompt,
			RuntimeHint:            buildRuntimePromptHint(execRuntime),
			SkillDirs:              splitCSV(*skillsDirs),
			StreamModel:            *streamModel,
			ThinkingMode:           *thinkingMode,
			ThinkingBudget:         *thinkingBudget,
			ReasoningEffort:        *reasoningEffort,
		})
		if err != nil {
			return err
		}
		return runOnce(ctx, rt, runtime.RunRequest{
			AppName:             *appName,
			UserID:              *userID,
			SessionID:           *sessionID,
			Input:               *input,
			Agent:               ag,
			Model:               llm,
			Tools:               resolved.Tools,
			CoreTools:           tool.CoreToolsConfig{Runtime: execRuntime},
			Policies:            resolved.Policies,
			LSPBroker:           lspBroker,
			LSPActivationTools:  lspActivationTools,
			AutoActivateLSP:     autoActivateLSPLanguages(workspace.CWD, resolved.Tools),
			ContextWindowTokens: *contextWindow,
		}, runRenderConfig{
			ShowReasoning: configStore.ShowReasoning(),
			Writer:        os.Stdout,
		})
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
		AutoActivateLSP:        autoActivateLSPLanguages(workspace.CWD, resolved.Tools),
		LSPActivationTools:     lspActivationTools,
		ModelAlias:             alias,
		Model:                  llm,
		ModelFactory:           factory,
		ConfigStore:            configStore,
		CredentialStore:        credentials,
		SessionIndex:           index,
		SystemPrompt:           *systemPrompt,
		PromptConfigDir:        *promptConfigDir,
		EnableLSPRoutingPolicy: hasToolName(resolved.Tools, "LSP_ACTIVATE"),
		SkillDirs:              splitCSV(*skillsDirs),
		StreamModel:            *streamModel,
		ThinkingMode:           *thinkingMode,
		ThinkingBudget:         *thinkingBudget,
		ReasoningEffort:        *reasoningEffort,
		ShowReasoning:          configStore.ShowReasoning(),
		HistoryFile:            historyPath,
		LSPBroker:              lspBroker,
		Version:                version.String(),
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

	reasoning, err := parseReasoning(in.ThinkingMode, in.ThinkingBudget, in.ReasoningEffort)
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
	if safeSummary := summarizeSafeCommands(execRuntime.SafeCommands(), 10); safeSummary != "-" {
		lines = append(lines, "- safe_commands="+safeSummary)
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

func summarizeSafeCommands(commands []string, limit int) string {
	if len(commands) == 0 {
		return "-"
	}
	if limit <= 0 {
		limit = 1
	}
	normalized := make([]string, 0, len(commands))
	seen := map[string]struct{}{}
	for _, one := range commands {
		trimmed := strings.TrimSpace(one)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	if len(normalized) == 0 {
		return "-"
	}
	if len(normalized) <= limit {
		return strings.Join(normalized, ",")
	}
	return fmt.Sprintf("%s,+%d more", strings.Join(normalized[:limit], ","), len(normalized)-limit)
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
		"bash-allowlist": "-safe-commands",
		"bash-deny-meta": "-permission-mode",
	}
	for _, arg := range args {
		for flagName, replacement := range removed {
			short := "-" + flagName
			long := "--" + flagName
			if arg == short || arg == long || strings.HasPrefix(arg, short+"=") || strings.HasPrefix(arg, long+"=") {
				return fmt.Errorf("flag %q 已移除，请使用 %s", flagName, replacement)
			}
		}
	}
	return nil
}

func autoActivateLSPLanguages(workspaceDir string, tools []tool.Tool) []string {
	if !hasToolName(tools, "LSP_ACTIVATE") {
		return nil
	}
	if strings.TrimSpace(workspaceDir) == "" {
		return nil
	}
	goModPath := filepath.Join(workspaceDir, "go.mod")
	if _, err := os.Stat(goModPath); err == nil {
		return []string{"go"}
	}
	matches, err := filepath.Glob(filepath.Join(workspaceDir, "*.go"))
	if err == nil && len(matches) > 0 {
		return []string{"go"}
	}
	return nil
}

func lspActivationToolNames(tools []tool.Tool) []string {
	if hasToolName(tools, "LSP_ACTIVATE") {
		return []string{"LSP_ACTIVATE"}
	}
	return nil
}

func hasToolName(tools []tool.Tool, name string) bool {
	target := strings.TrimSpace(name)
	if target == "" {
		return false
	}
	for _, one := range tools {
		if one == nil {
			continue
		}
		if one.Name() == target {
			return true
		}
	}
	return false
}

func parseReasoning(mode string, budget int, effort string) (model.ReasoningConfig, error) {
	cfg := model.ReasoningConfig{Effort: strings.TrimSpace(effort)}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
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

func exitErr(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
