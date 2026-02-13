package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
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

// CompactionConfig configures runtime history compaction behavior.
type CompactionConfig struct {
	Enabled                    bool
	WatermarkRatio             float64
	MinWatermarkRatio          float64
	MaxWatermarkRatio          float64
	DefaultContextWindowTokens int
	ReserveOutputTokens        int
	SafetyMarginTokens         int
	PreserveRecentTurns        int
	SummaryChunkTokens         int
	MaxModelSummaryRetries     int
	Strategy                   CompactionStrategy
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
	if cfg.PreserveRecentTurns <= 0 {
		cfg.PreserveRecentTurns = 2
	}
	if cfg.SummaryChunkTokens <= 0 {
		cfg.SummaryChunkTokens = 6000
	}
	if cfg.MaxModelSummaryRetries <= 0 {
		cfg.MaxModelSummaryRetries = 3
	}
	if !cfg.Enabled {
		cfg.Enabled = true
	}
	return cfg
}

func (r *Runtime) compactIfNeeded(ctx context.Context, in compactInput) (*session.Event, error) {
	windowEvents := agentHistoryEvents(contextWindowEvents(in.Events))
	if len(windowEvents) == 0 {
		return nil, nil
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
		return nil, nil
	}

	toSummarize, tail := splitCompactionTarget(windowEvents, r.compaction.PreserveRecentTurns)
	if len(toSummarize) == 0 {
		return nil, nil
	}

	summary, summarizedEvents, err := r.summarizeForCompaction(ctx, in.Model, toSummarize, inputBudget)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(summary) == "" {
		return nil, nil
	}

	lastSummarizedID := toSummarize[len(toSummarize)-1].ID
	compactionEvent := &session.Event{
		ID:        eventID(),
		SessionID: in.Session.ID,
		Time:      time.Now(),
		Message: model.Message{
			Role: model.RoleSystem,
			Text: summary,
		},
		Meta: map[string]any{
			metaKind: metaKindCompaction,
			metaCompaction: map[string]any{
				"version":                1,
				"trigger":                in.Trigger,
				"note":                   strings.TrimSpace(in.Note),
				"summarized_to_event_id": lastSummarizedID,
				"summarized_events":      summarizedEvents,
				"pre_tokens":             currentTokens,
				"window_tokens":          windowTokens,
				"watermark_ratio":        r.compaction.WatermarkRatio,
			},
		},
	}
	postTokens := estimateEventsTokens(append([]*session.Event{compactionEvent}, tail...))
	meta := compactionEvent.Meta[metaCompaction].(map[string]any)
	meta["post_tokens"] = postTokens

	if err := r.store.AppendEvent(ctx, in.Session, compactionEvent); err != nil {
		return nil, err
	}
	return compactionEvent, nil
}

func (r *Runtime) summarizeForCompaction(ctx context.Context, llm model.LLM, events []*session.Event, inputBudget int) (string, int, error) {
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

func splitCompactionTarget(window []*session.Event, preserveRecentTurns int) ([]*session.Event, []*session.Event) {
	if len(window) == 0 {
		return nil, nil
	}
	userIndices := make([]int, 0, 16)
	for i, ev := range window {
		if ev != nil && ev.Message.Role == model.RoleUser {
			userIndices = append(userIndices, i)
		}
	}
	if len(userIndices) == 0 {
		return window, nil
	}
	if preserveRecentTurns < 1 {
		preserveRecentTurns = 1
	}
	if len(userIndices) <= preserveRecentTurns {
		return nil, window
	}
	cutoff := userIndices[len(userIndices)-preserveRecentTurns]
	if cutoff <= 0 || cutoff >= len(window) {
		return nil, window
	}
	return window[:cutoff], window[cutoff:]
}

func contextWindowEvents(events []*session.Event) []*session.Event {
	if len(events) == 0 {
		return nil
	}
	lastCompaction := -1
	for i := len(events) - 1; i >= 0; i-- {
		if isCompactionEvent(events[i]) {
			lastCompaction = i
			break
		}
	}
	if lastCompaction < 0 {
		return events
	}
	return events[lastCompaction:]
}

func isCompactionEvent(ev *session.Event) bool {
	if ev == nil || ev.Meta == nil {
		return false
	}
	kind, ok := ev.Meta[metaKind]
	if !ok {
		return false
	}
	kindText, ok := kind.(string)
	return ok && kindText == metaKindCompaction
}

func heuristicFallbackSummary(events []*session.Event, inputBudget int) string {
	if len(events) == 0 {
		return "Fallback summary: no events available."
	}
	tail := events
	if len(tail) > 24 {
		tail = tail[len(tail)-24:]
	}
	var b strings.Builder
	b.WriteString("Fallback summary (heuristic, model compaction degraded):\n")
	for _, ev := range tail {
		if ev == nil {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", ev.Message.Role, clipText(eventToText(ev), 240))
	}
	fmt.Fprintf(&b, "Estimated context budget=%d tokens.\n", inputBudget)
	return strings.TrimSpace(b.String())
}

func eventToText(ev *session.Event) string {
	if ev == nil {
		return ""
	}
	msg := ev.Message
	if msg.ToolResponse != nil {
		raw, _ := json.Marshal(msg.ToolResponse.Result)
		return fmt.Sprintf("tool_response name=%s result=%s", msg.ToolResponse.Name, string(raw))
	}
	if len(msg.ToolCalls) > 0 {
		raw, _ := json.Marshal(msg.ToolCalls)
		return fmt.Sprintf("tool_calls=%s text=%s", string(raw), msg.Text)
	}
	return msg.Text
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
