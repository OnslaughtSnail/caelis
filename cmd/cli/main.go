package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	launcherfull "github.com/OnslaughtSnail/caelis/cmd/launcher/full"
	"github.com/OnslaughtSnail/caelis/internal/envload"
	"github.com/OnslaughtSnail/caelis/internal/version"
	"github.com/OnslaughtSnail/caelis/kernel/bootstrap"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/llmagent"
	"github.com/OnslaughtSnail/caelis/kernel/lspadapter/gopls"
	"github.com/OnslaughtSnail/caelis/kernel/lspbroker"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	pluginbuiltin "github.com/OnslaughtSnail/caelis/kernel/plugin/builtin"
	"github.com/OnslaughtSnail/caelis/kernel/promptpipeline"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/filestore"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolmcp "github.com/OnslaughtSnail/caelis/kernel/tool/mcptoolset"
)

func main() {
	launcher := launcherfull.NewLauncher(
		runCLI,
		notImplementedLauncher("api"),
		notImplementedLauncher("web"),
	)
	if err := launcher.Execute(context.Background(), os.Args[1:]); err != nil {
		if strings.Contains(os.Getenv("DEBUG"), "1") {
			fmt.Fprintf(os.Stderr, "%s\n", launcher.CommandLineSyntax())
		}
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
		maxSteps         = fs.Int("max-steps", configStore.MaxSteps(), "Max LLM-tool loop steps (0 means unlimited)")
		credentialStore  = fs.String("credential-store", configStore.CredentialStoreMode(), "Credential store mode: auto|file|ephemeral")
		skillsDirs       = fs.String("skills-dirs", "~/.agents/skills,.agents/skills", "Comma-separated skill directories")
		streamModel      = fs.Bool("stream", false, "Enable model streaming mode")
		thinkingMode     = fs.String("thinking-mode", "auto", "Thinking mode: auto|on|off")
		thinkingBudget   = fs.Int("thinking-budget", 1024, "Thinking token budget when thinking-mode=on")
		reasoningEffort  = fs.String("reasoning-effort", "", "Reasoning effort hint: low|medium|high")
		compactWatermark = fs.Float64("compact-watermark", 0.7, "Auto compaction watermark ratio (0.5-0.9)")
		contextWindow    = fs.Int("context-window", 0, "Model context window tokens override")
		execMode         = fs.String("exec-mode", string(toolexec.ModeNoSandbox), "Tool execution mode: no_sandbox|sandbox")
		sandboxType      = fs.String("sandbox-type", "", "Sandbox backend type when exec-mode=sandbox")
		bashStrategy     = fs.String("bash-strategy", string(toolexec.BashStrategyAuto), "BASH strategy: auto|full_access|agent_decided|strict (strict asks approval for non-allowlist or meta commands)")
		bashAllowlist    = fs.String("bash-allowlist", "", "Comma-separated allowed base commands for BASH strategy")
		bashDenyMeta     = fs.Bool("bash-deny-meta", true, "Deny shell meta chars in strict/agent_decided strategy")
		mcpConfigPath    = fs.String("mcp-config", defaultMCPConfigPath(), "MCP config JSON path (default ~/.agents/mcp_servers.json)")
		showVersion      = fs.Bool("version", false, "Show version and exit")
	)
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
	if err := configStore.SetCredentialStoreMode(*credentialStore); err != nil {
		return err
	}
	credentials, err := loadOrInitCredentialStore(initialAppName, *credentialStore)
	if err != nil {
		return err
	}
	if err := migrateInlineProviderTokens(configStore, credentials); err != nil {
		return err
	}

	if _, err := envload.LoadNearest(); err != nil {
		return err
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
		Mode:        toolexec.Mode(strings.TrimSpace(*execMode)),
		SandboxType: strings.TrimSpace(*sandboxType),
		BashPolicy: toolexec.BashPolicy{
			Strategy:      toolexec.BashStrategy(strings.TrimSpace(*bashStrategy)),
			Allowlist:     splitCSV(*bashAllowlist),
			DenyMetaChars: *bashDenyMeta,
		},
	})
	if err != nil {
		return err
	}
	ctx = pluginbuiltin.WithExecutionRuntime(ctx, execRuntime)
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
		ctx = pluginbuiltin.WithMCPToolManager(ctx, mcpManager)
	}
	lspBroker := lspbroker.New()
	goAdapter, err := gopls.New(gopls.Config{Runtime: execRuntime})
	if err != nil {
		return err
	}
	if err := lspBroker.RegisterAdapter(goAdapter); err != nil {
		return err
	}

	resolved, err := bootstrap.Assemble(ctx, bootstrap.AssembleSpec{
		ToolProviders:   splitCSV(*toolProviders),
		PolicyProviders: splitCSV(*policyProviders),
	})
	if err != nil {
		return err
	}

	factory := modelproviders.NewFactory()
	for _, providerCfg := range configStore.ProviderConfigs() {
		providerCfg = hydrateProviderAuthToken(providerCfg, credentials)
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
			MaxSteps:               *maxSteps,
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
			ContextWindowTokens: *contextWindow,
		}, runRenderConfig{
			ShowReasoning: true,
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
		ModelAlias:             alias,
		Model:                  llm,
		ModelFactory:           factory,
		ConfigStore:            configStore,
		CredentialStore:        credentials,
		SessionIndex:           index,
		SystemPrompt:           *systemPrompt,
		PromptConfigDir:        *promptConfigDir,
		EnableLSPRoutingPolicy: hasToolName(resolved.Tools, "LSP_ACTIVATE"),
		MaxSteps:               *maxSteps,
		SkillDirs:              splitCSV(*skillsDirs),
		StreamModel:            *streamModel,
		ThinkingMode:           *thinkingMode,
		ThinkingBudget:         *thinkingBudget,
		ReasoningEffort:        *reasoningEffort,
		ShowReasoning:          true,
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
	MaxSteps               int
	SkillDirs              []string
	StreamModel            bool
	ThinkingMode           string
	ThinkingBudget         int
	ReasoningEffort        string
}

