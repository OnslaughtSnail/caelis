package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

// CompactionSummarizeInput describes compaction summarization input.
type CompactionSummarizeInput struct {
	Events                 []*session.Event
	InputBudget            int
	SummaryChunkTokens     int
	MaxModelSummaryRetries int
	PlanSummary            string
	ProgressSummary        string
	ActiveTasksSummary     string
	LatestBlockerSummary   string
}

// CompactionSummarizeResult is one compaction summary result.
type CompactionSummarizeResult struct {
	Text             string
	SummarizedEvents int
}

// CompactionStrategy abstracts how runtime summarizes history chunks.
type CompactionStrategy interface {
	Summarize(context.Context, model.LLM, CompactionSummarizeInput) (CompactionSummarizeResult, error)
}

const (
	defaultCompactionSystemPrompt = "You are writing a CONTEXT CHECKPOINT for an autonomous coding agent that must continue work after history truncation. Do not write a project retrospective or final answer. Focus on what the next model turn must execute."
	defaultCompactionUserPrefix   = "Create a continuation checkpoint from the transcript below.\nYou will also receive runtime state separately, so do not waste space repeating it unless it changes the next action.\n\nOutput requirements:\n- Use exactly these Markdown headings in this order:\n  1) ## Active Objective\n  2) ## Current Progress\n  3) ## Key Decisions\n  4) ## Constraints And Preferences\n  5) ## Risks And Unknowns\n  6) ## Immediate Next Actions\n- Keep the content concrete, short, and execution-oriented.\n- Preserve unresolved user intent, durable decisions, and the next legal actions after failures.\n- If something is unknown, explicitly write \"unknown\".\n- Return only checkpoint content, no preface.\n\n"
	defaultCompactionMergePrefix  = "Merge the following continuation checkpoint chunks into one checkpoint.\nKeep the same six section headings, remove duplicates, preserve actionable next steps, and return only the merged body.\n\n"
)

// MapReduceCompactionStrategyConfig configures default map-reduce compaction.
type MapReduceCompactionStrategyConfig struct {
	SystemPrompt string
	UserPrefix   string
	MergePrefix  string
}

// MapReduceCompactionStrategy is default token-budgeted compaction strategy.
type MapReduceCompactionStrategy struct {
	systemPrompt string
	userPrefix   string
	mergePrefix  string
}

// NewMapReduceCompactionStrategy builds one map-reduce compaction strategy.
func NewMapReduceCompactionStrategy(cfg MapReduceCompactionStrategyConfig) *MapReduceCompactionStrategy {
	systemPrompt := strings.TrimSpace(cfg.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = defaultCompactionSystemPrompt
	}
	userPrefix := strings.TrimSpace(cfg.UserPrefix)
	if userPrefix == "" {
		userPrefix = defaultCompactionUserPrefix
	}
	mergePrefix := strings.TrimSpace(cfg.MergePrefix)
	if mergePrefix == "" {
		mergePrefix = defaultCompactionMergePrefix
	}
	return &MapReduceCompactionStrategy{
		systemPrompt: systemPrompt,
		userPrefix:   userPrefix,
		mergePrefix:  mergePrefix,
	}
}

// DefaultCompactionStrategy returns kernel default compaction strategy.
func DefaultCompactionStrategy() CompactionStrategy {
	return NewMapReduceCompactionStrategy(MapReduceCompactionStrategyConfig{})
}

func (s *MapReduceCompactionStrategy) Summarize(
	ctx context.Context,
	llm model.LLM,
	in CompactionSummarizeInput,
) (CompactionSummarizeResult, error) {
	if len(in.Events) == 0 {
		return CompactionSummarizeResult{}, nil
	}
	retries := in.MaxModelSummaryRetries
	if retries < 1 {
		retries = 1
	}
	working := append([]*session.Event(nil), in.Events...)
	for attempt := 0; attempt < retries; attempt++ {
		chunkBudget := in.SummaryChunkTokens / (attempt + 1)
		if chunkBudget < 800 {
			chunkBudget = 800
		}
		summary, err := s.summarizeByMapReduce(ctx, llm, in, working, chunkBudget)
		if err == nil && strings.TrimSpace(summary) != "" {
			return CompactionSummarizeResult{
				Text:             strings.TrimSpace(summary),
				SummarizedEvents: len(working),
			}, nil
		}
		if err == nil {
			break
		}
		if !isContextOverflowError(err) {
			break
		}
		if len(working) <= 4 {
			break
		}
		working = working[len(working)/2:]
	}
	return CompactionSummarizeResult{
		Text:             heuristicFallbackSummary(working, in.InputBudget),
		SummarizedEvents: len(working),
	}, nil
}

func (s *MapReduceCompactionStrategy) summarizeByMapReduce(
	ctx context.Context,
	llm model.LLM,
	in CompactionSummarizeInput,
	events []*session.Event,
	chunkBudget int,
) (string, error) {
	chunks := splitByTokenBudget(events, chunkBudget)
	summaries := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		transcript := eventsToTranscript(chunk)
		out, err := s.callCompactionModel(ctx, llm, s.userPrompt(in, transcript))
		if err != nil {
			return "", err
		}
		summaries = append(summaries, out)
	}
	if len(summaries) == 0 {
		return "", nil
	}
	if len(summaries) == 1 {
		return summaries[0], nil
	}
	merged := strings.Join(summaries, "\n\n")
	return s.callCompactionModel(ctx, llm, s.mergePrefix+merged)
}

func (s *MapReduceCompactionStrategy) userPrompt(in CompactionSummarizeInput, transcript string) string {
	var b strings.Builder
	b.WriteString(s.userPrefix)
	if runtimeState := compactRuntimeStateBlock(in); runtimeState != "" {
		b.WriteString(runtimeState)
		b.WriteString("\n\n")
	}
	b.WriteString("Transcript:\n\n")
	b.WriteString(transcript)
	return b.String()
}

func compactRuntimeStateBlock(in CompactionSummarizeInput) string {
	lines := make([]string, 0, 6)
	if text := strings.TrimSpace(in.PlanSummary); text != "" {
		lines = append(lines, "plan_summary="+singleLineSummary(text))
	}
	if text := strings.TrimSpace(in.ProgressSummary); text != "" {
		lines = append(lines, "progress_summary="+singleLineSummary(text))
	}
	if text := strings.TrimSpace(in.ActiveTasksSummary); text != "" {
		lines = append(lines, "active_tasks_summary="+singleLineSummary(text))
	}
	if text := strings.TrimSpace(in.LatestBlockerSummary); text != "" {
		lines = append(lines, "latest_blocker_summary="+singleLineSummary(text))
	}
	if len(lines) == 0 {
		return ""
	}
	return "<runtime_state>\n" + strings.Join(lines, "\n") + "\n</runtime_state>"
}

func singleLineSummary(text string) string {
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

func (s *MapReduceCompactionStrategy) callCompactionModel(
	ctx context.Context,
	llm model.LLM,
	userPrompt string,
) (string, error) {
	req := &model.Request{
		Instructions: []model.Part{model.NewTextPart(s.systemPrompt)},
		Messages: []model.Message{
			model.NewTextMessage(model.RoleUser, userPrompt),
		},
	}
	var last *model.Response
	for event, err := range llm.Generate(ctx, req) {
		if err != nil {
			return "", err
		}
		if event != nil && event.Response != nil {
			last = event.Response
		}
	}
	if last == nil {
		return "", fmt.Errorf("runtime: compaction got empty model response")
	}
	return strings.TrimSpace(last.Message.TextContent()), nil
}
