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

type streamDriver interface {
	SubscribeStream(context.Context, appgateway.EventEnvelope) (<-chan appgateway.EventEnvelope, bool)
}

type subagentWaitDriver interface {
	WaitSubagent(context.Context, string) (tuiadapterruntime.SubagentSnapshot, error)
}

type subagentStreamDriver interface {
	SubscribeSubagentStream(context.Context, string) (<-chan tuiadapterruntime.SubagentStreamFrame, bool)
}

// ConfigFromDriver populates legacy Config callbacks from an adapter runtime.Driver.
// sender must be non-nil; its Send field is populated after Program creation
// but before the user can trigger ExecuteLine.
func ConfigFromDriver(driver tuiadapterruntime.Driver, sender *ProgramSender, base Config) Config {
	base.Driver = driver
	base.Commands = appendAgentSlashCommands(driver, base.Commands)

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
			return strings.TrimSpace(status.Model), formatContextUsageStatus(status.TotalTokens, status.ContextWindowTokens)
		}
	}

	if base.RefreshWorkspace == nil {
		base.RefreshWorkspace = func() string {
			return driver.WorkspaceDir()
		}
	}

	if base.MentionComplete == nil {
		base.MentionComplete = func(query string, limit int) ([]CompletionCandidate, error) {
			candidates, err := driver.CompleteMention(context.Background(), query, limit)
			if err != nil {
				return nil, err
			}
			out := make([]CompletionCandidate, len(candidates))
			for i, c := range candidates {
				out[i] = CompletionCandidate{
					Value:   c.Value,
					Display: c.Display,
					Detail:  c.Detail,
					Path:    c.Path,
				}
			}
			return out, nil
		}
	}

	if base.FileComplete == nil {
		base.FileComplete = func(query string, limit int) ([]CompletionCandidate, error) {
			candidates, err := driver.CompleteFile(context.Background(), query, limit)
			if err != nil {
				return nil, err
			}
			out := make([]CompletionCandidate, len(candidates))
			for i, c := range candidates {
				out[i] = CompletionCandidate{
					Value:   c.Value,
					Display: c.Display,
					Detail:  c.Detail,
					Path:    c.Path,
				}
			}
			return out, nil
		}
	}

	if base.SkillComplete == nil {
		base.SkillComplete = func(query string, limit int) ([]CompletionCandidate, error) {
			candidates, err := driver.CompleteSkill(context.Background(), query, limit)
			if err != nil {
				return nil, err
			}
			out := make([]CompletionCandidate, len(candidates))
			for i, c := range candidates {
				out[i] = CompletionCandidate{
					Value:   c.Value,
					Display: c.Display,
					Detail:  c.Detail,
					Path:    c.Path,
				}
			}
			return out, nil
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
					Title:     c.Title,
					Prompt:    c.Prompt,
					Model:     c.Model,
					Workspace: c.Workspace,
					Age:       c.Age,
					UpdatedAt: c.UpdatedAt,
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
	if strings.HasPrefix(text, "@") {
		return dispatchMentionCommand(driver, sender, text)
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
		return TaskResultMsg{Err: friendlyCommandError("submit", err)}
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
		startTerminalStreamForwarder(ctx, driver, env, send)
		if env.Event.Kind == appgateway.EventKindApprovalRequested {
			sendApprovalPrompt(turn, env.Event.ApprovalPayload, send)
		}
	}

	return TaskResultMsg{}
}

func startTerminalStreamForwarder(ctx context.Context, driver tuiadapterruntime.Driver, env appgateway.EventEnvelope, send func(tea.Msg)) {
	if send == nil {
		return
	}
	streamer, ok := driver.(streamDriver)
	if !ok {
		return
	}
	events, ok := streamer.SubscribeStream(ctx, env)
	if !ok || events == nil {
		return
	}
	go func() {
		for terminalEnv := range events {
			send(terminalEnv)
		}
	}()
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
	case "agent":
		return slashAgent(driver, send, args)
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
		return slashDynamicAgent(driver, send, cmd, args)
	}
}

func slashHelp(send func(tea.Msg)) TaskResultMsg {
	sendNotice(send, defaultHelpText())
	return TaskResultMsg{SuppressTurnDivider: true}
}

