package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	appagents "github.com/OnslaughtSnail/caelis/internal/app/agents"
	appskills "github.com/OnslaughtSnail/caelis/internal/app/skills"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuiapp"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	"github.com/OnslaughtSnail/caelis/internal/idutil"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
)

const connectModelCacheTTL = 30 * time.Second

var discoverModelsFn = modelproviders.DiscoverModels

type teaProgramSender struct {
	mu   sync.RWMutex
	send func(msg any)
}

func (s *teaProgramSender) set(send func(msg any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.send = send
}

func (s *teaProgramSender) Send(msg any) {
	s.mu.RLock()
	send := s.send
	s.mu.RUnlock()
	if send != nil {
		send(msg)
	}
}

type teaOutputWriter struct {
	sender *teaProgramSender
	diag   *tuiDiagnostics
	mu     sync.Mutex
}

func (w *teaOutputWriter) Write(p []byte) (int, error) {
	if w == nil || len(p) == 0 {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.diag != nil {
		w.diag.ObserveLogBytes(len(p))
	}
	if w.sender != nil {
		w.sender.Send(tuievents.LogChunkMsg{Chunk: string(p)})
	}
	return len(p), nil
}

type teaPromptBroker struct {
	sender *teaProgramSender

	mu      sync.Mutex
	closed  bool
	pending map[chan tuievents.PromptResponse]struct{}
}

func newTeaPromptBroker(sender *teaProgramSender) *teaPromptBroker {
	return &teaPromptBroker{
		sender:  sender,
		pending: map[chan tuievents.PromptResponse]struct{}{},
	}
}

func (b *teaPromptBroker) ReadLine(prompt string) (string, error) {
	return b.requestPrompt(prompt, false)
}

func (b *teaPromptBroker) ReadSecret(prompt string) (string, error) {
	return b.requestPrompt(prompt, true)
}

func (b *teaPromptBroker) requestPrompt(prompt string, secret bool) (string, error) {
	return b.requestPromptWithOptions(prompt, secret, nil, "", nil, false, false, false)
}

func (b *teaPromptBroker) RequestChoicePrompt(prompt string, choices []tuievents.PromptChoice, defaultChoice string, filterable bool) (string, error) {
	return b.requestPromptWithOptions(prompt, false, choices, defaultChoice, nil, filterable, false, false)
}

func (b *teaPromptBroker) RequestStructuredPrompt(req tuievents.PromptRequestMsg) (string, error) {
	return b.requestPromptWithRequest(req)
}

func (b *teaPromptBroker) RequestMultiChoicePrompt(prompt string, choices []tuievents.PromptChoice, selectedChoices []string, filterable bool) (string, error) {
	allowFreeformInput := false
	for _, choice := range choices {
		if choice.AlwaysVisible {
			allowFreeformInput = true
			break
		}
	}
	return b.requestPromptWithOptions(prompt, false, choices, "", selectedChoices, filterable, true, allowFreeformInput)
}

func (b *teaPromptBroker) requestPromptWithOptions(prompt string, secret bool, choices []tuievents.PromptChoice, defaultChoice string, selectedChoices []string, filterable bool, multiSelect bool, allowFreeformInput bool) (string, error) {
	req := tuievents.PromptRequestMsg{
		Prompt:             prompt,
		Secret:             secret,
		Choices:            append([]tuievents.PromptChoice(nil), choices...),
		DefaultChoice:      defaultChoice,
		SelectedChoices:    append([]string(nil), selectedChoices...),
		Filterable:         filterable,
		MultiSelect:        multiSelect,
		AllowFreeformInput: allowFreeformInput,
	}
	return b.requestPromptWithRequest(req)
}

func (b *teaPromptBroker) requestPromptWithRequest(req tuievents.PromptRequestMsg) (string, error) {
	response := make(chan tuievents.PromptResponse, 1)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return "", errInputEOF
	}
	b.pending[response] = struct{}{}
	b.mu.Unlock()

	req.Response = response
	req.Choices = append([]tuievents.PromptChoice(nil), req.Choices...)
	req.SelectedChoices = append([]string(nil), req.SelectedChoices...)
	req.Details = append([]tuievents.PromptDetail(nil), req.Details...)
	b.sender.Send(req)

	result, ok := <-response
	b.mu.Lock()
	delete(b.pending, response)
	b.mu.Unlock()
	if !ok {
		return "", errInputEOF
	}
	if result.Err != nil {
		switch strings.TrimSpace(result.Err.Error()) {
		case tuievents.PromptErrInterrupt:
			return "", errInputInterrupt
		case tuievents.PromptErrEOF:
			return "", errInputEOF
		default:
			return "", result.Err
		}
	}
	return strings.TrimSpace(result.Line), nil
}

func (b *teaPromptBroker) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.pending {
		ch <- tuievents.PromptResponse{Err: errors.New(tuievents.PromptErrEOF)}
	}
}

