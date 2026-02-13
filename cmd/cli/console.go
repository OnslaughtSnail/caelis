package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/bootstrap"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/lspbroker"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"
)

type cliConsole struct {
	baseCtx context.Context
	rt      *runtime.Runtime

	appName       string
	userID        string
	sessionID     string
	contextWindow int
	workspace     workspaceContext

	resolved    *bootstrap.ResolvedSpec
	execRuntime toolexec.Runtime
	sandboxType string
	autoLSP     []string
	lspActivate []string
	lspBroker   *lspbroker.Broker

	modelAlias             string
	llm                    model.LLM
	modelFactory           *modelproviders.Factory
	configStore            *appConfigStore
	credentialStore        *credentialStore
	sessionIndex           *sessionIndex
	systemPrompt           string
	promptConfigDir        string
	enableLSPRoutingPolicy bool
	skillDirs              []string
	streamModel            bool
	thinkingMode           string
	thinkingBudget         int
	reasoningEffort        string
	showReasoning          bool
	version                string

	editor   lineEditor
	out      io.Writer
	approver *terminalApprover
	commands map[string]slashCommand

	runMu           sync.Mutex
	activeRunCancel context.CancelFunc
	interruptMu     sync.Mutex
	lastInterruptAt time.Time
}

const interruptExitWindow = 2 * time.Second

type slashCommand struct {
	Usage       string
	Description string
	Handle      func(*cliConsole, []string) (bool, error)
}

func newCLIConsole(cfg cliConsoleConfig) *cliConsole {
	commands := []string{"help", "version", "exit", "new", "compact", "status", "permission", "sandbox", "sessions", "models", "model", "connect", "thinking", "effort", "stream", "reasoning", "tools"}
	editor, _ := newLineEditor(lineEditorConfig{
		HistoryFile: cfg.HistoryFile,
		Commands:    commands,
	})
	var out io.Writer = os.Stdout
	if editor != nil {
		out = editor.Output()
	}
	console := &cliConsole{
		baseCtx:                cfg.BaseContext,
		rt:                     cfg.Runtime,
		appName:                cfg.AppName,
		userID:                 cfg.UserID,
		sessionID:              cfg.SessionID,
		contextWindow:          cfg.ContextWindow,
		workspace:              cfg.Workspace,
		resolved:               cfg.Resolved,
		execRuntime:            cfg.ExecRuntime,
		sandboxType:            strings.TrimSpace(cfg.SandboxType),
		autoLSP:                append([]string(nil), cfg.AutoActivateLSP...),
		lspActivate:            append([]string(nil), cfg.LSPActivationTools...),
		lspBroker:              cfg.LSPBroker,
		modelAlias:             cfg.ModelAlias,
		llm:                    cfg.Model,
		modelFactory:           cfg.ModelFactory,
		configStore:            cfg.ConfigStore,
		credentialStore:        cfg.CredentialStore,
		sessionIndex:           cfg.SessionIndex,
		systemPrompt:           cfg.SystemPrompt,
		promptConfigDir:        cfg.PromptConfigDir,
		enableLSPRoutingPolicy: cfg.EnableLSPRoutingPolicy,
		skillDirs:              append([]string(nil), cfg.SkillDirs...),
		streamModel:            cfg.StreamModel,
		thinkingMode:           cfg.ThinkingMode,
		thinkingBudget:         cfg.ThinkingBudget,
		reasoningEffort:        cfg.ReasoningEffort,
		showReasoning:          cfg.ShowReasoning,
		version:                strings.TrimSpace(cfg.Version),
		editor:                 editor,
		out:                    out,
	}
	safeCommands := []string(nil)
	if cfg.ExecRuntime != nil {
		safeCommands = cfg.ExecRuntime.SafeCommands()
	}
	console.approver = newTerminalApprover(editor, out, safeCommands)
	console.commands = map[string]slashCommand{
		"help":    {Usage: "/help", Description: "显示命令帮助", Handle: handleHelp},
		"version": {Usage: "/version", Description: "显示版本信息", Handle: handleVersion},
		"exit":    {Usage: "/exit", Description: "退出 CLI", Handle: handleExit},
		"new":     {Usage: "/new", Description: "开始新的对话会话", Handle: handleNew},
		"compact": {Usage: "/compact [note]", Description: "手动触发一次上下文压缩", Handle: handleCompact},
		"status":  {Usage: "/status", Description: "查看当前会话配置", Handle: handleStatus},
		"permission": {
			Usage:       "/permission [default|full_control]",
			Description: "查看或切换权限模式",
			Handle:      handlePermission,
		},
		"sandbox": {
			Usage:       "/sandbox [<type>]",
			Description: "查看或切换 sandbox 配置",
			Handle:      handleSandbox,
		},
		"models":   {Usage: "/models", Description: "列出可用模型别名", Handle: handleModels},
		"model":    {Usage: "/model <alias>", Description: "切换当前模型", Handle: handleModel},
		"connect":  {Usage: "/connect", Description: "交互式添加/更新模型 Provider", Handle: handleConnect},
		"thinking": {Usage: "/thinking <auto|on|off> [budget]", Description: "切换思考模式与预算", Handle: handleThinking},
		"effort":   {Usage: "/effort <low|medium|high|off>", Description: "设置 reasoning effort", Handle: handleEffort},
		"stream":   {Usage: "/stream <on|off>", Description: "切换流式输出", Handle: handleStream},
		"reasoning": {Usage: "/reasoning <on|off>", Description: "切换 reasoning 内容展示",
			Handle: handleReasoning},
		"tools":    {Usage: "/tools", Description: "查看当前工具列表", Handle: handleTools},
		"sessions": {Usage: "/sessions [resume <session-id>]", Description: "列出并切换当前工作区会话", Handle: handleSessions},
	}
	return console
}

