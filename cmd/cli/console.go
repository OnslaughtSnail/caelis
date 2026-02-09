package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/bootstrap"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/lspbroker"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
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
	maxSteps               int
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
}

type slashCommand struct {
	Usage       string
	Description string
	Handle      func(*cliConsole, []string) (bool, error)
}

func newCLIConsole(cfg cliConsoleConfig) *cliConsole {
	commands := []string{"help", "version", "exit", "compact", "status", "sessions", "models", "model", "connect", "thinking", "effort", "stream", "reasoning", "tools"}
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
		maxSteps:               cfg.MaxSteps,
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
	console.approver = &terminalApprover{editor: editor, out: out}
	console.commands = map[string]slashCommand{
		"help":     {Usage: "/help", Description: "显示命令帮助", Handle: handleHelp},
		"version":  {Usage: "/version", Description: "显示版本信息", Handle: handleVersion},
		"exit":     {Usage: "/exit", Description: "退出 CLI", Handle: handleExit},
		"compact":  {Usage: "/compact [note]", Description: "手动触发一次上下文压缩", Handle: handleCompact},
		"status":   {Usage: "/status", Description: "查看当前会话配置", Handle: handleStatus},
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
	MaxSteps               int
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
	defer func() {
		if c.editor != nil {
			_ = c.editor.Close()
		}
	}()
	for {
		line, err := c.editor.ReadLine("> ")
		if err != nil {
			if errors.Is(err, errInputInterrupt) {
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
			continue
		}
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
			fmt.Fprintf(c.out, "error: %v\n", err)
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
		MaxSteps:               c.maxSteps,
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
	return runOnce(ctx, c.rt, runtime.RunRequest{
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
		ContextWindowTokens: c.contextWindow,
	}, runRenderConfig{
		ShowReasoning: c.showReasoning,
		Writer:        c.out,
	})
}

func handleHelp(c *cliConsole, args []string) (bool, error) {
	_ = args
	c.printf("Available commands:\n")
	order := []string{"help", "version", "status", "sessions", "models", "model", "connect", "thinking", "effort", "stream", "reasoning", "tools", "compact", "exit"}
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
	c.printf("exec_mode=%s sandbox_type=%s bash_strategy=%s\n",
		c.execRuntime.Mode(), c.execRuntime.SandboxType(), c.execRuntime.BashPolicy().Strategy)
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

type terminalApprover struct {
	editor lineEditor
	out    io.Writer
}

func (a *terminalApprover) Approve(ctx context.Context, req toolexec.ApprovalRequest) (bool, error) {
	_ = ctx
	fmt.Fprintf(a.out, "\n? 审批请求 %s (%s)\n", req.ToolName, req.Action)
	fmt.Fprintf(a.out, "! %s\n", req.Reason)
	fmt.Fprintf(a.out, "$ %s\n", req.Command)
	if a.editor == nil {
		return false, nil
	}
	line, err := a.editor.ReadLine("? allow [y/N]: ")
	if err != nil {
		if errors.Is(err, errInputInterrupt) || errors.Is(err, errInputEOF) {
			return false, nil
		}
		return false, err
	}
	line = strings.ToLower(line)
	return line == "y" || line == "yes", nil
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
