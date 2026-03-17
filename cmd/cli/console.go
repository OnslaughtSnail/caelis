package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	appassembly "github.com/OnslaughtSnail/caelis/internal/app/assembly"
	appskills "github.com/OnslaughtSnail/caelis/internal/app/skills"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	kernelpolicy "github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/kernel/taskstream"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	toolshell "github.com/OnslaughtSnail/caelis/kernel/tool/builtin/shell"

	"github.com/OnslaughtSnail/caelis/internal/approvalqueue"
	image "github.com/OnslaughtSnail/caelis/internal/cli/imageutil"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuiapp"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/idutil"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
)

type cliConsole struct {
	baseCtx context.Context
	rt      *runtime.Runtime

	appName       string
	userID        string
	sessionID     string
	contextWindow int
	workspace     workspaceContext
	workspaceLine string

	resolved          *appassembly.ResolvedSpec
	sessionStore      session.Store
	execRuntime       toolexec.Runtime
	execRuntimeView   *swappableRuntime
	sandboxType       string
	sandboxHelperPath string

	modelAlias            string
	llm                   model.LLM
	modelFactory          *modelproviders.Factory
	configStore           *appConfigStore
	credentialStore       *credentialStore
	sessionIndex          *sessionIndex
	systemPrompt          string
	enableExperimentalLSP bool
	skillDirs             []string
	streamModel           bool
	thinkingBudget        int
	reasoningEffort       string
	showReasoning         bool
	version               string
	uiMode                interactiveUIMode
	noColor               bool
	verbose               bool
	inputRefs             *inputReferenceResolver
	tuiDiag               *tuiDiagnostics
	lastPromptTokens      int // cached context usage estimate for TUI status
	sessionMode           string

	editor   lineEditor
	prompter promptReader
	out      io.Writer
	ui       *ui
	approver *terminalApprover
	commands map[string]slashCommand

	runMu           sync.Mutex
	activeRunCancel context.CancelFunc
	activeRunner    runtime.Runner
	interruptMu     sync.Mutex
	lastInterruptAt time.Time
	outMu           sync.Mutex

	imageCache          *image.Cache
	pendingAttachments  []model.ContentPart
	attachmentLibrary   map[string]model.ContentPart
	pendingAttachmentMu sync.Mutex
	tuiSender           interface{ Send(msg any) } // set in TUI mode for hint updates
	delegatePreviewer   *delegatePreviewProjector
	bashWatchMu         sync.Mutex
	bashTaskWatches     map[string]context.CancelFunc
	connectModelCacheMu sync.Mutex
	connectModelCache   map[string]connectModelCacheEntry
}

const interruptExitWindow = 2 * time.Second
const transientHintDuration = 1600 * time.Millisecond

const (
	btwControlOpenTag  = "<caelis-btw hidden=\"true\">"
	btwControlCloseTag = "</caelis-btw>"
)

type slashCommand struct {
	Usage       string
	Description string
	Handle      func(*cliConsole, []string) (bool, error)
}

type promptReader interface {
	ReadLine(prompt string) (string, error)
	ReadSecret(prompt string) (string, error)
}

type choicePromptReader interface {
	RequestChoicePrompt(prompt string, choices []tuievents.PromptChoice, defaultChoice string, filterable bool) (string, error)
}

type structuredPromptReader interface {
	RequestStructuredPrompt(req tuievents.PromptRequestMsg) (string, error)
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
		baseCtx:               cfg.BaseContext,
		rt:                    cfg.Runtime,
		appName:               cfg.AppName,
		userID:                cfg.UserID,
		sessionID:             cfg.SessionID,
		contextWindow:         cfg.ContextWindow,
		workspace:             cfg.Workspace,
		workspaceLine:         strings.TrimSpace(cfg.WorkspaceLine),
		resolved:              cfg.Resolved,
		sessionStore:          cfg.SessionStore,
		execRuntime:           cfg.ExecRuntime,
		execRuntimeView:       cfg.ExecRuntimeView,
		sandboxType:           strings.TrimSpace(cfg.SandboxType),
		sandboxHelperPath:     strings.TrimSpace(cfg.SandboxHelperPath),
		modelAlias:            cfg.ModelAlias,
		llm:                   cfg.Model,
		modelFactory:          cfg.ModelFactory,
		configStore:           cfg.ConfigStore,
		credentialStore:       cfg.CredentialStore,
		sessionIndex:          cfg.SessionIndex,
		systemPrompt:          cfg.SystemPrompt,
		enableExperimentalLSP: cfg.EnableExperimentalLSP,
		skillDirs:             append([]string(nil), cfg.SkillDirs...),
		streamModel:           true,
		thinkingBudget:        cfg.ThinkingBudget,
		reasoningEffort:       cfg.ReasoningEffort,
		showReasoning:         true,
		version:               strings.TrimSpace(cfg.Version),
		uiMode:                mode,
		noColor:               cfg.NoColor,
		verbose:               cfg.Verbose,
		inputRefs:             cfg.InputRefs,
		tuiDiag:               cfg.TUIDiagnostics,
		imageCache:            image.NewCache(32),
		attachmentLibrary:     map[string]model.ContentPart{},
		connectModelCache:     map[string]connectModelCacheEntry{},
		delegatePreviewer:     newDelegatePreviewProjector(),
		bashTaskWatches:       map[string]context.CancelFunc{},
		editor:                editor,
		prompter:              editor,
		out:                   out,
		ui:                    baseUI,
	}
	console.approver = newTerminalApprover(console.prompter, out, baseUI)
	console.approver.modeResolver = func() string { return console.sessionMode }
	console.commands = map[string]slashCommand{
		"help":    {Usage: "/help", Description: "Show available commands", Handle: handleHelp},
		"btw":     {Usage: "/btw <question>", Description: "Ask an ephemeral side question without modifying history", Handle: handleBTW},
		"version": {Usage: "/version", Description: "Show version", Handle: handleVersion},
		"exit":    {Usage: "/exit", Description: "Exit the CLI", Handle: handleExit},
		"quit":    {Usage: "/quit", Description: "Alias of /exit", Handle: handleExit},
		"new":     {Usage: "/new", Description: "Start a new conversation session", Handle: handleNew},
		"fork":    {Usage: "/fork", Description: "Fork current conversation into a new session", Handle: handleFork},
		"compact": {Usage: "/compact [note]", Description: "Compact context history", Handle: handleCompact},
		"status":  {Usage: "/status", Description: "Show current session status", Handle: handleStatus},
		"sandbox": {
			Usage:       "/sandbox [auto|<type>]",
			Description: "View or switch sandbox type (auto/bwrap/landlock experimental)",
			Handle:      handleSandbox,
		},
		"model":   {Usage: "/model use <alias> [reasoning] | /model del [alias ...]", Description: "Switch models or remove configured models", Handle: handleModel},
		"connect": {Usage: "/connect", Description: "Interactive provider and model setup", Handle: handleConnect},
		"resume":  {Usage: "/resume [session-id]", Description: "Resume latest or specified session", Handle: handleResume},
	}
	console.applyModelRuntimeSettings(console.modelAlias)
	console.syncSessionModeFromStore()
	return console
}

type cliConsoleConfig struct {
	BaseContext           context.Context
	Runtime               *runtime.Runtime
	AppName               string
	UserID                string
	SessionID             string
	ContextWindow         int
	Workspace             workspaceContext
	WorkspaceLine         string
	Resolved              *appassembly.ResolvedSpec
	SessionStore          session.Store
	ExecRuntime           toolexec.Runtime
	ExecRuntimeView       *swappableRuntime
	SandboxType           string
	SandboxHelperPath     string
	ModelAlias            string
	Model                 model.LLM
	ModelFactory          *modelproviders.Factory
	ConfigStore           *appConfigStore
	CredentialStore       *credentialStore
	SessionIndex          *sessionIndex
	SystemPrompt          string
	EnableExperimentalLSP bool
	SkillDirs             []string
	ThinkingBudget        int
	ReasoningEffort       string
	InputRefs             *inputReferenceResolver
	TUIDiagnostics        *tuiDiagnostics
	HistoryFile           string
	Version               string
	NoColor               bool
	Verbose               bool
	UIMode                string
}

