package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/bootstrap"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	kernelpolicy "github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/skills"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"

	image "github.com/OnslaughtSnail/caelis/internal/cli/imageutil"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
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
	uiMode                 interactiveUIMode
	noColor                bool
	verbose                bool
	inputRefs              *inputReferenceResolver
	tuiDiag                *tuiDiagnostics
	lastPromptTokens       int // cached prompt token count from resume or run

	editor   lineEditor
	prompter promptReader
	out      io.Writer
	ui       *ui
	approver *terminalApprover
	commands map[string]slashCommand

	runMu           sync.Mutex
	activeRunCancel context.CancelFunc
	interruptMu     sync.Mutex
	lastInterruptAt time.Time
	outMu           sync.Mutex

	imageCache          *image.Cache
	pendingAttachments  []model.ContentPart
	pendingAttachmentMu sync.Mutex
	tuiSender           interface{ Send(msg any) } // set in TUI mode for hint updates
	connectModelCacheMu sync.Mutex
	connectModelCache   map[string]connectModelCacheEntry
}

const interruptExitWindow = 2 * time.Second

type slashCommand struct {
	Usage       string
	Description string
	Handle      func(*cliConsole, []string) (bool, error)
}

type promptReader interface {
	ReadLine(prompt string) (string, error)
	ReadSecret(prompt string) (string, error)
}

type connectModelCacheEntry struct {
	models    []string
	expiresAt time.Time
}

func newCLIConsole(cfg cliConsoleConfig) *cliConsole {
	mode := interactiveUIMode(strings.ToLower(strings.TrimSpace(cfg.UIMode)))
	if mode == "" {
		mode = uiModeTUI
	}
	var editor lineEditor
	var out io.Writer = os.Stdout
	baseUI := newUI(out, cfg.NoColor, cfg.Verbose)
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
		streamModel:            true,
		thinkingMode:           cfg.ThinkingMode,
		thinkingBudget:         cfg.ThinkingBudget,
		reasoningEffort:        cfg.ReasoningEffort,
		showReasoning:          true,
		version:                strings.TrimSpace(cfg.Version),
		uiMode:                 mode,
		noColor:                cfg.NoColor,
		verbose:                cfg.Verbose,
		inputRefs:              cfg.InputRefs,
		tuiDiag:                cfg.TUIDiagnostics,
		imageCache:             image.NewCache(32),
		connectModelCache:      map[string]connectModelCacheEntry{},
		editor:                 editor,
		prompter:               editor,
		out:                    out,
		ui:                     baseUI,
	}
	console.approver = newTerminalApprover(console.prompter, out, baseUI)
	console.commands = map[string]slashCommand{
		"help":    {Usage: "/help", Description: "Show available commands", Handle: handleHelp},
		"version": {Usage: "/version", Description: "Show version", Handle: handleVersion},
		"exit":    {Usage: "/exit", Description: "Exit the CLI", Handle: handleExit},
		"quit":    {Usage: "/quit", Description: "Alias of /exit", Handle: handleExit},
		"new":     {Usage: "/new", Description: "Start a new conversation session", Handle: handleNew},
		"fork":    {Usage: "/fork", Description: "Fork current conversation into a new session", Handle: handleFork},
		"compact": {Usage: "/compact [note]", Description: "Compact context history", Handle: handleCompact},
		"status":  {Usage: "/status", Description: "Show current session status", Handle: handleStatus},
		"permission": {
			Usage:       "/permission [default|full_control]",
			Description: "View or switch permission mode",
			Handle:      handlePermission,
		},
		"sandbox": {
			Usage:       "/sandbox [<type>]",
			Description: "View or switch sandbox type",
			Handle:      handleSandbox,
		},
		"model":   {Usage: "/model <alias> [reasoning]", Description: "Switch active model and reasoning level", Handle: handleModel},
		"connect": {Usage: "/connect [provider] [model] [base_url] [timeout_seconds] [api_key] [context_window_tokens] [max_output_tokens] [reasoning_levels]", Description: "Add or update a model provider", Handle: handleConnect},
		"tools":   {Usage: "/tools", Description: "List available tools", Handle: handleTools},
		"skills":  {Usage: "/skills", Description: "List discovered skills", Handle: handleSkills},
		"resume":  {Usage: "/resume [session-id]", Description: "Resume latest or specified session", Handle: handleResume},
	}
	console.applyModelRuntimeSettings(console.modelAlias)
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
	ThinkingMode           string
	ThinkingBudget         int
	ReasoningEffort        string
	InputRefs              *inputReferenceResolver
	TUIDiagnostics         *tuiDiagnostics
	HistoryFile            string
	Version                string
	NoColor                bool
	Verbose                bool
	UIMode                 string
}