type cliConsoleConfig struct {
	BaseContext            context.Context
	Runtime                *runtime.Runtime
	AppName                string
	UserID                 string
	SessionID              string
	ContextWindow          int
	Workspace              workspaceContext
	Resolved               *bootstrap.ResolvedSpec
	ExecRuntime            toolexec.Runtime
	SandboxType            string
	AutoActivateLSP        []string
	LSPActivationTools     []string
	LSPBroker              *lspbroker.Broker
	ModelAlias             string
	Model                  model.LLM
	ModelFactory           *modelproviders.Factory
	ConfigStore            *appConfigStore
	CredentialStore        *credentialStore
	SessionIndex           *sessionIndex
	SystemPrompt           string
	PromptConfigDir        string
	EnableLSPRoutingPolicy bool
	SkillDirs              []string
	StreamModel            bool
	ThinkingMode           string
	ThinkingBudget         int
	ReasoningEffort        string
	ShowReasoning          bool
	HistoryFile            string
	Version                string
}

func (c *cliConsole) loop() error {
	c.printf("Interactive mode. /help 查看命令。\n")
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt)
	exitCh := make(chan struct{}, 1)
	stopSignals := make(chan struct{})
	go c.handleInterruptSignals(sigCh, exitCh, stopSignals)
	defer func() {
		close(stopSignals)
		signal.Stop(sigCh)
		if c.editor != nil {
			_ = c.editor.Close()
		}
		if closeErr := toolexec.Close(c.execRuntime); closeErr != nil {
			c.printf("warn: close execution runtime failed: %v\n", closeErr)
		}
	}()
	for {
		select {
		case <-exitCh:
			c.printf("\n")
			return nil
		default:
		}
		line, err := c.editor.ReadLine("> ")
		if err != nil {
			if errors.Is(err, errInputInterrupt) {
				if c.registerInterruptAndShouldExit() {
					c.printf("\n")
					return nil
				}
				c.printf("\n")
				continue
			}
			if errors.Is(err, errInputEOF) {
				c.printf("\n")
				return nil
			}
			return err
		}
		if line == "" {
			c.resetInterruptWindow()
			continue
		}
		c.resetInterruptWindow()
		if strings.HasPrefix(line, "/") {
			exitNow, err := c.handleSlash(line)
			if err != nil {
				fmt.Fprintf(c.out, "error: %v\n", err)
			}
			if exitNow {
				return nil
			}
			continue
		}
		if err := c.runPrompt(line); err != nil {
			if errors.Is(err, context.Canceled) {
				c.printf("! execution interrupted\n")
				continue
			}
			fmt.Fprintf(c.out, "error: %v\n", err)
		}
	}
}