func (c *cliConsole) loop() error {
	switch c.uiMode {
	case uiModeTUI:
		return c.loopTUITea()
	default:
		return fmt.Errorf("unsupported ui mode %q", c.uiMode)
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

func (c *cliConsole) shouldHandleAsSlashCommand(line string) bool {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return false
	}
	parts := strings.Fields(strings.TrimPrefix(line, "/"))
	if len(parts) == 0 {
		return false
	}
	cmd := strings.ToLower(strings.TrimSpace(parts[0]))
	if cmd == "" {
		return false
	}
	if _, ok := c.commands[cmd]; ok {
		return true
	}
	return looksLikeSlashCommandToken(cmd)
}

func looksLikeSlashCommandToken(token string) bool {
	if token == "" {
		return false
	}
	for i, r := range token {
		if unicode.IsLetter(r) {
			continue
		}
		if i > 0 && (unicode.IsDigit(r) || r == '-' || r == '_') {
			continue
		}
		return false
	}
	return true
}

func (c *cliConsole) runPrompt(input string) error {
	return c.runPromptWithAttachments(input, nil)
}

func (c *cliConsole) runPromptWithAttachments(input string, attachments []tuiapp.Attachment) error {
	prepared, err := c.preparePromptSubmission(input, attachments)
	if err != nil {
		return err
	}
	submission := runtime.Submission{
		Text:         prepared.runInput,
		ContentParts: append([]model.ContentPart(nil), prepared.contentParts...),
		Mode:         runtime.SubmissionConversation,
	}
	if runner := c.getActiveRunner(); runner != nil {
		// Submit to the existing runner for prompt queue-jumping. The runner
		// is still active, so we must not close it on error — the owner
		// goroutine is responsible for its lifecycle.
		return runner.Submit(submission)
	}
	return c.runPreparedSubmission(prepared, submission)
}

type preparedPromptSubmission struct {
	agent        agent.Agent
	runInput     string
	contentParts []model.ContentPart
}

func (c *cliConsole) preparePromptSubmission(input string, attachments []tuiapp.Attachment) (preparedPromptSubmission, error) {
	if c.llm == nil {
		return preparedPromptSubmission{}, fmt.Errorf("no model configured, use /connect to add provider and select model")
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
	visibleInput := resolvedInput
	resolvedInput = c.injectedPrompt(resolvedInput)
	controlInput := strings.TrimSpace(c.injectedPrompt(""))
	// Load image content parts from resolved file references.
	var resolvedImageParts []model.ContentPart
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
			resolvedImageParts = append(resolvedImageParts, part)
			c.ui.Note("attached image: %s\n", relPath)
		}
	}
	var contentParts []model.ContentPart
	// Consume any pending clipboard attachments.
	var consumedPending int
	var pendingParts []model.ContentPart
	if len(attachments) > 0 {
		pendingLibrary := c.pendingAttachmentLibrary()
		contentParts = append(contentParts, buildInterleavedContentParts(visibleInput, attachments, pendingLibrary)...)
		consumedPending = len(c.consumePendingAttachmentsByName(attachmentNames(attachments)))
	} else {
		pendingParts = c.consumePendingAttachments()
		if strings.TrimSpace(visibleInput) != "" && (len(pendingParts) > 0 || len(resolvedImageParts) > 0) {
			contentParts = append(contentParts, model.ContentPart{
				Type: model.ContentPartText,
				Text: visibleInput,
			})
		}
		contentParts = append(contentParts, pendingParts...)
		consumedPending = len(pendingParts)
	}
	contentParts = append(contentParts, resolvedImageParts...)
	runInput := resolvedInput
	if len(contentParts) > 0 {
		if controlInput != "" {
			contentParts = append([]model.ContentPart{{
				Type: model.ContentPartText,
				Text: controlInput,
			}}, contentParts...)
		}
		runInput = ""
	}
	if c.tuiSender != nil && consumedPending > 0 {
		c.tuiSender.Send(tuievents.AttachmentCountMsg{Count: 0})
	}
	ag, err := buildAgent(buildAgentInput{
		AppName:                     c.appName,
		WorkspaceDir:                c.workspace.CWD,
		EnableExperimentalLSPPrompt: c.enableExperimentalLSP,
		BasePrompt:                  c.systemPrompt,
		SkillDirs:                   c.skillDirs,
		StreamModel:                 c.streamModel,
		ThinkingBudget:              c.thinkingBudget,
		ReasoningEffort:             c.reasoningEffort,
		ModelProvider:               resolveProviderName(c.modelFactory, c.modelAlias),
		ModelName:                   resolveModelName(c.modelFactory, c.modelAlias),
		ModelConfig: func() modelproviders.Config {
			if c.modelFactory == nil {
				return modelproviders.Config{}
			}
			cfg, _ := c.modelFactory.ConfigForAlias(c.modelAlias)
			return cfg
		}(),
	})
	if err != nil {
		return preparedPromptSubmission{}, err
	}
	return preparedPromptSubmission{
		agent:        ag,
		runInput:     runInput,
		contentParts: append([]model.ContentPart(nil), contentParts...),
	}, nil
}

func (c *cliConsole) runPreparedSubmission(prepared preparedPromptSubmission, submission runtime.Submission) error {
	ctx := toolexec.WithApprover(c.baseCtx, c.approver)
	ctx = kernelpolicy.WithToolAuthorizer(ctx, c.approver)
	if c.tuiSender != nil {
		ctx = taskstream.WithStreamer(ctx, taskstream.StreamerFunc(func(_ context.Context, ev taskstream.Event) {
			if c.tuiSender == nil {
				return
			}
			c.tuiSender.Send(tuievents.TaskStreamMsg{
				Label:  ev.Label,
				TaskID: ev.TaskID,
				CallID: ev.CallID,
				Stream: ev.Stream,
				Chunk:  ev.Chunk,
				State:  ev.State,
				Reset:  ev.Reset,
				Final:  ev.Final,
			})
		}))
		ctx = toolexec.WithOutputStreamer(ctx, toolexec.OutputStreamerFunc(func(_ context.Context, chunk toolexec.OutputChunk) {
			if c.tuiSender == nil || !strings.EqualFold(strings.TrimSpace(chunk.ToolName), toolshell.BashToolName) {
				return
			}
			if strings.TrimSpace(chunk.Text) == "" {
				return
			}
			c.tuiSender.Send(tuievents.TaskStreamMsg{
				Label:  chunk.ToolName,
				CallID: chunk.ToolCallID,
				Stream: chunk.Stream,
				Chunk:  chunk.Text,
			})
		}))
		ctx = sessionstream.WithStreamer(ctx, sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
			c.forwardSessionEventToTUI(c.sessionID, update)
		}))
	}
	runCtx, cancel := context.WithCancel(ctx)
	pendingTUIToolCalls := map[string]toolCallSnapshot{}
	runner, err := c.rt.Run(runCtx, runtime.RunRequest{
		AppName:             c.appName,
		UserID:              c.userID,
		SessionID:           c.sessionID,
		Input:               submission.Text,
		ContentParts:        submission.ContentParts,
		Agent:               prepared.agent,
		Model:               c.llm,
		Tools:               c.resolved.Tools,
		CoreTools:           tool.CoreToolsConfig{Runtime: c.execRuntime},
		Policies:            c.resolved.Policies,
		ContextWindowTokens: c.contextWindow,
	})
	if err != nil {
		cancel()
		return err
	}
	c.setActiveRun(cancel, runner)
	defer func() {
		c.clearActiveRun()
		cancel()
		_ = runner.Close() // Close always returns nil; safe to ignore.
	}()
	return runRunner(runner, runRenderConfig{
		ShowReasoning: c.showReasoning,
		Verbose:       c.ui.verbose,
		Writer:        c.out,
		UI:            c.ui,
		OnEvent: func(ev *session.Event) bool {
			c.refreshContextUsageFromEvent(ev)
			return c.forwardEventToTUI(ev, pendingTUIToolCalls)
		},
		OnUsage: func(floor int) {
			c.refreshContextUsageEstimate(floor)
		},
	})
}

func (c *cliConsole) runBTW(question string, attachments []tuiapp.Attachment) error {
	question = strings.TrimSpace(question)
	if question == "/btw" || strings.HasPrefix(question, "/btw ") {
		question = strings.TrimSpace(strings.TrimPrefix(question, "/btw"))
	}
	if question == "" {
		return fmt.Errorf("usage: /btw <question>")
	}
	prepared, err := c.preparePromptSubmission(injectBTWPrompt(question), attachments)
	if err != nil {
		return err
	}
	submission := runtime.Submission{
		Text:         prepared.runInput,
		ContentParts: append([]model.ContentPart(nil), prepared.contentParts...),
		Mode:         runtime.SubmissionOverlay,
	}
	if runner := c.getActiveRunner(); runner != nil {
		return runner.Submit(submission)
	}
	if c.tuiSender != nil {
		return c.startBTWAsync(prepared, submission)
	}
	return c.runBTWBlocking(prepared, submission)
}