func (c *cliConsole) loop() error {
	switch c.uiMode {
	case uiModeTUI:
		return c.loopTUITea()
	default:
		return fmt.Errorf("unsupported ui mode %q", c.uiMode)
	}
}

func (c *cliConsole) loopLine() error {
	if c.editor == nil {
		return fmt.Errorf("line editor is not available")
	}
	for _, line := range c.startupLines() {
		c.printf("%s\n", line)
	}
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
				c.ui.Error("%v\n", err)
			}
			if exitNow {
				return nil
			}
			continue
		}
		if err := c.runPrompt(line); err != nil {
			if errors.Is(err, context.Canceled) {
				c.ui.Warn("execution interrupted\n")
				continue
			}
			c.ui.Error("%v\n", err)
		}
	}
}

func (c *cliConsole) startupLines() []string {
	versionText := strings.TrimSpace(c.version)
	if versionText == "" {
		versionText = "unknown"
	}
	return []string{
		fmt.Sprintf("Caelis %s", versionText),
		fmt.Sprintf("Workspace  %s", strings.TrimSpace(c.workspace.CWD)),
		"Commands   /help  /resume  /new",
		"Tip        Type your message and press Enter",
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
		if suggestion := closestCommand(cmd, commandNames(c.commands)); suggestion != "" {
			return false, fmt.Errorf("unknown command %q -- did you mean /%s?", cmd, suggestion)
		}
		return false, fmt.Errorf("unknown command %q, use /help", cmd)
	}
	return handler.Handle(c, parts[1:])
}

func (c *cliConsole) runPrompt(input string) error {
	if c.llm == nil {
		return fmt.Errorf("no model configured, use /connect to add provider and select model")
	}
	resolvedInput := input
	var resolvedPaths []string
	if c.inputRefs != nil {
		result, err := c.inputRefs.RewriteInput(input)
		if err != nil {
			c.ui.Warn("input reference resolution skipped: %v\n", err)
		} else {
			resolvedInput = result.Text
			resolvedPaths = result.ResolvedPaths
			for _, note := range result.Notes {
				c.ui.Note("%s\n", note)
			}
		}
	}
	// Load image content parts from resolved file references.
	var contentParts []model.ContentPart
	if c.inputRefs != nil && len(resolvedPaths) > 0 {
		for _, relPath := range resolvedPaths {
			if !image.IsImagePath(relPath) {
				continue
			}
			absPath := c.inputRefs.AbsPath(relPath)
			part, err := image.LoadAsContentPartCached(absPath, c.imageCache)
			if err != nil {
				c.ui.Warn("image load skipped: %s: %v\n", relPath, err)
				continue
			}
			contentParts = append(contentParts, part)
			c.ui.Note("attached image: %s\n", relPath)
		}
	}
	// Consume any pending clipboard attachments.
	pendingParts := c.consumePendingAttachments()
	contentParts = append(contentParts, pendingParts...)
	if c.tuiSender != nil && len(pendingParts) > 0 {
		c.tuiSender.Send(tuievents.AttachmentCountMsg{Count: 0})
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
		ModelProvider:          resolveProviderName(c.modelFactory, c.modelAlias),
		ModelName:              resolveModelName(c.modelFactory, c.modelAlias),
	})
	if err != nil {
		return err
	}
	ctx := toolexec.WithApprover(c.baseCtx, c.approver)
	ctx = kernelpolicy.WithToolAuthorizer(ctx, c.approver)
	runCtx, cancel := context.WithCancel(ctx)
	c.setActiveRunCancel(cancel)
	defer func() {
		c.clearActiveRunCancel()
		cancel()
	}()
	pendingTUIToolCalls := map[string]toolCallSnapshot{}
	return runOnce(runCtx, c.rt, runtime.RunRequest{
		AppName:             c.appName,
		UserID:              c.userID,
		SessionID:           c.sessionID,
		Input:               resolvedInput,
		ContentParts:        contentParts,
		Agent:               ag,
		Model:               c.llm,
		Tools:               c.resolved.Tools,
		CoreTools:           tool.CoreToolsConfig{Runtime: c.execRuntime},
		Policies:            c.resolved.Policies,
		ContextWindowTokens: c.contextWindow,
	}, runRenderConfig{
		ShowReasoning: c.showReasoning,
		Verbose:       c.ui.verbose,
		Writer:        c.out,
		UI:            c.ui,
		OnEvent: func(ev *session.Event) bool {
			return c.forwardEventToTUI(ev, pendingTUIToolCalls)
		},
		OnUsage: func(pt int) {
			c.lastPromptTokens = pt
		},
	})
}