func slashDynamicAgent(driver tuiadapterruntime.Driver, send func(tea.Msg), agent string, prompt string) TaskResultMsg {
	agent = strings.ToLower(strings.TrimSpace(agent))
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		if isRegisteredAgentCommand(driver, agent) {
			sendNotice(send, fmt.Sprintf("usage: /%s <prompt>", agent))
		} else {
			sendNotice(send, fmt.Sprintf("unknown command: /%s\nrun /help to see supported commands", agent))
		}
		return TaskResultMsg{SuppressTurnDivider: true}
	}
	snapshot, err := driver.StartAgentSubagent(context.Background(), agent, prompt)
	if err != nil {
		return TaskResultMsg{Err: friendlyCommandError("/"+agent, err)}
	}
	sendDynamicSubagentStarted(send, snapshot, true)
	startDynamicSubagentOutputBridge(driver, send, snapshot)
	return TaskResultMsg{SuppressTurnDivider: true}
}

func isRegisteredAgentCommand(driver tuiadapterruntime.Driver, agent string) bool {
	agent = strings.ToLower(strings.TrimSpace(agent))
	if agent == "" {
		return false
	}
	agents, err := driver.ListAgents(context.Background(), 200)
	if err != nil {
		return false
	}
	for _, item := range agents {
		if strings.EqualFold(strings.TrimSpace(item.Name), agent) {
			return true
		}
	}
	return false
}

func dispatchMentionCommand(driver tuiadapterruntime.Driver, sender *ProgramSender, text string) TaskResultMsg {
	var send func(tea.Msg)
	if sender != nil {
		send = sender.Send
	}
	handle, prompt := splitFirst(strings.TrimSpace(text))
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if handle == "" || strings.TrimSpace(prompt) == "" {
		sendNotice(send, "usage: @handle <prompt>")
		return TaskResultMsg{SuppressTurnDivider: true}
	}
	snapshot, err := driver.ContinueSubagent(context.Background(), handle, prompt)
	if err != nil {
		return TaskResultMsg{Err: friendlyCommandError("@"+handle, err)}
	}
	sendDynamicSubagentStarted(send, snapshot, false)
	startDynamicSubagentOutputBridge(driver, send, snapshot)
	return TaskResultMsg{SuppressTurnDivider: true}
}

func slashAgent(driver tuiadapterruntime.Driver, send func(tea.Msg), args string) TaskResultMsg {
	ctx := context.Background()
	sub, rest := splitFirst(strings.TrimSpace(args))
	switch sub {
	case "", "help":
		sendNotice(send, agentHelpText())
		return TaskResultMsg{SuppressTurnDivider: true}
	case "list":
		agents, err := driver.ListAgents(ctx, 20)
		if err != nil {
			return TaskResultMsg{Err: friendlyCommandError("agent list", err)}
		}
		status, _ := driver.AgentStatus(ctx)
		sendNotice(send, formatAgentList(agents, status))
		return TaskResultMsg{SuppressTurnDivider: true}
	case "status":
		sendNotice(send, "usage: /agent list | add <builtin> | use <agent|local> | remove <agent>")
		return TaskResultMsg{SuppressTurnDivider: true}
	case "add":
		target := strings.TrimSpace(rest)
		if target == "" {
			sendNotice(send, "usage: /agent add <name>")
			return TaskResultMsg{SuppressTurnDivider: true}
		}
		status, err := driver.AddAgent(ctx, target)
		if err != nil {
			return TaskResultMsg{Err: friendlyCommandError("agent add", err)}
		}
		sendNotice(send, fmt.Sprintf("agent registered: %s", target))
		sendNotice(send, formatAgentStatusSnapshot(status))
		refreshAgentSlashCommandsViaSend(driver, send)
		return TaskResultMsg{SuppressTurnDivider: true}
	case "remove":
		target := strings.TrimSpace(rest)
		if target == "" {
			sendNotice(send, "usage: /agent remove <agent>\nrun /agent list to inspect registered agents")
			return TaskResultMsg{SuppressTurnDivider: true}
		}
		status, err := driver.RemoveAgent(ctx, target)
		if err != nil {
			return TaskResultMsg{Err: friendlyCommandError("agent remove", err)}
		}
		sendNotice(send, fmt.Sprintf("agent unregistered: %s", target))
		sendNotice(send, formatAgentStatusSnapshot(status))
		refreshAgentSlashCommandsViaSend(driver, send)
		return TaskResultMsg{SuppressTurnDivider: true}
	case "use":
		target := strings.TrimSpace(rest)
		if target == "" {
			sendNotice(send, "usage: /agent use <agent|local>\nrun /agent list for registered agents")
			return TaskResultMsg{SuppressTurnDivider: true}
		}
		status, err := driver.HandoffAgent(ctx, target)
		if err != nil {
			return TaskResultMsg{Err: friendlyCommandError("agent use", err)}
		}
		sendNotice(send, fmt.Sprintf("controller switched: %s", target))
		sendNotice(send, formatAgentStatusSnapshot(status))
		return TaskResultMsg{SuppressTurnDivider: true}
	default:
		sendNotice(send, "usage: /agent list | add <builtin> | use <agent|local> | remove <agent>")
		return TaskResultMsg{SuppressTurnDivider: true}
	}
}