func (c *cliConsole) loopTUITea(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("cli: context is required")
	}
	if !isTTY(os.Stdin) || !isTTY(os.Stdout) {
		return fmt.Errorf("tui mode requires an interactive terminal")
	}

	previousOut := c.out
	previousUI := c.ui
	previousPrompter := c.prompter
	previousApprover := c.approver

	sender := &teaProgramSender{}
	c.tuiSender = sender
	promptBroker := newTeaPromptBroker(sender)
	writer := &teaOutputWriter{sender: sender, diag: c.tuiDiag}

	c.out = writer
	c.ui = newUI(writer, true, c.verbose)
	c.prompter = promptBroker
	c.approver = newTerminalApprover(c.prompter, writer, c.ui)
	c.approver.modeResolver = func() string { return c.sessionMode }
	if c.tuiDiag != nil {
		c.tuiDiag.SetRedrawMode("fullscreen")
	}

	defer func() {
		promptBroker.Close()
		c.tuiSender = nil
		c.out = previousOut
		c.ui = previousUI
		c.prompter = previousPrompter
		c.approver = previousApprover
		if previousOut != nil {
			_, _ = io.WriteString(previousOut, ansi.ResetModeAltScreenSaveCursor+ansi.ShowCursor)
		}
		if closeErr := toolexec.Close(c.execRuntime); closeErr != nil {
			c.printf("warn: close execution runtime failed: %v\n", closeErr)
		}
	}()

	model := tuiapp.NewModel(tuiapp.Config{
		Version:              strings.TrimSpace(c.version),
		Workspace:            c.workspaceLine,
		ModelAlias:           c.modelAlias,
		ShowWelcomeCard:      true,
		FrameBatchMainStream: true,
		StreamTickInterval:   33 * time.Millisecond,
		Commands:             c.availableCommandNames(),
		Wizards:              buildWizardDefs(),
		ExecuteLine: func(submission tuiapp.Submission) tuievents.TaskResultMsg {
			line := strings.TrimSpace(submission.Text)
			if submission.Mode == tuiapp.SubmissionModeOverlay {
				err := c.runBTWContext(ctx, line, submission.Attachments)
				if err != nil {
					if c.tuiSender != nil {
						c.tuiSender.Send(tuievents.BTWErrorMsg{Text: err.Error()})
						if c.currentRunKind() == runOccupancyNone {
							c.tuiSender.Send(tuievents.SetRunningMsg{Running: false})
						}
					}
					return tuievents.TaskResultMsg{Err: err, ContinueRunning: c.currentRunKind() != runOccupancyNone}
				}
				return tuievents.TaskResultMsg{ContinueRunning: true}
			}
			if c.shouldHandleAsSlashCommand(line) {
				exitNow, err := c.handleSlashContext(ctx, line)
				if err == nil && c.currentRunKind() == runOccupancyExternalAgent {
					return tuievents.TaskResultMsg{ContinueRunning: true}
				}
				return tuievents.TaskResultMsg{ExitNow: exitNow, Err: err}
			}
			if c.currentRunKind() == runOccupancyExternalAgent {
				return tuievents.TaskResultMsg{Err: errExternalAgentRunBusy, ContinueRunning: true}
			}
			if alias, prompt, ok := parseParticipantRouteInput(line); ok {
				if prompt == "" {
					return tuievents.TaskResultMsg{Err: fmt.Errorf("usage: @%s <prompt>", alias), ContinueRunning: true}
				}
				err := c.routeExternalParticipantContext(ctx, alias, prompt)
				if err == nil {
					return tuievents.TaskResultMsg{ContinueRunning: true}
				}
				return tuievents.TaskResultMsg{Err: err, ContinueRunning: c.currentRunKind() != runOccupancyNone}
			}
			if c.getActiveRunner() != nil {
				err := c.runPromptWithAttachmentsContext(ctx, line, submission.Attachments)
				return tuievents.TaskResultMsg{Err: err, ContinueRunning: true}
			}
			err := c.runPromptWithAttachmentsContext(ctx, line, submission.Attachments)
			if errors.Is(err, context.Canceled) {
				return tuievents.TaskResultMsg{Interrupted: true}
			}
			return tuievents.TaskResultMsg{Err: err}
		},
		CancelRunning: func() bool {
			return c.cancelActiveRun()
		},
		ToggleMode: func() (string, error) {
			return c.togglePlanMode()
		},
		ModeLabel: func() string {
			return c.sessionModeLabel()
		},
		RefreshWorkspace: func() string {
			return c.readWorkspaceStatusLine()
		},
		RefreshStatus: func() (string, string) {
			return c.readTUIStatus()
		},
		MentionComplete: func(query string, limit int) ([]string, error) {
			begin := time.Now()
			candidates, err := c.participantAliasesContext(ctx, query, limit)
			if c.tuiDiag != nil {
				c.tuiDiag.ObserveMentionLatency(time.Since(begin))
			}
			return candidates, err
		},
		FileComplete: func(query string, limit int) ([]string, error) {
			if c.inputRefs == nil {
				return nil, nil
			}
			return c.inputRefs.CompleteFiles(query, limit)
		},
		SkillComplete: func(query string, limit int) ([]string, error) {
			discovered := appskills.DiscoverMeta(c.skillDirs)
			if len(discovered.Metas) == 0 {
				return nil, nil
			}
			query = strings.ToLower(query)
			var matches []string
			for _, m := range discovered.Metas {
				name := m.Name
				if query == "" || strings.Contains(strings.ToLower(name), query) {
					matches = append(matches, name)
					if len(matches) >= limit {
						break
					}
				}
			}
			return matches, nil
		},
		ResumeComplete: func(query string, limit int) ([]tuiapp.ResumeCandidate, error) {
			return c.completeResumeCandidates(query, limit)
		},
		SlashArgComplete: func(command string, query string, limit int) ([]tuiapp.SlashArgCandidate, error) {
			return c.completeSlashArgCandidatesContext(ctx, command, query, limit)
		},
		PasteClipboardImage: func() ([]string, string, error) {
			return c.pasteClipboardImage()
		},
		ClearAttachments: func() []string {
			return c.clearPendingAttachments()
		},
		SetAttachments: func(names []string) []string {
			return c.setPendingAttachments(names)
		},
		OnDiagnostics: func(d tuiapp.Diagnostics) {
			if c.tuiDiag == nil {
				return
			}
			c.tuiDiag.UpdateFromModel(
				d.Frames,
				d.IncrementalFrames,
				d.FullRepaints,
				d.SlowFrames,
				d.LastFrameDuration,
				d.AvgFrameDuration,
				d.MaxFrameDuration,
				d.RenderBytes,
				d.PeakFrameBytes,
				d.LastRenderAt,
				d.LastInputAt,
				d.LastInputLatency,
				d.AvgInputLatency,
				d.P95InputLatency,
				d.LastMentionLatency,
				d.RedrawMode,
			)
		},
	})

	program := tea.NewProgram(model, tea.WithFPS(30))
	sender.set(func(msg any) { program.Send(msg) })

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		for range sigCh {
			if c.cancelActiveRun() {
				sender.Send(tuievents.SetHintMsg{
					Hint:           "interrupt requested",
					ClearAfter:     transientHintDuration,
					Priority:       tuievents.HintPriorityCritical,
					ClearOnMessage: true,
				})
				continue
			}
			sender.Send(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
		}
	}()

	if _, err := program.Run(); err != nil {
		return err
	}
	return nil
}

