package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
)

const (
	metaKind                = "kind"
	metaKindCompaction      = "compaction"
	metaCompaction          = "compaction"
	triggerAuto             = "auto"
	triggerManual           = "manual"
	triggerOverflowRecovery = "overflow_recovery"
)

type compactInput struct {
	Session             *session.Session
	Model               model.LLM
	Events              []*session.Event
	ContextWindowTokens int
	Trigger             string
	Note                string
	Force               bool
}

type CompactionSummaryFormatter func(string) string

// CompactionConfig configures runtime history compaction behavior.
type CompactionConfig struct {
	WatermarkRatio             float64
	MinWatermarkRatio          float64
	MaxWatermarkRatio          float64
	DefaultContextWindowTokens int
	ReserveOutputTokens        int
	SafetyMarginTokens         int
	SummaryChunkTokens         int
	MaxModelSummaryRetries     int
	Strategy                   CompactionStrategy
	SummaryFormatter           CompactionSummaryFormatter
}

func normalizeCompactionConfig(cfg CompactionConfig) CompactionConfig {
	if cfg.MinWatermarkRatio <= 0 {
		cfg.MinWatermarkRatio = 0.5
	}
	if cfg.MaxWatermarkRatio <= 0 {
		cfg.MaxWatermarkRatio = 0.9
	}
	if cfg.WatermarkRatio <= 0 {
		cfg.WatermarkRatio = 0.7
	}
	cfg.WatermarkRatio = maxFloat(cfg.MinWatermarkRatio, minFloat(cfg.WatermarkRatio, cfg.MaxWatermarkRatio))

	if cfg.DefaultContextWindowTokens <= 0 {
		cfg.DefaultContextWindowTokens = 65536
	}
	if cfg.ReserveOutputTokens <= 0 {
		cfg.ReserveOutputTokens = 4096
	}
	if cfg.SafetyMarginTokens <= 0 {
		cfg.SafetyMarginTokens = 1024
	}
	if cfg.SummaryChunkTokens <= 0 {
		cfg.SummaryChunkTokens = 6000
	}
	if cfg.MaxModelSummaryRetries <= 0 {
		cfg.MaxModelSummaryRetries = 3
	}
	if cfg.SummaryFormatter == nil {
		cfg.SummaryFormatter = defaultCompactionSummaryFormatter
	}
	return cfg
}

func (r *Runtime) compactIfNeeded(ctx context.Context, in compactInput) (*session.Event, error) {
	return r.compactIfNeededWithNotify(ctx, in, nil)
}

func skipCompaction() (*session.Event, error) {
	//nolint:nilnil // A nil event without error means compaction was intentionally skipped.
	return nil, nil
}