func slashNew(driver tuiadapterruntime.Driver, send func(tea.Msg)) TaskResultMsg {
	ctx := context.Background()
	session, err := driver.NewSession(ctx)
	if err != nil {
		return TaskResultMsg{Err: friendlyCommandError("new session", err)}
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
			return TaskResultMsg{Err: friendlyCommandError("list sessions", err)}
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
	if _, err := driver.ResumeSession(ctx, sessionID); err != nil {
		return TaskResultMsg{Err: friendlyCommandError("resume session", err)}
	}
	if send != nil {
		send(ClearHistoryMsg{})
	}

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
	}

	refreshStatusViaSend(driver, send)
	return TaskResultMsg{SuppressTurnDivider: true}
}

func slashStatus(driver tuiadapterruntime.Driver, send func(tea.Msg)) TaskResultMsg {
	ctx := context.Background()
	status, err := driver.Status(ctx)
	if err != nil {
		return TaskResultMsg{Err: friendlyCommandError("status", err)}
	}
	sendNotice(send, formatStatusSnapshot(status))
	return TaskResultMsg{SuppressTurnDivider: true}
}

func slashConnect(driver tuiadapterruntime.Driver, send func(tea.Msg), args string) TaskResultMsg {
	ctx := context.Background()
	cfg := parseConnectArgs(args)
	if cfg.Provider == "" || cfg.Model == "" {
		sendNotice(send, "usage: /connect\nrun /connect to open the guided setup wizard")
		return TaskResultMsg{SuppressTurnDivider: true}
	}
	if strings.EqualFold(strings.TrimSpace(cfg.Provider), "codefree") {
		sendNotice(send, "opening CodeFree OAuth in your browser and waiting for authentication...")
	}
	status, err := driver.Connect(ctx, cfg)
	if err != nil {
		return TaskResultMsg{Err: friendlyCommandError("connect", err)}
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
			return TaskResultMsg{Err: friendlyCommandError("model use", err)}
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
			return TaskResultMsg{Err: friendlyCommandError("model delete", err)}
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
			return TaskResultMsg{Err: friendlyCommandError("sandbox", err)}
		}
		lines := []string{
			fmt.Sprintf("sandbox requested: %s", firstNonEmpty(strings.TrimSpace(status.SandboxRequestedBackend), "-")),
			fmt.Sprintf("sandbox resolved: %s", firstNonEmpty(strings.TrimSpace(status.SandboxResolvedBackend), firstNonEmpty(strings.TrimSpace(status.SandboxType), "-"))),
			fmt.Sprintf("session mode: %s", firstNonEmpty(strings.TrimSpace(status.SessionMode), "default")),
			fmt.Sprintf("route: %s", firstNonEmpty(strings.TrimSpace(status.Route), "-")),
		}
		if status.FallbackReason != "" {
			lines = append(lines, fmt.Sprintf("fallback: %s", status.FallbackReason))
		}
		if status.HostExecution || status.FullAccessMode {
			lines = append(lines, "warning: commands may execute on the host with reduced isolation")
		}
		sendNotice(send, strings.Join(lines, "\n"))
		return TaskResultMsg{SuppressTurnDivider: true}
	}
	status, err := driver.SetSandboxBackend(ctx, backend)
	if err != nil {
		return TaskResultMsg{Err: friendlyCommandError("sandbox", err)}
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
		return TaskResultMsg{Err: friendlyCommandError("compact", err)}
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

func sendDynamicSubagentStarted(send func(tea.Msg), snapshot tuiadapterruntime.SubagentSnapshot, started bool) {
	if send == nil {
		return
	}
	mention := subagentMention(snapshot)
	if mention == "" {
		return
	}
	agent := strings.TrimSpace(snapshot.Agent)
	if started {
		sendNotice(send, fmt.Sprintf("subagent %s started: %s", mention, agent))
		return
	}
	sendNotice(send, fmt.Sprintf("subagent %s continued: %s", mention, agent))
}

func startDynamicSubagentOutputBridge(driver tuiadapterruntime.Driver, send func(tea.Msg), snapshot tuiadapterruntime.SubagentSnapshot) {
	if send == nil {
		return
	}
	if !snapshot.Running && sendDynamicSubagentSnapshotOutput(send, snapshot) {
		return
	}
	taskID := strings.TrimSpace(snapshot.TaskID)
	if taskID == "" {
		sendDynamicSubagentSnapshotOutput(send, snapshot)
		return
	}
	if streamer, ok := driver.(subagentStreamDriver); ok {
		if frames, ok := streamer.SubscribeSubagentStream(context.Background(), taskID); ok && frames != nil {
			actor := subagentMention(snapshot)
			go func() {
				for frame := range frames {
					if strings.TrimSpace(frame.Text) == "" {
						continue
					}
					send(AssistantStreamMsg{
						Kind:  firstNonEmpty(strings.TrimSpace(frame.Stream), "answer"),
						Actor: actor,
						Text:  frame.Text,
						Final: !frame.Running && frame.Closed,
					})
				}
			}()
			return
		}
	}
	sendDynamicSubagentSnapshotOutput(send, snapshot)
	startDynamicSubagentSnapshotWatcher(driver, send, snapshot)
}

func sendDynamicSubagentSnapshotOutput(send func(tea.Msg), snapshot tuiadapterruntime.SubagentSnapshot) bool {
	if send == nil {
		return false
	}
	output := strings.TrimSpace(firstNonEmpty(snapshot.Result, snapshot.OutputPreview))
	if output == "" {
		return false
	}
	send(AssistantStreamMsg{
		Kind:  "answer",
		Actor: subagentMention(snapshot),
		Text:  output,
		Final: !snapshot.Running,
	})
	return true
}

func startDynamicSubagentSnapshotWatcher(driver tuiadapterruntime.Driver, send func(tea.Msg), snapshot tuiadapterruntime.SubagentSnapshot) {
	if send == nil || !snapshot.Running || strings.TrimSpace(snapshot.TaskID) == "" {
		return
	}
	waiter, ok := driver.(subagentWaitDriver)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(snapshot.TaskID)
	go func() {
		for {
			next, err := waiter.WaitSubagent(context.Background(), taskID)
			if err != nil {
				sendNotice(send, fmt.Sprintf("subagent %s wait failed: %v", taskID, err))
				return
			}
			sendDynamicSubagentSnapshotOutput(send, next)
			if !next.Running {
				return
			}
		}
	}()
}

func subagentMention(snapshot tuiadapterruntime.SubagentSnapshot) string {
	mention := strings.TrimSpace(snapshot.Mention)
	if mention != "" {
		return mention
	}
	if handle := strings.TrimSpace(snapshot.Handle); handle != "" {
		return "@" + strings.TrimPrefix(handle, "@")
	}
	return ""
}

func appendAgentSlashCommands(driver tuiadapterruntime.Driver, commands []string) []string {
	if len(commands) == 0 {
		commands = DefaultCommands()
	}
	out := append([]string(nil), commands...)
	seen := map[string]struct{}{}
	for _, command := range out {
		seen[strings.ToLower(strings.TrimSpace(command))] = struct{}{}
	}
	agents, err := driver.ListAgents(context.Background(), 200)
	if err != nil {
		return out
	}
	for _, agent := range agents {
		name := strings.ToLower(strings.TrimSpace(agent.Name))
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		out = append(out, name)
		seen[name] = struct{}{}
	}
	return out
}

func refreshAgentSlashCommandsViaSend(driver tuiadapterruntime.Driver, send func(tea.Msg)) {
	if send == nil {
		return
	}
	send(SetCommandsMsg{Commands: appendAgentSlashCommands(driver, DefaultCommands())})
}

func sendStatusUpdate(send func(tea.Msg), status tuiadapterruntime.StatusSnapshot) {
	if send != nil {
		send(SetStatusMsg{
			Workspace: status.Workspace,
			Model:     status.Model,
			Context:   formatContextUsageStatus(status.TotalTokens, status.ContextWindowTokens),
		})
	}
}

func formatContextUsageStatus(totalTokens int, contextWindow int) string {
	if contextWindow <= 0 {
		return ""
	}
	if totalTokens < 0 {
		totalTokens = 0
	}
	percent := 0
	if contextWindow > 0 {
		percent = int(float64(totalTokens)*100/float64(contextWindow) + 0.5)
		if percent < 0 {
			percent = 0
		}
	}
	return fmt.Sprintf("%s/%s(%d%%)", formatCompactTokenCount(totalTokens), formatCompactTokenCount(contextWindow), percent)
}

func formatCompactTokenCount(tokens int) string {
	if tokens < 1000 {
		return strconv.Itoa(max(tokens, 0))
	}
	value := float64(tokens) / 1000.0
	text := fmt.Sprintf("%.1fk", value)
	return strings.Replace(text, ".0k", "k", 1)
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
				Outcome:  string(appgateway.ApprovalStatusSelected),
				OptionID: selected,
				Approved: approvalOptionAllows(opt.Kind, opt.Name, opt.ID),
			}
		}
	}
	switch strings.ToLower(selected) {
	case "approve", "allow", "yes", "y":
		return appgateway.ApprovalDecision{Outcome: string(appgateway.ApprovalStatusApproved), Approved: true}
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
				Outcome:  string(appgateway.ApprovalStatusSelected),
				OptionID: strings.TrimSpace(opt.ID),
				Approved: false,
			}
		}
	}
	return appgateway.ApprovalDecision{Outcome: string(appgateway.ApprovalStatusRejected), Approved: false}
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
		secret := dashAsEmpty(parts[4])
		if strings.HasPrefix(strings.ToLower(secret), "env:") {
			cfg.TokenEnv = strings.TrimSpace(secret[len("env:"):])
		} else if strings.HasPrefix(secret, "$") {
			cfg.TokenEnv = strings.TrimSpace(strings.TrimPrefix(secret, "$"))
		} else {
			cfg.APIKey = secret
		}
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
	if len(parts) == 4 && cfg.TimeoutSeconds == 0 && cfg.APIKey == "" && cfg.TokenEnv == "" {
		cfg.TokenEnv = dashAsEmpty(parts[3])
	}
	return cfg
}

