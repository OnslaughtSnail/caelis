package runtime

import (
	"context"
	"fmt"
	"strings"

	compact "github.com/OnslaughtSnail/caelis/kernel/compaction"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

// CompactionSummarizeInput describes compaction summarization input.
type CompactionSummarizeInput struct {
	Events                 []*session.Event
	InputBudget            int
	SummaryChunkTokens     int
	MaxModelSummaryRetries int
	PriorCheckpoint        compact.Checkpoint
	RuntimeState           compact.RuntimeState
}

// CompactionSummarizeResult is one compaction summary result.
type CompactionSummarizeResult struct {
	Text             string
	Checkpoint       compact.Checkpoint
	SummarizedEvents int
}

// CompactionStrategy abstracts how runtime summarizes history chunks.
type CompactionStrategy interface {
	Summarize(context.Context, model.LLM, CompactionSummarizeInput) (CompactionSummarizeResult, error)
}

const (
	defaultCompactionSystemPrompt = "You are updating a CONTEXT CHECKPOINT for an autonomous agent that must continue work after history truncation. This checkpoint must survive repeated compaction cycles without losing the task objective, durable user constraints, durable decisions, verified references, or immediate next steps."
	defaultCompactionUserPrefix   = "Update the continuation checkpoint using the transcript below.\nPreserve durable user constraints and decisions from the existing checkpoint unless newer transcript evidence supersedes them.\nPrefer concrete references such as file paths, task IDs, tool names, commands, symbols, and exact blockers.\nDo not write a retrospective or final answer. Focus on what the next model turn must remember and execute.\n\nOutput requirements:\n- Use exactly these Markdown headings in this order:\n  1) ## Active Objective\n  2) ## Durable Constraints\n  3) ## Durable Decisions\n  4) ## Verified Facts And References\n  5) ## Current Progress\n  6) ## Open Questions And Risks\n  7) ## Immediate Next Actions\n- Keep entries concise, factual, and continuation-oriented.\n- Remove stale actions that are already complete.\n- If a section is unknown, write \"unknown\" or \"none\" as appropriate.\n- Return only checkpoint content.\n\n"
	defaultCompactionMergePrefix  = "Merge the existing checkpoint and checkpoint candidates below into one continuation checkpoint.\nPreserve durable constraints and decisions, keep only current risks and next actions, remove duplicates, and return only the merged checkpoint body using the same seven headings.\n\n"
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
			checkpoint := compact.MergeCheckpoints(in.PriorCheckpoint, compact.ParseCheckpointMarkdown(summary), in.RuntimeState)
			return CompactionSummarizeResult{
				Text:             compact.RenderCheckpointMarkdown(checkpoint),
				Checkpoint:       checkpoint,
				SummarizedEvents: len(working),
			}, nil
		}
		if err == nil {
			break
		}
		if !compact.IsContextOverflowError(err) {
			break
		}
		if len(working) <= 4 {
			break
		}
		working = working[len(working)/2:]
	}
	checkpoint := compact.HeuristicFallbackCheckpoint(working, in.PriorCheckpoint, in.RuntimeState, in.InputBudget)
	return CompactionSummarizeResult{
		Text:             compact.RenderCheckpointMarkdown(checkpoint),
		Checkpoint:       checkpoint,
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
	chunks := compact.SplitByTokenBudget(events, chunkBudget)
	summaries := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		transcript := compact.EventsToTranscript(chunk)
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
	var b strings.Builder
	b.WriteString(s.mergePrefix)
	if in.PriorCheckpoint.HasContent() {
		prior := compact.RenderCheckpointMarkdown(in.PriorCheckpoint)
		b.WriteString("Existing checkpoint:\n\n")
		b.WriteString(prior)
		b.WriteString("\n\n")
	}
	b.WriteString("Checkpoint candidates:\n\n")
	b.WriteString(strings.Join(summaries, "\n\n"))
	return s.callCompactionModel(ctx, llm, b.String())
}

func (s *MapReduceCompactionStrategy) userPrompt(in CompactionSummarizeInput, transcript string) string {
	var b strings.Builder
	b.WriteString(s.userPrefix)
	if in.PriorCheckpoint.HasContent() {
		prior := compact.RenderCheckpointMarkdown(in.PriorCheckpoint)
		b.WriteString("Existing checkpoint:\n\n")
		b.WriteString(prior)
		b.WriteString("\n\n")
	}
	if runtimeState := compactRuntimeStateBlock(in); runtimeState != "" {
		b.WriteString(runtimeState)
		b.WriteString("\n\n")
	}
	b.WriteString("Transcript:\n\n")
	b.WriteString(transcript)
	return b.String()
}

func compactRuntimeStateBlock(in CompactionSummarizeInput) string {
	return compact.CompactRuntimeStateBlock(in.RuntimeState)
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