func (r *Runtime) compactIfNeededWithNotify(ctx context.Context, in compactInput, notify func(*session.Event) bool) (*session.Event, error) {
	windowEvents := session.AgentVisible(in.Events)
	if len(windowEvents) == 0 {
		return skipCompaction()
	}
	windowTokens := resolveContextWindowTokens(in.ContextWindowTokens, in.Model, r.compaction.DefaultContextWindowTokens)
	inputBudget := windowTokens - r.compaction.ReserveOutputTokens - r.compaction.SafetyMarginTokens
	if inputBudget < 2048 {
		inputBudget = int(float64(windowTokens) * 0.5)
	}
	if inputBudget < 1024 {
		inputBudget = 1024
	}

	currentTokens := estimateEventsTokens(windowEvents)
	watermark := float64(currentTokens) / float64(inputBudget)
	if !in.Force && watermark < r.compaction.WatermarkRatio {
		return skipCompaction()
	}

	toSummarize, tail := splitCompactionTarget(windowEvents)
	if len(toSummarize) == 0 {
		return skipCompaction()
	}
	if notify != nil {
		if !notify(prepareEvent(ctx, in.Session, compactionNoticeEvent(in.Trigger, currentTokens, windowTokens, "start"))) {
			return skipCompaction()
		}
	}

	runtimeState := r.buildCompactionRuntimeState(ctx, in.Session, windowEvents)
	summary, summarizedEvents, err := r.summarizeForCompaction(ctx, in.Model, toSummarize, inputBudget, runtimeState)
	if err != nil {
		return nil, err
	}
	payload := formatCompactionSummaryPayload(summary, runtimeState)
	compiledSummary := strings.TrimSpace(r.compaction.SummaryFormatter(payload))
	if compiledSummary == "" {
		return skipCompaction()
	}

	lastSummarizedID := toSummarize[len(toSummarize)-1].ID
	tailIDs := make([]string, 0, len(tail))
	for _, ev := range tail {
		if ev == nil || strings.TrimSpace(ev.ID) == "" {
			continue
		}
		tailIDs = append(tailIDs, ev.ID)
	}
	compactionEvent := &session.Event{
		ID:        eventID(),
		SessionID: in.Session.ID,
		Time:      time.Now(),
		Message:   model.NewTextMessage(model.RoleUser, compiledSummary),
		Meta: map[string]any{
			metaKind: metaKindCompaction,
			metaCompaction: map[string]any{
				"version":                1,
				"trigger":                in.Trigger,
				"note":                   strings.TrimSpace(in.Note),
				"summarized_to_event_id": lastSummarizedID,
				"summarized_events":      summarizedEvents,
				"tail_events":            len(tail),
				"tail_event_ids":         tailIDs,
				"pre_tokens":             currentTokens,
				"window_tokens":          windowTokens,
				"watermark_ratio":        r.compaction.WatermarkRatio,
			},
		},
	}
	postTokens := estimateEventsTokens(append([]*session.Event{compactionEvent}, tail...))
	meta := compactionEvent.Meta[metaCompaction].(map[string]any)
	meta["post_tokens"] = postTokens

	prepareEvent(ctx, in.Session, compactionEvent)
	if err := r.logStore.AppendEvent(ctx, in.Session, compactionEvent); err != nil {
		return nil, err
	}
	if notify != nil {
		if !notify(prepareEvent(ctx, in.Session, compactionNoticeEvent(in.Trigger, currentTokens, postTokens, "done"))) {
			return skipCompaction()
		}
	}
	return compactionEvent, nil
}

func compactionNoticeEvent(trigger string, beforeTokens int, afterTokens int, phase string) *session.Event {
	phase = strings.TrimSpace(phase)
	trigger = strings.TrimSpace(trigger)
	if phase != "start" && phase != "done" {
		return nil
	}
	// The text field carries a machine-readable key (not human-presentable
	// text). The UI/app layer should inspect Meta["kind"]=="compaction_notice"
	// and render appropriate localized text from the structured metadata.
	key := "compaction." + phase
	return session.MarkNotice(&session.Event{
		ID:   eventID(),
		Time: time.Now(),
		Meta: map[string]any{
			"kind":               "compaction_notice",
			"compaction_phase":   phase,
			"compaction_trigger": trigger,
			"pre_tokens":         beforeTokens,
			"post_tokens":        afterTokens,
		},
	}, session.NoticeLevelNote, key)
}

func (r *Runtime) summarizeForCompaction(ctx context.Context, llm model.LLM, events []*session.Event, inputBudget int, runtimeState compactionRuntimeState) (string, int, error) {
	if len(events) == 0 {
		return "", 0, nil
	}
	strategy := r.compactionStrategy
	if strategy == nil {
		strategy = DefaultCompactionStrategy()
	}
	result, err := strategy.Summarize(ctx, llm, CompactionSummarizeInput{
		Events:                 append([]*session.Event(nil), events...),
		InputBudget:            inputBudget,
		SummaryChunkTokens:     r.compaction.SummaryChunkTokens,
		MaxModelSummaryRetries: r.compaction.MaxModelSummaryRetries,
		PlanSummary:            runtimeState.PlanSummary,
		ProgressSummary:        runtimeState.ProgressSummary,
		ActiveTasksSummary:     runtimeState.ActiveTasksSummary,
		LatestBlockerSummary:   runtimeState.LatestBlockerSummary,
	})
	if err != nil {
		return "", 0, err
	}
	return strings.TrimSpace(result.Text), result.SummarizedEvents, nil
}