func (c *cliConsole) emitAssistantEventToTUI(ev *session.Event) {
	if c == nil || c.tuiSender == nil || ev == nil {
		return
	}
	msg := ev.Message
	if msg.Role != model.RoleAssistant {
		return
	}
	if eventIsPartial(ev) {
		channel := strings.ToLower(strings.TrimSpace(eventChannel(ev)))
		switch channel {
		case "reasoning":
			c.emitAssistantChunkToTUI("reasoning", msg.Reasoning, false)
			c.emitAssistantChunkToTUI("answer", msg.Text, false)
		case "answer":
			// Mixed chunk payloads are rare but valid; keep deterministic order.
			c.emitAssistantChunkToTUI("reasoning", msg.Reasoning, false)
			c.emitAssistantChunkToTUI("answer", msg.Text, false)
		default:
			c.emitAssistantChunkToTUI("reasoning", msg.Reasoning, false)
			c.emitAssistantChunkToTUI("answer", msg.Text, false)
		}
		return
	}
	// Final assistant events may contain both reasoning and answer.
	c.emitAssistantChunkToTUI("reasoning", strings.TrimSpace(msg.Reasoning), true)
	c.emitAssistantChunkToTUI("answer", strings.TrimSpace(msg.Text), true)
}

func (c *cliConsole) emitAssistantChunkToTUI(kind string, text string, final bool) {
	if c == nil || c.tuiSender == nil || text == "" {
		return
	}
	streamKind := strings.ToLower(strings.TrimSpace(kind))
	switch streamKind {
	case "reasoning":
		if !c.showReasoning {
			return
		}
		c.tuiSender.Send(tuievents.AssistantStreamMsg{
			Kind:  "reasoning",
			Text:  text,
			Final: final,
		})
	default:
		c.tuiSender.Send(tuievents.AssistantStreamMsg{
			Kind:  "answer",
			Text:  text,
			Final: final,
		})
	}
}

