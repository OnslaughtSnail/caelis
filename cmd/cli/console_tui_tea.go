package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/OnslaughtSnail/caelis/internal/cli/tuiapp"
	"github.com/OnslaughtSnail/caelis/internal/cli/tuievents"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	modelproviders "github.com/OnslaughtSnail/caelis/kernel/model/providers"
	"github.com/OnslaughtSnail/caelis/kernel/skills"
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
	return b.requestPromptWithOptions(prompt, secret, nil, "", nil, false, false)
}

func (b *teaPromptBroker) RequestChoicePrompt(prompt string, choices []tuievents.PromptChoice, defaultChoice string, filterable bool) (string, error) {
	return b.requestPromptWithOptions(prompt, false, choices, defaultChoice, nil, filterable, false)
}

func (b *teaPromptBroker) RequestMultiChoicePrompt(prompt string, choices []tuievents.PromptChoice, selectedChoices []string, filterable bool) (string, error) {
	return b.requestPromptWithOptions(prompt, false, choices, "", selectedChoices, filterable, true)
}

func (b *teaPromptBroker) requestPromptWithOptions(prompt string, secret bool, choices []tuievents.PromptChoice, defaultChoice string, selectedChoices []string, filterable bool, multiSelect bool) (string, error) {
	response := make(chan tuievents.PromptResponse, 1)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return "", errInputEOF
	}
	b.pending[response] = struct{}{}
	b.mu.Unlock()

	b.sender.Send(tuievents.PromptRequestMsg{
		Prompt:          prompt,
		Secret:          secret,
		Choices:         append([]tuievents.PromptChoice(nil), choices...),
		DefaultChoice:   defaultChoice,
		SelectedChoices: append([]string(nil), selectedChoices...),
		Filterable:      filterable,
		MultiSelect:     multiSelect,
		Response:        response,
	})

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