func (c *cliConsole) handleInterruptSignals(sigCh <-chan os.Signal, exitCh chan<- struct{}, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-sigCh:
			if c.cancelActiveRun() {
				c.noteInterrupt()
				continue
			}
			// readline already reports Ctrl+C via errInputInterrupt; avoid
			// double-counting the same keypress as two interrupts.
			if c.usesReadlineEditor() {
				continue
			}
			if c.registerInterruptAndShouldExit() {
				select {
				case exitCh <- struct{}{}:
				default:
				}
			}
		}
	}
}

func (c *cliConsole) handleSlash(line string) (bool, error) {
	parts := strings.Fields(strings.TrimPrefix(line, "/"))
	if len(parts) == 0 {
		return false, nil
	}
	cmd := strings.ToLower(parts[0])
	handler, ok := c.commands[cmd]
	if !ok {
		return false, fmt.Errorf("unknown command %q, use /help", cmd)
	}
	return handler.Handle(c, parts[1:])
}

func (c *cliConsole) runPrompt(input string) error {
	if c.llm == nil {
		return fmt.Errorf("no model configured, use /connect to add provider and select model")
	}
	ag, err := buildAgent(buildAgentInput{
		AppName:                c.appName,
		WorkspaceDir:           c.workspace.CWD,
		PromptConfigDir:        c.promptConfigDir,
		EnableLSPRoutingPolicy: c.enableLSPRoutingPolicy,
		BasePrompt:             c.systemPrompt,
		RuntimeHint:            buildRuntimePromptHint(c.execRuntime),
		SkillDirs:              c.skillDirs,
		StreamModel:            c.streamModel,
		ThinkingMode:           c.thinkingMode,
		ThinkingBudget:         c.thinkingBudget,
		ReasoningEffort:        c.reasoningEffort,
	})
	if err != nil {
		return err
	}
	ctx := toolexec.WithApprover(c.baseCtx, c.approver)
	runCtx, cancel := context.WithCancel(ctx)
	c.setActiveRunCancel(cancel)
	defer func() {
		c.clearActiveRunCancel()
		cancel()
	}()
	return runOnce(runCtx, c.rt, runtime.RunRequest{
		AppName:             c.appName,
		UserID:              c.userID,
		SessionID:           c.sessionID,
		Input:               input,
		Agent:               ag,
		Model:               c.llm,
		Tools:               c.resolved.Tools,
		CoreTools:           tool.CoreToolsConfig{Runtime: c.execRuntime},
		Policies:            c.resolved.Policies,
		LSPBroker:           c.lspBroker,
		LSPActivationTools:  append([]string(nil), c.lspActivate...),
		AutoActivateLSP:     append([]string(nil), c.autoLSP...),
		ContextWindowTokens: c.contextWindow,
	}, runRenderConfig{
		ShowReasoning: c.showReasoning,
		Writer:        c.out,
	})
}

func (c *cliConsole) setActiveRunCancel(cancel context.CancelFunc) {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	c.activeRunCancel = cancel
}

func (c *cliConsole) clearActiveRunCancel() {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	c.activeRunCancel = nil
}