func (c *cliConsole) startBTWAsync(prepared preparedPromptSubmission, submission runtime.Submission) error {
	ctx := toolexec.WithApprover(c.baseCtx, c.approver)
	ctx = kernelpolicy.WithToolAuthorizer(ctx, c.approver)
	if c.tuiSender != nil {
		ctx = taskstream.WithStreamer(ctx, taskstream.StreamerFunc(func(_ context.Context, ev taskstream.Event) {
			if c.tuiSender == nil {
				return
			}
			c.tuiSender.Send(tuievents.TaskStreamMsg{
				Label:  ev.Label,
				TaskID: ev.TaskID,
				CallID: ev.CallID,
				Stream: ev.Stream,
				Chunk:  ev.Chunk,
				State:  ev.State,
				Reset:  ev.Reset,
				Final:  ev.Final,
			})
		}))
		ctx = toolexec.WithOutputStreamer(ctx, toolexec.OutputStreamerFunc(func(_ context.Context, chunk toolexec.OutputChunk) {
			if c.tuiSender == nil || !strings.EqualFold(strings.TrimSpace(chunk.ToolName), toolshell.BashToolName) {
				return
			}
			if strings.TrimSpace(chunk.Text) == "" {
				return
			}
			c.tuiSender.Send(tuievents.TaskStreamMsg{
				Label:  chunk.ToolName,
				CallID: chunk.ToolCallID,
				Stream: chunk.Stream,
				Chunk:  chunk.Text,
			})
		}))
		ctx = sessionstream.WithStreamer(ctx, sessionstream.StreamerFunc(func(_ context.Context, update sessionstream.Update) {
			c.forwardSessionEventToTUI(c.sessionID, update)
		}))
	}
	runCtx, cancel := context.WithCancel(ctx)
	runner, err := c.rt.Run(runCtx, runtime.RunRequest{
		AppName:             c.appName,
		UserID:              c.userID,
		SessionID:           c.sessionID,
		Agent:               prepared.agent,
		Model:               c.llm,
		Tools:               c.resolved.Tools,
		CoreTools:           tool.CoreToolsConfig{Runtime: c.execRuntime},
		Policies:            c.resolved.Policies,
		ContextWindowTokens: c.contextWindow,
	})
	if err != nil {
		cancel()
		return err
	}
	if err := runner.Submit(submission); err != nil {
		cancel()
		_ = runner.Close()
		return err
	}
	c.setActiveRun(cancel, runner)
	go func() {
		defer func() {
			c.clearActiveRun()
			cancel()
			_ = runner.Close()
			if c.tuiSender != nil {
				c.tuiSender.Send(tuievents.SetRunningMsg{Running: false})
			}
		}()
		pendingTUIToolCalls := map[string]toolCallSnapshot{}
		err := runRunner(runner, runRenderConfig{
			ShowReasoning: c.showReasoning,
			Verbose:       c.ui.verbose,
			Writer:        c.out,
			UI:            c.ui,
			OnEvent: func(ev *session.Event) bool {
				c.refreshContextUsageFromEvent(ev)
				return c.forwardEventToTUI(ev, pendingTUIToolCalls)
			},
			OnUsage: func(floor int) {
				c.refreshContextUsageEstimate(floor)
			},
		})
		if err != nil && c.tuiSender != nil {
			c.tuiSender.Send(tuievents.BTWErrorMsg{Text: err.Error()})
		}
	}()
	return nil
}

func (c *cliConsole) runBTWBlocking(prepared preparedPromptSubmission, submission runtime.Submission) error {
	ctx := toolexec.WithApprover(c.baseCtx, c.approver)
	ctx = kernelpolicy.WithToolAuthorizer(ctx, c.approver)
	runCtx, cancel := context.WithCancel(ctx)
	runner, err := c.rt.Run(runCtx, runtime.RunRequest{
		AppName:             c.appName,
		UserID:              c.userID,
		SessionID:           c.sessionID,
		Agent:               prepared.agent,
		Model:               c.llm,
		Tools:               c.resolved.Tools,
		CoreTools:           tool.CoreToolsConfig{Runtime: c.execRuntime},
		Policies:            c.resolved.Policies,
		ContextWindowTokens: c.contextWindow,
	})
	if err != nil {
		cancel()
		return err
	}
	if err := runner.Submit(submission); err != nil {
		cancel()
		_ = runner.Close()
		return err
	}
	c.setActiveRun(cancel, runner)
	defer func() {
		c.clearActiveRun()
		cancel()
		_ = runner.Close()
	}()
	return runRunner(runner, runRenderConfig{
		ShowReasoning: c.showReasoning,
		Verbose:       c.ui.verbose,
		Writer:        c.out,
		UI:            c.ui,
		OnUsage: func(floor int) {
			c.refreshContextUsageEstimate(floor)
		},
	})
}

func injectBTWPrompt(question string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return question
	}
	return question + "\n\n" + btwControlBlock()
}

func btwControlBlock() string {
	return btwControlOpenTag + `
This is an ephemeral /btw side question.
Answer in one short response using only the context already present in this session.
Do not call tools, do not read new files, do not run commands, and do not search.
Do not ask follow-up questions.
` + btwControlCloseTag
}

func (c *cliConsole) refreshContextUsageFromEvent(ev *session.Event) {
	if c == nil || ev == nil || eventIsPartial(ev) || ev.Message.Role != model.RoleAssistant {
		return
	}
	c.refreshContextUsageEstimate(usageFloorFromMeta(ev.Meta))
}

func (c *cliConsole) refreshContextUsageEstimate(minimum int) {
	if c == nil {
		return
	}
	if minimum < 0 {
		minimum = 0
	}
	current := minimum
	if c.rt != nil && strings.TrimSpace(c.appName) != "" && strings.TrimSpace(c.userID) != "" && strings.TrimSpace(c.sessionID) != "" {
		usage, err := c.rt.ContextUsage(c.baseCtx, runtime.UsageRequest{
			AppName:             c.appName,
			UserID:              c.userID,
			SessionID:           c.sessionID,
			Model:               c.llm,
			ContextWindowTokens: c.contextWindow,
		})
		if err == nil && usage.CurrentTokens > current {
			current = usage.CurrentTokens
		}
	}
	c.lastPromptTokens = current
}

