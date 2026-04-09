package compaction

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type RuntimeState struct {
	PlanSummary          string
	ProgressSummary      string
	ActiveTasksSummary   string
	LatestBlockerSummary string
}

func SplitByTokenBudget(events []*session.Event, budget int) [][]*session.Event {
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
		tokens := EstimateEventTokens(ev)
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

func HeuristicFallbackSummary(events []*session.Event, inputBudget int) string {
	return RenderCheckpointMarkdown(HeuristicFallbackCheckpoint(events, Checkpoint{}, RuntimeState{}, inputBudget))
}

func DefaultSummaryFormatter(summary string) string {
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

func FormatSummaryPayload(summary string, runtimeState RuntimeState) string {
	summary = strings.TrimSpace(summary)
	runtimeBlock := FormatInjectedRuntimeState(runtimeState)
	switch {
	case runtimeBlock == "":
		return summary
	case summary == "":
		return runtimeBlock
	default:
		return runtimeBlock + "\n\n" + summary
	}
}

func FormatInjectedRuntimeState(state RuntimeState) string {
	lines := make([]string, 0, 6)
	if text := strings.TrimSpace(state.PlanSummary); text != "" {
		lines = append(lines, "plan_summary="+SingleLineSummary(text))
	}
	if text := strings.TrimSpace(state.ProgressSummary); text != "" {
		lines = append(lines, "progress_summary="+SingleLineSummary(text))
	}
	if text := strings.TrimSpace(state.ActiveTasksSummary); text != "" {
		lines = append(lines, "active_tasks_summary="+SingleLineSummary(text))
	}
	if text := strings.TrimSpace(state.LatestBlockerSummary); text != "" {
		lines = append(lines, "latest_blocker_summary="+SingleLineSummary(text))
	}
	if len(lines) == 0 {
		return ""
	}
	return "<runtime_state>\n" + strings.Join(lines, "\n") + "\n</runtime_state>"
}

func SingleLineSummary(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	parts := strings.Split(text, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.TrimPrefix(part, "-"))
		part = strings.TrimSpace(strings.TrimPrefix(part, "*"))
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(out, " | ")
}

func EventToText(ev *session.Event) string {
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

func EventsToTranscript(events []*session.Event) string {
	var b strings.Builder
	for _, ev := range events {
		if ev == nil {
			continue
		}
		timestamp := "unknown_time"
		if !ev.Time.IsZero() {
			timestamp = ev.Time.Format(time.RFC3339)
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n",
			timestamp,
			ev.Message.Role,
			EventToText(ev),
		)
	}
	return b.String()
}

func EstimateEventsTokens(events []*session.Event) int {
	total := 0
	for _, ev := range events {
		total += EstimateEventTokens(ev)
	}
	return total
}

func EstimateEventTokens(ev *session.Event) int {
	if ev == nil {
		return 0
	}
	return estimateTextTokens(EventToText(ev)) + 10
}

func IsContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	if model.IsContextOverflow(err) {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, keyword := range []string{
		"context length",
		"context window",
		"prompt is too long",
		"too many tokens",
		"maximum context",
		"input is too long",
		"token limit",
		"max context",
	} {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func ClipText(text string, maxRunes int) string {
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

func MinFloat(a, b float64) float64 {
	return math.Min(a, b)
}

func MaxFloat(a, b float64) float64 {
	return math.Max(a, b)
}

func estimateTextTokens(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	runes := utf8.RuneCountInString(text)
	tokens := runes / 4
	if runes%4 != 0 {
		tokens++
	}
	return max(tokens, 1)
}