func (c *cliConsole) forwardEventToTUI(ev *session.Event, pendingToolCalls map[string]toolCallSnapshot) bool {
	if c == nil || c.tuiSender == nil || ev == nil {
		return false
	}
	msg := ev.Message
	handled := false
	if msg.Role == model.RoleAssistant {
		// Keep assistant rendering deterministic, even for mixed assistant+toolcall events.
		c.emitAssistantEventToTUI(ev)
		handled = true
	}
	if len(msg.ToolCalls) > 0 {
		for _, call := range msg.ToolCalls {
			parsedArgs := parseToolArgsForDisplay(call.Args)
			if pendingToolCalls != nil && call.ID != "" {
				pendingToolCalls[call.ID] = toolCallSnapshot{
					Args: cloneAnyMap(parsedArgs),
				}
			}
			c.tuiSender.Send(tuievents.LogChunkMsg{
				Chunk: fmt.Sprintf("▸ %s %s\n", call.Name, summarizeToolArgs(call.Name, parsedArgs)),
			})
		}
		handled = true
	}
	if msg.ToolResponse != nil {
		var callArgs map[string]any
		if pendingToolCalls != nil && msg.ToolResponse.ID != "" {
			if snapshot, ok := pendingToolCalls[msg.ToolResponse.ID]; ok {
				callArgs = snapshot.Args
				delete(pendingToolCalls, msg.ToolResponse.ID)
			}
		}
		diffMsg, tooLarge, ok := buildPatchDiffBlockMsg(msg.ToolResponse.Name, msg.ToolResponse.Result, callArgs)
		if ok {
			if tooLarge {
				summary := strings.TrimSpace(summarizeToolResponseWithCall(msg.ToolResponse.Name, msg.ToolResponse.Result, callArgs))
				if summary == "" {
					summary = "rich diff skipped: too large"
				} else {
					summary += " (rich diff skipped: too large)"
				}
				c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("✓ %s %s\n", msg.ToolResponse.Name, summary)})
				return true
			}
			c.tuiSender.Send(diffMsg)
			return true
		}
		// Suppress result line for read-only FS tools (the call line is sufficient).
		if isReadOnlyFSTool(msg.ToolResponse.Name) && !hasToolError(msg.ToolResponse.Result) {
			return true
		}
		summary := summarizeToolResponseWithCall(msg.ToolResponse.Name, msg.ToolResponse.Result, callArgs)
		if strings.TrimSpace(summary) != "" {
			c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("✓ %s %s\n", msg.ToolResponse.Name, summary)})
		}
		return true
	}
	return handled
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
	switch c.editor.(type) {
	case *readlineEditor, *linerEditor:
		return true
	default:
		return false
	}
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
	helpSection := func(title string, names []string) {
		c.ui.Section(title)
		for _, name := range names {
			cmd := c.commands[name]
			c.ui.Plain("  %-24s %s\n", cmd.Usage, cmd.Description)
		}
	}
	helpSection("Session", []string{"new", "fork", "resume", "compact", "status"})
	helpSection("Model", []string{"model", "connect"})
	helpSection("Security", []string{"permission", "sandbox"})
	helpSection("Other", []string{"tools", "skills", "help", "version", "exit", "quit"})
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
	c.sessionID = nextConversationSessionID()
	c.lastPromptTokens = 0
	_ = c.clearPendingAttachments()
	if c.tuiSender != nil {
		c.tuiSender.Send(tuievents.ClearHistoryMsg{})
		c.tuiSender.Send(tuievents.AttachmentCountMsg{Count: 0})
		modelText, contextText := c.readTUIStatus()
		c.tuiSender.Send(tuievents.SetStatusMsg{Model: modelText, Context: contextText})
		c.tuiSender.Send(tuievents.SetHintMsg{Hint: "started new session"})
		return false, nil
	}
	c.printf("new session started: %s\n", c.sessionID)
	return false, nil
}