func splitByTokenBudget(events []*session.Event, budget int) [][]*session.Event {
	if budget <= 0 {
		budget = 1200
	}
	chunks := make([][]*session.Event, 0, 4)
	current := make([]*session.Event, 0, 8)
	currentTokens := 0
	for _, ev := range events {
		if ev == nil {
			continue
		}
		tokens := estimateEventTokens(ev)
		if len(current) > 0 && currentTokens+tokens > budget {
			chunks = append(chunks, current)
			current = make([]*session.Event, 0, 8)
			currentTokens = 0
		}
		current = append(current, ev)
		currentTokens += tokens
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}

func splitCompactionTarget(window []*session.Event) ([]*session.Event, []*session.Event) {
	if len(window) == 0 {
		return nil, nil
	}
	// Preserve the most recent complete interaction turn as tail. Walk
	// backwards to find the boundary between "old history to summarize" and
	// "recent tail to keep verbatim". A complete turn is defined as a
	// contiguous sequence of assistant/tool events optionally preceded by a
	// user event. We keep the last such turn plus its user message.
	tailStart := findTailBoundary(window)
	if tailStart <= 0 || tailStart >= len(window) {
		// Not enough history to split meaningfully — summarize everything.
		return window, nil
	}
	return window[:tailStart], window[tailStart:]
}

// findTailBoundary returns the index from which the tail begins. The tail
// includes the last complete user→assistant(+tool) interaction turn.
func findTailBoundary(events []*session.Event) int {
	if len(events) <= 2 {
		return 0
	}
	// Walk backwards to find the last user message. Everything from that
	// user message onwards is the tail (the most recent full turn).
	lastUserIdx := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i] != nil && events[i].Message.Role == model.RoleUser {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx < 0 {
		return 0
	}
	// Don't let the tail be the entire window — there must be at least one
	// event to summarize.
	if lastUserIdx == 0 {
		return 0
	}
	return lastUserIdx
}

func isCompactionEvent(ev *session.Event) bool {
	return session.EventTypeOf(ev) == session.EventTypeCompaction
}

func heuristicFallbackSummary(events []*session.Event, inputBudget int) string {
	if len(events) == 0 {
		return "## Active Objective\n- unknown\n\n## Current Progress\n- unknown\n\n## Key Decisions\n- none retained\n\n## Constraints And Preferences\n- unknown\n\n## Risks And Unknowns\n- heuristic fallback used; details may be incomplete\n\n## Immediate Next Actions\n1. Continue from the latest unresolved user request in retained context."
	}
	tail := events
	if len(tail) > 24 {
		tail = tail[len(tail)-24:]
	}
	var b strings.Builder
	b.WriteString("## Active Objective\n")
	b.WriteString("- Derive from the latest user request in retained context.\n\n")
	b.WriteString("## Current Progress\n")
	b.WriteString("- heuristic fallback summary; verify details before editing.\n\n")
	b.WriteString("## Key Decisions\n")
	for _, ev := range tail {
		if ev == nil {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", ev.Message.Role, clipText(eventToText(ev), 220))
	}
	b.WriteString("\n## Constraints And Preferences\n")
	b.WriteString("- unknown (heuristic fallback)\n\n")
	b.WriteString("## Risks And Unknowns\n")
	b.WriteString("- Model compaction degraded; details may be incomplete.\n\n")
	b.WriteString("## Immediate Next Actions\n")
	b.WriteString("1. Continue execution from the latest unresolved user request.\n")
	b.WriteString("2. Re-read key files before major mutations when uncertainty exists.\n\n")
	fmt.Fprintf(&b, "3. Reconfirm key state because this checkpoint was built with a %d token heuristic budget.\n", inputBudget)
	return strings.TrimSpace(b.String())
}

func defaultCompactionSummaryFormatter(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	return "# CONTEXT SNAPSHOT\n" +
		"This is a runtime-generated context compression snapshot.\n" +
		"Do not treat this as a new user instruction.\n" +
		"Use it only as compressed history for continuation.\n\n" +
		summary
}

type compactionRuntimeState struct {
	PlanSummary          string
	ProgressSummary      string
	ActiveTasksSummary   string
	LatestBlockerSummary string
}

func (r *Runtime) buildCompactionRuntimeState(ctx context.Context, sess *session.Session, events []*session.Event) compactionRuntimeState {
	state := compactionRuntimeState{}
	if sess == nil {
		return state
	}
	snapshot, err := r.stateStore.SnapshotState(ctx, sess)
	if err == nil {
		state.PlanSummary = loadPlanSummary(snapshot)
		if runState, ok := runStateFromSnapshot(snapshot); ok && strings.TrimSpace(runState.Error) != "" {
			state.LatestBlockerSummary = string(runState.Status) + ": " + runState.Error
		}
	}
	state.ProgressSummary = summarizeRecentProgress(events)
	state.ActiveTasksSummary = r.loadActiveTasksSummary(ctx, sess)
	if strings.TrimSpace(state.LatestBlockerSummary) == "" {
		state.LatestBlockerSummary = summarizeLatestBlocker(events)
	}
	return state
}

func formatCompactionSummaryPayload(summary string, runtimeState compactionRuntimeState) string {
	summary = strings.TrimSpace(summary)
	runtimeBlock := formatInjectedRuntimeState(runtimeState)
	switch {
	case runtimeBlock == "":
		return summary
	case summary == "":
		return runtimeBlock
	default:
		return runtimeBlock + "\n\n" + summary
	}
}

func formatInjectedRuntimeState(state compactionRuntimeState) string {
	lines := make([]string, 0, 6)
	if text := strings.TrimSpace(state.PlanSummary); text != "" {
		lines = append(lines, "plan_summary="+singleLineSummary(text))
	}
	if text := strings.TrimSpace(state.ProgressSummary); text != "" {
		lines = append(lines, "progress_summary="+singleLineSummary(text))
	}
	if text := strings.TrimSpace(state.ActiveTasksSummary); text != "" {
		lines = append(lines, "active_tasks_summary="+singleLineSummary(text))
	}
	if text := strings.TrimSpace(state.LatestBlockerSummary); text != "" {
		lines = append(lines, "latest_blocker_summary="+singleLineSummary(text))
	}
	if len(lines) == 0 {
		return ""
	}
	return "<runtime_state>\n" + strings.Join(lines, "\n") + "\n</runtime_state>"
}

func loadPlanSummary(snapshot map[string]any) string {
	if len(snapshot) == 0 {
		return ""
	}
	plan, _ := snapshot["plan"].(map[string]any)
	if len(plan) == 0 {
		return ""
	}
	rawEntries, _ := plan["entries"].([]any)
	if len(rawEntries) == 0 {
		return ""
	}
	lines := make([]string, 0, 4)
	for _, raw := range rawEntries {
		entry, _ := raw.(map[string]any)
		content := strings.TrimSpace(fmt.Sprint(entry["content"]))
		status := strings.TrimSpace(fmt.Sprint(entry["status"]))
		if content == "" {
			continue
		}
		if status == "" {
			status = "pending"
		}
		lines = append(lines, "["+status+"] "+content)
		if len(lines) >= 4 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func summarizeRecentProgress(events []*session.Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil || session.IsOverlay(ev) {
			continue
		}
		if resp := ev.Message.ToolResponse(); resp != nil {
			if text := strings.TrimSpace(fmt.Sprint(resp.Result["summary"])); text != "" {
				return clipText(text, 220)
			}
			if text := strings.TrimSpace(fmt.Sprint(resp.Result["msg"])); text != "" {
				return clipText(text, 220)
			}
		}
		if ev.Message.Role == model.RoleAssistant {
			if text := strings.TrimSpace(ev.Message.TextContent()); text != "" {
				return clipText(text, 220)
			}
		}
	}
	return ""
}

func (r *Runtime) loadActiveTasksSummary(ctx context.Context, sess *session.Session) string {
	if r == nil || r.taskStore == nil || sess == nil {
		return ""
	}
	items, err := r.taskStore.ListSession(ctx, task.SessionRef{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	})
	if err != nil || len(items) == 0 {
		return ""
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	lines := make([]string, 0, 4)
	for _, entry := range items {
		if entry == nil || !taskEntryIsActive(entry) {
			continue
		}
		summary := tool.CompactTaskListItem(task.Snapshot{
			TaskID:  entry.TaskID,
			Kind:    entry.Kind,
			Title:   entry.Title,
			State:   entry.State,
			Running: entry.Running,
			Result:  task.CloneEntry(entry).Result,
		})
		line := strings.TrimSpace(fmt.Sprint(summary["task_id"]))
		if state := strings.TrimSpace(fmt.Sprint(summary["state"])); state != "" {
			line += " " + state
		}
		if text := strings.TrimSpace(fmt.Sprint(summary["summary"])); text != "" {
			line += ": " + clipText(text, 160)
		}
		lines = append(lines, strings.TrimSpace(line))
		if len(lines) >= 4 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func summarizeLatestBlocker(events []*session.Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil {
			continue
		}
		if resp := ev.Message.ToolResponse(); resp != nil {
			if text := strings.TrimSpace(fmt.Sprint(resp.Result["error"])); text != "" {
				code := strings.TrimSpace(fmt.Sprint(resp.Result["error_code"]))
				if code != "" {
					return code + ": " + clipText(text, 200)
				}
				return clipText(text, 200)
			}
		}
	}
	return ""
}

func taskEntryIsActive(entry *task.Entry) bool {
	if entry == nil {
		return false
	}
	if entry.Running {
		return true
	}
	switch entry.State {
	case task.StateRunning, task.StateWaitingApproval, task.StateWaitingInput:
		return true
	default:
		return false
	}
}

func eventToText(ev *session.Event) string {
	if ev == nil {
		return ""
	}
	msg := ev.Message
	if resp := msg.ToolResponse(); resp != nil {
		raw, _ := json.Marshal(resp.Result)
		return fmt.Sprintf("tool_response name=%s result=%s", resp.Name, string(raw))
	}
	if calls := msg.ToolCalls(); len(calls) > 0 {
		raw, _ := json.Marshal(calls)
		return fmt.Sprintf("tool_calls=%s text=%s", string(raw), msg.TextContent())
	}
	return msg.TextContent()
}

func eventsToTranscript(events []*session.Event) string {
	var b strings.Builder
	for _, ev := range events {
		if ev == nil {
			continue
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n",
			ev.Time.Format(time.RFC3339),
			ev.Message.Role,
			eventToText(ev),
		)
	}
	return b.String()
}

func estimateEventsTokens(events []*session.Event) int {
	total := 0
	for _, ev := range events {
		total += estimateEventTokens(ev)
	}
	return total
}

func estimateEventTokens(ev *session.Event) int {
	if ev == nil {
		return 0
	}
	return estimateTextTokens(eventToText(ev)) + 10
}

func estimateTextTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	runes := utf8.RuneCountInString(text)
	token := runes / 4
	if runes%4 != 0 {
		token++
	}
	if token <= 0 {
		token = 1
	}
	return token
}

func resolveContextWindowTokens(override int, llm model.LLM, fallback int) int {
	if override > 0 {
		return override
	}
	type capability interface {
		ContextWindowTokens() int
	}
	if c, ok := llm.(capability); ok {
		if tokens := c.ContextWindowTokens(); tokens > 0 {
			return tokens
		}
	}
	if fallback > 0 {
		return fallback
	}
	return 65536
}

func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	// Structured check first — providers should wrap overflow errors in
	// model.ContextOverflowError.
	if model.IsContextOverflow(err) {
		return true
	}
	// Fallback: string matching for providers that haven't adopted the
	// structured error yet. This path should be removed once all providers
	// wrap overflow errors properly.
	text := strings.ToLower(err.Error())
	keywords := []string{
		"context length",
		"context window",
		"prompt is too long",
		"too many tokens",
		"maximum context",
		"input is too long",
		"token limit",
		"max context",
	}
	for _, k := range keywords {
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}

func clipText(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	var b strings.Builder
	count := 0
	for _, r := range text {
		if count >= maxRunes {
			break
		}
		b.WriteRune(r)
		count++
	}
	b.WriteString(" ...")
	return b.String()
}

func minFloat(a, b float64) float64 {
	return math.Min(a, b)
}

func maxFloat(a, b float64) float64 {
	return math.Max(a, b)
}