func formatStatusSnapshot(status tuiadapterruntime.StatusSnapshot) string {
	lines := []string{"status:"}
	lines = append(lines, fmt.Sprintf("  session:   %s", firstNonEmpty(strings.TrimSpace(status.SessionID), "-")))
	lines = append(lines, fmt.Sprintf("  provider:  %s", firstNonEmpty(strings.TrimSpace(status.Provider), deriveProviderFromAlias(status.Model), "not configured")))
	lines = append(lines, fmt.Sprintf("  model:     %s", firstNonEmpty(strings.TrimSpace(status.ModelName), deriveModelNameFromAlias(status.Model), "not configured")))
	lines = append(lines, fmt.Sprintf("  alias:     %s", firstNonEmpty(strings.TrimSpace(status.Model), "-")))
	lines = append(lines, fmt.Sprintf("  mode:      %s", firstNonEmpty(strings.TrimSpace(status.ModeLabel), "default")))
	lines = append(lines, fmt.Sprintf("  sandbox:   %s", firstNonEmpty(strings.TrimSpace(status.SandboxResolvedBackend), strings.TrimSpace(status.SandboxType), "auto")))
	lines = append(lines, fmt.Sprintf("  route:     %s", firstNonEmpty(strings.TrimSpace(status.Route), "-")))
	lines = append(lines, fmt.Sprintf("  workspace: %s", firstNonEmpty(strings.TrimSpace(status.Workspace), "-")))
	lines = append(lines, fmt.Sprintf("  store:     %s", firstNonEmpty(strings.TrimSpace(status.StoreDir), "-")))
	if usage := formatContextUsageStatus(status.TotalTokens, status.ContextWindowTokens); usage != "" {
		lines = append(lines, fmt.Sprintf("  context:   %s", usage))
	}
	if status.FallbackReason != "" {
		lines = append(lines, "  fallback:  "+strings.TrimSpace(status.FallbackReason))
	}
	if strings.TrimSpace(status.Model) == "" && strings.TrimSpace(status.Provider) == "" && strings.TrimSpace(status.ModelName) == "" {
		lines = append(lines, "next: run /connect to configure a provider and model")
	}
	if status.MissingAPIKey {
		lines = append(lines, "warning: API key is missing; reconnect with a key or use env:YOUR_API_KEY")
	}
	if status.HostExecution || status.FullAccessMode {
		lines = append(lines, "warning: commands may run on the host with reduced sandbox isolation")
	}
	if strings.TrimSpace(status.FallbackReason) != "" {
		lines = append(lines, "warning: requested sandbox backend is unavailable and a fallback is in effect")
	}
	return strings.Join(lines, "\n")
}