func handleFork(c *cliConsole, args []string) (bool, error) {
	if len(args) != 0 {
		return false, fmt.Errorf("usage: /fork")
	}
	previous := strings.TrimSpace(c.sessionID)
	if previous == "" {
		return false, fmt.Errorf("cannot fork without an active session")
	}
	c.sessionID = nextConversationSessionID()
	_ = c.clearPendingAttachments()
	if c.tuiSender != nil {
		c.tuiSender.Send(tuievents.AttachmentCountMsg{Count: 0})
		c.tuiSender.Send(tuievents.SetHintMsg{Hint: "fork succeeded"})
		return false, nil
	}
	c.printf("fork succeeded\n")
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
	c.ui.Section("Model")
	c.ui.KeyValue("model", c.modelAlias)
	c.ui.KeyValue("stream", fmt.Sprintf("%v", c.streamModel))
	c.ui.KeyValue("thinking", fmt.Sprintf("%s (budget=%d)", c.thinkingMode, c.thinkingBudget))
	c.ui.KeyValue("effort", c.reasoningEffort)
	c.ui.KeyValue("reasoning", fmt.Sprintf("%v", c.showReasoning))

	c.ui.Section("Session")
	c.ui.KeyValue("workspace", c.workspace.CWD)
	c.ui.KeyValue("session", c.sessionID)

	c.ui.Section("Security")
	mode := c.execRuntime.PermissionMode()
	switch mode {
	case toolexec.PermissionModeFullControl:
		c.ui.KeyValue("permission", fmt.Sprintf("%s  route=host", mode))
	default:
		if c.execRuntime.FallbackToHost() {
			c.ui.KeyValue("permission", fmt.Sprintf("%s  sandbox=%s  route=host (fallback, reason=%s)",
				mode, c.execRuntime.SandboxType(), c.execRuntime.FallbackReason()))
		} else {
			c.ui.KeyValue("permission", fmt.Sprintf("%s  sandbox=%s  route=sandbox",
				mode, c.execRuntime.SandboxType()))
		}
	}
	c.ui.KeyValue("sandbox_policy", runtimePolicyHint(c.execRuntime.SandboxPolicy()))

	if c.rt != nil {
		runState, err := c.rt.RunState(c.baseCtx, runtime.RunStateRequest{
			AppName:   c.appName,
			UserID:    c.userID,
			SessionID: c.sessionID,
		})
		if err != nil {
			return false, err
		}
		c.ui.Section("Runtime")
		if runState.HasLifecycle {
			c.ui.KeyValue("run_state", fmt.Sprintf("%s  phase=%s", runState.Status, stringOrDash(runState.Phase)))
			if strings.TrimSpace(runState.Error) != "" {
				c.ui.KeyValue("error", truncateInline(runState.Error, 160))
			}
			if strings.TrimSpace(string(runState.ErrorCode)) != "" {
				c.ui.KeyValue("error_code", string(runState.ErrorCode))
			}
		} else {
			c.ui.KeyValue("run_state", "none")
		}
	}

	if c.llm == nil {
		c.ui.KeyValue("context", "not available (no model configured)")
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
	c.ui.Section("Context")
	c.ui.KeyValue("usage", fmt.Sprintf("%s  input_budget=%d  events=%d", formatUsage(usage), usage.InputBudget, usage.EventCount))
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
	// Validate type by constructing a default-mode runtime.
	if _, err := toolexec.New(toolexec.Config{
		PermissionMode: toolexec.PermissionModeDefault,
		SandboxType:    sandboxType,
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

func handleModel(c *cliConsole, args []string) (bool, error) {
	if len(args) < 1 || len(args) > 2 {
		return false, fmt.Errorf("usage: /model <alias> [reasoning]")
	}
	if c.modelFactory == nil {
		return false, fmt.Errorf("model factory is not configured")
	}
	alias := strings.TrimSpace(args[0])
	if c.configStore != nil {
		alias = c.configStore.ResolveModelAlias(alias)
	}
	targetAlias := strings.ToLower(alias)
	llm, err := c.modelFactory.NewByAlias(alias)
	if err != nil {
		return false, err
	}
	settings := defaultModelRuntimeSettings()
	if c.configStore != nil {
		settings = c.configStore.ModelRuntimeSettings(targetAlias)
	}
	if len(args) == 2 {
		cfg, ok := c.modelFactory.ConfigForAlias(targetAlias)
		if !ok {
			return false, fmt.Errorf("model config not found for alias %q", targetAlias)
		}
		opt, err := resolveModelReasoningOption(cfg, args[1])
		if err != nil {
			return false, err
		}
		settings.ThinkingMode = opt.ThinkingMode
		settings.ReasoningEffort = opt.ReasoningEffort
	}

	c.modelAlias = targetAlias
	c.llm = llm
	if len(args) == 2 {
		c.thinkingMode = settings.ThinkingMode
		c.thinkingBudget = settings.ThinkingBudget
		c.reasoningEffort = settings.ReasoningEffort
		if c.configStore != nil {
			if err := c.configStore.SetModelRuntimeSettings(targetAlias, settings); err != nil {
				return false, err
			}
		}
	} else {
		c.applyModelRuntimeSettings(targetAlias)
	}
	if c.configStore != nil {
		if err := c.configStore.SetDefaultModel(targetAlias); err != nil {
			fmt.Fprintf(c.out, "warn: update default model failed: %v\n", err)
		}
	}
	if len(args) == 2 {
		if strings.TrimSpace(c.reasoningEffort) != "" {
			c.printf("model switched to %s (reasoning=%s effort=%s)\n", alias, c.thinkingMode, c.reasoningEffort)
		} else {
			c.printf("model switched to %s (reasoning=%s)\n", alias, c.thinkingMode)
		}
	} else {
		c.printf("model switched to %s\n", alias)
	}
	return false, nil
}

// resolveContextWindowForDisplay returns the context window token limit for the
// current model. Uses the explicit CLI override first, then the connected model
// config value, then falls back to the model capability catalog.
func (c *cliConsole) resolveContextWindowForDisplay() int {
	if c.contextWindow > 0 {
		return c.contextWindow
	}
	if c.modelFactory == nil {
		return 0
	}
	cfg, ok := c.modelFactory.ConfigForAlias(c.modelAlias)
	if !ok {
		return 0
	}
	if cfg.ContextWindowTokens > 0 {
		return cfg.ContextWindowTokens
	}
	caps, found := modelproviders.LookupModelCapabilities(cfg.Provider, cfg.Model)
	if found && caps.ContextWindowTokens > 0 {
		return caps.ContextWindowTokens
	}
	return 0
}

func (c *cliConsole) applyModelRuntimeSettings(alias string) {
	settings := modelRuntimeSettings{
		ThinkingMode:    defaultThinkingMode,
		ThinkingBudget:  defaultThinkingBudget,
		ReasoningEffort: defaultReasoningEffort,
	}
	if c.configStore != nil {
		settings = c.configStore.ModelRuntimeSettings(alias)
	}
	c.thinkingMode = settings.ThinkingMode
	c.thinkingBudget = settings.ThinkingBudget
	c.reasoningEffort = settings.ReasoningEffort
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

func handleSkills(c *cliConsole, args []string) (bool, error) {
	_ = args
	discovered := skills.DiscoverMeta(c.skillDirs)
	if len(discovered.Metas) == 0 {
		c.printf("skills: (none discovered)\n")
		for _, w := range discovered.Warnings {
			c.ui.Warn("  %v\n", w)
		}
		return false, nil
	}
	c.printf("skills:\n")
	for _, m := range discovered.Metas {
		c.printf("  - %-20s %s\n", m.Name, m.Description)
	}
	if len(discovered.Warnings) > 0 {
		for _, w := range discovered.Warnings {
			c.ui.Warn("  %v\n", w)
		}
	}
	return false, nil
}

func handleResume(c *cliConsole, args []string) (bool, error) {
	if c.sessionIndex == nil {
		return false, fmt.Errorf("session index is not available")
	}
	if len(args) > 1 {
		return false, fmt.Errorf("usage: /resume [session-id]")
	}
	target := ""
	if len(args) == 1 {
		target = strings.TrimSpace(args[0])
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
	} else {
		rec, ok, err := c.sessionIndex.MostRecentWorkspaceSession(c.workspace.Key, c.sessionID)
		if err != nil {
			return false, err
		}
		if !ok || strings.TrimSpace(rec.SessionID) == "" {
			return false, fmt.Errorf("no resumable session found in current workspace")
		}
		target = rec.SessionID
	}
	c.sessionID = target
	if err := c.renderResumedSessionEvents(); err != nil {
		return false, err
	}
	return false, nil
}

func (c *cliConsole) renderResumedSessionEvents() error {
	if c == nil || c.rt == nil {
		return nil
	}
	events, err := c.rt.SessionEvents(c.baseCtx, runtime.SessionEventsRequest{
		AppName:          c.appName,
		UserID:           c.userID,
		SessionID:        c.sessionID,
		Limit:            200,
		IncludeLifecycle: false,
	})
	if err != nil {
		return err
	}
	// Extract usage info from the most recent event with usage metadata.
	c.lastPromptTokens = extractLastUsage(events)
	if c.tuiSender == nil || len(events) == 0 {
		return nil
	}
	// In TUI mode, replay directly through structured events so assistant
	// Markdown is rendered by the same block renderer as live streaming,
	// avoiding mixed prefix-coloring and formatting artifacts.
	c.tuiSender.Send(tuievents.ClearHistoryMsg{})
	modelText, contextText := c.readTUIStatus()
	c.tuiSender.Send(tuievents.SetStatusMsg{Model: modelText, Context: contextText})
	pendingToolCalls := map[string]toolCallSnapshot{}
	for _, ev := range events {
		if ev == nil || eventIsPartial(ev) {
			continue
		}
		msg := ev.Message
		if msg.Role == model.RoleUser {
			userText := strings.TrimSpace(msg.Text)
			if userText == "" {
				userText = userTextFromContentParts(msg.ContentParts)
			}
			if userText != "" {
				c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("> %s\n", userText)})
			}
			for _, part := range msg.ContentParts {
				if part.Type != model.ContentPartImage {
					continue
				}
				name := strings.TrimSpace(part.FileName)
				if name == "" {
					name = "image"
				}
				c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("! [image: %s]\n", name)})
			}
			continue
		}
		if c.forwardEventToTUI(ev, pendingToolCalls) {
			continue
		}
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		switch msg.Role {
		case model.RoleSystem:
			c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("! %s\n", text)})
		default:
			c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: fmt.Sprintf("- %s\n", text)})
		}
	}
	return nil
}