func (c *cliConsole) emitAssistantEventToTUI(ev *session.Event) {
	if c == nil || c.tuiSender == nil || ev == nil {
		return
	}
	msg := ev.Message
	if msg.Role != model.RoleAssistant {
		return
	}
	if session.IsOverlay(ev) {
		if eventIsPartial(ev) {
			c.tuiSender.Send(tuievents.BTWOverlayMsg{Text: msg.Text, Final: false})
			return
		}
		c.tuiSender.Send(tuievents.BTWOverlayMsg{Text: strings.TrimSpace(msg.Text), Final: true})
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

type tuiForwardOptions struct {
	ShowMutationDiff bool
	ReplayMode       bool
}

func (c *cliConsole) forwardEventToTUI(ev *session.Event, pendingToolCalls map[string]toolCallSnapshot) bool {
	return c.forwardEventToTUIWithOptions(ev, pendingToolCalls, tuiForwardOptions{
		ShowMutationDiff: true,
	})
}

func (c *cliConsole) forwardEventToTUIWithOptions(ev *session.Event, pendingToolCalls map[string]toolCallSnapshot, opts tuiForwardOptions) bool {
	if c == nil || c.tuiSender == nil || ev == nil {
		return false
	}
	if session.IsOverlay(ev) {
		c.emitAssistantEventToTUI(ev)
		return true
	}
	if notice, ok := session.EventNotice(ev); ok {
		c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: formatSessionNoticeChunk(notice)})
		return true
	}
	msg := ev.Message
	handled := false
	if msg.Role == model.RoleSystem {
		text := strings.TrimSpace(msg.Text)
		if text != "" {
			c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: text + "\n"})
			return true
		}
	}
	if msg.Role == model.RoleAssistant {
		// Keep assistant rendering deterministic, even for mixed assistant+toolcall events.
		c.emitAssistantEventToTUI(ev)
		handled = true
	}
	if msg.Role == model.RoleUser {
		userText := visibleUserText(msg)
		if userText != "" {
			c.tuiSender.Send(tuievents.UserMessageMsg{Text: userText})
			return true
		}
	}
	if len(msg.ToolCalls) > 0 {
		previewRuntime := c.execRuntime
		previewFS := newMutationPreviewFS(nil)
		if !opts.ReplayMode && c.execRuntime != nil && c.execRuntime.FileSystem() != nil {
			previewFS = newMutationPreviewFS(c.execRuntime.FileSystem())
			previewRuntime = mutationPreviewRuntime{base: c.execRuntime, fsys: previewFS}
		}
		for _, call := range msg.ToolCalls {
			if isPlanToolName(call.Name) {
				if pendingToolCalls != nil && call.ID != "" {
					pendingToolCalls[call.ID] = toolCallSnapshot{
						Args: cloneAnyMap(parseToolArgsForDisplay(call.Args)),
					}
				}
				handled = true
				continue
			}
			parsedArgs := parseToolArgsForDisplay(call.Args)
			visuals := toolCallMutationVisuals{}
			visualsOK := false
			if isFileMutationTool(call.Name) && !opts.ReplayMode {
				visuals, visualsOK = buildToolCallMutationVisuals(previewRuntime, call.Name, parsedArgs)
				if visualsOK && previewFS != nil && strings.TrimSpace(visuals.PreviewPath) != "" {
					previewFS.Stage(visuals.PreviewPath, visuals.PreviewNew)
				}
			}
			if pendingToolCalls != nil && call.ID != "" {
				diffShown := opts.ShowMutationDiff && visualsOK && visuals.DiffShown
				pendingToolCalls[call.ID] = toolCallSnapshot{
					Args:          cloneAnyMap(parsedArgs),
					RichDiffShown: diffShown,
					ChangeCounts:  visuals.ChangeCounts,
				}
			}
			c.tuiSender.Send(tuievents.LogChunkMsg{
				Chunk: fmt.Sprintf("▸ %s %s\n", displayToolCallName(call.Name, parsedArgs), summarizeToolArgs(call.Name, parsedArgs)),
			})
			if strings.EqualFold(strings.TrimSpace(call.Name), toolshell.BashToolName) || strings.EqualFold(strings.TrimSpace(call.Name), tool.DelegateTaskToolName) {
				c.tuiSender.Send(tuievents.TaskStreamMsg{
					Label:  call.Name,
					CallID: call.ID,
					Reset:  true,
				})
			}
			if opts.ShowMutationDiff && visualsOK && visuals.DiffShown {
				c.tuiSender.Send(visuals.DiffMsg)
			}
		}
		handled = true
	}
	if msg.ToolResponse != nil {
		c.syncBashTaskWatch(msg.ToolResponse.ID, msg.ToolResponse.Name, msg.ToolResponse.Result)
		emittedTaskStream := c.emitTaskStreamFromToolResult(msg.ToolResponse)
		toolName := strings.TrimSpace(msg.ToolResponse.Name)
		if strings.EqualFold(strings.TrimSpace(msg.ToolResponse.Name), toolshell.BashToolName) {
			c.tuiSender.Send(tuievents.TaskStreamMsg{
				Label:  msg.ToolResponse.Name,
				CallID: msg.ToolResponse.ID,
				Final:  true,
			})
			hasError := hasToolError(msg.ToolResponse.Result)
			exitCode, _ := asInt(msg.ToolResponse.Result["exit_code"])
			if strings.TrimSpace(asString(msg.ToolResponse.Result["stdout"])) != "" ||
				strings.TrimSpace(asString(msg.ToolResponse.Result["stderr"])) != "" {
				return true
			}
			if emittedTaskStream || (!hasError && exitCode == 0) {
				return true
			}
		}
		var (
			callArgs      map[string]any
			richDiffShown bool
			changeCounts  mutationChangeCounts
		)
		if pendingToolCalls != nil && msg.ToolResponse.ID != "" {
			if snapshot, ok := pendingToolCalls[msg.ToolResponse.ID]; ok {
				callArgs = snapshot.Args
				richDiffShown = snapshot.RichDiffShown
				changeCounts = snapshot.ChangeCounts
				delete(pendingToolCalls, msg.ToolResponse.ID)
			}
		}
		if isFileMutationTool(msg.ToolResponse.Name) && !hasToolError(msg.ToolResponse.Result) {
			if richDiffShown {
				return true
			}
			if resultCounts := mutationChangeCountsFromResult(msg.ToolResponse.Name, msg.ToolResponse.Result, callArgs); resultCounts != (mutationChangeCounts{}) {
				changeCounts = resultCounts
			} else if opts.ReplayMode && strings.EqualFold(msg.ToolResponse.Name, "WRITE") {
				if legacyCounts := legacyWriteMutationChangeCounts(msg.ToolResponse.Result, callArgs); legacyCounts != (mutationChangeCounts{}) {
					changeCounts = legacyCounts
				}
			}
			summary := formatMutationChangeSummary(changeCounts)
			if changeCounts == (mutationChangeCounts{}) {
				summary = summarizeToolResponseWithCall(msg.ToolResponse.Name, msg.ToolResponse.Result, callArgs)
			}
			displayName := displayToolResponseName(toolName, callArgs, msg.ToolResponse.Result)
			c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: formatToolResultLine("✓ ", displayName, summary)})
			return true
		}
		if strings.EqualFold(toolName, tool.PlanToolName) && !hasToolError(msg.ToolResponse.Result) {
			c.tuiSender.Send(planUpdateMsgFromToolPayload(callArgs, msg.ToolResponse.Result))
			return true
		}
		if compact := summarizeCompactToolResponseForTUI(msg.ToolResponse.Name, msg.ToolResponse.Result); compact != "" && !hasToolError(msg.ToolResponse.Result) {
			displayName := displayToolResponseName(toolName, callArgs, msg.ToolResponse.Result)
			c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: formatToolResultLine("✓ ", displayName, compact)})
			return true
		}
		if strings.EqualFold(toolName, tool.DelegateTaskToolName) && !hasToolError(msg.ToolResponse.Result) {
			return true
		}
		// Suppress result line for read-only FS tools (the call line is sufficient).
		if isReadOnlyFSTool(msg.ToolResponse.Name) && !hasToolError(msg.ToolResponse.Result) {
			return true
		}
		summary := summarizeToolResponseWithCall(msg.ToolResponse.Name, msg.ToolResponse.Result, callArgs)
		if strings.EqualFold(toolName, tool.TaskToolName) && emittedTaskStream {
			summary = ""
		}
		if strings.EqualFold(toolName, tool.DelegateTaskToolName) && emittedTaskStream {
			summary = ""
		}
		if strings.TrimSpace(summary) != "" {
			prefix := "✓ "
			if hasToolError(msg.ToolResponse.Result) && !strings.EqualFold(toolName, toolshell.BashToolName) {
				prefix = "! "
			}
			displayName := displayToolResponseName(toolName, callArgs, msg.ToolResponse.Result)
			c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: formatToolResultLine(prefix, displayName, summary)})
		}
		if strings.EqualFold(toolName, tool.TaskToolName) {
			return emittedTaskStream || strings.TrimSpace(summary) != ""
		}
		return true
	}
	return handled
}

func formatSessionNoticeChunk(notice session.Notice) string {
	text := strings.TrimSpace(notice.Text)
	if text == "" {
		return ""
	}
	switch notice.Level {
	case session.NoticeLevelWarn:
		return fmt.Sprintf("! %s\n", text)
	case session.NoticeLevelNote:
		return fmt.Sprintf("  note: %s\n", text)
	default:
		return text + "\n"
	}
}

func (c *cliConsole) emitTaskStreamFromToolResult(resp *model.ToolResponse) bool {
	if c == nil || c.tuiSender == nil || resp == nil {
		return false
	}
	events := taskstream.EventsFromResult(resp.Result)
	if len(events) == 0 {
		return false
	}
	for _, ev := range events {
		label := ev.Label
		if label == "" {
			label = resp.Name
		}
		callID := ev.CallID
		if callID == "" {
			callID = resp.ID
		}
		msg := tuievents.TaskStreamMsg{
			Label:  label,
			TaskID: ev.TaskID,
			CallID: callID,
			Stream: ev.Stream,
			Chunk:  ev.Chunk,
			State:  ev.State,
			Reset:  ev.Reset,
			Final:  ev.Final,
		}
		if c.shouldSuppressWatchedBashTaskStream(resp.Name, msg) {
			continue
		}
		c.tuiSender.Send(msg)
	}
	return true
}

func planUpdateMsgFromToolPayload(callArgs map[string]any, result map[string]any) tuievents.PlanUpdateMsg {
	var msg tuievents.PlanUpdateMsg
	var entries []tuievents.PlanEntry
	for _, source := range []any{callArgs["entries"], result["entries"]} {
		if err := decodePlanEntries(source, &entries); err == nil {
			break
		}
		entries = nil
	}
	for _, item := range entries {
		content := strings.TrimSpace(item.Content)
		status := strings.TrimSpace(item.Status)
		if content == "" || status == "" {
			continue
		}
		msg.Entries = append(msg.Entries, tuievents.PlanEntry{Content: content, Status: status})
	}
	return msg
}

func isPlanToolName(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), tool.PlanToolName)
}

