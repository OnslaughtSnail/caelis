package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	compact "github.com/OnslaughtSnail/caelis/kernel/compaction"
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
	defaultHardTailRatio    = 1.5
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
	TailTokenRatio             float64
	MinTailTokens              int
	MaxTailTokens              int
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
	cfg.WatermarkRatio = compact.MaxFloat(cfg.MinWatermarkRatio, compact.MinFloat(cfg.WatermarkRatio, cfg.MaxWatermarkRatio))
	if cfg.TailTokenRatio <= 0 {
		cfg.TailTokenRatio = 0.2
	}
	if cfg.MinTailTokens <= 0 {
		cfg.MinTailTokens = 1024
	}
	if cfg.MaxTailTokens <= 0 {
		cfg.MaxTailTokens = 4096
	}

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
		cfg.SummaryFormatter = compact.DefaultSummaryFormatter
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
	if in.Model == nil && !in.Force {
		return skipCompaction()
	}
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

	currentTokens := compact.EstimateEventsTokens(windowEvents)
	watermark := float64(currentTokens) / float64(inputBudget)
	if !in.Force && watermark < r.compaction.WatermarkRatio {
		return skipCompaction()
	}

	priorCheckpoint, visibleEvents := extractPriorCheckpoint(windowEvents)
	if len(visibleEvents) == 0 {
		return skipCompaction()
	}
	tailBudget := int(float64(inputBudget) * r.compaction.TailTokenRatio)
	tailBudget = max(tailBudget, r.compaction.MinTailTokens)
	tailBudget = min(tailBudget, r.compaction.MaxTailTokens)
	toSummarize, tail := compact.SplitTargetWithOptions(visibleEvents, compact.SplitOptions{
		SoftTailTokens: tailBudget,
		HardTailTokens: max(int(float64(tailBudget)*defaultHardTailRatio), r.compaction.MinTailTokens),
		MinTailEvents:  2,
	})
	if len(toSummarize) == 0 {
		return skipCompaction()
	}
	if notify != nil {
		if !notify(prepareEvent(ctx, in.Session, compactionNoticeEvent(in.Trigger, currentTokens, windowTokens, "start"))) {
			return skipCompaction()
		}
	}

	runtimeState := r.buildCompactionRuntimeState(ctx, in.Session, windowEvents)
	summaryResult, err := r.summarizeForCompaction(ctx, in.Model, toSummarize, inputBudget, priorCheckpoint, runtimeState)
	if err != nil {
		return nil, err
	}
	compiledSummary := strings.TrimSpace(r.compaction.SummaryFormatter(strings.TrimSpace(summaryResult.Text)))
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
				"version":                compact.CheckpointVersion,
				"checkpoint":             compact.CheckpointMeta(summaryResult.Checkpoint),
				"trigger":                in.Trigger,
				"note":                   strings.TrimSpace(in.Note),
				"summarized_to_event_id": lastSummarizedID,
				"summarized_events":      summaryResult.SummarizedEvents,
				"tail_events":            len(tail),
				"tail_event_ids":         tailIDs,
				"tail_tokens":            compact.EstimateEventsTokens(tail),
				"pre_tokens":             currentTokens,
				"window_tokens":          windowTokens,
				"watermark_ratio":        r.compaction.WatermarkRatio,
			},
		},
	}
	postTokens := compact.EstimateEventsTokens(append([]*session.Event{compactionEvent}, tail...))
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

func (r *Runtime) summarizeForCompaction(
	ctx context.Context,
	llm model.LLM,
	events []*session.Event,
	inputBudget int,
	priorCheckpoint compact.Checkpoint,
	runtimeState compactionRuntimeState,
) (CompactionSummarizeResult, error) {
	if len(events) == 0 {
		return CompactionSummarizeResult{}, nil
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
		PriorCheckpoint:        priorCheckpoint,
		RuntimeState:           toCompactionRuntimeState(runtimeState),
	})
	if err != nil {
		return CompactionSummarizeResult{}, err
	}
	result.Text = strings.TrimSpace(result.Text)
	if result.Checkpoint.HasContent() {
		result.Checkpoint = compact.NormalizeCheckpoint(result.Checkpoint)
		result.Text = compact.RenderCheckpointMarkdown(result.Checkpoint)
		return result, nil
	}
	if parsed := compact.ParseCheckpointMarkdown(result.Text); parsed.HasContent() {
		result.Checkpoint = compact.MergeCheckpoints(priorCheckpoint, parsed, toCompactionRuntimeState(runtimeState))
	} else {
		result.Checkpoint = compact.HeuristicFallbackCheckpoint(events, priorCheckpoint, toCompactionRuntimeState(runtimeState), inputBudget)
	}
	result.Text = compact.RenderCheckpointMarkdown(result.Checkpoint)
	return result, nil
}

func isCompactionEvent(ev *session.Event) bool {
	return session.EventTypeOf(ev) == session.EventTypeCompaction
}

type compactionRuntimeState struct {
	PlanSummary          string
	ProgressSummary      string
	ActiveTasksSummary   string
	LatestBlockerSummary string
}

func toCompactionRuntimeState(state compactionRuntimeState) compact.RuntimeState {
	return compact.RuntimeState{
		PlanSummary:          state.PlanSummary,
		ProgressSummary:      state.ProgressSummary,
		ActiveTasksSummary:   state.ActiveTasksSummary,
		LatestBlockerSummary: state.LatestBlockerSummary,
	}
}

func extractPriorCheckpoint(events []*session.Event) (compact.Checkpoint, []*session.Event) {
	if len(events) == 0 {
		return compact.Checkpoint{}, nil
	}
	var prior compact.Checkpoint
	visible := make([]*session.Event, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if isCompactionEvent(ev) {
			if cp, ok := compact.CheckpointFromEvent(ev); ok {
				prior = compact.MergeCheckpoints(prior, cp, compact.RuntimeState{})
			}
			continue
		}
		visible = append(visible, ev)
	}
	return prior, visible
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
				return compact.ClipText(text, 220)
			}
			if text := strings.TrimSpace(fmt.Sprint(resp.Result["msg"])); text != "" {
				return compact.ClipText(text, 220)
			}
		}
		if ev.Message.Role == model.RoleAssistant {
			if text := strings.TrimSpace(ev.Message.TextContent()); text != "" {
				return compact.ClipText(text, 220)
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
			line += ": " + compact.ClipText(text, 160)
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
					return code + ": " + compact.ClipText(text, 200)
				}
				return compact.ClipText(text, 200)
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
	return compact.IsContextOverflowError(err)
}
