package tuiapp

// driver_bridge.go bridges the adapter runtime.Driver interface into the legacy
// Config callback fields. This is the key migration adapter.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	appgateway "github.com/OnslaughtSnail/caelis/gateway"
	tuiadapterruntime "github.com/OnslaughtSnail/caelis/gateway/adapter/tui/runtime"
)

// ProgramSender is set after the tea.Program is created so that the
// ExecuteLine goroutine can send intermediate TUI messages.
type ProgramSender struct {
	Send func(tea.Msg)
}

// ConfigFromDriver populates legacy Config callbacks from an adapter runtime.Driver.
// sender must be non-nil; its Send field is populated after Program creation
// but before the user can trigger ExecuteLine.
func ConfigFromDriver(driver tuiadapterruntime.Driver, sender *ProgramSender, base Config) Config {
	base.Driver = driver

	if base.ExecuteLine == nil {
		base.ExecuteLine = func(sub Submission) TaskResultMsg {
			return executeLineViaDriver(driver, sender, sub)
		}
	}

	if base.RefreshStatus == nil {
		base.RefreshStatus = func() (string, string) {
			status, err := driver.Status(context.Background())
			if err != nil {
				return "not configured", ""
			}
			return strings.TrimSpace(status.Model), formatPromptTokenStatus(status.PromptTokens)
		}
	}

	if base.RefreshWorkspace == nil {
		base.RefreshWorkspace = func() string {
			return driver.WorkspaceDir()
		}
	}

	if base.MentionComplete == nil {
		base.MentionComplete = func(query string, limit int) ([]string, error) {
			return driver.CompleteMention(context.Background(), query, limit)
		}
	}

	if base.FileComplete == nil {
		base.FileComplete = func(query string, limit int) ([]string, error) {
			return driver.CompleteFile(context.Background(), query, limit)
		}
	}

	if base.SkillComplete == nil {
		base.SkillComplete = func(query string, limit int) ([]string, error) {
			return driver.CompleteSkill(context.Background(), query, limit)
		}
	}

	if base.ResumeComplete == nil {
		base.ResumeComplete = func(query string, limit int) ([]ResumeCandidate, error) {
			candidates, err := driver.CompleteResume(context.Background(), query, limit)
			if err != nil {
				return nil, err
			}
			out := make([]ResumeCandidate, len(candidates))
			for i, c := range candidates {
				out[i] = ResumeCandidate{
					SessionID: c.SessionID,
					Prompt:    c.Prompt,
					Age:       c.Age,
				}
			}
			return out, nil
		}
	}

	if base.SlashArgComplete == nil {
		base.SlashArgComplete = func(command string, query string, limit int) ([]SlashArgCandidate, error) {
			candidates, err := driver.CompleteSlashArg(context.Background(), command, query, limit)
			if err != nil {
				return nil, err
			}
			out := make([]SlashArgCandidate, len(candidates))
			for i, c := range candidates {
				out[i] = SlashArgCandidate{
					Value:   c.Value,
					Display: c.Display,
					Detail:  c.Detail,
					NoAuth:  c.NoAuth,
				}
			}
			return out, nil
		}
	}

	if base.CancelRunning == nil {
		base.CancelRunning = func() bool {
			err := driver.Interrupt(context.Background())
			return err == nil
		}
	}

	if base.ToggleMode == nil {
		base.ToggleMode = func() (string, error) {
			status, err := driver.CycleSessionMode(context.Background())
			if err != nil {
				return "", err
			}
			switch strings.ToLower(strings.TrimSpace(status.SessionMode)) {
			case "plan":
				return "plan mode enabled", nil
			case "full_access":
				return "full access mode enabled", nil
			default:
				return "default mode enabled", nil
			}
		}
	}

	if base.ReadClipboardText == nil {
		base.ReadClipboardText = defaultReadClipboardText
	}

	if base.WriteClipboardText == nil {
		base.WriteClipboardText = defaultWriteClipboardText
	}

	return base
}

// ---------------------------------------------------------------------------
// ExecuteLine: the single submission entry point
// ---------------------------------------------------------------------------