func (c *cliConsole) updateExecutionRuntime(mode toolexec.PermissionMode, sandboxType string) error {
	prevRuntime := c.execRuntime
	nextRuntime, err := toolexec.New(toolexec.Config{
		PermissionMode: mode,
		SandboxType:    sandboxType,
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
		PermissionMode: string(c.execRuntime.PermissionMode()),
		SandboxType:    c.sandboxType,
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
	prompter       promptReader
	out            io.Writer
	ui             *ui
	mu             sync.RWMutex
	sessionAllowed map[string]struct{}
	toolAllowed    map[string]struct{}
}

func newTerminalApprover(prompter promptReader, out io.Writer, u *ui) *terminalApprover {
	return &terminalApprover{
		prompter:       prompter,
		out:            out,
		ui:             u,
		sessionAllowed: map[string]struct{}{},
		toolAllowed:    map[string]struct{}{},
	}
}

func (a *terminalApprover) Approve(ctx context.Context, req toolexec.ApprovalRequest) (bool, error) {
	_ = ctx
	if a.isAllowedInSession(req.Command) {
		return true, nil
	}
	if a.ui != nil {
		a.ui.ApprovalHeader(req.ToolName, req.Action)
		a.ui.ApprovalReason(req.Reason)
		a.ui.ApprovalCommand(req.Command)
	} else {
		fmt.Fprintf(a.out, "\n? Approval: %s (%s)\n  %s\n  $ %s\n", req.ToolName, req.Action, req.Reason, req.Command)
	}
	if a.prompter == nil {
		return false, &toolexec.ApprovalAbortedError{Reason: "no interactive approver available"}
	}
	line, err := a.prompter.ReadLine(approvalPrompt)
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
			if a.ui != nil {
				a.ui.ApprovalSessionNote(key)
			} else {
				fmt.Fprintf(a.out, "  Added to session allowlist: %s\n", key)
			}
		}
		return true, nil
	case "n", "no", "", "c", "cancel":
		return false, &toolexec.ApprovalAbortedError{Reason: "approval denied by user"}
	default:
		return false, &toolexec.ApprovalAbortedError{Reason: "approval denied by user"}
	}
}