func (c *cliConsole) cancelActiveRun() bool {
	c.runMu.Lock()
	cancel := c.activeRunCancel
	c.runMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (c *cliConsole) usesReadlineEditor() bool {
	_, ok := c.editor.(*readlineEditor)
	return ok
}

func (c *cliConsole) noteInterrupt() {
	c.interruptMu.Lock()
	defer c.interruptMu.Unlock()
	c.lastInterruptAt = time.Now()
}

func (c *cliConsole) registerInterruptAndShouldExit() bool {
	c.interruptMu.Lock()
	defer c.interruptMu.Unlock()
	now := time.Now()
	shouldExit := !c.lastInterruptAt.IsZero() && now.Sub(c.lastInterruptAt) <= interruptExitWindow
	c.lastInterruptAt = now
	return shouldExit
}

func (c *cliConsole) resetInterruptWindow() {
	c.interruptMu.Lock()
	defer c.interruptMu.Unlock()
	c.lastInterruptAt = time.Time{}
}

func handleHelp(c *cliConsole, args []string) (bool, error) {
	_ = args
	c.printf("Available commands:\n")
	order := []string{"help", "version", "status", "new", "permission", "sandbox", "sessions", "models", "model", "connect", "thinking", "effort", "stream", "reasoning", "tools", "compact", "exit"}
	for _, name := range order {
		cmd := c.commands[name]
		c.printf("  %-24s %s\n", cmd.Usage, cmd.Description)
	}
	return false, nil
}

func handleVersion(c *cliConsole, args []string) (bool, error) {
	_ = args
	if strings.TrimSpace(c.version) == "" {
		c.printf("version=unknown\n")
		return false, nil
	}
	c.printf("version=%s\n", c.version)
	return false, nil
}

func handleExit(c *cliConsole, args []string) (bool, error) {
	_ = c
	_ = args
	return true, nil
}

func handleNew(c *cliConsole, args []string) (bool, error) {
	if len(args) != 0 {
		return false, fmt.Errorf("usage: /new")
	}
	previous := strings.TrimSpace(c.sessionID)
	c.sessionID = nextConversationSessionID()
	if previous == "" {
		c.printf("new session started: %s\n", c.sessionID)
		return false, nil
	}
	c.printf("new session started: %s (from %s)\n", c.sessionID, previous)
	return false, nil
}

func handleCompact(c *cliConsole, args []string) (bool, error) {
	if c.llm == nil {
		return false, fmt.Errorf("no model configured, use /connect first")
	}
	note := strings.TrimSpace(strings.Join(args, " "))
	beforeUsage, _ := c.rt.ContextUsage(c.baseCtx, runtime.UsageRequest{
		AppName:             c.appName,
		UserID:              c.userID,
		SessionID:           c.sessionID,
		Model:               c.llm,
		ContextWindowTokens: c.contextWindow,
	})
	ev, err := c.rt.Compact(c.baseCtx, runtime.CompactRequest{
		AppName:             c.appName,
		UserID:              c.userID,
		SessionID:           c.sessionID,
		Model:               c.llm,
		Note:                note,
		ContextWindowTokens: c.contextWindow,
	})
	if err != nil {
		return false, err
	}
	if ev == nil {
		if beforeUsage.WindowTokens > 0 {
			c.printf("compact: skipped (%s)\n", formatUsage(beforeUsage))
		}
	} else {
		afterUsage, _ := c.rt.ContextUsage(c.baseCtx, runtime.UsageRequest{
			AppName:             c.appName,
			UserID:              c.userID,
			SessionID:           c.sessionID,
			Model:               c.llm,
			ContextWindowTokens: c.contextWindow,
		})
		if afterUsage.WindowTokens > 0 {
			c.printf("compact: success, event_id=%s, %s -> %s\n", ev.ID, formatUsage(beforeUsage), formatUsage(afterUsage))
		} else {
			c.printf("compact: success, event_id=%s\n", ev.ID)
		}
	}
	return false, nil
}

func handleStatus(c *cliConsole, args []string) (bool, error) {
	_ = args
	c.printf("model=%s stream=%v thinking=%s budget=%d effort=%s reasoning_display=%v\n",
		c.modelAlias, c.streamModel, c.thinkingMode, c.thinkingBudget, c.reasoningEffort, c.showReasoning)
	c.printf("workspace=%s session=%s\n", c.workspace.CWD, c.sessionID)
	mode := c.execRuntime.PermissionMode()
	switch mode {
	case toolexec.PermissionModeFullControl:
		c.printf("permission_mode=%s route=host\n", mode)
	default:
		if c.execRuntime.FallbackToHost() {
			c.printf("permission_mode=%s sandbox_type=%s route=host (fallback: host+approval, reason=%s)\n",
				mode, c.execRuntime.SandboxType(), c.execRuntime.FallbackReason())
		} else {
			c.printf("permission_mode=%s sandbox_type=%s route=sandbox\n",
				mode, c.execRuntime.SandboxType())
		}
	}
	c.printf("sandbox_policy=%s\n", runtimePolicyHint(c.execRuntime.SandboxPolicy()))
	if c.rt != nil {
		runState, err := c.rt.RunState(c.baseCtx, runtime.RunStateRequest{
			AppName:   c.appName,
			UserID:    c.userID,
			SessionID: c.sessionID,
		})
		if err != nil {
			return false, err
		}
		if runState.HasLifecycle {
			c.printf("run_state=%s phase=%s\n", runState.Status, stringOrDash(runState.Phase))
			if strings.TrimSpace(runState.Error) != "" {
				c.printf("run_state_error=%s\n", truncateInline(runState.Error, 160))
			}
			if strings.TrimSpace(string(runState.ErrorCode)) != "" {
				c.printf("run_state_error_code=%s\n", runState.ErrorCode)
			}
		} else {
			c.printf("run_state=none\n")
		}
	}
	if c.llm == nil {
		c.printf("context_usage=not available (no model configured)\n")
		return false, nil
	}
	usage, err := c.rt.ContextUsage(c.baseCtx, runtime.UsageRequest{
		AppName:             c.appName,
		UserID:              c.userID,
		SessionID:           c.sessionID,
		Model:               c.llm,
		ContextWindowTokens: c.contextWindow,
	})
	if err != nil {
		return false, err
	}
	c.printf("context_usage=%s (input_budget=%d, events=%d)\n", formatUsage(usage), usage.InputBudget, usage.EventCount)
	return false, nil
}

func handlePermission(c *cliConsole, args []string) (bool, error) {
	if len(args) == 0 {
		c.printf("permission_mode=%s sandbox_type=%s\n", c.execRuntime.PermissionMode(), c.sandboxType)
		return false, nil
	}
	if len(args) != 1 {
		return false, fmt.Errorf("usage: /permission [default|full_control]")
	}
	mode := toolexec.PermissionMode(strings.ToLower(strings.TrimSpace(args[0])))
	switch mode {
	case toolexec.PermissionModeDefault, toolexec.PermissionModeFullControl:
	default:
		return false, fmt.Errorf("invalid permission mode %q, expected default|full_control", args[0])
	}
	if err := c.updateExecutionRuntime(mode, c.sandboxType); err != nil {
		return false, err
	}
	c.persistRuntimeSettings()
	if c.execRuntime.FallbackToHost() {
		c.printf("permission updated: mode=%s sandbox_type=%s (fallback: host+approval, reason=%s)\n", c.execRuntime.PermissionMode(), c.sandboxType, c.execRuntime.FallbackReason())
	} else {
		c.printf("permission updated: mode=%s sandbox_type=%s\n", c.execRuntime.PermissionMode(), c.sandboxType)
	}
	return false, nil
}

func handleSandbox(c *cliConsole, args []string) (bool, error) {
	if len(args) == 0 {
		c.printf("sandbox_type=%s permission_mode=%s\n", c.sandboxType, c.execRuntime.PermissionMode())
		return false, nil
	}
	if len(args) != 1 {
		return false, fmt.Errorf("usage: /sandbox [<type>]")
	}
	sandboxType := strings.TrimSpace(args[0])
	if sandboxType == "" {
		return false, fmt.Errorf("sandbox type cannot be empty")
	}
	safeCommands := []string(nil)
	if c.execRuntime != nil {
		safeCommands = c.execRuntime.SafeCommands()
	}
	// Validate type by constructing a default-mode runtime.
	if _, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    sandboxType,
		SafeCommands:   safeCommands,
	}); err != nil {
		return false, err
	}
	c.sandboxType = sandboxType
	mode := c.execRuntime.PermissionMode()
	if mode == toolexec.PermissionModeFullControl {
		c.persistRuntimeSettings()
		c.printf("sandbox updated: sandbox_type=%s (will apply when permission_mode=default)\n", c.sandboxType)
		return false, nil
	}
	if err := c.updateExecutionRuntime(mode, c.sandboxType); err != nil {
		return false, err
	}
	c.persistRuntimeSettings()
	if c.execRuntime.FallbackToHost() {
		c.printf("sandbox updated: sandbox_type=%s (fallback: host+approval, reason=%s)\n", c.sandboxType, c.execRuntime.FallbackReason())
	} else {
		c.printf("sandbox updated: sandbox_type=%s\n", c.sandboxType)
	}
	return false, nil
}