func executeLineViaDriver(driver tuiadapterruntime.Driver, sender *ProgramSender, sub Submission) TaskResultMsg {
	text := strings.TrimSpace(sub.Text)

	// Slash command dispatch.
	if strings.HasPrefix(text, "/") {
		return dispatchSlashCommand(driver, sender, text)
	}

	// Normal submission → Driver.Submit → streaming events.
	ctx := context.Background()
	turn, err := driver.Submit(ctx, tuiadapterruntime.Submission{
		Text:        sub.Text,
		DisplayText: "",
		Mode:        tuiadapterruntime.SubmissionMode(sub.Mode),
		Attachments: convertAttachments(sub.Attachments),
	})
	if err != nil {
		return TaskResultMsg{Err: fmt.Errorf("submit: %w", err)}
	}
	if turn == nil {
		return TaskResultMsg{SuppressTurnDivider: true}
	}
	defer turn.Close()

	send := sender.Send
	for env := range turn.Events() {
		if send == nil {
			continue
		}
		send(env)
		if env.Event.Kind == appgateway.EventKindApprovalRequested {
			sendApprovalPrompt(turn, env.Event.ApprovalPayload, send)
		}
	}

	return TaskResultMsg{}
}

// ---------------------------------------------------------------------------
// Slash command dispatch
// ---------------------------------------------------------------------------

func dispatchSlashCommand(driver tuiadapterruntime.Driver, sender *ProgramSender, text string) TaskResultMsg {
	cmd, args := splitSlash(text)
	send := sender.Send

	switch cmd {
	case "help":
		return slashHelp(send)
	case "new":
		return slashNew(driver, send)
	case "resume":
		return slashResume(driver, send, args)
	case "status":
		return slashStatus(driver, send)
	case "connect":
		return slashConnect(driver, send, args)
	case "model":
		return slashModel(driver, send, args)
	case "sandbox":
		return slashSandbox(driver, send, args)
	case "compact":
		return slashCompact(driver, send, args)
	case "exit", "quit":
		return TaskResultMsg{ExitNow: true}
	default:
		sendNotice(send, fmt.Sprintf("unknown command: /%s", cmd))
		return TaskResultMsg{SuppressTurnDivider: true}
	}
}

func slashHelp(send func(tea.Msg)) TaskResultMsg {
	lines := []string{
		"available commands:",
		"  /connect",
		"  /model use|del <alias>",
		"  /new",
		"  /resume [session-id]",
		"  /sandbox [auto|seatbelt|bwrap|landlock]",
		"  /status",
		"  /exit",
		"  /quit",
	}
	sendNotice(send, strings.Join(lines, "\n"))
	return TaskResultMsg{SuppressTurnDivider: true}
}

func slashNew(driver tuiadapterruntime.Driver, send func(tea.Msg)) TaskResultMsg {
	ctx := context.Background()
	session, err := driver.NewSession(ctx)
	if err != nil {
		return TaskResultMsg{Err: fmt.Errorf("new session: %w", err)}
	}
	if send != nil {
		send(ClearHistoryMsg{})
	}
	sendNotice(send, fmt.Sprintf("new session: %s", session.SessionID))
	refreshStatusViaSend(driver, send)
	return TaskResultMsg{SuppressTurnDivider: true}
}

func slashResume(driver tuiadapterruntime.Driver, send func(tea.Msg), args string) TaskResultMsg {
	ctx := context.Background()
	sessionID := strings.TrimSpace(args)
	if sessionID == "" {
		// List available sessions.
		candidates, err := driver.ListSessions(ctx, 10)
		if err != nil {
			return TaskResultMsg{Err: fmt.Errorf("list sessions: %w", err)}
		}
		if len(candidates) == 0 {
			sendNotice(send, "no sessions available to resume")
			return TaskResultMsg{SuppressTurnDivider: true}
		}
		var lines []string
		lines = append(lines, "available sessions:")
		for _, c := range candidates {
			line := fmt.Sprintf("  %s", c.SessionID)
			if c.Prompt != "" {
				line += fmt.Sprintf("  %s", c.Prompt)
			}
			if c.Age != "" {
				line += fmt.Sprintf("  (%s)", c.Age)
			}
			lines = append(lines, line)
		}
		sendNotice(send, strings.Join(lines, "\n"))
		return TaskResultMsg{SuppressTurnDivider: true}
	}

	// Resume specific session.
	session, err := driver.ResumeSession(ctx, sessionID)
	if err != nil {
		return TaskResultMsg{Err: fmt.Errorf("resume session: %w", err)}
	}
	if send != nil {
		send(ClearHistoryMsg{})
	}
	sendNotice(send, fmt.Sprintf("resumed session: %s", session.SessionID))

	// Replay historical events into transcript.
	events, err := driver.ReplayEvents(ctx)
	if err != nil {
		sendNotice(send, fmt.Sprintf("warning: replay failed: %v", err))
	} else if len(events) > 0 {
		for _, env := range events {
			if send != nil {
				send(env)
			}
		}
		sendNotice(send, fmt.Sprintf("replayed %d events", len(events)))
	}

	refreshStatusViaSend(driver, send)
	return TaskResultMsg{SuppressTurnDivider: true}
}