func decodePlanEntries(in any, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (c *cliConsole) setActiveRun(cancel context.CancelFunc, runner runtime.Runner) {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	c.activeRunCancel = cancel
	c.activeRunner = runner
}

func (c *cliConsole) clearActiveRun() {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	c.activeRunCancel = nil
	c.activeRunner = nil
}

func (c *cliConsole) setActiveRunCancel(cancel context.CancelFunc) {
	c.setActiveRun(cancel, nil)
}

func (c *cliConsole) clearActiveRunCancel() {
	c.clearActiveRun()
}

func (c *cliConsole) getActiveRunner() runtime.Runner {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	return c.activeRunner
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
	helpSection("Security", []string{"sandbox"})
	helpSection("Other", []string{"btw", "help", "version", "exit", "quit"})
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

func handleBTW(c *cliConsole, args []string) (bool, error) {
	question := strings.TrimSpace(strings.Join(args, " "))
	if question == "" {
		return false, fmt.Errorf("usage: /btw <question>")
	}
	return false, c.runBTW(question, nil)
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
	if err := c.persistSessionMode(); err != nil {
		return false, err
	}
	if c.tuiSender != nil {
		c.tuiSender.Send(tuievents.ClearHistoryMsg{})
		c.tuiSender.Send(tuievents.PlanUpdateMsg{})
		c.tuiSender.Send(tuievents.AttachmentCountMsg{Count: 0})
		modelText, contextText := c.readTUIStatus()
		c.tuiSender.Send(tuievents.SetStatusMsg{Model: modelText, Context: contextText})
		c.tuiSender.Send(tuievents.SetHintMsg{Hint: "started new session", ClearAfter: transientHintDuration})
		return false, nil
	}
	c.printf("new session started: %s\n", idutil.ShortDisplay(c.sessionID))
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
	if err := c.persistSessionMode(); err != nil {
		return false, err
	}
	if c.tuiSender != nil {
		c.tuiSender.Send(tuievents.AttachmentCountMsg{Count: 0})
		c.tuiSender.Send(tuievents.PlanUpdateMsg{})
		c.tuiSender.Send(tuievents.SetHintMsg{Hint: "fork succeeded", ClearAfter: transientHintDuration})
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
		c.refreshContextUsageEstimate(beforeUsage.CurrentTokens)
		c.syncTUIStatus()
		if beforeUsage.WindowTokens > 0 {
			c.printf("compact: skipped (%s tokens)\n", formatCompactTokenUsage(beforeUsage.CurrentTokens))
		}
	} else {
		afterUsage, _ := c.rt.ContextUsage(c.baseCtx, runtime.UsageRequest{
			AppName:             c.appName,
			UserID:              c.userID,
			SessionID:           c.sessionID,
			Model:               c.llm,
			ContextWindowTokens: c.contextWindow,
		})
		c.refreshContextUsageEstimate(afterUsage.CurrentTokens)
		c.syncTUIStatus()
		c.printf("compact: success, %s -> %s tokens\n", formatCompactTokenUsage(beforeUsage.CurrentTokens), formatCompactTokenUsage(afterUsage.CurrentTokens))
	}
	return false, nil
}

func (c *cliConsole) syncTUIStatus() {
	if c == nil || c.tuiSender == nil {
		return
	}
	modelText, contextText := c.readTUIStatus()
	c.tuiSender.Send(tuievents.SetStatusMsg{Model: modelText, Context: contextText})
}

func handleStatus(c *cliConsole, args []string) (bool, error) {
	_ = args
	c.ui.Section("Model")
	c.ui.KeyValue("model", c.modelAlias)
	c.ui.KeyValue("stream", fmt.Sprintf("%v", c.streamModel))
	effortLabel := strings.TrimSpace(c.reasoningEffort)
	if effortLabel == "" {
		effortLabel = "auto"
	}
	c.ui.KeyValue("reasoning", fmt.Sprintf("%s (budget=%d)", effortLabel, c.thinkingBudget))
	c.ui.KeyValue("effort", c.reasoningEffort)
	c.ui.KeyValue("reasoning", fmt.Sprintf("%v", c.showReasoning))

	c.ui.Section("Session")
	c.ui.KeyValue("workspace", c.workspace.CWD)
	c.ui.KeyValue("session", idutil.ShortDisplay(c.sessionID))
	c.ui.KeyValue("mode", sessionmode.Normalize(c.sessionMode))

	c.ui.Section("Security")
	mode := c.execRuntime.PermissionMode()
	switch mode {
	case toolexec.PermissionModeFullControl:
		c.ui.KeyValue("permission", fmt.Sprintf("%s  route=host", mode))
	default:
		if c.execRuntime.FallbackToHost() {
			c.ui.KeyValue("permission", fmt.Sprintf("%s  sandbox=%s  route=host (fallback, reason=%s)",
				mode, sandboxTypeDisplayLabel(c.execRuntime.SandboxType()), c.execRuntime.FallbackReason()))
		} else {
			c.ui.KeyValue("permission", fmt.Sprintf("%s  sandbox=%s  route=sandbox",
				mode, sandboxTypeDisplayLabel(c.execRuntime.SandboxType())))
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
		c.printf("permission_mode=%s session_mode=%s sandbox_type=%s\n", c.execRuntime.PermissionMode(), sessionmode.Normalize(c.sessionMode), sandboxTypeDisplayLabel(c.sandboxType))
		return false, nil
	}
	if len(args) != 1 {
		return false, fmt.Errorf("usage: /permission [default|full_control]")
	}
	raw := strings.ToLower(strings.TrimSpace(args[0]))
	if raw == sessionmode.FullMode {
		raw = string(toolexec.PermissionModeFullControl)
	}
	mode := toolexec.PermissionMode(raw)
	switch mode {
	case toolexec.PermissionModeDefault, toolexec.PermissionModeFullControl:
	default:
		return false, fmt.Errorf("invalid permission mode %q, expected default|full_control", args[0])
	}
	if err := c.setPermissionMode(mode); err != nil {
		return false, err
	}
	if c.execRuntime.FallbackToHost() {
		c.printf("permission updated: permission_mode=%s session_mode=%s sandbox_type=%s (fallback: host+approval, reason=%s)\n", c.execRuntime.PermissionMode(), sessionmode.Normalize(c.sessionMode), sandboxTypeDisplayLabel(c.sandboxType), c.execRuntime.FallbackReason())
	} else {
		c.printf("permission updated: permission_mode=%s session_mode=%s sandbox_type=%s\n", c.execRuntime.PermissionMode(), sessionmode.Normalize(c.sessionMode), sandboxTypeDisplayLabel(c.sandboxType))
	}
	return false, nil
}

func handleSandbox(c *cliConsole, args []string) (bool, error) {
	if len(args) == 0 {
		c.printf("sandbox_type=%s permission_mode=%s\n", sandboxTypeDisplayLabel(c.sandboxType), c.execRuntime.PermissionMode())
		return false, nil
	}
	if len(args) != 1 {
		return false, fmt.Errorf("usage: /sandbox [auto|<type>]")
	}
	rawSandboxType := strings.TrimSpace(args[0])
	if rawSandboxType == "" {
		return false, fmt.Errorf("sandbox type cannot be empty")
	}
	sandboxType := normalizeSandboxType(rawSandboxType)
	if sandboxType == "" && !strings.EqualFold(rawSandboxType, "auto") && !strings.EqualFold(rawSandboxType, "default") {
		return false, fmt.Errorf("invalid sandbox type %q, expected auto|bwrap|landlock", args[0])
	}
	if sandboxType != "" {
		// Validate type by constructing a default-mode runtime.
		if err := validateExplicitSandboxType(sandboxType, c.sandboxHelperPath); err != nil {
			return false, err
		}
	}
	c.sandboxType = sandboxType
	mode := c.execRuntime.PermissionMode()
	if mode == toolexec.PermissionModeFullControl {
		c.persistRuntimeSettings()
		c.printf("sandbox updated: sandbox_type=%s (will apply when permission_mode=default)\n", sandboxTypeDisplayLabel(c.sandboxType))
		return false, nil
	}
	if err := c.updateExecutionRuntime(mode, c.sandboxType); err != nil {
		return false, err
	}
	c.persistRuntimeSettings()
	if c.execRuntime.FallbackToHost() {
		c.printf("sandbox updated: sandbox_type=%s (fallback: host+approval, reason=%s)\n", sandboxTypeDisplayLabel(c.sandboxType), c.execRuntime.FallbackReason())
	} else {
		c.printf("sandbox updated: sandbox_type=%s\n", sandboxTypeDisplayLabel(c.sandboxType))
	}
	return false, nil
}

func handleModel(c *cliConsole, args []string) (bool, error) {
	if len(args) == 0 {
		return false, fmt.Errorf("usage: /model use <alias> [reasoning] | /model del [alias ...]")
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "use":
		return handleModelUse(c, args[1:])
	case "del":
		return handleModelDelete(c, args[1:])
	default:
		return handleModelUse(c, args)
	}
}

func handleModelUse(c *cliConsole, args []string) (bool, error) {
	if len(args) < 1 || len(args) > 2 {
		return false, fmt.Errorf("usage: /model use <alias> [reasoning]")
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
		settings.ReasoningEffort = opt.ReasoningEffort
	}

	c.modelAlias = targetAlias
	c.llm = llm
	if len(args) == 2 {
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
		displayReasoning := ""
		if cfg, ok := c.modelFactory.ConfigForAlias(targetAlias); ok {
			displayReasoning = selectedReasoningDisplay(cfg, c.reasoningEffort)
		}
		if strings.TrimSpace(c.reasoningEffort) != "" {
			if strings.TrimSpace(displayReasoning) == "" {
				displayReasoning = c.reasoningEffort
			}
			c.printModelSwitchMessage("model switched to %s (reasoning=%s)\n", alias, displayReasoning)
		} else {
			c.printModelSwitchMessage("model switched to %s (reasoning=auto)\n", alias)
		}
	} else {
		c.printModelSwitchMessage("model switched to %s\n", alias)
	}
	return false, nil
}

func handleModelDelete(c *cliConsole, args []string) (bool, error) {
	if c.configStore == nil {
		return false, fmt.Errorf("config store is not configured")
	}
	aliases, err := resolveDeleteModelAliases(c, args)
	if err != nil {
		return false, err
	}
	removedAliases := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		removed, ok, err := c.configStore.RemoveProvider(alias)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, fmt.Errorf("model %q is not configured", alias)
		}
		credentialRef := normalizeCredentialRef(removed.Auth.CredentialRef)
		if credentialRef == "" {
			credentialRef = defaultCredentialRef(removed.Provider, removed.BaseURL)
		}
		if credentialRef != "" && c.credentialStore != nil && !c.configStore.CredentialRefInUse(credentialRef, removed.Alias) {
			if err := c.credentialStore.Delete(credentialRef); err != nil {
				return false, err
			}
		}
		removedAliases = append(removedAliases, strings.ToLower(strings.TrimSpace(removed.Alias)))
	}
	if err := c.reloadConfiguredModels(); err != nil {
		return false, err
	}
	for _, alias := range removedAliases {
		c.printf("model removed: %s\n", alias)
	}
	return false, nil
}

func resolveDeleteModelAliases(c *cliConsole, args []string) ([]string, error) {
	if c == nil || c.configStore == nil {
		return nil, fmt.Errorf("config store is not configured")
	}
	normalize := func(raw string) string {
		value := strings.TrimSpace(raw)
		if value == "" {
			return ""
		}
		if resolved := c.configStore.ResolveModelAlias(value); resolved != "" {
			value = resolved
		}
		return strings.ToLower(strings.TrimSpace(value))
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(args))
	for _, raw := range args {
		for _, part := range splitArrayInput(raw) {
			alias := normalize(part)
			if alias == "" {
				continue
			}
			if _, ok := seen[alias]; ok {
				continue
			}
			seen[alias] = struct{}{}
			out = append(out, alias)
		}
	}
	if len(out) > 0 {
		return out, nil
	}
	choices := configuredModelDeleteChoices(c)
	if len(choices) == 0 {
		return nil, fmt.Errorf("no configured models")
	}
	selected, err := c.promptMultiChoice("Select models to remove", choices, true)
	if err != nil {
		return nil, err
	}
	for _, one := range selected {
		alias := normalize(one)
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		out = append(out, alias)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no model selected")
	}
	return out, nil
}

func configuredModelDeleteChoices(c *cliConsole) []promptChoiceItem {
	if c == nil || c.configStore == nil {
		return nil
	}
	aliases := c.configStore.ConfiguredModelAliases()
	choices := make([]promptChoiceItem, 0, len(aliases))
	for _, alias := range aliases {
		alias = strings.ToLower(strings.TrimSpace(alias))
		if alias == "" {
			continue
		}
		choices = append(choices, promptChoiceItem{
			Label: alias,
			Value: alias,
		})
	}
	return choices
}

func selectedReasoningDisplay(cfg modelproviders.Config, effort string) string {
	profile := reasoningProfileForConfig(cfg)
	effort = normalizeReasoningLevel(effort)
	switch profile.Mode {
	case reasoningModeToggle:
		if effort == "none" {
			return "off"
		}
		return "on"
	default:
		return effort
	}
}

func (c *cliConsole) printModelSwitchMessage(format string, args ...any) {
	if c != nil && c.ui != nil {
		c.ui.Note(format, args...)
		return
	}
	c.printf("note: "+format, args...)
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
	caps, found := lookupCatalogModelCapabilities(cfg.Provider, cfg.Model)
	if found && caps.ContextWindowTokens > 0 {
		return caps.ContextWindowTokens
	}
	return 0
}

func (c *cliConsole) applyModelRuntimeSettings(alias string) {
	settings := modelRuntimeSettings{
		ThinkingBudget:  defaultThinkingBudget,
		ReasoningEffort: defaultReasoningEffort,
	}
	if c.configStore != nil {
		settings = c.configStore.ModelRuntimeSettings(alias)
	}
	c.thinkingBudget = settings.ThinkingBudget
	c.reasoningEffort = settings.ReasoningEffort
}

func (c *cliConsole) reloadConfiguredModels() error {
	factory := modelproviders.NewFactory()
	if c.configStore != nil {
		for _, providerCfg := range c.configStore.ProviderConfigs() {
			providerCfg = hydrateProviderAuthToken(providerCfg, c.credentialStore)
			modelcatalogApplyConfigDefaults(&providerCfg)
			if err := factory.Register(providerCfg); err != nil {
				return err
			}
		}
	}
	c.modelFactory = factory

	currentAlias := strings.ToLower(strings.TrimSpace(c.modelAlias))
	if c.configStore != nil && currentAlias != "" {
		currentAlias = c.configStore.ResolveModelAlias(currentAlias)
	}
	if currentAlias == "" && c.configStore != nil {
		currentAlias = c.configStore.DefaultModel()
	}
	if currentAlias != "" {
		if _, ok := factory.ConfigForAlias(currentAlias); !ok && c.configStore != nil {
			currentAlias = c.configStore.DefaultModel()
		}
	}
	if currentAlias == "" {
		c.modelAlias = ""
		c.llm = nil
		c.applyModelRuntimeSettings("")
		return nil
	}
	llm, err := factory.NewByAlias(currentAlias)
	if err != nil {
		c.modelAlias = ""
		c.llm = nil
		c.applyModelRuntimeSettings("")
		return nil
	}
	c.modelAlias = currentAlias
	c.llm = llm
	c.applyModelRuntimeSettings(currentAlias)
	return nil
}

func handleTools(c *cliConsole, args []string) (bool, error) {
	_ = args
	coreTools, err := tool.EnsureCoreTools(c.resolved.Tools, tool.CoreToolsConfig{
		Runtime: c.execRuntime,
	})
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
	discovered := appskills.DiscoverMeta(c.skillDirs)
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
		resolved, ok, err := c.sessionIndex.ResolveWorkspaceSessionID(c.workspace.Key, target)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, fmt.Errorf("session %q not found in current workspace", target)
		}
		target = resolved
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
	if _, err := c.rt.ReconcileSession(c.baseCtx, runtime.ReconcileSessionRequest{
		AppName:     c.appName,
		UserID:      c.userID,
		SessionID:   c.sessionID,
		ExecRuntime: c.execRuntime,
	}); err != nil {
		return false, err
	}
	if err := c.restoreSessionMode(c.loadSessionMode()); err != nil {
		return false, err
	}
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
	c.refreshContextUsageEstimate(extractLastUsage(events))
	if c.tuiSender == nil || len(events) == 0 {
		return nil
	}
	// In TUI mode, replay directly through structured events so assistant
	// Markdown is rendered by the same block renderer as live streaming,
	// avoiding mixed prefix-coloring and formatting artifacts.
	c.tuiSender.Send(tuievents.ClearHistoryMsg{})
	c.tuiSender.Send(tuievents.PlanUpdateMsg{})
	modelText, contextText := c.readTUIStatus()
	c.tuiSender.Send(tuievents.SetStatusMsg{Model: modelText, Context: contextText})
	pendingToolCalls := map[string]toolCallSnapshot{}
	for _, ev := range events {
		if ev == nil || eventIsPartial(ev) {
			continue
		}
		msg := ev.Message
		if msg.Role == model.RoleUser {
			userText := visibleUserText(msg)
			if userText != "" {
				c.tuiSender.Send(tuievents.UserMessageMsg{Text: userText})
			}
			continue
		}
		if c.forwardEventToTUIWithOptions(ev, pendingToolCalls, tuiForwardOptions{
			ShowMutationDiff: false,
			ReplayMode:       true,
		}) {
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
	c.tuiSender.Send(tuievents.TaskResultMsg{})
	return nil
}

func (c *cliConsole) updateExecutionRuntime(mode toolexec.PermissionMode, sandboxType string) error {
	prevRuntime := c.execRuntime
	nextRuntime, err := newExecutionRuntime(mode, sandboxType, c.sandboxHelperPath)
	if err != nil {
		return err
	}
	c.execRuntime = nextRuntime
	if c.execRuntimeView != nil {
		c.execRuntimeView.Set(nextRuntime)
	}
	if err := c.refreshShellToolRuntime(); err != nil {
		c.execRuntime = prevRuntime
		if c.execRuntimeView != nil {
			c.execRuntimeView.Set(prevRuntime)
		}
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
	modeResolver   func() string
	mu             sync.RWMutex
	queue          *approvalqueue.Queue
	sessionAllowed map[string]struct{}
	authAllowed    map[string]struct{}
}

func newTerminalApprover(prompter promptReader, out io.Writer, u *ui) *terminalApprover {
	return &terminalApprover{
		prompter:       prompter,
		out:            out,
		ui:             u,
		queue:          approvalqueue.New(),
		sessionAllowed: map[string]struct{}{},
		authAllowed:    map[string]struct{}{},
	}
}

func (a *terminalApprover) Approve(ctx context.Context, req toolexec.ApprovalRequest) (bool, error) {
	_ = ctx
	if sessionmode.IsFullAccess(a.currentMode()) {
		if sessionmode.IsDangerousCommand(req.Command) {
			return false, &toolexec.ApprovalAbortedError{Reason: "dangerous command blocked in full_access mode"}
		}
		return true, nil
	}
	key := sessionApprovalKey(req.Command)
	if a.prompter == nil {
		return false, &toolexec.ApprovalAbortedError{Reason: "no interactive approver available"}
	}
	if a.isAllowedInSession(req.Command) {
		return true, nil
	}
	var allowed bool
	err := a.queue.Do(ctx, func(context.Context) error {
		if a.isAllowedInSession(req.Command) {
			allowed = true
			return nil
		}
		line, err := a.readApprovalChoice(req, key)
		if err != nil {
			if errors.Is(err, errInputInterrupt) || errors.Is(err, errInputEOF) {
				a.emitCommandApprovalOutcome(req, key, "cancel")
				return &toolexec.ApprovalAbortedError{Reason: "approval canceled by user"}
			}
			return err
		}
		line = strings.ToLower(strings.TrimSpace(line))
		switch line {
		case "y", "yes", "o", "once", "proceed":
			a.emitCommandApprovalOutcome(req, key, "once")
			allowed = true
			return nil
		case "a", "always", "s", "session":
			if key != "" {
				a.mu.Lock()
				a.sessionAllowed[key] = struct{}{}
				a.mu.Unlock()
			}
			a.emitCommandApprovalOutcome(req, key, "session")
			allowed = true
			return nil
		case "n", "no", "", "c", "cancel":
			a.emitCommandApprovalOutcome(req, key, "cancel")
			return &toolexec.ApprovalAbortedError{Reason: "approval denied by user"}
		default:
			a.emitCommandApprovalOutcome(req, key, "cancel")
			return &toolexec.ApprovalAbortedError{Reason: "approval denied by user"}
		}
	})
	return allowed, err
}

func (a *terminalApprover) AuthorizeTool(ctx context.Context, req kernelpolicy.ToolAuthorizationRequest) (bool, error) {
	_ = ctx
	if sessionmode.IsFullAccess(a.currentMode()) {
		return true, nil
	}
	scopeKey := toolAuthorizationScopeKey(req)
	if scopeKey == "" {
		return true, nil
	}
	if a.prompter == nil {
		return false, &toolexec.ApprovalAbortedError{Reason: "no interactive approver available"}
	}
	if a.isAuthorizationAllowedInSession(scopeKey) {
		return true, nil
	}
	var allowed bool
	err := a.queue.Do(ctx, func(context.Context) error {
		if a.isAuthorizationAllowedInSession(scopeKey) {
			allowed = true
			return nil
		}
		line, err := a.readToolAuthorizationChoice(req, scopeKey)
		if err != nil {
			if errors.Is(err, errInputInterrupt) || errors.Is(err, errInputEOF) {
				a.emitToolApprovalOutcome(req, scopeKey, "cancel")
				return &toolexec.ApprovalAbortedError{Reason: "approval canceled by user"}
			}
			return err
		}
		line = strings.ToLower(strings.TrimSpace(line))
		switch line {
		case "y", "yes", "o", "once", "proceed":
			a.emitToolApprovalOutcome(req, scopeKey, "once")
			allowed = true
			return nil
		case "a", "always", "s", "session":
			a.mu.Lock()
			a.authAllowed[scopeKey] = struct{}{}
			a.mu.Unlock()
			a.emitToolApprovalOutcome(req, scopeKey, "session")
			allowed = true
			return nil
		case "n", "no", "", "c", "cancel":
			a.emitToolApprovalOutcome(req, scopeKey, "cancel")
			return &toolexec.ApprovalAbortedError{Reason: "approval denied by user"}
		default:
			a.emitToolApprovalOutcome(req, scopeKey, "cancel")
			return &toolexec.ApprovalAbortedError{Reason: "approval denied by user"}
		}
	})
	return allowed, err
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

func (a *terminalApprover) isAuthorizationAllowedInSession(scopeKey string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.authAllowed[scopeKey]
	return ok
}

func (a *terminalApprover) currentMode() string {
	if a == nil || a.modeResolver == nil {
		return sessionmode.DefaultMode
	}
	return sessionmode.Normalize(a.modeResolver())
}

func sessionApprovalKey(command string) string {
	segments := shellCommandSegments(command)
	for _, segment := range segments {
		tokens := shellSegmentTokens(segment)
		if len(tokens) == 0 {
			continue
		}
		base := strings.ToLower(filepath.Base(tokens[0]))
		if isApprovalWrapperCommand(base) {
			continue
		}
		switch base {
		case "go", "git", "npm", "pnpm", "yarn", "cargo", "make":
			if len(tokens) > 1 && !strings.HasPrefix(tokens[1], "-") {
				return strings.TrimSpace(base + " " + strings.ToLower(tokens[1]))
			}
			return strings.TrimSpace(base)
		default:
			return ""
		}
	}
	return ""
}

func toolApprovalKey(toolName string) string {
	return strings.ToUpper(strings.TrimSpace(toolName))
}

func toolAuthorizationScopeKey(req kernelpolicy.ToolAuthorizationRequest) string {
	if key := strings.TrimSpace(req.ScopeKey); key != "" {
		return key
	}
	if path := strings.TrimSpace(req.Path); path != "" {
		return filepath.Dir(path)
	}
	return toolApprovalKey(req.ToolName)
}

func (a *terminalApprover) readApprovalChoice(req toolexec.ApprovalRequest, sessionKey string) (string, error) {
	if chooser, ok := a.prompter.(structuredPromptReader); ok {
		return chooser.RequestStructuredPrompt(commandApprovalPromptRequest(req, sessionKey))
	}
	a.renderCommandApprovalRequest(req)
	if chooser, ok := a.prompter.(choicePromptReader); ok {
		return chooser.RequestChoicePrompt(
			commandApprovalTitle(),
			approvalChoicesForSessionKey(sessionKey),
			"y",
			false,
		)
	}
	if sessionKey != "" {
		return a.prompter.ReadLine(approvalPromptAllowAlwaysDeny)
	}
	return a.prompter.ReadLine(approvalPromptAllowDeny)
}

func (a *terminalApprover) readToolAuthorizationChoice(req kernelpolicy.ToolAuthorizationRequest, scopeKey string) (string, error) {
	if chooser, ok := a.prompter.(structuredPromptReader); ok {
		return chooser.RequestStructuredPrompt(toolAuthorizationPromptRequest(req, scopeKey))
	}
	a.renderToolAuthorizationRequest(req)
	if chooser, ok := a.prompter.(choicePromptReader); ok {
		return chooser.RequestChoicePrompt(
			toolAuthorizationTitle(req),
			toolAuthorizationChoices(scopeKey),
			"y",
			false,
		)
	}
	return a.prompter.ReadLine(toolAuthPrompt)
}

func (a *terminalApprover) renderCommandApprovalRequest(req toolexec.ApprovalRequest) {
	if a == nil || a.ui == nil {
		return
	}
	a.ui.ApprovalTitle(commandApprovalTitle())
	if label, value := commandApprovalSummary(req); label != "" && value != "" {
		a.ui.ApprovalMeta(label, value)
	}
	if reason := approvalReasonText(req.Reason); reason != "" {
		a.ui.ApprovalMeta("Reason", reason)
	}
}

func (a *terminalApprover) renderToolAuthorizationRequest(req kernelpolicy.ToolAuthorizationRequest) {
	if a == nil || a.ui == nil {
		return
	}
	a.ui.ApprovalTitle(toolAuthorizationTitle(req))
	if label, value := toolAuthorizationSummary(req); label != "" && value != "" {
		a.ui.ApprovalMeta(label, value)
	}
	if reason := approvalReasonText(req.Reason); reason != "" {
		a.ui.ApprovalMeta("Reason", reason)
	}
}

func approvalChoicesForSessionKey(sessionKey string) []tuievents.PromptChoice {
	choices := []tuievents.PromptChoice{
		{Label: "approve", Value: "y", Detail: "this time"},
	}
	if sessionKey != "" {
		choices = append(choices, tuievents.PromptChoice{
			Label:  "always",
			Value:  "a",
			Detail: "remember " + compactApprovalScope(sessionKey),
		})
	}
	choices = append(choices, tuievents.PromptChoice{
		Label:  "reject",
		Value:  "n",
		Detail: "skip it",
	})
	return choices
}

func toolAuthorizationChoices(scopeKey string) []tuievents.PromptChoice {
	return []tuievents.PromptChoice{
		{Label: "approve", Value: "y", Detail: "this time"},
		{Label: "always", Value: "a", Detail: "remember " + compactApprovalScope(scopeKey)},
		{Label: "reject", Value: "n", Detail: "skip it"},
	}
}

func commandApprovalTitle() string {
	return "Would you like to run the following command?"
}

func toolAuthorizationTitle(req kernelpolicy.ToolAuthorizationRequest) string {
	if strings.TrimSpace(req.Path) != "" {
		return "Would you like to make the following edits?"
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(req.Permission)), "mcp") {
		return "Would you like to call the following external tool?"
	}
	return "Would you like to authorize the following tool?"
}

func (a *terminalApprover) emitCommandApprovalOutcome(req toolexec.ApprovalRequest, sessionKey string, decision string) {
	if a == nil || a.ui == nil {
		return
	}
	if a.usesStructuredPrompts() {
		return
	}
	target := shortApprovalTarget(req.Command)
	switch decision {
	case "once":
		a.ui.ApprovalOutcome(true, "You approved running "+target+" this time.")
	case "session":
		scope := target
		if strings.TrimSpace(sessionKey) != "" {
			scope = sessionKey
		}
		a.ui.ApprovalOutcome(true, "You approved this session for commands matching "+scope+".")
	case "cancel":
		a.ui.ApprovalOutcome(false, "You did not approve running "+target+".")
	}
}

func (a *terminalApprover) emitToolApprovalOutcome(req kernelpolicy.ToolAuthorizationRequest, scopeKey string, decision string) {
	if a == nil || a.ui == nil {
		return
	}
	if a.usesStructuredPrompts() {
		return
	}
	target := toolApprovalOutcomeTarget(req)
	switch decision {
	case "once":
		a.ui.ApprovalOutcome(true, "You approved "+target+" this time.")
	case "session":
		scope := target
		if strings.TrimSpace(scopeKey) != "" {
			scope = shortApprovalTarget(scopeKey)
		}
		if strings.TrimSpace(req.Path) != "" {
			a.ui.ApprovalOutcome(true, "You approved this session for edits under "+scope+".")
		} else {
			a.ui.ApprovalOutcome(true, "You approved this session for tool requests under "+scope+".")
		}
	case "cancel":
		a.ui.ApprovalOutcome(false, "You did not approve "+target+".")
	}
}

func (a *terminalApprover) usesStructuredPrompts() bool {
	if a == nil || a.prompter == nil {
		return false
	}
	_, ok := a.prompter.(structuredPromptReader)
	return ok
}

func commandApprovalPromptRequest(req toolexec.ApprovalRequest, sessionKey string) tuievents.PromptRequestMsg {
	details := make([]tuievents.PromptDetail, 0, 2)
	if label, value := commandApprovalSummary(req); label != "" && value != "" {
		details = append(details, tuievents.PromptDetail{Label: label, Value: value, Emphasis: true})
	}
	if reason := approvalReasonText(req.Reason); reason != "" {
		details = append(details, tuievents.PromptDetail{Label: "Reason", Value: reason})
	}
	return tuievents.PromptRequestMsg{
		Title:         commandApprovalTitle(),
		Prompt:        commandApprovalTitle(),
		Details:       details,
		Choices:       approvalChoicesForSessionKey(sessionKey),
		DefaultChoice: "y",
	}
}

func toolAuthorizationPromptRequest(req kernelpolicy.ToolAuthorizationRequest, scopeKey string) tuievents.PromptRequestMsg {
	details := make([]tuievents.PromptDetail, 0, 2)
	if label, value := toolAuthorizationSummary(req); label != "" && value != "" {
		details = append(details, tuievents.PromptDetail{Label: label, Value: value, Emphasis: true})
	}
	if reason := approvalReasonText(req.Reason); reason != "" {
		details = append(details, tuievents.PromptDetail{Label: "Reason", Value: reason})
	}
	title := toolAuthorizationTitle(req)
	return tuievents.PromptRequestMsg{
		Title:         title,
		Prompt:        title,
		Details:       details,
		Choices:       toolAuthorizationChoices(scopeKey),
		DefaultChoice: "y",
	}
}

func approvalReasonText(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ""
	}
	lower := strings.ToLower(reason)
	switch lower {
	case "require_escalated requested":
		return ""
	default:
		return reason
	}
}

func commandApprovalSummary(req toolexec.ApprovalRequest) (label string, value string) {
	label = strings.ToUpper(strings.TrimSpace(req.ToolName))
	if label == "" {
		label = "COMMAND"
	}
	value = strings.TrimSpace(req.Command)
	if value != "" {
		return label, value
	}
	if action := strings.TrimSpace(req.Action); action != "" {
		return label, strings.ReplaceAll(action, "_", " ")
	}
	return "", ""
}

func toolAuthorizationSummary(req kernelpolicy.ToolAuthorizationRequest) (label string, value string) {
	label = strings.ToUpper(strings.TrimSpace(req.ToolName))
	if label == "" {
		label = "TOOL"
	}
	switch {
	case strings.TrimSpace(req.Path) != "":
		value = strings.TrimSpace(req.Path)
	case strings.TrimSpace(req.Target) != "":
		value = strings.TrimSpace(req.Target)
	case strings.TrimSpace(req.Preview) != "":
		value = strings.TrimSpace(req.Preview)
	default:
		value = approvalReasonText(req.Reason)
	}
	if value == "" {
		return "", ""
	}
	return label, value
}

func toolApprovalOutcomeTarget(req kernelpolicy.ToolAuthorizationRequest) string {
	switch {
	case strings.TrimSpace(req.Path) != "":
		target := shortApprovalTarget(req.Path)
		if target != "\"\"" {
			return "edits to " + target
		}
	case strings.TrimSpace(req.Target) != "":
		return "tool access to " + shortApprovalTarget(req.Target)
	case strings.TrimSpace(req.ToolName) != "":
		return "tool " + shortApprovalTarget(req.ToolName)
	}
	return "this tool request"
}

func compactApprovalScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "this"
	}
	return truncateInline(scope, 24)
}