func handleModels(c *cliConsole, args []string) (bool, error) {
	_ = args
	current := strings.ToLower(strings.TrimSpace(c.modelAlias))
	refs := []string(nil)
	if c.configStore != nil {
		refs = c.configStore.ConfiguredModelRefs()
	}
	if len(refs) > 0 {
		c.printf("models:\n")
		for _, ref := range refs {
			marker := " "
			if ref == current {
				marker = "*"
			}
			c.printf("  %s %s\n", marker, ref)
		}
		return false, nil
	}
	if c.modelFactory == nil {
		return false, fmt.Errorf("no models configured, use /connect")
	}
	list := c.modelFactory.ListModels()
	if len(list) == 0 {
		return false, fmt.Errorf("no models configured, use /connect")
	}
	c.printf("models: %s\n", strings.Join(list, ", "))
	return false, nil
}

func handleModel(c *cliConsole, args []string) (bool, error) {
	if len(args) != 1 {
		return false, fmt.Errorf("usage: /model <alias>")
	}
	if c.modelFactory == nil {
		return false, fmt.Errorf("model factory is not configured")
	}
	alias := strings.TrimSpace(args[0])
	if c.configStore != nil {
		alias = c.configStore.ResolveModelAlias(alias)
	}
	llm, err := c.modelFactory.NewByAlias(alias)
	if err != nil {
		return false, err
	}
	c.modelAlias = strings.ToLower(alias)
	c.llm = llm
	if c.configStore != nil {
		if err := c.configStore.SetDefaultModel(c.modelAlias); err != nil {
			fmt.Fprintf(c.out, "warn: update default model failed: %v\n", err)
		}
	}
	c.printf("model switched to %s\n", alias)
	return false, nil
}