func (a *terminalApprover) AuthorizeTool(ctx context.Context, req kernelpolicy.ToolAuthorizationRequest) (bool, error) {
	_ = ctx
	toolKey := toolApprovalKey(req.ToolName)
	if toolKey == "" {
		return true, nil
	}
	if a.isToolAllowedInSession(toolKey) {
		return true, nil
	}

	if a.ui != nil {
		a.ui.ToolAuthHeader(toolKey)
		if reason := strings.TrimSpace(req.Reason); reason != "" {
			a.ui.ApprovalReason(reason)
		}
	} else {
		fmt.Fprintf(a.out, "\n? Authorize tool: %s\n", toolKey)
		if reason := strings.TrimSpace(req.Reason); reason != "" {
			fmt.Fprintf(a.out, "  %s\n", reason)
		}
	}
	if a.prompter == nil {
		return false, &toolexec.ApprovalAbortedError{Reason: "no interactive approver available"}
	}
	line, err := a.prompter.ReadLine(toolAuthPrompt)
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
		a.mu.Lock()
		a.toolAllowed[toolKey] = struct{}{}
		a.mu.Unlock()
		if a.ui != nil {
			a.ui.ApprovalSessionNote(toolKey)
		} else {
			fmt.Fprintf(a.out, "  Added to session allowlist: %s\n", toolKey)
		}
		return true, nil
	case "n", "no", "", "c", "cancel":
		return false, &toolexec.ApprovalAbortedError{Reason: "approval denied by user"}
	default:
		return false, &toolexec.ApprovalAbortedError{Reason: "approval denied by user"}
	}
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