// readTUIStatus returns the model label and context summary for the TUI status bar.
func (c *cliConsole) readTUIStatus() (string, string) {
	modelLabel := strings.TrimSpace(c.modelAlias)
	if modelLabel == "" {
		modelLabel = "no model"
	}
	if level := c.statusReasoningLevelLabel(); level != "" {
		modelLabel = modelLabel + " [" + level + "]"
	}
	pt := c.lastPromptTokens
	if pt < 0 {
		pt = 0
	}
	cw := c.resolveContextWindowForDisplay()
	var contextStr string
	switch {
	case cw > 0:
		pct := int(float64(pt) / float64(cw) * 100)
		used := formatTokenCount(pt)
		if used == "" {
			used = "0"
		}
		total := formatTokenCount(cw)
		if total == "" {
			total = "0"
		}
		contextStr = fmt.Sprintf("%s/%s(%d%%)", used, total, pct)
	case pt > 0:
		contextStr = formatTokenCount(pt)
	default:
		contextStr = "0"
	}
	return modelLabel, contextStr
}

func workspaceStatusLine(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	label := shortenHomeDir(cwd)
	if branch, dirty := gitBranchStatus(cwd); branch != "" {
		label += " [⎇ " + branch
		if dirty {
			label += "*"
		}
		label += "]"
	}
	return label
}

func (c *cliConsole) readWorkspaceStatusLine() string {
	if c == nil {
		return ""
	}
	return workspaceStatusLine(c.workspace.CWD)
}

func shortenHomeDir(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	return strings.Replace(path, home, "~", 1)
}

func gitBranchStatus(cwd string) (string, bool) {
	branchOut, err := exec.Command("git", "-C", cwd, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	if err != nil {
		branchOut, err = exec.Command("git", "-C", cwd, "rev-parse", "--short", "HEAD").Output()
		if err != nil {
			return "", false
		}
	}
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" {
		return "", false
	}
	statusOut, err := exec.Command("git", "-C", cwd, "status", "--porcelain", "--ignore-submodules=dirty").Output()
	dirty := err == nil && strings.TrimSpace(string(statusOut)) != ""
	return branch, dirty
}

func (c *cliConsole) statusReasoningLevelLabel() string {
	if c == nil {
		return ""
	}
	profile := c.currentReasoningProfile()
	effort := normalizeReasoningLevel(c.reasoningEffort)
	switch profile.Mode {
	case reasoningModeEffort:
		if effort == "none" {
			return ""
		}
		if effort == "" {
			if profile.DefaultEffort == "" {
				return "reasoning on"
			}
			return profile.DefaultEffort
		}
		return effort
	case reasoningModeFixed:
		return "reasoning on"
	case reasoningModeToggle:
		if effort == "none" {
			return ""
		}
		return "reasoning on"
	default:
		return ""
	}
}

func (c *cliConsole) currentReasoningProfile() reasoningProfile {
	if c == nil {
		return reasoningProfile{Mode: reasoningModeNone}
	}
	if c.modelFactory != nil {
		if cfg, ok := c.modelFactory.ConfigForAlias(c.modelAlias); ok {
			return reasoningProfileForConfig(cfg)
		}
	}
	provider := resolveProviderName(c.modelFactory, c.modelAlias)
	model := resolveModelName(c.modelFactory, c.modelAlias)
	if provider == "" && model == "" {
		return reasoningProfile{Mode: reasoningModeNone}
	}
	return reasoningProfileForModel(provider, model)
}