func handleThinking(c *cliConsole, args []string) (bool, error) {
	if len(args) < 1 || len(args) > 2 {
		return false, fmt.Errorf("usage: /thinking <auto|on|off> [budget]")
	}
	mode := strings.ToLower(strings.TrimSpace(args[0]))
	budget := c.thinkingBudget
	if len(args) == 2 {
		value, err := strconv.Atoi(args[1])
		if err != nil || value < 0 {
			return false, fmt.Errorf("invalid budget: %q", args[1])
		}
		budget = value
	}
	if _, err := parseReasoning(mode, budget, c.reasoningEffort); err != nil {
		return false, err
	}
	c.thinkingMode = mode
	c.thinkingBudget = budget
	c.persistRuntimeSettings()
	c.printf("thinking updated: mode=%s budget=%d\n", c.thinkingMode, c.thinkingBudget)
	return false, nil
}

func handleEffort(c *cliConsole, args []string) (bool, error) {
	if len(args) != 1 {
		return false, fmt.Errorf("usage: /effort <low|medium|high|off>")
	}
	value := strings.ToLower(strings.TrimSpace(args[0]))
	switch value {
	case "off":
		c.reasoningEffort = ""
	case "low", "medium", "high":
		c.reasoningEffort = value
	default:
		return false, fmt.Errorf("invalid effort %q", value)
	}
	c.persistRuntimeSettings()
	c.printf("reasoning effort=%s\n", c.reasoningEffort)
	return false, nil
}

func handleStream(c *cliConsole, args []string) (bool, error) {
	if len(args) != 1 {
		return false, fmt.Errorf("usage: /stream <on|off>")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "on":
		c.streamModel = true
	case "off":
		c.streamModel = false
	default:
		return false, fmt.Errorf("usage: /stream <on|off>")
	}
	c.persistRuntimeSettings()
	c.printf("stream=%v\n", c.streamModel)
	return false, nil
}

func handleReasoning(c *cliConsole, args []string) (bool, error) {
	if len(args) != 1 {
		return false, fmt.Errorf("usage: /reasoning <on|off>")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "on":
		c.showReasoning = true
	case "off":
		c.showReasoning = false
	default:
		return false, fmt.Errorf("usage: /reasoning <on|off>")
	}
	c.persistRuntimeSettings()
	c.printf("reasoning display=%v\n", c.showReasoning)
	return false, nil
}

func handleTools(c *cliConsole, args []string) (bool, error) {
	_ = args
	coreTools, err := tool.EnsureCoreTools(c.resolved.Tools, tool.CoreToolsConfig{Runtime: c.execRuntime})
	if err != nil {
		return false, err
	}
	c.printf("tools:\n")
	for _, one := range coreTools {
		if one == nil {
			continue
		}
		c.printf("  - %s\n", one.Name())
	}
	return false, nil
}

func handleSessions(c *cliConsole, args []string) (bool, error) {
	if c.sessionIndex == nil {
		return false, fmt.Errorf("session index is not available")
	}
	if len(args) == 0 {
		return printWorkspaceSessions(c)
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "resume":
		if len(args) != 2 {
			return false, fmt.Errorf("usage: /sessions resume <session-id>")
		}
		target := strings.TrimSpace(args[1])
		if target == "" {
			return false, fmt.Errorf("session-id is required")
		}
		ok, err := c.sessionIndex.HasWorkspaceSession(c.workspace.Key, target)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, fmt.Errorf("session %q not found in current workspace", target)
		}
		c.sessionID = target
		c.printf("session resumed: %s\n", c.sessionID)
		return false, nil
	default:
		return false, fmt.Errorf("usage: /sessions [resume <session-id>]")
	}
}