func slashStatus(driver tuiadapterruntime.Driver, send func(tea.Msg)) TaskResultMsg {
	ctx := context.Background()
	status, err := driver.Status(ctx)
	if err != nil {
		return TaskResultMsg{Err: fmt.Errorf("status: %w", err)}
	}
	var lines []string
	lines = append(lines, "status:")
	if status.SessionID != "" {
		lines = append(lines, fmt.Sprintf("  session:   %s", status.SessionID))
	}
	if status.Model != "" {
		lines = append(lines, fmt.Sprintf("  model:     %s", status.Model))
	}
	if status.Workspace != "" {
		lines = append(lines, fmt.Sprintf("  workspace: %s", status.Workspace))
	}
	if status.ModeLabel != "" {
		lines = append(lines, fmt.Sprintf("  mode:      %s", status.ModeLabel))
	}
	if status.SandboxType != "" {
		lines = append(lines, fmt.Sprintf("  sandbox:   %s", status.SandboxType))
	}
	if status.Route != "" {
		lines = append(lines, fmt.Sprintf("  route:     %s", status.Route))
	}
	if status.FallbackReason != "" {
		lines = append(lines, fmt.Sprintf("  fallback:  %s", status.FallbackReason))
	}
	if status.Surface != "" {
		lines = append(lines, fmt.Sprintf("  surface:   %s", status.Surface))
	}
	sendNotice(send, strings.Join(lines, "\n"))
	return TaskResultMsg{SuppressTurnDivider: true}
}

func slashConnect(driver tuiadapterruntime.Driver, send func(tea.Msg), args string) TaskResultMsg {
	ctx := context.Background()
	cfg := parseConnectArgs(args)
	if cfg.Provider == "" || cfg.Model == "" {
		sendNotice(send, "usage: /connect")
		return TaskResultMsg{SuppressTurnDivider: true}
	}
	status, err := driver.Connect(ctx, cfg)
	if err != nil {
		return TaskResultMsg{Err: fmt.Errorf("connect: %w", err)}
	}
	sendNotice(send, fmt.Sprintf("connected: %s", status.Model))
	sendStatusUpdate(send, status)
	return TaskResultMsg{SuppressTurnDivider: true}
}

func slashModel(driver tuiadapterruntime.Driver, send func(tea.Msg), args string) TaskResultMsg {
	ctx := context.Background()
	sub, rest := splitFirst(strings.TrimSpace(args))
	switch sub {
	case "use":
		alias := strings.TrimSpace(rest)
		if alias == "" {
			sendNotice(send, "usage: /model use <alias>")
			return TaskResultMsg{SuppressTurnDivider: true}
		}
		status, err := driver.UseModel(ctx, alias)
		if err != nil {
			return TaskResultMsg{Err: fmt.Errorf("model use: %w", err)}
		}
		sendNotice(send, fmt.Sprintf("model switched to: %s", status.Model))
		sendStatusUpdate(send, status)
	case "del", "delete", "rm":
		alias := strings.TrimSpace(rest)
		if alias == "" {
			sendNotice(send, "usage: /model del <alias>")
			return TaskResultMsg{SuppressTurnDivider: true}
		}
		if err := driver.DeleteModel(ctx, alias); err != nil {
			return TaskResultMsg{Err: fmt.Errorf("model delete: %w", err)}
		}
		sendNotice(send, fmt.Sprintf("model deleted: %s", alias))
		refreshStatusViaSend(driver, send)
	default:
		sendNotice(send, "usage: /model use|del <alias>")
	}
	return TaskResultMsg{SuppressTurnDivider: true}
}

