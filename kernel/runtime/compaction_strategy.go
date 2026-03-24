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
	defaultCompactionSystemPrompt = "You are writing a CONTEXT CHECKPOINT for an autonomous coding agent that must continue work after history truncation. Do not write a project retrospective or final answer. Focus on what the next model turn must execute."
	defaultCompactionUserPrefix   = "Create a continuation checkpoint from the transcript below.\n\nOutput requirements:\n- Use exactly these Markdown headings in this order:\n  1) ## Active Objective\n  2) ## Current Task State\n  3) ## Completed Tasks\n  4) ## Pending Next Tasks\n  5) ## Constraints And Preferences\n  6) ## Open Issues And Risks\n  7) ## Critical References\n- Keep items concrete and execution-oriented.\n- Preserve critical tool outcomes, errors, and unresolved user intent.\n- For Pending Next Tasks, provide an ordered checklist.\n- If something is unknown, explicitly write \"unknown\".\n- Return only checkpoint content, no preface.\n\nTranscript:\n\n"
	defaultCompactionMergePrefix  = "Merge the following checkpoint chunks into one continuation checkpoint.\nKeep the same section headings and remove duplicates.\nReturn only the merged checkpoint body:\n\n"
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