func agentHelpText() string {
	lines := []string{
		"/agent commands:",
		"  /agent list          list registered ACP agents and current controller",
		"  /agent add NAME      register a built-in ACP agent",
		"  /agent use NAME      switch the main controller to a registered ACP agent",
		"  /agent use local     return the main controller to the local kernel",
		"  /agent remove NAME   unregister an ACP agent",
	}
	return strings.Join(lines, "\n")
}

func formatAgentCatalog(agents []tuiadapterruntime.AgentCandidate) string {
	if len(agents) == 0 {
		return "no ACP agents are registered\nnext: run /agent add <builtin>"
	}
	lines := []string{"registered ACP agents:"}
	for _, agent := range agents {
		line := "  " + strings.TrimSpace(agent.Name)
		if desc := strings.TrimSpace(agent.Description); desc != "" {
			line += "  " + desc
		}
		lines = append(lines, line)
	}
	lines = append(lines, "next: use /<agent> <prompt> for a child subagent, or /agent use <agent> to switch the main controller")
	return strings.Join(lines, "\n")
}

func formatAgentList(agents []tuiadapterruntime.AgentCandidate, status tuiadapterruntime.AgentStatusSnapshot) string {
	lines := []string{"agent registry:"}
	lines = append(lines, fmt.Sprintf("  controller: %s", firstNonEmpty(strings.TrimSpace(status.ControllerLabel), strings.TrimSpace(status.ControllerKind), "local")))
	if len(agents) == 0 {
		lines = append(lines, "  registered: none")
		lines = append(lines, "next: run /agent add <builtin>")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "  registered:")
	for _, agent := range agents {
		line := "    " + strings.TrimSpace(agent.Name)
		if desc := strings.TrimSpace(agent.Description); desc != "" {
			line += "  " + desc
		}
		lines = append(lines, line)
	}
	lines = append(lines, "next: /<agent> <prompt> starts a child; /agent use <agent> switches the main controller")
	return strings.Join(lines, "\n")
}