func shortApprovalTarget(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "\"\""
	}
	text = strings.Join(strings.Fields(text), " ")
	const maxLen = 80
	if len(text) > maxLen {
		text = text[:maxLen-1] + "…"
	}
	return strconv.Quote(text)
}

func isApprovalWrapperCommand(base string) bool {
	switch strings.ToLower(strings.TrimSpace(base)) {
	case "", "cd", "pwd", "export", "unset", "alias", "source", ".", "grep", "egrep", "fgrep", "head", "tail":
		return true
	default:
		return false
	}
}

func shellCommandSegments(command string) []string {
	var (
		segments []string
		buf      strings.Builder
		squote   bool
		dquote   bool
		escape   bool
	)
	flush := func() {
		part := strings.TrimSpace(buf.String())
		if part != "" {
			segments = append(segments, part)
		}
		buf.Reset()
	}
	runes := []rune(command)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if escape {
			buf.WriteRune(r)
			escape = false
			continue
		}
		switch r {
		case '\\':
			escape = true
			buf.WriteRune(r)
		case '\'':
			if !dquote {
				squote = !squote
			}
			buf.WriteRune(r)
		case '"':
			if !squote {
				dquote = !dquote
			}
			buf.WriteRune(r)
		case ';':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			flush()
		case '&':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			if i+1 < len(runes) && runes[i+1] == '&' {
				flush()
				i++
				continue
			}
			buf.WriteRune(r)
		case '|':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			flush()
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
		default:
			buf.WriteRune(r)
		}
	}
	flush()
	return segments
}