func printWorkspaceSessions(c *cliConsole) (bool, error) {
	items, err := c.sessionIndex.ListWorkspaceSessions(c.workspace.Key, 50)
	if err != nil {
		return false, err
	}
	c.printf("workspace: %s\n", c.workspace.CWD)
	if len(items) == 0 {
		c.printf("sessions: (empty)\n")
		return false, nil
	}
	c.printf("sessions:\n")
	now := time.Now()
	for _, one := range items {
		marker := " "
		if one.SessionID == c.sessionID {
			marker = "*"
		}
		last := "never"
		if !one.LastEventAt.IsZero() {
			last = one.LastEventAt.Format(time.RFC3339)
		}
		preview := strings.TrimSpace(one.LastUserMessage)
		if preview != "" {
			runes := []rune(preview)
			if len(runes) > 48 {
				preview = string(runes[:48]) + "..."
			}
		} else {
			preview = "-"
		}
		age := "-"
		if !one.LastEventAt.IsZero() {
			age = now.Sub(one.LastEventAt).Round(time.Second).String()
		}
		c.printf(" %s %s  events=%d  last=%s (%s)  user=%s\n", marker, one.SessionID, one.EventCount, last, age, preview)
	}
	return false, nil
}

func (c *cliConsole) updateExecutionRuntime(mode toolexec.PermissionMode, sandboxType string) error {
	safeCommands := []string(nil)
	if c.execRuntime != nil {
		safeCommands = c.execRuntime.SafeCommands()
	}
	prevRuntime := c.execRuntime
	nextRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: mode,
		SandboxType:    sandboxType,
		SafeCommands:   safeCommands,
	})
	if err != nil {
		return err
	}
	c.execRuntime = nextRuntime
	if err := c.refreshShellToolRuntime(); err != nil {
		c.execRuntime = prevRuntime
		_ = toolexec.Close(nextRuntime)
		return err
	}
	if prevRuntime != nil && prevRuntime != nextRuntime {
		if closeErr := toolexec.Close(prevRuntime); closeErr != nil {
			c.printf("warn: close previous runtime failed: %v\n", closeErr)
		}
	}
	return nil
}

func (c *cliConsole) persistRuntimeSettings() {
	if c == nil || c.configStore == nil {
		return
	}
	if c.execRuntime == nil {
		return
	}
	if err := c.configStore.SetRuntimeSettings(runtimeSettings{
		StreamModel:     c.streamModel,
		ThinkingMode:    c.thinkingMode,
		ThinkingBudget:  c.thinkingBudget,
		ReasoningEffort: c.reasoningEffort,
		ShowReasoning:   c.showReasoning,
		PermissionMode:  string(c.execRuntime.PermissionMode()),
		SandboxType:     c.sandboxType,
	}); err != nil {
		c.printf("warn: persist runtime settings failed: %v\n", err)
	}
}

func (c *cliConsole) refreshShellToolRuntime() error {
	if c.resolved == nil || len(c.resolved.Tools) == 0 {
		return nil
	}
	for i, one := range c.resolved.Tools {
		if one == nil || one.Name() != toolshell.BashToolName {
			continue
		}
		bashTool, err := toolshell.NewBash(toolshell.BashConfig{Runtime: c.execRuntime})
		if err != nil {
			return err
		}
		c.resolved.Tools[i] = bashTool
	}
	return nil
}

type terminalApprover struct {
	editor         lineEditor
	out            io.Writer
	mu             sync.RWMutex
	defaultAllowed map[string]struct{}
	sessionAllowed map[string]struct{}
}

func newTerminalApprover(editor lineEditor, out io.Writer, safeCommands []string) *terminalApprover {
	approver := &terminalApprover{
		editor:         editor,
		out:            out,
		defaultAllowed: map[string]struct{}{},
		sessionAllowed: map[string]struct{}{},
	}
	for _, key := range defaultApprovalKeys(safeCommands) {
		approver.defaultAllowed[key] = struct{}{}
	}
	return approver
}