func (a *terminalApprover) isToolAllowedInSession(toolKey string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.toolAllowed[toolKey]
	return ok
}

func sessionApprovalKey(command string) string {
	return strings.TrimSpace(command)
}

func toolApprovalKey(toolName string) string {
	return strings.ToUpper(strings.TrimSpace(toolName))
}

func (c *cliConsole) addPendingAttachment(part model.ContentPart) {
	c.pendingAttachmentMu.Lock()
	defer c.pendingAttachmentMu.Unlock()
	c.pendingAttachments = append(c.pendingAttachments, part)
}

func (c *cliConsole) consumePendingAttachments() []model.ContentPart {
	c.pendingAttachmentMu.Lock()
	defer c.pendingAttachmentMu.Unlock()
	parts := c.pendingAttachments
	c.pendingAttachments = nil
	return parts
}

func (c *cliConsole) clearPendingAttachments() int {
	c.pendingAttachmentMu.Lock()
	defer c.pendingAttachmentMu.Unlock()
	c.pendingAttachments = nil
	return 0
}

// pasteClipboardImage extracts an image from the system clipboard, saves it to
// a temp directory, and adds it as a pending attachment. Returns the current
// pending attachment count, a legacy hint string (always empty), and any error.
func (c *cliConsole) pasteClipboardImage() (int, string, error) {
	raw, mime, err := image.ExtractClipboardImage()
	if err != nil {
		return 0, "", fmt.Errorf("clipboard: %w", err)
	}
	if raw == nil {
		return 0, "", nil // no image in clipboard
	}
	if len(raw) > image.MaxImageBytes {
		return 0, "", fmt.Errorf("clipboard image too large: %d bytes (max %d)", len(raw), image.MaxImageBytes)
	}
	// Save to temp directory for inspection.
	tmpDir := filepath.Join(os.TempDir(), "caelis-clipboard")
	_ = os.MkdirAll(tmpDir, 0o755)
	tmpName := fmt.Sprintf("clipboard-%d.png", time.Now().UnixNano())
	tmpPath := filepath.Join(tmpDir, tmpName)
	_ = os.WriteFile(tmpPath, raw, 0o644)

	part, err := image.ContentPartFromBytes(raw, mime, tmpName, c.imageCache)
	if err != nil {
		return 0, "", fmt.Errorf("clipboard image: %w", err)
	}
	c.addPendingAttachment(part)
	c.pendingAttachmentMu.Lock()
	count := len(c.pendingAttachments)
	c.pendingAttachmentMu.Unlock()
	return count, "", nil
}

func (c *cliConsole) printf(format string, args ...any) {
	out := c.out
	if out == nil {
		out = os.Stdout
	}
	c.outMu.Lock()
	defer c.outMu.Unlock()
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