func shellSegmentTokens(segment string) []string {
	var (
		tokens []string
		buf    strings.Builder
		squote bool
		dquote bool
		escape bool
	)
	flush := func() {
		token := strings.TrimSpace(buf.String())
		if token == "" {
			buf.Reset()
			return
		}
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "=") && len(tokens) == 0 {
			buf.Reset()
			return
		}
		tokens = append(tokens, token)
		buf.Reset()
	}
	for _, r := range segment {
		if escape {
			buf.WriteRune(r)
			escape = false
			continue
		}
		switch r {
		case '\\':
			escape = true
		case '\'':
			if !dquote {
				squote = !squote
				continue
			}
			buf.WriteRune(r)
		case '"':
			if !squote {
				dquote = !dquote
				continue
			}
			buf.WriteRune(r)
		case ' ', '\t', '\n':
			if squote || dquote {
				buf.WriteRune(r)
				continue
			}
			flush()
		default:
			buf.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func (c *cliConsole) addPendingAttachment(part model.ContentPart) {
	c.pendingAttachmentMu.Lock()
	defer c.pendingAttachmentMu.Unlock()
	c.pendingAttachments = append(c.pendingAttachments, part)
	name := strings.TrimSpace(part.FileName)
	if name != "" {
		c.attachmentLibrary[name] = part
	}
}

func (c *cliConsole) consumePendingAttachments() []model.ContentPart {
	c.pendingAttachmentMu.Lock()
	defer c.pendingAttachmentMu.Unlock()
	parts := c.pendingAttachments
	c.pendingAttachments = nil
	return parts
}

func (c *cliConsole) clearPendingAttachments() []string {
	c.pendingAttachmentMu.Lock()
	defer c.pendingAttachmentMu.Unlock()
	c.pendingAttachments = nil
	return nil
}

func (c *cliConsole) setPendingAttachments(names []string) []string {
	c.pendingAttachmentMu.Lock()
	defer c.pendingAttachmentMu.Unlock()
	if len(names) == 0 {
		c.pendingAttachments = nil
		return nil
	}
	parts := make([]model.ContentPart, 0, len(names))
	restored := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		part, ok := c.attachmentLibrary[name]
		if !ok {
			continue
		}
		parts = append(parts, part)
		restored = append(restored, name)
	}
	c.pendingAttachments = parts
	return restored
}

// pasteClipboardImage extracts an image from the system clipboard, saves it to
// a temp directory, and adds it as a pending attachment. Returns the current
// pending attachment count, a UI hint string, and any error.
func (c *cliConsole) pasteClipboardImage() ([]string, string, error) {
	raw, mime, err := image.ExtractClipboardImage()
	if err != nil {
		return nil, "", fmt.Errorf("clipboard: %w", err)
	}
	if raw == nil {
		return nil, "", nil // no image in clipboard
	}
	if len(raw) > image.MaxImageBytes {
		return nil, "", fmt.Errorf("clipboard image too large: %d bytes (max %d)", len(raw), image.MaxImageBytes)
	}
	// Save to temp directory for inspection.
	tmpDir := filepath.Join(os.TempDir(), "caelis-clipboard")
	_ = os.MkdirAll(tmpDir, 0o755)
	tmpName := fmt.Sprintf("clipboard-%d%s", time.Now().UnixNano(), clipboardImageExtension(mime))
	tmpPath := filepath.Join(tmpDir, tmpName)
	_ = os.WriteFile(tmpPath, raw, 0o644)

	part, err := image.ContentPartFromBytes(raw, mime, tmpName, c.imageCache)
	if err != nil {
		return nil, "", fmt.Errorf("clipboard image: %w", err)
	}
	c.addPendingAttachment(part)
	c.pendingAttachmentMu.Lock()
	names := make([]string, 0, len(c.pendingAttachments))
	for _, one := range c.pendingAttachments {
		name := strings.TrimSpace(one.FileName)
		if name == "" {
			name = "image"
		}
		names = append(names, name)
	}
	c.pendingAttachmentMu.Unlock()
	return names, "", nil
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

func clipboardImageExtension(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/tiff":
		return ".tiff"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

func formatUsage(usage runtime.ContextUsage) string {
	if usage.WindowTokens <= 0 {
		return "0/0"
	}
	return fmt.Sprintf("%d/%d (%.1f%%)", usage.CurrentTokens, usage.WindowTokens, usage.Ratio*100)
}

func formatCompactTokenUsage(tokens int) string {
	if tokens <= 0 {
		return "0"
	}
	if formatted := formatTokenCount(tokens); formatted != "" {
		return formatted
	}
	return strconv.Itoa(tokens)
}

func stringOrDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}