func (c *cliConsole) completeResumeCandidates(query string, limit int) ([]tuiapp.ResumeCandidate, error) {
	if c == nil || c.sessionIndex == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	records, err := c.sessionIndex.ListWorkspaceSessionsPageContext(c.baseCtx, c.workspace.Key, 1, 200)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.ResumeCandidate, 0, limit)
	for _, rec := range records {
		sid := strings.TrimSpace(rec.SessionID)
		if sid == "" || sid == c.sessionID || rec.EventCount <= 0 {
			continue
		}
		prompt := sessionIndexPreview(rec, 100)
		age := "-"
		if !rec.LastEventAt.IsZero() {
			age = now.Sub(rec.LastEventAt).Round(time.Second).String()
		}
		if q != "" {
			idMatch := strings.Contains(strings.ToLower(sid), q)
			promptMatch := strings.Contains(strings.ToLower(prompt), q)
			if !idMatch && !promptMatch {
				continue
			}
		}
		out = append(out, tuiapp.ResumeCandidate{
			SessionID: idutil.ShortDisplay(sid),
			Prompt:    truncateInline(prompt, 100),
			Age:       age,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (c *cliConsole) completeSlashArgCandidates(command string, query string, limit int) ([]tuiapp.SlashArgCandidate, error) {
	return c.completeSlashArgCandidatesContext(c.baseCtx, command, query, limit)
}

func (c *cliConsole) completeSlashArgCandidatesContext(ctx context.Context, command string, query string, limit int) ([]tuiapp.SlashArgCandidate, error) {
	if c == nil {
		return nil, nil
	}
	rawCmd := strings.TrimSpace(command)
	cmd := strings.ToLower(rawCmd)
	switch {
	case strings.HasPrefix(cmd, "model use "):
		alias := strings.TrimSpace(rawCmd[len("model use "):])
		if alias == "" {
			return nil, nil
		}
		return c.completeModelReasoningCandidates(alias, query, limit), nil
	case strings.HasPrefix(cmd, "model-reasoning:"):
		alias, ok := parseModelReasoningPayload(rawCmd)
		if !ok {
			return nil, nil
		}
		return c.completeModelReasoningCandidates(alias, query, limit), nil
	case strings.HasPrefix(cmd, "model "):
		actionQuery := strings.TrimSpace(strings.TrimPrefix(rawCmd, "model"))
		if actionQuery == "" {
			return c.completeModelCommandCandidates(query, limit), nil
		}
		if query != "" {
			actionQuery += " " + query
		}
		return c.completeModelCommandCandidates(actionQuery, limit), nil
	case strings.HasPrefix(cmd, "agent "):
		actionQuery := strings.TrimSpace(strings.TrimPrefix(rawCmd, "agent"))
		if actionQuery == "" {
			return c.completeAgentCommandCandidates(query, limit), nil
		}
		if query != "" {
			actionQuery += " " + query
		}
		return c.completeAgentCommandCandidates(actionQuery, limit), nil
	case strings.HasPrefix(cmd, "connect-model:"):
		payload := strings.TrimPrefix(rawCmd, "connect-model:")
		provider, baseURL, timeoutSeconds, apiKey, hasRemoteContext := parseConnectModelPayload(payload)
		if hasRemoteContext {
			return c.completeConnectModelCandidatesRemoteContext(ctx, provider, baseURL, timeoutSeconds, apiKey, query, limit), nil
		}
		return c.completeConnectModelCandidates(provider, query, limit), nil
	case strings.HasPrefix(cmd, "connect-context:"):
		payload := strings.TrimPrefix(rawCmd, "connect-context:")
		return c.completeConnectContextCandidates(payload, query, limit), nil
	case strings.HasPrefix(cmd, "connect-maxout:"):
		payload := strings.TrimPrefix(rawCmd, "connect-maxout:")
		return c.completeConnectMaxOutputCandidates(payload, query, limit), nil
	case strings.HasPrefix(cmd, "connect-reasoning-levels:"):
		payload := strings.TrimPrefix(rawCmd, "connect-reasoning-levels:")
		return c.completeConnectReasoningLevelsCandidates(payload, query, limit), nil
	case strings.HasPrefix(cmd, "connect-baseurl:"):
		provider := strings.TrimPrefix(strings.ToLower(rawCmd), "connect-baseurl:")
		return c.completeConnectBaseURLCandidates(provider, query, limit), nil
	case strings.HasPrefix(cmd, "connect-timeout:"):
		return c.completeConnectTimeoutCandidates(query, limit), nil
	case strings.HasPrefix(cmd, "connect-apikey:"):
		return nil, nil
	}
	switch cmd {
	case "model":
		return c.completeModelCommandCandidates(query, limit), nil
	case "agent":
		return c.completeAgentCommandCandidates(query, limit), nil
	case "sandbox":
		return c.completeSandboxCandidates(query, limit), nil
	case "connect":
		return c.completeConnectCandidates(query, limit), nil
	default:
		return nil, nil
	}
}

func parseModelReasoningPayload(command string) (string, bool) {
	payload := strings.TrimSpace(strings.TrimPrefix(command, "model-reasoning:"))
	if payload == "" {
		return "", false
	}
	decoded, err := url.QueryUnescape(payload)
	if err != nil {
		return "", false
	}
	alias := strings.ToLower(strings.TrimSpace(decoded))
	if alias == "" {
		return "", false
	}
	return alias, true
}

func parseConnectModelPayload(payload string) (provider, baseURL string, timeoutSeconds int, apiKey string, hasRemoteContext bool) {
	parts := strings.Split(payload, "|")
	provider = strings.ToLower(strings.TrimSpace(parts[0]))
	if provider == "" {
		return "", "", 0, "", false
	}
	if len(parts) < 4 {
		return provider, "", 0, "", false
	}
	decodedBaseURL, err := url.QueryUnescape(strings.TrimSpace(parts[1]))
	if err != nil {
		return provider, "", 0, "", false
	}
	decodedAPIKey, err := url.QueryUnescape(strings.TrimSpace(parts[3]))
	if err != nil {
		return provider, "", 0, "", false
	}
	timeout, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil || timeout <= 0 {
		return provider, "", 0, "", false
	}
	decodedBaseURL = strings.TrimSpace(decodedBaseURL)
	decodedAPIKey = strings.TrimSpace(decodedAPIKey)
	if decodedBaseURL == "" {
		return provider, "", 0, "", false
	}
	return provider, decodedBaseURL, timeout, decodedAPIKey, true
}

func parseConnectSettingsPayload(payload string) (provider, baseURL string, timeoutSeconds int, apiKey, model string, ok bool) {
	parts := strings.Split(payload, "|")
	provider = strings.ToLower(strings.TrimSpace(parts[0]))
	if provider == "" {
		return "", "", 0, "", "", false
	}
	if len(parts) < 5 {
		return provider, "", 0, "", "", false
	}
	decodedBaseURL, err := url.QueryUnescape(strings.TrimSpace(parts[1]))
	if err != nil {
		return provider, "", 0, "", "", false
	}
	decodedAPIKey, err := url.QueryUnescape(strings.TrimSpace(parts[3]))
	if err != nil {
		return provider, "", 0, "", "", false
	}
	decodedModel, err := url.QueryUnescape(strings.TrimSpace(parts[4]))
	if err != nil {
		return provider, "", 0, "", "", false
	}
	timeout, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil || timeout < 0 {
		return provider, "", 0, "", "", false
	}
	decodedBaseURL = strings.TrimSpace(decodedBaseURL)
	decodedAPIKey = strings.TrimSpace(decodedAPIKey)
	decodedModel = strings.TrimSpace(decodedModel)
	if decodedBaseURL == "" || decodedModel == "" {
		return provider, "", 0, "", "", false
	}
	return provider, decodedBaseURL, timeout, decodedAPIKey, decodedModel, true
}

func (c *cliConsole) completeModelCandidates(query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	type item struct {
		alias    string
		provider string
		model    string
		baseURL  string
	}
	parse := func(alias string) item {
		one := item{alias: strings.ToLower(strings.TrimSpace(alias))}
		if one.alias == "" {
			return one
		}
		if c.modelFactory != nil {
			if cfg, ok := c.modelFactory.ConfigForAlias(one.alias); ok {
				one.provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
				one.model = strings.TrimSpace(cfg.Model)
				one.baseURL = strings.TrimSpace(cfg.BaseURL)
			}
		}
		if one.provider == "" || one.model == "" {
			parts := strings.SplitN(one.alias, "/", 2)
			if len(parts) == 2 {
				if one.provider == "" {
					one.provider = strings.ToLower(strings.TrimSpace(parts[0]))
				}
				if one.model == "" {
					one.model = strings.TrimSpace(parts[1])
				}
			}
		}
		return one
	}

	aliases := make([]string, 0, 16)
	if c.configStore != nil {
		aliases = append(aliases, c.configStore.ConfiguredModelAliases()...)
	}
	if len(aliases) == 0 && c.modelFactory != nil {
		aliases = append(aliases, c.modelFactory.ListModels()...)
	}
	if len(aliases) == 0 {
		return nil
	}

	items := make([]item, 0, len(aliases))
	for _, alias := range aliases {
		parsed := parse(alias)
		if parsed.alias == "" {
			continue
		}
		items = append(items, parsed)
	}
	duplicateCount := map[string]int{}
	for _, one := range items {
		if one.provider == "" || one.model == "" {
			continue
		}
		duplicateCount[one.provider+"/"+strings.ToLower(one.model)]++
	}
	sort.SliceStable(items, func(i, j int) bool {
		pi := items[i].provider
		pj := items[j].provider
		if pi != pj {
			return pi < pj
		}
		mi := strings.ToLower(items[i].model)
		mj := strings.ToLower(items[j].model)
		if mi != mj {
			return mi < mj
		}
		return items[i].alias < items[j].alias
	})

	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, minInt(limit, len(items)))
	for _, one := range items {
		display := one.alias
		if one.provider != "" && one.model != "" {
			display = canonicalModelRef(one.provider, one.model)
			if duplicateCount[one.provider+"/"+strings.ToLower(one.model)] > 1 {
				display = fmt.Sprintf("%s (%s)", display, compactEndpointForDisplay(one.baseURL))
			}
		}
		if q != "" {
			text := strings.ToLower(one.provider + " " + one.model)
			if !strings.Contains(text, q) {
				continue
			}
		}
		out = append(out, tuiapp.SlashArgCandidate{
			Value:   one.alias,
			Display: display,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (c *cliConsole) completeModelCommandCandidates(query string, limit int) []tuiapp.SlashArgCandidate {
	raw := strings.TrimLeft(query, " \t")
	hasTrailingSpace := strings.HasSuffix(query, " ") || strings.HasSuffix(query, "\t")
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return completeModelActionCandidates("", limit)
	}
	action := strings.ToLower(strings.TrimSpace(fields[0]))
	if !hasTrailingSpace && len(fields) == 1 {
		switch action {
		case "use", "del":
		default:
			return completeModelActionCandidates(action, limit)
		}
	}
	switch action {
	case "use":
		if len(fields) == 1 {
			return c.completeModelCandidates("", limit)
		}
		if len(fields) == 2 && !hasTrailingSpace {
			return c.completeModelCandidates(fields[1], limit)
		}
		alias := fields[1]
		reasoningQuery := ""
		if len(fields) >= 3 {
			reasoningQuery = fields[len(fields)-1]
		}
		return c.completeModelReasoningCandidates(alias, reasoningQuery, limit)
	case "del":
		return nil
	default:
		return completeModelActionCandidates(action, limit)
	}
}

func completeModelActionCandidates(query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	actions := []tuiapp.SlashArgCandidate{
		{Value: "use", Display: "use"},
		{Value: "del", Display: "del"},
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, len(actions))
	for _, one := range actions {
		if q != "" && !strings.Contains(strings.ToLower(one.Value), q) {
			continue
		}
		out = append(out, one)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (c *cliConsole) completeAgentCommandCandidates(query string, limit int) []tuiapp.SlashArgCandidate {
	raw := strings.TrimLeft(query, " \t")
	hasTrailingSpace := strings.HasSuffix(query, " ") || strings.HasSuffix(query, "\t")
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return completeAgentActionCandidates("", limit)
	}
	action := strings.ToLower(strings.TrimSpace(fields[0]))
	if !hasTrailingSpace && len(fields) == 1 {
		switch action {
		case "list", "add", "rm":
		default:
			return completeAgentActionCandidates(action, limit)
		}
	}
	switch action {
	case "list":
		return nil
	case "add":
		if len(fields) == 1 {
			return completeAgentBuiltinCandidates("", limit)
		}
		if len(fields) == 2 && !hasTrailingSpace {
			return completeAgentBuiltinCandidates(fields[1], limit)
		}
		return nil
	case "rm":
		if len(fields) == 1 {
			return c.completeConfiguredAgentCandidates("", limit)
		}
		if len(fields) == 2 && !hasTrailingSpace {
			return c.completeConfiguredAgentCandidates(fields[1], limit)
		}
		return nil
	default:
		return completeAgentActionCandidates(action, limit)
	}
}

func completeAgentActionCandidates(query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	actions := []tuiapp.SlashArgCandidate{
		{Value: "list", Display: "list"},
		{Value: "add", Display: "add"},
		{Value: "rm", Display: "rm"},
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, len(actions))
	for _, one := range actions {
		if q != "" && !strings.Contains(strings.ToLower(one.Value), q) {
			continue
		}
		out = append(out, one)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func completeAgentBuiltinCandidates(query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	q := strings.ToLower(strings.TrimSpace(query))
	builtins := appagents.KnownBuiltins()
	out := make([]tuiapp.SlashArgCandidate, 0, minInt(limit, len(builtins)))
	for _, one := range builtins {
		text := strings.ToLower(strings.TrimSpace(one.ID) + " " + strings.TrimSpace(one.Stability) + " " + strings.TrimSpace(one.Description))
		if q != "" && !strings.Contains(text, q) {
			continue
		}
		display := one.ID
		if one.Stability != "" {
			display = fmt.Sprintf("%s (%s)", one.ID, one.Stability)
		}
		out = append(out, tuiapp.SlashArgCandidate{
			Value:   one.ID,
			Display: display,
			Detail:  strings.TrimSpace(one.Description),
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (c *cliConsole) completeConfiguredAgentCandidates(query string, limit int) []tuiapp.SlashArgCandidate {
	if c == nil || c.configStore == nil {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	q := strings.ToLower(strings.TrimSpace(query))
	records := c.configStore.normalizedAgentRecords()
	out := make([]tuiapp.SlashArgCandidate, 0, minInt(limit, len(records)))
	for _, rec := range records {
		stability := appagents.NormalizeStability(rec.Stability)
		text := strings.ToLower(strings.TrimSpace(rec.Name) + " " + stability + " " + strings.TrimSpace(rec.Command) + " " + strings.TrimSpace(rec.Description))
		if q != "" && !strings.Contains(text, q) {
			continue
		}
		display := rec.Name
		if stability != "" {
			display = fmt.Sprintf("%s (%s)", rec.Name, stability)
		}
		out = append(out, tuiapp.SlashArgCandidate{
			Value:   rec.Name,
			Display: display,
			Detail:  strings.TrimSpace(rec.Command),
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func compactEndpointForDisplay(baseURL string) string {
	value := strings.TrimSpace(baseURL)
	if value == "" {
		return "default endpoint"
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	host := strings.TrimSpace(parsed.Host)
	path := strings.Trim(strings.TrimSpace(parsed.Path), "/")
	if host == "" {
		return value
	}
	if path == "" {
		return host
	}
	return host + "/" + path
}

func (c *cliConsole) completeModelReasoningCandidates(alias string, query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		return nil
	}
	if c.configStore != nil {
		alias = c.configStore.ResolveModelAlias(alias)
	}
	cfg := modelproviders.Config{Alias: alias}
	if c.modelFactory != nil {
		if foundCfg, ok := c.modelFactory.ConfigForAlias(alias); ok {
			cfg = foundCfg
		}
	}
	if strings.TrimSpace(cfg.Provider) == "" || strings.TrimSpace(cfg.Model) == "" {
		parts := strings.SplitN(alias, "/", 2)
		if len(parts) == 2 {
			cfg.Provider = strings.TrimSpace(parts[0])
			cfg.Model = strings.TrimSpace(parts[1])
		}
	}
	options := modelReasoningOptionsForConfig(cfg)
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, minInt(limit, len(options)))
	for _, one := range options {
		value := strings.TrimSpace(one.Value)
		if value == "" {
			continue
		}
		display := strings.TrimSpace(one.Display)
		if display == "" {
			display = value
		}
		if q != "" {
			text := strings.ToLower(display + " " + value)
			if !strings.Contains(text, q) {
				continue
			}
		}
		out = append(out, tuiapp.SlashArgCandidate{Value: value, Display: display})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (c *cliConsole) completeSandboxCandidates(query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	seen := map[string]struct{}{}
	order := make([]string, 0, len(availableSandboxTypes())+1)
	appendType := func(value string) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		order = append(order, value)
	}
	appendType(c.sandboxType)
	appendType("auto")
	for _, one := range availableSandboxTypes() {
		appendType(one)
	}

	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, minInt(limit, len(order)))
	for _, one := range order {
		if q != "" && !strings.Contains(one, q) {
			continue
		}
		display, detail := sandboxCompletionLabel(one)
		out = append(out, tuiapp.SlashArgCandidate{Value: one, Display: display, Detail: detail})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func sandboxCompletionLabel(value string) (display string, detail string) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "", "auto", "default":
		if runtime.GOOS == "linux" {
			return "auto", "try bwrap, then landlock"
		}
		if runtime.GOOS == "darwin" {
			return "auto", "use seatbelt"
		}
		return "auto", "use platform default"
	case "bwrap":
		if runtime.GOOS == "linux" {
			return "bwrap", "preferred on Linux"
		}
		return "bwrap", ""
	case "landlock":
		if runtime.GOOS == "linux" {
			return "landlock", "fallback, reduced isolation semantics"
		}
		return "landlock", "reduced isolation semantics"
	default:
		return normalized, ""
	}
}

func (c *cliConsole) completeConnectCandidates(query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, minInt(limit, len(providerTemplates)))
	for _, tpl := range providerTemplates {
		label := strings.TrimSpace(tpl.label)
		if label == "" {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(label), q) {
			continue
		}
		out = append(out, tuiapp.SlashArgCandidate{Value: label, Display: label, NoAuth: tpl.noAuthRequired})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (c *cliConsole) completeConnectModelCandidates(provider string, query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return nil
	}
	seen := map[string]struct{}{}
	models := make([]string, 0, 20)
	addModel := func(name string) {
		value := strings.TrimSpace(name)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		models = append(models, value)
	}
	for _, one := range commonModelsForProvider(provider) {
		addModel(one)
	}
	if c.configStore != nil {
		for _, cfg := range c.configStore.ProviderConfigs() {
			if strings.ToLower(strings.TrimSpace(cfg.Provider)) != provider {
				continue
			}
			addModel(cfg.Model)
		}
	}
	sort.Strings(models)
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, minInt(limit, len(models)))
	for _, one := range models {
		if q != "" && !strings.Contains(strings.ToLower(one), q) {
			continue
		}
		out = append(out, tuiapp.SlashArgCandidate{Value: one, Display: one})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (c *cliConsole) completeConnectModelCandidatesRemote(provider, baseURL string, timeoutSeconds int, apiKey, query string, limit int) []tuiapp.SlashArgCandidate {
	return c.completeConnectModelCandidatesRemoteContext(c.baseCtx, provider, baseURL, timeoutSeconds, apiKey, query, limit)
}

func (c *cliConsole) completeConnectModelCandidatesRemoteContext(ctx context.Context, provider, baseURL string, timeoutSeconds int, apiKey, query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	baseURL = strings.TrimSpace(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	if provider == "" || baseURL == "" || timeoutSeconds <= 0 {
		return nil
	}
	tpl, ok := findProviderTemplate(provider)
	if !ok {
		return nil
	}
	// No-auth providers (e.g. Ollama) can discover without an API key.
	if apiKey == "" && !tpl.noAuthRequired {
		return nil
	}
	cacheKey := buildConnectModelCacheKey(provider, baseURL, timeoutSeconds, apiKey)
	if cached, ok := c.getConnectModelCache(cacheKey); ok {
		return filterConnectModelCandidates(cached, query, limit)
	}
	authCfg := modelproviders.AuthConfig{
		Type:  modelproviders.AuthAPIKey,
		Token: apiKey,
	}
	if tpl.noAuthRequired {
		authCfg = modelproviders.AuthConfig{
			Type:  modelproviders.AuthNone,
			Token: apiKey,
		}
	}
	cfg := modelproviders.Config{
		Provider: strings.TrimSpace(tpl.provider),
		API:      tpl.api,
		BaseURL:  baseURL,
		Timeout:  time.Duration(timeoutSeconds) * time.Second,
		Auth:     authCfg,
	}
	if ctx == nil {
		return nil
	}
	models, err := discoverModelsFn(ctx, cfg)
	if err != nil || len(models) == 0 {
		c.setConnectModelCache(cacheKey, nil)
		return nil
	}
	seen := map[string]struct{}{}
	names := make([]string, 0, len(models))
	for _, item := range models {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	c.setConnectModelCache(cacheKey, names)
	return filterConnectModelCandidates(names, query, limit)
}

func connectWizardSuggestedSettings(provider, model string) (contextWindowTokens int, maxOutputTokens int, reasoningLevels []string) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)

	contextWindowTokens = defaultCatalogModelCapabilities().ContextWindowTokens
	maxOutputTokens = defaultCatalogModelCapabilities().DefaultMaxOutputTokens
	if tpl, ok := findProviderTemplate(provider); ok {
		if tpl.defaultContextToken > 0 {
			contextWindowTokens = tpl.defaultContextToken
		}
		maxOutputTokens = defaultMaxOutputForTemplate(tpl)
	}

	if caps, ok := lookupDynamicCatalogCapabilities(provider, model); ok {
		if caps.ContextWindowTokens > 0 {
			contextWindowTokens = caps.ContextWindowTokens
		}
		if caps.DefaultMaxOutputTokens > 0 {
			maxOutputTokens = caps.DefaultMaxOutputTokens
		} else if caps.MaxOutputTokens > 0 {
			maxOutputTokens = caps.MaxOutputTokens
		}
		if _, exactKnown := lookupBaseCatalogModelCapabilities(provider, model); !exactKnown {
			maxOutputTokens = recommendedCatalogFallbackMaxOutputTokens(contextWindowTokens, maxOutputTokens, caps.SupportsReasoning)
		}
		reasoningLevels = normalizeReasoningLevels(caps.ReasoningEfforts)
		if len(reasoningLevels) == 0 && !caps.SupportsReasoning {
			reasoningLevels = []string{"none"}
		}
	}
	return contextWindowTokens, maxOutputTokens, reasoningLevels
}

func (c *cliConsole) completeConnectContextCandidates(payload string, query string, _ int) []tuiapp.SlashArgCandidate {
	provider, _, _, _, model, ok := parseConnectSettingsPayload(payload)
	if !ok {
		return nil
	}
	ctxTokens, _, _ := connectWizardSuggestedSettings(provider, model)
	value := strconv.Itoa(ctxTokens)
	q := strings.ToLower(strings.TrimSpace(query))
	if q != "" && !strings.Contains(strings.ToLower(value), q) {
		return nil
	}
	return []tuiapp.SlashArgCandidate{{Value: value, Display: value}}
}

func (c *cliConsole) completeConnectMaxOutputCandidates(payload string, query string, _ int) []tuiapp.SlashArgCandidate {
	provider, _, _, _, model, ok := parseConnectSettingsPayload(payload)
	if !ok {
		return nil
	}
	_, maxTokens, _ := connectWizardSuggestedSettings(provider, model)
	value := strconv.Itoa(maxTokens)
	q := strings.ToLower(strings.TrimSpace(query))
	if q != "" && !strings.Contains(strings.ToLower(value), q) {
		return nil
	}
	return []tuiapp.SlashArgCandidate{{Value: value, Display: value}}
}

func (c *cliConsole) completeConnectReasoningLevelsCandidates(payload string, query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	provider, _, _, _, model, ok := parseConnectSettingsPayload(payload)
	if !ok {
		return nil
	}
	_, _, levels := connectWizardSuggestedSettings(provider, model)
	candidates := make([]tuiapp.SlashArgCandidate, 0, 2)
	if len(levels) == 0 {
		candidates = append(candidates, tuiapp.SlashArgCandidate{
			Value:   "-",
			Display: "(empty, unknown support)",
		})
	} else {
		csv := strings.Join(levels, ",")
		candidates = append(candidates, tuiapp.SlashArgCandidate{
			Value:   csv,
			Display: csv,
		})
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, minInt(limit, len(candidates)))
	for _, one := range candidates {
		text := strings.ToLower(strings.TrimSpace(one.Display + " " + one.Value))
		if q != "" && !strings.Contains(text, q) {
			continue
		}
		out = append(out, one)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func buildConnectModelCacheKey(provider, baseURL string, timeoutSeconds int, apiKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(apiKey)))
	return strings.ToLower(strings.TrimSpace(provider)) + "|" +
		strings.TrimSpace(baseURL) + "|" +
		strconv.Itoa(timeoutSeconds) + "|" +
		hex.EncodeToString(sum[:])
}

func filterConnectModelCandidates(names []string, query string, limit int) []tuiapp.SlashArgCandidate {
	if len(names) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 20
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, minInt(limit, len(names)))
	for _, name := range names {
		value := strings.TrimSpace(name)
		if value == "" {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(value), q) {
			continue
		}
		out = append(out, tuiapp.SlashArgCandidate{Value: value, Display: value})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (c *cliConsole) getConnectModelCache(key string) ([]string, bool) {
	if c == nil || strings.TrimSpace(key) == "" {
		return nil, false
	}
	c.connectModelCacheMu.Lock()
	defer c.connectModelCacheMu.Unlock()
	entry, ok := c.connectModelCache[key]
	if !ok {
		return nil, false
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		delete(c.connectModelCache, key)
		return nil, false
	}
	return append([]string(nil), entry.models...), true
}

func (c *cliConsole) setConnectModelCache(key string, names []string) {
	if c == nil || strings.TrimSpace(key) == "" {
		return
	}
	c.connectModelCacheMu.Lock()
	defer c.connectModelCacheMu.Unlock()
	if c.connectModelCache == nil {
		c.connectModelCache = map[string]connectModelCacheEntry{}
	}
	c.connectModelCache[key] = connectModelCacheEntry{
		models:    append([]string(nil), names...),
		expiresAt: time.Now().Add(connectModelCacheTTL),
	}
}

func (c *cliConsole) completeConnectBaseURLCandidates(provider string, query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return nil
	}
	seen := map[string]struct{}{}
	baseURLs := make([]string, 0, 8)
	addBaseURL := func(raw string) {
		value := strings.TrimSpace(raw)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		baseURLs = append(baseURLs, value)
	}
	if tpl, ok := findProviderTemplate(provider); ok {
		addBaseURL(tpl.defaultBaseURL)
	}
	if c.configStore != nil {
		for _, cfg := range c.configStore.ProviderConfigs() {
			if strings.ToLower(strings.TrimSpace(cfg.Provider)) != provider {
				continue
			}
			addBaseURL(cfg.BaseURL)
		}
	}
	if len(baseURLs) == 0 {
		return nil
	}
	// Keep default URL as first item, sort the rest for stable display.
	head := baseURLs[0]
	tail := append([]string(nil), baseURLs[1:]...)
	sort.Strings(tail)
	baseURLs = append([]string{head}, tail...)

	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, minInt(limit, len(baseURLs)))
	for _, one := range baseURLs {
		if q != "" && !strings.Contains(strings.ToLower(one), q) {
			continue
		}
		out = append(out, tuiapp.SlashArgCandidate{Value: one, Display: one})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (c *cliConsole) completeConnectTimeoutCandidates(query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	candidates := []string{"30", "60", "120"}
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, len(candidates))
	for _, one := range candidates {
		if q != "" && !strings.Contains(strings.ToLower(one), q) {
			continue
		}
		out = append(out, tuiapp.SlashArgCandidate{Value: one, Display: one + "s"})
		if len(out) >= limit {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Wizard definitions — declarative multi-step slash command flows
// ---------------------------------------------------------------------------

func buildWizardDefs() []tuiapp.WizardDef {
	return nil
}