func formatAgentStatusSnapshot(status tuiadapterruntime.AgentStatusSnapshot) string {
	lines := []string{"agent status:"}
	lines = append(lines, fmt.Sprintf("  session:     %s", firstNonEmpty(strings.TrimSpace(status.SessionID), "-")))
	lines = append(lines, fmt.Sprintf("  controller:  %s", firstNonEmpty(strings.TrimSpace(status.ControllerLabel), strings.TrimSpace(status.ControllerKind), "local kernel")))
	lines = append(lines, fmt.Sprintf("  kind:        %s", firstNonEmpty(strings.TrimSpace(status.ControllerKind), "kernel")))
	lines = append(lines, fmt.Sprintf("  active turn: %t", status.HasActiveTurn))
	if len(status.Participants) == 0 {
		lines = append(lines, "  participants: none")
	} else {
		lines = append(lines, "  participants:")
		for _, participant := range status.Participants {
			lines = append(lines, fmt.Sprintf("    %s  %s  %s", firstNonEmpty(strings.TrimSpace(participant.ID), "-"), firstNonEmpty(strings.TrimSpace(participant.Label), "-"), strings.TrimSpace(participant.Role)))
		}
	}
	if len(status.AvailableAgents) == 0 {
		lines = append(lines, "next: no ACP agents are configured")
	} else if len(status.Participants) == 0 && strings.TrimSpace(status.ControllerKind) == "" {
		lines = append(lines, "next: run /agent add <builtin> to register an ACP agent")
	} else if len(status.Participants) == 0 {
		lines = append(lines, "next: run /<agent> <prompt> to start a child subagent")
	}
	return strings.Join(lines, "\n")
}