func (a *terminalApprover) Approve(ctx context.Context, req toolexec.ApprovalRequest) (bool, error) {
	_ = ctx
	if a.isAllowedByDefault(req.Command) || a.isAllowedInSession(req.Command) {
		return true, nil
	}
	fmt.Fprintf(a.out, "\n? 审批请求 %s (%s)\n", req.ToolName, req.Action)
	fmt.Fprintf(a.out, "! %s\n", req.Reason)
	fmt.Fprintf(a.out, "$ %s\n", req.Command)
	if a.editor == nil {
		return false, &toolexec.ApprovalAbortedError{Reason: "no interactive approver available"}
	}
	line, err := a.editor.ReadLine("? allow [y]同意 / [a]本会话放行 / [N]取消: ")
	if err != nil {
		if errors.Is(err, errInputInterrupt) || errors.Is(err, errInputEOF) {
			return false, &toolexec.ApprovalAbortedError{Reason: "approval canceled by user"}
		}
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	switch line {
	case "y", "yes":
		return true, nil
	case "a", "always":
		key := sessionApprovalKey(req.Command)
		if key == "" {
			key = strings.TrimSpace(req.Command)
		}
		if key != "" {
			a.mu.Lock()
			a.sessionAllowed[key] = struct{}{}
			a.mu.Unlock()
			fmt.Fprintf(a.out, "! 已加入当前会话白名单: %s\n", key)
		}
		return true, nil
	case "n", "no", "", "c", "cancel":
		return false, &toolexec.ApprovalAbortedError{Reason: "approval denied by user"}
	default:
		return false, &toolexec.ApprovalAbortedError{Reason: "approval denied by user"}
	}
}

func (a *terminalApprover) isAllowedByDefault(command string) bool {
	segments := splitApprovalSegments(command)
	if len(segments) == 0 {
		return false
	}
	if strings.ContainsAny(command, "<>$`\\&") {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, segment := range segments {
		key := approvalSegmentKey(segment)
		if key == "" {
			return false
		}
		if _, ok := a.defaultAllowed[key]; !ok {
			return false
		}
	}
	return true
}

func (a *terminalApprover) isAllowedInSession(command string) bool {
	key := sessionApprovalKey(command)
	if key == "" {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.sessionAllowed[key]
	return ok
}

func defaultApprovalKeys(safeCommands []string) []string {
	keys := make([]string, 0, len(safeCommands)+1)
	for _, one := range safeCommands {
		trimmed := strings.TrimSpace(one)
		if trimmed == "" {
			continue
		}
		keys = append(keys, filepath.Base(trimmed))
	}
	// git status is a low-risk read-only command and common in coding sessions.
	keys = append(keys, "git status")
	return dedupeStrings(keys)
}

func sessionApprovalKey(command string) string {
	return strings.TrimSpace(command)
}

func splitApprovalSegments(command string) []string {
	normalized := strings.TrimSpace(command)
	if normalized == "" {
		return nil
	}
	replacer := strings.NewReplacer("&&", ";", "||", ";", "|", ";")
	normalized = replacer.Replace(normalized)
	rawParts := strings.Split(normalized, ";")
	out := make([]string, 0, len(rawParts))
	for _, one := range rawParts {
		part := strings.TrimSpace(one)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func approvalSegmentKey(segment string) string {
	fields := strings.Fields(strings.TrimSpace(segment))
	if len(fields) == 0 {
		return ""
	}
	idx := 0
	for idx < len(fields) && isEnvAssignmentToken(fields[idx]) {
		idx++
	}
	if idx >= len(fields) {
		return ""
	}
	base := filepath.Base(fields[idx])
	if base == "git" {
		next := idx + 1
		for next < len(fields) && strings.HasPrefix(fields[next], "-") {
			next++
		}
		if next < len(fields) {
			return "git " + strings.ToLower(strings.TrimSpace(fields[next]))
		}
	}
	return base
}

func isEnvAssignmentToken(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	eq := strings.IndexByte(token, '=')
	if eq <= 0 {
		return false
	}
	name := token[:eq]
	if name == "" {
		return false
	}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' {
			continue
		}
		if i > 0 && (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func dedupeStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, one := range values {
		trimmed := strings.TrimSpace(one)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func (c *cliConsole) printf(format string, args ...any) {
	out := c.out
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, format, args...)
}

func formatUsage(usage runtime.ContextUsage) string {
	if usage.WindowTokens <= 0 {
		return "0/0"
	}
	return fmt.Sprintf("%d/%d (%.1f%%)", usage.CurrentTokens, usage.WindowTokens, usage.Ratio*100)
}

func stringOrDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
