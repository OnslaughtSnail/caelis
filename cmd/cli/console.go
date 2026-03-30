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

	"github.com/OnslaughtSnail/caelis/internal/app/acpext"
	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	appassembly "github.com/OnslaughtSnail/caelis/internal/app/assembly"
	appgateway "github.com/OnslaughtSnail/caelis/internal/app/gateway"
	"github.com/OnslaughtSnail/caelis/internal/app/sessionsvc"
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

	resolved              *appassembly.ResolvedSpec
	sessionStore          session.Store
	execRuntime           toolexec.Runtime
	execRuntimeView       *swappableRuntime
	sandboxType           string
	sandboxPolicy         toolexec.SandboxPolicy
	appliedSandboxType    string
	appliedSandboxTypeSet bool
	sandboxHelperPath     string

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
	activeRunner    sessionsvc.TurnHandle
	activeRunKind   runOccupancy
	interruptMu     sync.Mutex
	lastInterruptAt time.Time
	outMu           sync.Mutex

	imageCache          *image.Cache
	pendingAttachments  []model.ContentPart
	attachmentLibrary   map[string]model.ContentPart
	pendingAttachmentMu sync.Mutex
	tuiSender           interface{ Send(msg any) } // set in TUI mode for hint updates
	agentRegistry       *appagents.Registry
	spawnPreviewer      *spawnPreviewProjector
	resumeReplayMu      sync.Mutex
	resumeReplayCancel  context.CancelFunc
	bashWatchMu         sync.Mutex
	bashTaskWatches     map[string]context.CancelFunc
	connectModelCacheMu sync.Mutex
	connectModelCache   map[string]connectModelCacheEntry
	promptMu            sync.Mutex
	promptSnapshots     map[string]string
	sessionService      *sessionsvc.Service
	gateway             *appgateway.Gateway
	newACPAdapter       acpext.AdapterFactory
}

const interruptExitWindow = 2 * time.Second
const transientHintDuration = 1600 * time.Millisecond

const (
	btwControlOpenTag  = "<caelis-btw hidden=\"true\">"
	btwControlCloseTag = "</caelis-btw>"
)

func cliContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

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
		sandboxPolicy:         cfg.SandboxPolicy,
		appliedSandboxType:    canonicalSandboxSelection(cfg.AppliedSandboxType),
		appliedSandboxTypeSet: true,
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
		promptSnapshots:       map[string]string{},
		agentRegistry:         cfg.AgentRegistry,
		spawnPreviewer:        newSpawnPreviewProjector(),
		bashTaskWatches:       map[string]context.CancelFunc{},
		sessionService:        cfg.SessionService,
		gateway:               cfg.Gateway,
		newACPAdapter:         cfg.NewACPAdapter,
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
		"agent":   {Usage: "/agent list | add <builtin> | rm <name>", Description: "Manage configured ACP agents", Handle: handleAgent},
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
	SandboxPolicy         toolexec.SandboxPolicy
	AppliedSandboxType    string
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
	AgentRegistry         *appagents.Registry
	SessionService        *sessionsvc.Service
	Gateway               *appgateway.Gateway
	NewACPAdapter         acpext.AdapterFactory
}