func slashSandbox(driver tuiadapterruntime.Driver, send func(tea.Msg), args string) TaskResultMsg {
	ctx := context.Background()
	backend := strings.TrimSpace(args)
	if backend == "" {
		status, err := driver.Status(ctx)
		if err != nil {
			return TaskResultMsg{Err: fmt.Errorf("sandbox: %w", err)}
		}
		lines := []string{
			fmt.Sprintf("sandbox backend: %s", status.SandboxType),
			fmt.Sprintf("session mode: %s", status.SessionMode),
			fmt.Sprintf("route: %s", status.Route),
		}
		if status.FallbackReason != "" {
			lines = append(lines, fmt.Sprintf("fallback: %s", status.FallbackReason))
		}
		sendNotice(send, strings.Join(lines, "\n"))
		return TaskResultMsg{SuppressTurnDivider: true}
	}
	status, err := driver.SetSandboxBackend(ctx, backend)
	if err != nil {
		return TaskResultMsg{Err: fmt.Errorf("sandbox: %w", err)}
	}
	if strings.EqualFold(strings.TrimSpace(status.SessionMode), "full_access") {
		sendNotice(send, fmt.Sprintf("sandbox backend updated: %s (will apply in default/plan mode)", status.SandboxType))
	} else {
		sendNotice(send, fmt.Sprintf("sandbox backend: %s", status.SandboxType))
	}
	sendStatusUpdate(send, status)
	return TaskResultMsg{SuppressTurnDivider: true}
}