func (c *cliConsole) loopTUITea() error {
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
		if closeErr := toolexec.Close(c.execRuntime); closeErr != nil {
			c.printf("warn: close execution runtime failed: %v\n", closeErr)
		}
	}()

	model := tuiapp.NewModel(tuiapp.Config{
		Version:         strings.TrimSpace(c.version),
		Workspace:       c.workspace.CWD,
		ModelAlias:      c.modelAlias,
		ShowWelcomeCard: true,
		Commands:        commandNames(c.commands),
		Wizards:         buildWizardDefs(),
		ExecuteLine: func(line string) tuievents.TaskResultMsg {
			if strings.HasPrefix(strings.TrimSpace(line), "/") {
				exitNow, err := c.handleSlash(strings.TrimSpace(line))
				return tuievents.TaskResultMsg{ExitNow: exitNow, Err: err}
			}
			err := c.runPrompt(line)
			if errors.Is(err, context.Canceled) {
				return tuievents.TaskResultMsg{Err: errors.New("execution interrupted")}
			}
			return tuievents.TaskResultMsg{Err: err}
		},
		CancelRunning: func() bool {
			return c.cancelActiveRun()
		},
		RefreshStatus: func() (string, string) {
			return c.readTUIStatus()
		},
		MentionComplete: func(query string, limit int) ([]string, error) {
			begin := time.Now()
			if c.inputRefs == nil {
				return nil, nil
			}
			candidates, err := c.inputRefs.CompleteFiles(query, limit)
			if c.tuiDiag != nil {
				c.tuiDiag.ObserveMentionLatency(time.Since(begin))
			}
			return candidates, err
		},
		SkillComplete: func(query string, limit int) ([]string, error) {
			discovered := skills.DiscoverMeta(c.skillDirs)
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
			return c.completeSlashArgCandidates(command, query, limit)
		},
		PasteClipboardImage: func() (int, string, error) {
			return c.pasteClipboardImage()
		},
		ClearAttachments: func() int {
			return c.clearPendingAttachments()
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

	options := []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	}
	program := tea.NewProgram(model, options...)
	sender.set(func(msg any) { program.Send(msg) })

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		for range sigCh {
			if c.cancelActiveRun() {
				sender.Send(tuievents.SetHintMsg{Hint: "interrupt requested"})
				continue
			}
			sender.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
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
	if cw > 0 {
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
	} else if pt > 0 {
		contextStr = formatTokenCount(pt)
	} else {
		contextStr = "0"
	}
	return modelLabel, contextStr
}

func (c *cliConsole) statusReasoningLevelLabel() string {
	if c == nil {
		return ""
	}
	if normalizeReasoningSelection(c.thinkingMode) == "off" {
		return ""
	}
	level := normalizeReasoningLevel(c.reasoningEffort)
	if level == "" || level == "none" {
		return ""
	}
	return level
}

func (c *cliConsole) completeResumeCandidates(query string, limit int) ([]tuiapp.ResumeCandidate, error) {
	if c == nil || c.sessionIndex == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	records, err := c.sessionIndex.ListWorkspaceSessionsPage(c.workspace.Key, 1, 200)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.ResumeCandidate, 0, limit)
	for _, rec := range records {
		sid := strings.TrimSpace(rec.SessionID)
		if sid == "" || sid == c.sessionID {
			continue
		}
		prompt := strings.TrimSpace(rec.LastUserMessage)
		if prompt == "" {
			prompt = "-"
		}
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
			SessionID: sid,
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
	if c == nil {
		return nil, nil
	}
	rawCmd := strings.TrimSpace(command)
	cmd := strings.ToLower(rawCmd)
	switch {
	case strings.HasPrefix(cmd, "model-reasoning:"):
		alias, ok := parseModelReasoningPayload(rawCmd)
		if !ok {
			return nil, nil
		}
		return c.completeModelReasoningCandidates(alias, query, limit), nil
	case strings.HasPrefix(cmd, "connect-model:"):
		payload := strings.TrimPrefix(rawCmd, "connect-model:")
		provider, baseURL, timeoutSeconds, apiKey, hasRemoteContext := parseConnectModelPayload(payload)
		if hasRemoteContext {
			return c.completeConnectModelCandidatesRemote(provider, baseURL, timeoutSeconds, apiKey, query, limit), nil
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
		return c.completeModelCandidates(query, limit), nil
	case "sandbox":
		return c.completeSandboxCandidates(query, limit), nil
	case "connect":
		return c.completeConnectCandidates(query, limit), nil
	case "permission":
		return c.completePermissionCandidates(query, limit), nil
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
		aliases = append(aliases, c.configStore.ConfiguredModelRefs()...)
	}
	if len(aliases) == 0 && c.modelFactory != nil {
		aliases = append(aliases, c.modelFactory.ListModels()...)
	}
	if len(aliases) == 0 {
		return nil
	}

	byKey := map[string]item{}
	for _, alias := range aliases {
		parsed := parse(alias)
		if parsed.alias == "" {
			continue
		}
		key := parsed.alias
		if parsed.provider != "" && parsed.model != "" {
			key = parsed.provider + "/" + strings.ToLower(parsed.model)
		}
		prev, exists := byKey[key]
		if !exists {
			byKey[key] = parsed
			continue
		}
		// Prefer canonical provider/model refs when multiple aliases map to one model.
		if strings.Contains(parsed.alias, "/") && !strings.Contains(prev.alias, "/") {
			byKey[key] = parsed
		}
	}

	items := make([]item, 0, len(byKey))
	for _, one := range byKey {
		items = append(items, one)
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
		if one.provider != "" && one.model != "" && one.alias != one.provider+"/"+strings.ToLower(one.model) {
			display = fmt.Sprintf("%s/%s (%s)", one.provider, one.model, one.alias)
		}
		if q != "" {
			text := strings.ToLower(display + " " + one.alias + " " + one.provider + " " + one.model)
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
	order := make([]string, 0, 4)
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
	appendType(platformDefaultSandboxType())
	appendType("bwrap")
	appendType("seatbelt")

	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, minInt(limit, len(order)))
	for _, one := range order {
		if q != "" && !strings.Contains(one, q) {
			continue
		}
		out = append(out, tuiapp.SlashArgCandidate{Value: one, Display: one})
		if len(out) >= limit {
			break
		}
	}
	return out
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

func (c *cliConsole) completePermissionCandidates(query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
	candidates := []string{"default", "full_control"}
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]tuiapp.SlashArgCandidate, 0, len(candidates))
	for _, one := range candidates {
		if q != "" && !strings.Contains(one, q) {
			continue
		}
		out = append(out, tuiapp.SlashArgCandidate{Value: one, Display: one})
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
	ctx := c.baseCtx
	if ctx == nil {
		ctx = context.Background()
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
	maxOutputTokens = 4096
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
		reasoningLevels = normalizeReasoningLevels(caps.ReasoningEfforts)
		if len(reasoningLevels) == 0 && !caps.SupportsReasoning {
			reasoningLevels = []string{"none"}
		}
	}
	return contextWindowTokens, maxOutputTokens, reasoningLevels
}

func (c *cliConsole) completeConnectContextCandidates(payload string, query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
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

func (c *cliConsole) completeConnectMaxOutputCandidates(payload string, query string, limit int) []tuiapp.SlashArgCandidate {
	if limit <= 0 {
		limit = 20
	}
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
	return []tuiapp.WizardDef{
		buildModelWizard(),
	}
}

func buildModelWizard() tuiapp.WizardDef {
	return tuiapp.WizardDef{
		Command: "model",
		Steps: []tuiapp.WizardStepDef{
			{
				Key:       "alias",
				HintLabel: "/model",
				CompletionCommand: func(_ map[string]string) string {
					return "model"
				},
			},
			{
				Key:          "reasoning",
				HintLabel:    "/model reasoning",
				FreeformHint: "/model reasoning: type option and press enter",
				CompletionCommand: func(s map[string]string) string {
					return "model-reasoning:" + url.QueryEscape(strings.ToLower(strings.TrimSpace(s["alias"])))
				},
			},
		},
		BuildExecLine: func(s map[string]string) string {
			return "/model " + s["alias"] + " " + s["reasoning"]
		},
	}
}