func (c *cliConsole) loop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("cli: context is required")
	}
	switch c.uiMode {
	case uiModeTUI:
		return c.loopTUITea(ctx)
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

func (c *cliConsole) handleSlashContext(ctx context.Context, line string) (bool, error) {
	ctx = cliContext(ctx)
	parts := strings.Fields(strings.TrimPrefix(line, "/"))
	if len(parts) == 0 {
		return false, nil
	}
	cmd := strings.ToLower(parts[0])
	handler, ok := c.commands[cmd]
	if !ok {
		if desc, ok := c.dynamicSlashAgentDescriptor(cmd); ok {
			return false, c.runExternalAgentSlashContext(ctx, desc, strings.TrimSpace(strings.Join(parts[1:], " ")))
		}
		if suggestion := closestCommand(cmd, c.availableCommandNames()); suggestion != "" {
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
	if _, ok := c.dynamicSlashAgentDescriptor(cmd); ok {
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

func (c *cliConsole) runPromptWithAttachments(input string, attachments []tuiapp.Attachment) error {
	return c.runPromptWithAttachmentsContext(c.baseCtx, input, attachments)
}

func (c *cliConsole) runPromptWithAttachmentsContext(ctx context.Context, input string, attachments []tuiapp.Attachment) error {
	if c.currentRunKind() == runOccupancyExternalAgent {
		return errExternalAgentRunBusy
	}
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
	return c.runPreparedSubmissionContext(ctx, prepared, submission)
}

type preparedPromptSubmission struct {
	agent        agent.Agent
	runInput     string
	contentParts []model.ContentPart
}

func (c *cliConsole) sessionBoundary() (*sessionsvc.Service, error) {
	if c == nil {
		return nil, fmt.Errorf("console is nil")
	}
	if c.sessionService != nil {
		return c.sessionService, nil
	}
	execRuntime := c.executionRuntimeForSession()
	var tools []tool.Tool
	var policies []kernelpolicy.Hook
	if c.resolved != nil {
		tools = c.resolved.Tools
		policies = c.resolved.Policies
	}
	return sessionsvc.New(sessionsvc.ServiceConfig{
		Runtime:         c.rt,
		Store:           c.sessionStore,
		AppName:         c.appName,
		UserID:          c.userID,
		DefaultAgent:    c.configStore.DefaultAgent(),
		WorkspaceCWD:    c.workspace.CWD,
		Execution:       execRuntime,
		Tools:           tools,
		Policies:        policies,
		EnablePlan:      true,
		EnableSelfSpawn: true,
		Index:           &cliSessionIndexAdapter{index: c.sessionIndex},
		SubagentRunnerFactory: acpext.NewACPSubagentRunnerFactory(acpext.Config{
			Store:                c.sessionStore,
			WorkspaceCWD:         c.workspace.CWD,
			ClientRuntime:        execRuntime,
			ResolveAgentRegistry: c.configStore.AgentRegistry,
			NewAdapter:           c.newACPAdapter,
		}),
	})
}

func (c *cliConsole) executionRuntimeForSession() toolexec.Runtime {
	if c == nil {
		return nil
	}
	if c.execRuntimeView != nil {
		return c.execRuntimeView
	}
	return c.execRuntime
}

func (c *cliConsole) currentAppliedSandboxType() string {
	if c == nil {
		return ""
	}
	if c.appliedSandboxTypeSet {
		return canonicalSandboxSelection(c.appliedSandboxType)
	}
	return canonicalSandboxSelection(c.sandboxType)
}

func (c *cliConsole) sessionGateway() (*appgateway.Gateway, error) {
	if c == nil {
		return nil, fmt.Errorf("console is nil")
	}
	if c.gateway != nil {
		return c.gateway, nil
	}
	svc, err := c.sessionBoundary()
	if err != nil {
		return nil, err
	}
	return appgateway.New(svc)
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
	systemPrompt, err := c.ensureSessionPrompt(c.baseCtx)
	if err != nil {
		return preparedPromptSubmission{}, err
	}
	ag, err := buildAgent(buildAgentInput{
		AppName:                     c.appName,
		PromptRole:                  promptRoleMainSession,
		WorkspaceDir:                c.workspace.CWD,
		EnableExperimentalLSPPrompt: c.enableExperimentalLSP,
		BasePrompt:                  c.systemPrompt,
		FrozenPrompt:                systemPrompt,
		SkillDirs:                   c.skillDirs,
		DefaultAgent:                c.configStore.DefaultAgent(),
		AgentDescriptors:            c.configStore.AgentDescriptors(),
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

func (c *cliConsole) runPreparedSubmissionContext(ctx context.Context, prepared preparedPromptSubmission, submission runtime.Submission) error {
	ctx = cliContext(ctx)
	ctx = toolexec.WithApprover(ctx, c.approver)
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
	pendingTUIToolCalls := map[string]toolCallSnapshot{}
	gw, err := c.sessionGateway()
	if err != nil {
		return err
	}
	runResult, err := gw.RunTurn(ctx, appgateway.RunTurnRequest{
		Channel:             c.gatewayChannel(),
		SessionID:           c.sessionID,
		Input:               submission.Text,
		ContentParts:        submission.ContentParts,
		Agent:               prepared.agent,
		Model:               c.llm,
		ContextWindowTokens: c.contextWindow,
	})
	if err != nil {
		return err
	}
	c.sessionID = runResult.Session.SessionID
	runner := runResult.Handle
	interruptCtx := context.WithoutCancel(ctx)
	cancel := func() {
		_ = gw.InterruptSession(interruptCtx, c.gatewayChannel(), "console interrupt")
	}
	c.setActiveRun(cancel, runner)
	defer func() {
		c.clearActiveRun()
		_ = runner.Close() // Close always returns nil; safe to ignore.
	}()
	return runRunner(runner, runRenderConfig{
		ShowReasoning: c.showReasoning,
		Verbose:       c.ui.verbose,
		Writer:        c.out,
		UI:            c.ui,
		OnEvent: func(ev *session.Event) bool {
			c.refreshContextUsageFromEvent(ev)
			return c.forwardEventToTUIContext(ctx, ev, pendingTUIToolCalls)
		},
		OnUsage: func(floor int) {
			c.refreshContextUsageEstimate(floor)
		},
	})
}

func (c *cliConsole) runBTW(question string, attachments []tuiapp.Attachment) error {
	return c.runBTWContext(c.baseCtx, question, attachments)
}

func (c *cliConsole) runBTWContext(ctx context.Context, question string, attachments []tuiapp.Attachment) error {
	if c.currentRunKind() == runOccupancyExternalAgent {
		return errExternalAgentRunBusy
	}
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
		return c.startBTWAsyncContext(ctx, prepared, submission)
	}
	return c.runBTWBlockingContext(ctx, prepared, submission)
}

func (c *cliConsole) startBTWAsyncContext(ctx context.Context, prepared preparedPromptSubmission, submission runtime.Submission) error {
	ctx = cliContext(ctx)
	ctx = toolexec.WithApprover(ctx, c.approver)
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
	gw, err := c.sessionGateway()
	if err != nil {
		return err
	}
	runResult, err := gw.RunTurn(ctx, appgateway.RunTurnRequest{
		Channel:             c.gatewayChannel(),
		SessionID:           c.sessionID,
		Agent:               prepared.agent,
		Model:               c.llm,
		ContextWindowTokens: c.contextWindow,
	})
	if err != nil {
		return err
	}
	c.sessionID = runResult.Session.SessionID
	runner := runResult.Handle
	interruptCtx := context.WithoutCancel(ctx)
	if err := runner.Submit(submission); err != nil {
		_ = runner.Close()
		return err
	}
	cancel := func() {
		_ = gw.InterruptSession(interruptCtx, c.gatewayChannel(), "console interrupt")
	}
	c.setActiveRun(cancel, runner)
	go func(ctx context.Context) {
		defer func() {
			c.clearActiveRun()
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
				return c.forwardEventToTUIContext(ctx, ev, pendingTUIToolCalls)
			},
			OnUsage: func(floor int) {
				c.refreshContextUsageEstimate(floor)
			},
		})
		if err != nil && c.tuiSender != nil {
			c.tuiSender.Send(tuievents.BTWErrorMsg{Text: err.Error()})
		}
	}(ctx)
	return nil
}

func (c *cliConsole) runBTWBlockingContext(ctx context.Context, prepared preparedPromptSubmission, submission runtime.Submission) error {
	ctx = cliContext(ctx)
	ctx = toolexec.WithApprover(ctx, c.approver)
	ctx = kernelpolicy.WithToolAuthorizer(ctx, c.approver)
	gw, err := c.sessionGateway()
	if err != nil {
		return err
	}
	runResult, err := gw.RunTurn(ctx, appgateway.RunTurnRequest{
		Channel:             c.gatewayChannel(),
		SessionID:           c.sessionID,
		Agent:               prepared.agent,
		Model:               c.llm,
		ContextWindowTokens: c.contextWindow,
	})
	if err != nil {
		return err
	}
	c.sessionID = runResult.Session.SessionID
	runner := runResult.Handle
	if err := runner.Submit(submission); err != nil {
		_ = runner.Close()
		return err
	}
	interruptCtx := context.WithoutCancel(ctx)
	cancel := func() {
		_ = gw.InterruptSession(interruptCtx, c.gatewayChannel(), "console interrupt")
	}
	c.setActiveRun(cancel, runner)
	defer func() {
		c.clearActiveRun()
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
		text := msg.TextContent()
		if eventIsPartial(ev) {
			c.tuiSender.Send(tuievents.RawDeltaMsg{
				Target: tuievents.RawDeltaTargetBTW,
				Stream: "answer",
				Text:   text,
			})
			return
		}
		c.tuiSender.Send(tuievents.BTWOverlayMsg{Text: strings.TrimSpace(text), Final: true})
		return
	}
	actor := eventParticipantDisplay(ev)
	scopeID := eventParticipantSessionID(ev)
	reasoning := msg.ReasoningText()
	text := msg.TextContent()
	if eventIsPartial(ev) {
		channel := strings.ToLower(strings.TrimSpace(eventChannel(ev)))
		switch channel {
		case "reasoning":
			c.emitAssistantChunkToTUIWithScope("reasoning", scopeID, actor, reasoning, false)
			c.emitAssistantChunkToTUIWithScope("answer", scopeID, actor, text, false)
		case "answer":
			// Mixed chunk payloads are rare but valid; keep deterministic order.
			c.emitAssistantChunkToTUIWithScope("reasoning", scopeID, actor, reasoning, false)
			c.emitAssistantChunkToTUIWithScope("answer", scopeID, actor, text, false)
		default:
			c.emitAssistantChunkToTUIWithScope("reasoning", scopeID, actor, reasoning, false)
			c.emitAssistantChunkToTUIWithScope("answer", scopeID, actor, text, false)
		}
		return
	}
	// Final assistant events may contain both reasoning and answer.
	c.emitAssistantChunkToTUIWithScope("reasoning", scopeID, actor, strings.TrimSpace(reasoning), true)
	c.emitAssistantChunkToTUIWithScope("answer", scopeID, actor, strings.TrimSpace(text), true)
}

func eventParticipantDisplay(ev *session.Event) string {
	if ev == nil || len(ev.Meta) == 0 {
		return ""
	}
	return strings.TrimSpace(asString(ev.Meta[metaParticipantDisplay]))
}

func eventParticipantSessionID(ev *session.Event) string {
	if ev == nil || len(ev.Meta) == 0 {
		return ""
	}
	return strings.TrimSpace(asString(ev.Meta[metaChildSessionID]))
}

func isParticipantMirrorEvent(ev *session.Event) bool {
	return ev != nil && session.IsMirror(ev) && strings.TrimSpace(eventParticipantDisplay(ev)) != ""
}

func (c *cliConsole) emitAssistantChunkToTUIWithScope(kind string, scopeID string, actor string, text string, final bool) {
	if c == nil || c.tuiSender == nil || text == "" {
		return
	}
	streamKind := strings.ToLower(strings.TrimSpace(kind))
	switch streamKind {
	case "reasoning":
		if !c.showReasoning {
			return
		}
		c.tuiSender.Send(tuievents.RawDeltaMsg{
			Target:  tuievents.RawDeltaTargetAssistant,
			ScopeID: strings.TrimSpace(scopeID),
			Stream:  "reasoning",
			Actor:   strings.TrimSpace(actor),
			Text:    text,
			Final:   final,
		})
	default:
		c.tuiSender.Send(tuievents.RawDeltaMsg{
			Target:  tuievents.RawDeltaTargetAssistant,
			ScopeID: strings.TrimSpace(scopeID),
			Stream:  "answer",
			Actor:   strings.TrimSpace(actor),
			Text:    text,
			Final:   final,
		})
	}
}

type tuiForwardOptions struct {
	ShowMutationDiff bool
	ReplayMode       bool
}

func (c *cliConsole) forwardEventToTUI(ev *session.Event, pendingToolCalls map[string]toolCallSnapshot) bool {
	return c.forwardEventToTUIContext(c.baseCtx, ev, pendingToolCalls)
}

func (c *cliConsole) forwardEventToTUIContext(ctx context.Context, ev *session.Event, pendingToolCalls map[string]toolCallSnapshot) bool {
	ctx = cliContext(ctx)
	return c.forwardEventToTUIWithOptionsContext(ctx, ev, pendingToolCalls, tuiForwardOptions{
		ShowMutationDiff: true,
	})
}

func (c *cliConsole) forwardEventToTUIWithOptions(ev *session.Event, pendingToolCalls map[string]toolCallSnapshot, opts tuiForwardOptions) bool {
	return c.forwardEventToTUIWithOptionsContext(c.baseCtx, ev, pendingToolCalls, opts)
}

func (c *cliConsole) forwardEventToTUIWithOptionsContext(ctx context.Context, ev *session.Event, pendingToolCalls map[string]toolCallSnapshot, opts tuiForwardOptions) bool {
	ctx = cliContext(ctx)
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
		text := strings.TrimSpace(msg.TextContent())
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
			if isParticipantMirrorEvent(ev) && strings.TrimSpace(asString(ev.Meta[metaRouteKind])) != "" {
				c.tuiSender.Send(tuievents.ParticipantTurnStartMsg{
					SessionID: eventParticipantSessionID(ev),
					Actor:     eventParticipantDisplay(ev),
				})
			}
			return true
		}
	}
	if calls := msg.ToolCalls(); len(calls) > 0 {
		if isParticipantMirrorEvent(ev) {
			sessionID := eventParticipantSessionID(ev)
			for _, call := range calls {
				parsedArgs := parseToolArgsForDisplay(call.Args)
				c.tuiSender.Send(tuievents.ParticipantToolMsg{
					SessionID: sessionID,
					CallID:    call.ID,
					ToolName:  call.Name,
					Args:      formatExternalToolStart(call.Name, parsedArgs),
				})
			}
			return true
		}
		previewRuntime := c.execRuntime
		previewFS := newMutationPreviewFS(nil)
		if !opts.ReplayMode && c.execRuntime != nil && c.execRuntime.FileSystem() != nil {
			previewFS = newMutationPreviewFS(c.execRuntime.FileSystem())
			previewRuntime = mutationPreviewRuntime{base: c.execRuntime, fsys: previewFS}
		}
		for _, call := range calls {
			if isPlanToolName(call.Name) {
				if pendingToolCalls != nil && call.ID != "" {
					pendingToolCalls[call.ID] = toolCallSnapshot{
						Args: cloneAnyMap(parseToolArgsForDisplay(call.Args)),
					}
				}
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
			defaultSpawnAgent := ""
			if c.configStore != nil {
				defaultSpawnAgent = c.configStore.DefaultAgent()
			}
			displayName := displayToolCallName(call.Name, parsedArgs)
			summary := formatToolCallSummary(c.ui, call.Name, parsedArgs, defaultSpawnAgent)
			if visualsOK && strings.TrimSpace(visuals.CallSummary) != "" {
				summary = visuals.CallSummary
			}
			callLine := "▸ " + displayName
			if strings.TrimSpace(summary) != "" {
				callLine += " " + summary
			}
			c.tuiSender.Send(tuievents.LogChunkMsg{
				Chunk: callLine + "\n",
			})
			if strings.EqualFold(strings.TrimSpace(call.Name), tool.SpawnToolName) {
				if update, ok := subagentDomainUpdateFromSpawnToolCall(c.sessionID, call, parsedArgs, defaultSpawnAgent); ok {
					c.dispatchSubagentDomainUpdate(ctx, update)
				}
			}
			if strings.EqualFold(strings.TrimSpace(call.Name), toolshell.BashToolName) ||
				strings.EqualFold(strings.TrimSpace(call.Name), tool.SpawnToolName) {
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
	if resp := msg.ToolResponse(); resp != nil {
		if isParticipantMirrorEvent(ev) {
			callArgs := parseToolArgsForDisplay("")
			status := "completed"
			if hasToolError(resp.Result) {
				status = "failed"
			}
			c.tuiSender.Send(tuievents.ParticipantToolMsg{
				SessionID: eventParticipantSessionID(ev),
				CallID:    resp.ID,
				ToolName:  resp.Name,
				Output:    formatExternalToolResult(resp.Name, callArgs, resp.Result, status, false),
				Final:     true,
				Err:       status == "failed",
			})
			return true
		}
		c.syncBashTaskWatchContext(ctx, resp.ID, resp.Name, resp.Result)
		var (
			callArgs      map[string]any
			richDiffShown bool
			changeCounts  mutationChangeCounts
		)
		if pendingToolCalls != nil && resp.ID != "" {
			if snapshot, ok := pendingToolCalls[resp.ID]; ok {
				callArgs = snapshot.Args
				richDiffShown = snapshot.RichDiffShown
				changeCounts = snapshot.ChangeCounts
				delete(pendingToolCalls, resp.ID)
			}
		}
		emittedTaskStream := c.emitTaskStreamFromToolResult(resp, callArgs)
		toolName := strings.TrimSpace(resp.Name)
		if strings.EqualFold(toolName, tool.SpawnToolName) {
			if update, ok := subagentDomainUpdateFromSpawnToolResponse(c.sessionID, resp); ok {
				c.dispatchSubagentDomainUpdate(ctx, update)
			}
			for _, update := range subagentDomainUpdatesFromSpawnToolError(c.sessionID, resp) {
				c.dispatchSubagentDomainUpdate(ctx, update)
			}
		} else if strings.EqualFold(toolName, tool.TaskToolName) {
			for _, update := range subagentDomainUpdatesFromTaskToolResponse(c.sessionID, resp, callArgs) {
				c.dispatchSubagentDomainUpdate(ctx, update)
			}
		}
		if strings.EqualFold(strings.TrimSpace(resp.Name), toolshell.BashToolName) {
			if !emittedTaskStream {
				c.emitReplayBashOutput(resp)
			}
			c.tuiSender.Send(tuievents.TaskStreamMsg{
				Label:  resp.Name,
				CallID: resp.ID,
				Final:  true,
			})
			hasError := hasToolError(resp.Result)
			exitCode, _ := asInt(resp.Result["exit_code"])
			if strings.TrimSpace(firstNonEmpty(resp.Result, "result", "stdout", "stderr")) != "" {
				return true
			}
			if emittedTaskStream || (!hasError && exitCode == 0) {
				return true
			}
		}
		if isFileMutationTool(resp.Name) && !hasToolError(resp.Result) {
			if richDiffShown {
				return true
			}
			if resultCounts := mutationChangeCountsFromResult(resp.Name, resp.Result, callArgs); resultCounts != (mutationChangeCounts{}) {
				changeCounts = resultCounts
			}
			summary := formatMutationChangeSummary(changeCounts)
			if changeCounts == (mutationChangeCounts{}) {
				summary = summarizeToolResponseWithCall(resp.Name, resp.Result, callArgs)
			}
			displayName := displayToolResponseName(toolName, callArgs, resp.Result)
			c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: formatToolResultLine("✓ ", displayName, summary)})
			return true
		}
		if strings.EqualFold(toolName, tool.PlanToolName) && !hasToolError(resp.Result) {
			c.tuiSender.Send(planUpdateMsgFromToolPayload(callArgs, resp.Result))
			return true
		}
		if compact := summarizeCompactToolResponseForTUI(resp.Name, resp.Result); compact != "" && !hasToolError(resp.Result) {
			displayName := displayToolResponseName(toolName, callArgs, resp.Result)
			c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: formatToolResultLine("✓ ", displayName, compact)})
			return true
		}
		if strings.EqualFold(toolName, tool.SpawnToolName) {
			return true
		}
		// Suppress result line for read-only FS tools (the call line is sufficient).
		if isReadOnlyFSTool(resp.Name) && !hasToolError(resp.Result) {
			return true
		}
		summary := summarizeToolResponseWithCall(resp.Name, resp.Result, callArgs)
		if strings.EqualFold(toolName, tool.TaskToolName) && strings.EqualFold(strings.TrimSpace(asString(callArgs["action"])), "write") && !hasToolError(resp.Result) {
			summary = ""
		}
		if strings.EqualFold(toolName, tool.TaskToolName) && emittedTaskStream {
			summary = ""
		}
		if strings.EqualFold(toolName, tool.SpawnToolName) && emittedTaskStream {
			summary = ""
		}
		if strings.TrimSpace(summary) != "" {
			prefix := "✓ "
			if hasToolError(resp.Result) && !strings.EqualFold(toolName, toolshell.BashToolName) {
				prefix = "! "
			}
			displayName := displayToolResponseName(toolName, callArgs, resp.Result)
			c.tuiSender.Send(tuievents.LogChunkMsg{Chunk: formatToolResultLine(prefix, displayName, summary)})
		}
		if strings.EqualFold(toolName, tool.TaskToolName) {
			return emittedTaskStream || strings.TrimSpace(summary) != "" || strings.EqualFold(strings.TrimSpace(asString(callArgs["action"])), "write")
		}
		return true
	}
	return handled
}

func (c *cliConsole) emitReplayBashOutput(resp *model.ToolResponse) {
	if c == nil || c.tuiSender == nil || resp == nil || !strings.EqualFold(strings.TrimSpace(resp.Name), toolshell.BashToolName) {
		return
	}
	if stdout := asString(resp.Result["stdout"]); strings.TrimSpace(stdout) != "" {
		c.tuiSender.Send(tuievents.TaskStreamMsg{
			Label:  resp.Name,
			CallID: resp.ID,
			Stream: "stdout",
			Chunk:  stdout,
		})
	}
	if stderr := asString(resp.Result["stderr"]); strings.TrimSpace(stderr) != "" {
		c.tuiSender.Send(tuievents.TaskStreamMsg{
			Label:  resp.Name,
			CallID: resp.ID,
			Stream: "stderr",
			Chunk:  stderr,
		})
	}
}

func formatSessionNoticeChunk(notice session.Notice) string {
	text := renderNoticeText(notice)
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

func (c *cliConsole) emitTaskStreamFromToolResult(resp *model.ToolResponse, callArgs map[string]any) bool {
	if c == nil || c.tuiSender == nil || resp == nil {
		return false
	}
	events := taskstream.EventsFromResult(resp.Result)
	if len(events) == 0 {
		if msg, ok := syntheticTaskStreamMsgFromToolResult(resp, callArgs); ok {
			c.tuiSender.Send(msg)
		}
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

func syntheticTaskStreamMsgFromToolResult(resp *model.ToolResponse, callArgs map[string]any) (tuievents.TaskStreamMsg, bool) {
	if resp == nil || !strings.EqualFold(strings.TrimSpace(resp.Name), tool.TaskToolName) {
		return tuievents.TaskStreamMsg{}, false
	}
	action := strings.ToLower(strings.TrimSpace(asString(callArgs["action"])))
	switch action {
	case "wait", "status", "write":
	default:
		return tuievents.TaskStreamMsg{}, false
	}
	taskID := strings.TrimSpace(asString(resp.Result["task_id"]))
	if taskID == "" {
		return tuievents.TaskStreamMsg{}, false
	}
	state, chunk := taskPanelStateFromResult(resp.Result)
	if state == "" {
		return tuievents.TaskStreamMsg{}, false
	}
	if action != "write" && state == "running" {
		return tuievents.TaskStreamMsg{}, false
	}
	return tuievents.TaskStreamMsg{
		Label:  toolshell.BashToolName,
		TaskID: taskID,
		CallID: resp.ID,
		Stream: "stdout",
		Chunk:  chunk,
		State:  state,
	}, true
}

func taskPanelStateFromResult(result map[string]any) (state string, chunk string) {
	if len(result) == 0 {
		return "", ""
	}
	if explicit := strings.ToLower(strings.TrimSpace(asString(result["state"]))); explicit != "" {
		switch explicit {
		case "waiting_input", "waiting_approval", "running", "completed", "failed", "interrupted", "cancelled", "canceled", "terminated":
			return explicit, taskPanelChunkFromResult(result, explicit)
		}
	}
	progress := strings.TrimSpace(taskProgressMessage(firstNonEmpty(result, "msg", "message")))
	switch strings.ToLower(progress) {
	case "waiting for input":
		return "waiting_input", ""
	case "waiting for approval":
		return "waiting_approval", ""
	}
	return "", ""
}

func taskProgressMessage(text string) string {
	text = userFacingTaskMessage(text)
	if text == "" {
		return ""
	}
	_, rest, found := strings.Cut(text, "\n")
	if !found {
		return ""
	}
	return strings.TrimSpace(rest)
}

func taskPanelChunkFromResult(result map[string]any, state string) string {
	if chunk := strings.TrimSpace(firstNonEmpty(result, "result", "output", "stdout", "stderr")); chunk != "" {
		return chunk
	}
	return taskPanelChunkFromMessage(firstNonEmpty(result, "msg", "message"), state)
}

func taskPanelChunkFromMessage(text string, _ string) string {
	progress := taskProgressMessage(text)
	switch strings.ToLower(strings.TrimSpace(progress)) {
	case "", "waiting for input", "waiting for approval", "still running", "still going", "subagent is running":
		return ""
	default:
		return progress
	}
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

func (c *cliConsole) setActiveRun(cancel context.CancelFunc, runner sessionsvc.TurnHandle) {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	c.activeRunCancel = cancel
	c.activeRunner = runner
	c.activeRunKind = runOccupancyMainSession
}

func (c *cliConsole) clearActiveRun() {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	c.activeRunCancel = nil
	c.activeRunner = nil
	c.activeRunKind = runOccupancyNone
}

func (c *cliConsole) setActiveRunCancel(cancel context.CancelFunc) {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	c.activeRunCancel = cancel
	c.activeRunner = nil
	if cancel == nil {
		c.activeRunKind = runOccupancyNone
		return
	}
	c.activeRunKind = runOccupancyMainSession
}

func (c *cliConsole) getActiveRunner() sessionsvc.TurnHandle {
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
			cmd, ok := c.commands[name]
			if !ok {
				continue
			}
			c.ui.Plain("  %-24s %s\n", cmd.Usage, cmd.Description)
		}
	}
	helpSection("Session", []string{"new", "fork", "attach", "back", "resume", "compact", "status"})
	helpSection("Model", []string{"model", "connect", "agent"})
	helpSection("Security", []string{"sandbox"})
	helpSection("Other", []string{"btw", "help", "version", "exit", "quit"})
	if items := c.dynamicSlashAgents(); len(items) > 0 {
		c.ui.Section("Agents")
		for _, item := range items {
			desc := strings.TrimSpace(item.Description)
			if desc == "" {
				desc = "Run configured ACP agent in an isolated one-shot session"
			}
			c.ui.Plain("  %-24s %s\n", "/"+item.ID+" <prompt>", desc)
		}
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
	if gw, err := c.sessionGateway(); err == nil {
		info, startErr := gw.StartSession(c.baseCtx, appgateway.StartSessionRequest{
			Channel:            c.gatewayChannel(),
			PreferredSessionID: nextConversationSessionID(),
		})
		if startErr != nil {
			return false, startErr
		}
		c.sessionID = info.SessionID
	} else {
		c.sessionID = nextConversationSessionID()
	}
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
		c.tuiSender.Send(tuievents.SetStatusMsg{
			Workspace: c.readWorkspaceStatusLine(),
			Model:     modelText,
			Context:   contextText,
		})
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
	if gw, err := c.sessionGateway(); err == nil {
		info, forkErr := gw.ForkSession(c.baseCtx, appgateway.StartSessionRequest{
			Channel:            c.gatewayChannel(),
			PreferredSessionID: nextConversationSessionID(),
		})
		if forkErr != nil {
			return false, forkErr
		}
		c.sessionID = info.SessionID
	} else {
		c.sessionID = nextConversationSessionID()
	}
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
	c.tuiSender.Send(tuievents.SetStatusMsg{
		Workspace: c.readWorkspaceStatusLine(),
		Model:     modelText,
		Context:   contextText,
	})
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

func handleResume(c *cliConsole, args []string) (bool, error) {
	gw, err := c.sessionGateway()
	if err != nil && c.sessionIndex == nil {
		return false, fmt.Errorf("session index is not available")
	}
	if len(args) > 1 {
		return false, fmt.Errorf("usage: /resume [session-id]")
	}
	if err != nil || gw == nil {
		target := ""
		if len(args) == 1 {
			target = strings.TrimSpace(args[0])
			if target == "" {
				return false, fmt.Errorf("session-id is required")
			}
			resolved, ok, resolveErr := c.sessionIndex.ResolveWorkspaceSessionIDContext(c.baseCtx, c.workspace.Key, target)
			if resolveErr != nil {
				return false, resolveErr
			}
			if !ok {
				return false, fmt.Errorf("session %q not found in current workspace", target)
			}
			target = resolved
		} else {
			rec, ok, recentErr := c.sessionIndex.MostRecentWorkspaceSessionContext(c.baseCtx, c.workspace.Key, c.sessionID)
			if recentErr != nil {
				return false, recentErr
			}
			if !ok || strings.TrimSpace(rec.SessionID) == "" {
				return false, fmt.Errorf("no resumable session found in current workspace")
			}
			target = rec.SessionID
		}
		c.sessionID = target
		if _, reconcileErr := c.rt.ReconcileSession(c.baseCtx, runtime.ReconcileSessionRequest{
			AppName:     c.appName,
			UserID:      c.userID,
			SessionID:   c.sessionID,
			ExecRuntime: c.execRuntime,
		}); reconcileErr != nil {
			return false, reconcileErr
		}
		if restoreErr := c.restoreSessionMode(c.loadSessionMode()); restoreErr != nil {
			return false, restoreErr
		}
		if renderErr := c.renderResumedSessionEvents(); renderErr != nil {
			return false, renderErr
		}
		return false, nil
	}
	target := ""
	if len(args) == 1 {
		target = strings.TrimSpace(args[0])
		if target == "" {
			return false, fmt.Errorf("session-id is required")
		}
	}
	loaded, err := gw.ResumeSession(c.baseCtx, appgateway.ResumeSessionRequest{
		Channel:          c.gatewayChannel(),
		SessionID:        target,
		ExcludeSessionID: c.sessionID,
	})
	if err != nil {
		return false, err
	}
	if err := c.applyLoadedSession(loaded); err != nil {
		return false, err
	}
	if c.tuiSender != nil {
		c.tuiSender.Send(tuievents.SetHintMsg{Hint: c.sessionHint("resumed session"), ClearAfter: transientHintDuration})
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
	return c.renderSessionEvents(events)
}

func (c *cliConsole) applyLoadedSession(loaded sessionsvc.LoadedSession) error {
	if c == nil {
		return nil
	}
	c.sessionID = loaded.SessionID
	if err := c.restoreSessionMode(sessionmode.LoadSnapshot(loaded.State)); err != nil {
		return err
	}
	c.refreshContextUsageEstimate(extractLastUsage(loaded.Events))
	return c.renderSessionEvents(loaded.Events)
}

func (c *cliConsole) sessionHint(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if c == nil {
		return prefix
	}
	id := strings.TrimSpace(idutil.ShortDisplay(strings.TrimSpace(c.sessionID)))
	if prefix == "" || id == "" {
		return prefix
	}
	return prefix + ": " + id
}

func (c *cliConsole) renderSessionEvents(events []*session.Event) error {
	if c == nil {
		return nil
	}
	if c.tuiSender == nil || len(events) == 0 {
		return nil
	}
	// In TUI mode, replay directly through structured events so assistant
	// Markdown is rendered by the same block renderer as live streaming,
	// avoiding mixed prefix-coloring and formatting artifacts.
	c.tuiSender.Send(tuievents.ClearHistoryMsg{})
	c.tuiSender.Send(tuievents.PlanUpdateMsg{})
	c.spawnPreviewer = newSpawnPreviewProjector()
	replayCtx := c.resetResumeReplayContext()
	rootSessionID := strings.TrimSpace(c.sessionID)
	modelText, contextText := c.readTUIStatus()
	c.tuiSender.Send(tuievents.SetStatusMsg{
		Workspace: c.readWorkspaceStatusLine(),
		Model:     modelText,
		Context:   contextText,
	})
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
				if isParticipantMirrorEvent(ev) && strings.TrimSpace(asString(ev.Meta[metaRouteKind])) != "" {
					c.tuiSender.Send(tuievents.ParticipantTurnStartMsg{
						SessionID: eventParticipantSessionID(ev),
						Actor:     eventParticipantDisplay(ev),
					})
				}
			}
			continue
		}
		if c.forwardEventToTUIWithOptions(ev, pendingToolCalls, tuiForwardOptions{
			ShowMutationDiff: false,
			ReplayMode:       true,
		}) {
			continue
		}
		text := strings.TrimSpace(msg.TextContent())
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
	c.restoreResumedSubagentPanels(replayCtx, rootSessionID, events)
	c.restoreResumedExternalParticipants(replayCtx)
	c.tuiSender.Send(tuievents.TaskResultMsg{})
	return nil
}

func (c *cliConsole) resetResumeReplayContext() context.Context {
	if c == nil {
		return context.Background()
	}
	c.resumeReplayMu.Lock()
	defer c.resumeReplayMu.Unlock()
	if c.resumeReplayCancel != nil {
		c.resumeReplayCancel()
		c.resumeReplayCancel = nil
	}
	base := c.baseCtx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	c.resumeReplayCancel = cancel
	return ctx
}

func (c *cliConsole) updateExecutionRuntime(mode toolexec.PermissionMode, sandboxType string) error {
	requestedSandboxType := canonicalSandboxSelection(sandboxType)
	if c.execRuntime != nil && c.currentAppliedSandboxType() == requestedSandboxType {
		if setter, ok := c.execRuntime.(toolexec.PermissionModeSetter); ok {
			if err := setter.SetPermissionMode(mode); err != nil {
				return err
			}
			return nil
		}
	}
	prevRuntime := c.execRuntime
	nextRuntime, err := newExecutionRuntime(mode, sandboxType, c.sandboxHelperPath, c.sandboxPolicy)
	if err != nil {
		return err
	}
	c.execRuntime = nextRuntime
	if c.execRuntimeView != nil {
		c.execRuntimeView.Set(nextRuntime)
	}
	if c.execRuntimeView == nil {
		if err := c.refreshShellToolRuntime(); err != nil {
			c.execRuntime = prevRuntime
			if c.execRuntimeView != nil {
				c.execRuntimeView.Set(prevRuntime)
			}
			_ = toolexec.Close(nextRuntime)
			return err
		}
	}
	c.appliedSandboxType = requestedSandboxType
	c.appliedSandboxTypeSet = true
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
	runtime := c.executionRuntimeForSession()
	for i, one := range c.resolved.Tools {
		if one == nil || one.Name() != toolshell.BashToolName {
			continue
		}
		bashTool, err := toolshell.NewBash(toolshell.BashConfig{Runtime: runtime})
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
	if sessionmode.IsFullAccess(a.currentMode()) && !toolexec.InteractiveApprovalRequired(ctx) {
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
	if sessionmode.IsFullAccess(a.currentMode()) && !toolexec.InteractiveApprovalRequired(ctx) {
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
		return approvalInlinePreview(reason, approvalReasonMaxInlineCols)
	}
}

func commandApprovalSummary(req toolexec.ApprovalRequest) (label string, value string) {
	label = strings.ToUpper(strings.TrimSpace(req.ToolName))
	if label == "" {
		label = "COMMAND"
	}
	value = strings.TrimSpace(req.Command)
	if value != "" {
		return label, approvalPreview(value)
	}
	if action := strings.TrimSpace(req.Action); action != "" {
		return label, approvalPreview(strings.ReplaceAll(action, "_", " "))
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
		value = approvalPreview(strings.TrimSpace(req.Path))
	case strings.TrimSpace(req.Target) != "":
		value = approvalPreview(strings.TrimSpace(req.Target))
	case strings.TrimSpace(req.Preview) != "":
		value = approvalPreview(strings.TrimSpace(req.Preview))
	default:
		value = approvalReasonText(req.Reason)
	}
	if value == "" {
		return "", ""
	}
	return label, value
}

const (
	approvalPreviewMaxLines     = 8
	approvalPreviewMaxLineCols  = 120
	approvalPreviewMaxTotalCols = 320
	approvalReasonMaxInlineCols = 160
)

func approvalPreview(input string) string {
	return approvalPreviewWithLimits(input, approvalPreviewMaxLines, approvalPreviewMaxLineCols, approvalPreviewMaxTotalCols)
}

func approvalInlinePreview(input string, limit int) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	return truncateDisplayWidth(text, limit)
}

func approvalPreviewWithLimits(input string, maxLines int, maxLineCols int, maxTotalCols int) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	lines := strings.Split(strings.TrimSpace(input), "\n")
	out := make([]string, 0, min(maxLines, len(lines)))
	totalCols := 0
	usedAllLines := true
	for idx, raw := range lines {
		if idx >= maxLines {
			usedAllLines = false
			break
		}
		line := strings.TrimRight(raw, " \t")
		line = truncateDisplayWidth(line, maxLineCols)
		if strings.TrimSpace(line) == "" {
			line = ""
		}
		lineCols := displayWidth(line)
		if maxTotalCols > 0 && totalCols+lineCols > maxTotalCols {
			remaining := max(maxTotalCols-totalCols, 0)
			line = truncateDisplayWidth(line, remaining)
			lineCols = displayWidth(line)
			if strings.TrimSpace(line) != "" {
				out = append(out, line)
			}
			usedAllLines = idx == len(lines)-1
			totalCols += lineCols
			break
		}
		out = append(out, line)
		totalCols += lineCols
	}
	if len(out) == 0 {
		return ""
	}
	extraLines := len(lines) - len(out)
	switch {
	case extraLines > 0:
		out = append(out, fmt.Sprintf("... %d more lines", extraLines))
	case !usedAllLines:
		out = append(out, "... truncated")
	case maxTotalCols > 0 && totalCols >= maxTotalCols:
		out = append(out, "... truncated")
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func truncateDisplayWidth(input string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(input)
	if len(runes) <= limit {
		return input
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func displayWidth(input string) int {
	return len([]rune(input))
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