func slashCompact(driver tuiadapterruntime.Driver, send func(tea.Msg), args string) TaskResultMsg {
	ctx := context.Background()
	note := strings.TrimSpace(args)
	if err := driver.Compact(ctx, note); err != nil {
		return TaskResultMsg{Err: fmt.Errorf("compact: %w", err)}
	}
	sendNotice(send, "compaction completed")
	return TaskResultMsg{SuppressTurnDivider: true}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func sendNotice(send func(tea.Msg), text string) {
	if send != nil {
		send(LogChunkMsg{Chunk: text + "\n"})
	}
}

func sendStatusUpdate(send func(tea.Msg), status tuiadapterruntime.StatusSnapshot) {
	if send != nil {
		send(SetStatusMsg{
			Workspace: status.Workspace,
			Model:     status.Model,
			Context:   formatPromptTokenStatus(status.PromptTokens),
		})
	}
}

func formatPromptTokenStatus(promptTokens int) string {
	if promptTokens <= 0 {
		return ""
	}
	return fmt.Sprintf("%d prompt tok", promptTokens)
}

func refreshStatusViaSend(driver tuiadapterruntime.Driver, send func(tea.Msg)) {
	status, err := driver.Status(context.Background())
	if err != nil {
		return
	}
	sendStatusUpdate(send, status)
}

func sendApprovalPrompt(turn tuiadapterruntime.Turn, req *appgateway.ApprovalPayload, send func(tea.Msg)) {
	if turn == nil || req == nil || send == nil {
		return
	}
	responses := make(chan PromptResponse, 1)
	send(approvalToPromptRequest(req, responses))
	go awaitApprovalPrompt(turn, req, responses, send)
}

func awaitApprovalPrompt(turn tuiadapterruntime.Turn, req *appgateway.ApprovalPayload, responses <-chan PromptResponse, send func(tea.Msg)) {
	response, ok := <-responses
	if !ok {
		return
	}
	decision := approvalDecisionFromPrompt(req, response)
	if err := turn.Submit(context.Background(), appgateway.SubmitRequest{
		Kind:     appgateway.SubmissionKindApproval,
		Approval: &decision,
	}); err != nil {
		sendNotice(send, fmt.Sprintf("approval submit failed: %v", err))
	}
}

func approvalDecisionFromPrompt(req *appgateway.ApprovalPayload, response PromptResponse) appgateway.ApprovalDecision {
	selected := strings.TrimSpace(response.Line)
	if response.Err != nil || selected == "" {
		return rejectionApprovalDecision(req)
	}
	if req != nil {
		for _, opt := range req.Options {
			if strings.TrimSpace(opt.ID) != selected {
				continue
			}
			return appgateway.ApprovalDecision{
				Outcome:  "selected",
				OptionID: selected,
				Approved: approvalOptionAllows(opt.Kind, opt.Name, opt.ID),
			}
		}
	}
	switch strings.ToLower(selected) {
	case "approve", "allow", "yes", "y":
		return appgateway.ApprovalDecision{Outcome: "approved", Approved: true}
	default:
		return rejectionApprovalDecision(req)
	}
}

func rejectionApprovalDecision(req *appgateway.ApprovalPayload) appgateway.ApprovalDecision {
	if req != nil {
		for _, opt := range req.Options {
			if approvalOptionAllows(opt.Kind, opt.Name, opt.ID) {
				continue
			}
			return appgateway.ApprovalDecision{
				Outcome:  "selected",
				OptionID: strings.TrimSpace(opt.ID),
				Approved: false,
			}
		}
	}
	return appgateway.ApprovalDecision{Outcome: "rejected", Approved: false}
}

func approvalOptionAllows(kind string, name string, id string) bool {
	parts := []string{strings.ToLower(strings.TrimSpace(kind)), strings.ToLower(strings.TrimSpace(name)), strings.ToLower(strings.TrimSpace(id))}
	for _, part := range parts {
		if strings.HasPrefix(part, "allow") || strings.HasPrefix(part, "approve") {
			return true
		}
	}
	return false
}

func splitSlash(text string) (cmd, args string) {
	text = strings.TrimPrefix(strings.TrimSpace(text), "/")
	cmd, args, _ = strings.Cut(text, " ")
	cmd = strings.TrimSpace(strings.ToLower(cmd))
	args = strings.TrimSpace(args)
	return
}

func splitFirst(text string) (first, rest string) {
	first, rest, _ = strings.Cut(strings.TrimSpace(text), " ")
	first = strings.TrimSpace(first)
	rest = strings.TrimSpace(rest)
	return
}

func parseConnectArgs(args string) tuiadapterruntime.ConnectConfig {
	parts := strings.Fields(args)
	cfg := tuiadapterruntime.ConnectConfig{}
	if len(parts) >= 1 {
		cfg.Provider = parts[0]
	}
	if len(parts) >= 2 {
		cfg.Model = parts[1]
	}
	if len(parts) >= 3 {
		cfg.BaseURL = dashAsEmpty(parts[2])
	}
	if len(parts) >= 4 {
		if timeout, err := strconv.Atoi(dashAsEmpty(parts[3])); err == nil {
			cfg.TimeoutSeconds = timeout
		}
	}
	if len(parts) >= 5 {
		cfg.APIKey = dashAsEmpty(parts[4])
	}
	if len(parts) >= 6 {
		if contextWindow, err := strconv.Atoi(dashAsEmpty(parts[5])); err == nil {
			cfg.ContextWindowTokens = contextWindow
		}
	}
	if len(parts) >= 7 {
		if maxOutput, err := strconv.Atoi(dashAsEmpty(parts[6])); err == nil {
			cfg.MaxOutputTokens = maxOutput
		}
	}
	if len(parts) >= 8 {
		cfg.ReasoningLevels = parseReasoningLevels(parts[7])
	}
	if len(parts) == 4 && cfg.TimeoutSeconds == 0 && cfg.APIKey == "" {
		cfg.TokenEnv = dashAsEmpty(parts[3])
	}
	return cfg
}

func dashAsEmpty(value string) string {
	value = strings.TrimSpace(value)
	if value == "-" {
		return ""
	}
	return value
}

func parseReasoningLevels(raw string) []string {
	raw = dashAsEmpty(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.ToLower(strings.TrimSpace(part))
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func convertAttachments(items []Attachment) []tuiadapterruntime.Attachment {
	if len(items) == 0 {
		return nil
	}
	out := make([]tuiadapterruntime.Attachment, len(items))
	for i, item := range items {
		out[i] = tuiadapterruntime.Attachment{
			Name:   item.Name,
			Offset: item.Offset,
		}
	}
	return out
}

// Ensure gateway import is used.
var _ appgateway.EventEnvelope