func buildAgent(in buildAgentInput) (*llmagent.Agent, error) {
	assembled, err := promptpipeline.Assemble(promptpipeline.AssembleSpec{
		AppName:                in.AppName,
		WorkspaceDir:           in.WorkspaceDir,
		BasePrompt:             in.BasePrompt,
		SkillDirs:              in.SkillDirs,
		ConfigDir:              in.PromptConfigDir,
		EnableLSPRoutingPolicy: in.EnableLSPRoutingPolicy,
	})
	if err != nil {
		return nil, err
	}
	for _, warn := range assembled.Warnings {
		fmt.Fprintf(os.Stderr, "warn: %v\n", warn)
	}

	reasoning, err := parseReasoning(in.ThinkingMode, in.ThinkingBudget, in.ReasoningEffort)
	if err != nil {
		return nil, err
	}

	return llmagent.New(llmagent.Config{
		Name:              "main",
		SystemPrompt:      assembled.Prompt,
		MaxSteps:          in.MaxSteps,
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

type runRenderConfig struct {
	ShowReasoning bool
	Writer        io.Writer
}

func runOnce(ctx context.Context, rt *runtime.Runtime, req runtime.RunRequest, renderCfg runRenderConfig) error {
	invokeCtx := ctx
	out := renderCfg.Writer
	if out == nil {
		out = os.Stdout
	}
	render := &renderState{
		showReasoning: renderCfg.ShowReasoning,
		out:           out,
	}
	for ev, err := range rt.Run(invokeCtx, req) {
		if err != nil {
			return err
		}
		if ev == nil {
			continue
		}
		printEvent(ev, render)
	}
	if render.partialOpen {
		fmt.Fprintln(render.out)
	}
	return nil
}

type renderState struct {
	partialOpen          bool
	partialChannel       string
	seenAnswerPartial    bool
	seenReasoningPartial bool
	showReasoning        bool
	out                  io.Writer
}

func printEvent(ev *session.Event, state *renderState) {
	if ev == nil {
		return
	}
	if state != nil && eventIsPartial(ev) {
		channel := eventChannel(ev)
		chunk := ev.Message.Text
		if channel == "reasoning" {
			chunk = ev.Message.Reasoning
			if !state.showReasoning {
				return
			}
		}
		if chunk == "" {
			return
		}
		if state.partialOpen && state.partialChannel != channel {
			fmt.Fprintln(state.out)
			state.partialOpen = false
		}
		if !state.partialOpen {
			if channel == "reasoning" {
				fmt.Fprint(state.out, "~ ")
			} else {
				fmt.Fprint(state.out, "* ")
			}
		}
		fmt.Fprint(state.out, chunk)
		state.partialOpen = true
		state.partialChannel = channel
		if channel == "reasoning" {
			state.seenReasoningPartial = true
		} else {
			state.seenAnswerPartial = true
		}
		return
	}
	if state != nil && state.partialOpen {
		fmt.Fprintln(state.out)
		state.partialOpen = false
	}

	msg := ev.Message
	if msg.Role == model.RoleUser {
		return
	}
	if msg.ToolResponse != nil {
		fmt.Fprintf(state.out, "= %s %s\n", msg.ToolResponse.Name, summarizeToolResponse(msg.ToolResponse.Result))
		return
	}
	if len(msg.ToolCalls) > 0 {
		for i, call := range msg.ToolCalls {
			fmt.Fprintf(state.out, "#%d %s %s\n", i+1, call.Name, summarizeToolArgs(call.Args))
		}
		return
	}
	if msg.Role == model.RoleAssistant && state != nil && state.showReasoning && msg.Reasoning != "" && !state.seenReasoningPartial {
		fmt.Fprintf(state.out, "~ %s\n", strings.TrimSpace(msg.Reasoning))
	}
	if msg.Role == model.RoleAssistant && state != nil && state.seenAnswerPartial && msg.Text != "" {
		// Streaming mode already printed partial answer chunks.
	} else {
		text := strings.TrimSpace(msg.Text)
		if text != "" {
			switch msg.Role {
			case model.RoleAssistant:
				fmt.Fprintf(state.out, "* %s\n", text)
			case model.RoleSystem:
				fmt.Fprintf(state.out, "! %s\n", text)
			default:
				fmt.Fprintf(state.out, "- %s\n", text)
			}
		}
	}
	if state != nil && msg.Role == model.RoleAssistant {
		state.seenAnswerPartial = false
		state.seenReasoningPartial = false
	}
}

func summarizeToolArgs(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := fmt.Sprint(args[key])
		parts = append(parts, fmt.Sprintf("%s=%s", key, truncateInline(value, 72)))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func summarizeToolResponse(result map[string]any) string {
	if len(result) == 0 {
		return "{}"
	}
	if value := firstNonEmpty(result, "error", "stderr", "message"); value != "" {
		return truncateInline(value, 160)
	}
	if value := firstNonEmpty(result, "summary", "output", "result"); value != "" {
		return truncateInline(value, 160)
	}
	keys := make([]string, 0, len(result))
	for k := range result {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("{keys=%s}", strings.Join(keys, ","))
}

func firstNonEmpty(values map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, ok := values[key]
		if !ok {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(raw))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func truncateInline(input string, limit int) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	rs := []rune(text)
	if limit <= 0 || len(rs) <= limit {
		return text
	}
	if limit <= 3 {
		return string(rs[:limit])
	}
	return string(rs[:limit-3]) + "..."
}

func eventIsPartial(ev *session.Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	raw, ok := ev.Meta["partial"]
	if !ok {
		return false
	}
	flag, ok := raw.(bool)
	return ok && flag
}

func eventChannel(ev *session.Event) string {
	if ev == nil || ev.Meta == nil {
		return "answer"
	}
	raw, ok := ev.Meta["channel"]
	if !ok {
		return "answer"
	}
	channel, ok := raw.(string)
	if !ok || channel == "" {
		return "answer"
	}
	return channel
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