func deriveProviderFromAlias(alias string) string {
	left, _, ok := strings.Cut(strings.TrimSpace(alias), "/")
	if !ok {
		return ""
	}
	return strings.TrimSpace(left)
}

func deriveModelNameFromAlias(alias string) string {
	_, right, ok := strings.Cut(strings.TrimSpace(alias), "/")
	if !ok {
		return ""
	}
	return strings.TrimSpace(right)
}

func friendlyCommandError(action string, err error) error {
	if err == nil {
		return nil
	}
	raw := strings.TrimSpace(err.Error())
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "api key is missing"):
		return fmt.Errorf("%s: API key is missing. Use /connect and paste a key, or enter env:YOUR_API_KEY", action)
	case strings.Contains(lower, "base url is invalid"):
		return fmt.Errorf("%s: base URL is invalid. Use a full URL such as https://api.openai.com/v1", action)
	case strings.Contains(lower, "provider is not supported"), strings.Contains(lower, "unknown provider"):
		return fmt.Errorf("%s: provider is not supported. Run /connect and choose one of the listed providers", action)
	case strings.Contains(lower, "provider and model are required"), strings.Contains(lower, "model is required"):
		return fmt.Errorf("%s: provider or model is not configured. Run /connect to add one", action)
	case strings.Contains(lower, "unknown model alias"):
		return fmt.Errorf("%s: model alias was not found. Run /model and choose a configured alias, or use /connect first", action)
	case strings.Contains(lower, "ambiguous model alias"):
		return fmt.Errorf("%s: model alias is ambiguous. Type more of the alias or pick from /model", action)
	case strings.Contains(lower, "agent name is required"), strings.Contains(lower, "agent ") && (strings.Contains(lower, " is not configured") || strings.Contains(lower, " not found")):
		return fmt.Errorf("%s: agent was not found. Run /agent add <builtin> first, then /agent list to inspect registered agents", action)
	case strings.Contains(lower, "agent ") && strings.Contains(lower, " is ambiguous"):
		return fmt.Errorf("%s: agent name is ambiguous. Type more of the agent name or run /agent list", action)
	case strings.Contains(lower, "subagent handle") && strings.Contains(lower, "not found"):
		return fmt.Errorf("%s: handle was not found. Use @handle only for subagents created by /<agent> or SPAWN", action)
	case strings.Contains(lower, "participant id is required"), strings.Contains(lower, "participant ") && strings.Contains(lower, " is not attached"):
		return fmt.Errorf("%s: participant was not found", action)
	case strings.Contains(lower, "participant ") && strings.Contains(lower, " is ambiguous"):
		return fmt.Errorf("%s: participant target is ambiguous", action)
	case strings.Contains(lower, "control plane is not available"), strings.Contains(lower, "acp controller backend is not configured"):
		return fmt.Errorf("%s: ACP control plane is not configured for this stack. Check app assembly agent config before using /agent", action)
	case strings.Contains(lower, "unknown sandbox backend"), strings.Contains(lower, "unsupported by"):
		return fmt.Errorf("%s: sandbox backend is unavailable on this machine. Run /sandbox to inspect available backends", action)
	case strings.Contains(lower, "session not found"):
		return fmt.Errorf("%s: session could not be loaded. Run /resume to inspect available sessions", action)
	case strings.Contains(lower, "active turn"):
		return fmt.Errorf("%s: another turn is still running. Wait for it to finish or interrupt it before reconfiguring", action)
	default:
		return fmt.Errorf("%s: %w", action, err)
	}
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
