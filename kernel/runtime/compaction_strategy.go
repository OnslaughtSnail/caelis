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
	defaultCompactionSystemPrompt = "You are a conversation compactor. Produce a concise structured summary covering goals, constraints, key facts, completed actions, pending tasks, and important artifacts."
	defaultCompactionUserPrefix   = "Summarize the following conversation history. Preserve critical tool outcomes and unresolved issues. Return only the summary body:\n\n"
	defaultCompactionMergePrefix  = "Merge the following chunk summaries into one coherent final summary:\n\n"
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
		summary, err := s.summarizeByMapReduce(ctx, llm, working, chunkBudget)
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
	events []*session.Event,
	chunkBudget int,
) (string, error) {
	chunks := splitByTokenBudget(events, chunkBudget)
	summaries := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		transcript := eventsToTranscript(chunk)
		out, err := s.callCompactionModel(ctx, llm, s.userPrefix+transcript)
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

func (s *MapReduceCompactionStrategy) callCompactionModel(
	ctx context.Context,
	llm model.LLM,
	userPrompt string,
) (string, error) {
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Text: s.systemPrompt},
			{Role: model.RoleUser, Text: userPrompt},
		},
		Stream: false,
	}
	var last *model.Response
	for res, err := range llm.Generate(ctx, req) {
		if err != nil {
			return "", err
		}
		if res != nil {
			last = res
		}
	}
	if last == nil {
		return "", fmt.Errorf("runtime: compaction got empty model response")
	}
	return strings.TrimSpace(last.Message.Text), nil
}
