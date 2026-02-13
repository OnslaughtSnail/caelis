package llmagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"sort"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
	"github.com/OnslaughtSnail/caelis/kernel/toolcap"
)

// Config controls behavior of LLMAgent.
type Config struct {
	Name              string
	SystemPrompt      string
	MaxSteps          int // Deprecated: kept for compatibility and ignored.
	StreamModel       bool
	Reasoning         model.ReasoningConfig
	EmitPartialEvents bool
	ToolTruncation    tool.TruncationPolicy
	// ToolResultSanitizer controls how tool results are transformed before
	// being sent back to model context. Nil uses default sanitizer.
	ToolResultSanitizer func(map[string]any) map[string]any
}

// Agent is a minimal model-tool loop agent.
type Agent struct {
	cfg                 Config
	toolResultSanitizer func(map[string]any) map[string]any
}

const uiOnlyResultKeyPrefix = "_ui_"
const toolResultMetadataKey = "metadata"

func New(cfg Config) (*Agent, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("llmagent: name is required")
	}
	if cfg.ToolTruncation.MaxTokens <= 0 && cfg.ToolTruncation.MaxBytes <= 0 {
		cfg.ToolTruncation = tool.DefaultTruncationPolicy()
	}
	sanitizer := cfg.ToolResultSanitizer
	if sanitizer == nil {
		sanitizer = defaultSanitizeToolResultForModel
	}
	return &Agent{
		cfg:                 cfg,
		toolResultSanitizer: sanitizer,
	}, nil
}

func (a *Agent) Name() string {
	return a.cfg.Name
}

func (a *Agent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if ctx == nil {
			yield(nil, fmt.Errorf("llmagent: invocation context is nil"))
			return
		}
		if ctx.Model() == nil {
			yield(nil, fmt.Errorf("llmagent: model is nil"))
			return
		}

		messages := toMessagesWithSanitizer(ctx.History(), a.cfg.SystemPrompt, a.toolResultSanitizer)
		hooks := ctx.Policies()
		dupCount := map[string]int{}

		for {
			toolDecls := tool.Declarations(ctx.Tools())
			in, err := policy.ApplyBeforeModel(ctx, hooks, policy.ModelInput{Messages: messages, Tools: toolDecls})
			if err != nil {
				yield(nil, err)
				return
			}
			req := &model.Request{
				Messages:  in.Messages,
				Tools:     in.Tools,
				Stream:    a.cfg.StreamModel,
				Reasoning: a.cfg.Reasoning,
			}
			resp, err := a.generateWithRetry(ctx, req, func(partial *model.Response) error {
				if partial == nil || !a.cfg.EmitPartialEvents || !partial.Partial {
					return nil
				}
				if strings.TrimSpace(partial.Message.Reasoning) != "" {
					ev := &session.Event{
						ID:   newEventID(),
						Time: time.Now(),
						Message: model.Message{
							Role:      model.RoleAssistant,
							Reasoning: partial.Message.Reasoning,
						},
						Meta: map[string]any{
							"partial": true,
							"channel": "reasoning",
						},
					}
					if !yield(ev, nil) {
						return errYieldStopped
					}
				}
				if strings.TrimSpace(partial.Message.Text) != "" {
					ev := &session.Event{
						ID:      newEventID(),
						Time:    time.Now(),
						Message: model.Message{Role: model.RoleAssistant, Text: partial.Message.Text},
						Meta: map[string]any{
							"partial": true,
							"channel": "answer",
						},
					}
					if !yield(ev, nil) {
						return errYieldStopped
					}
				}
				return nil
			})
			if errors.Is(err, errYieldStopped) {
				return
			}
			if err != nil {
				yield(nil, err)
				return
			}
			if resp == nil {
				yield(nil, fmt.Errorf("llmagent: empty model response"))
				return
			}

			out, err := policy.ApplyBeforeOutput(ctx, hooks, policy.Output{Message: resp.Message})
			if err != nil {
				yield(nil, err)
				return
			}
			assistantMsg := out.Message
			if assistantMsg.Role == "" {
				assistantMsg.Role = model.RoleAssistant
			}
			assistantEvent := &session.Event{
				ID:      newEventID(),
				Time:    time.Now(),
				Message: assistantMsg,
				Meta:    responseMeta(resp),
			}
			if !yield(assistantEvent, nil) {
				return
			}

			messages = append(messages, assistantMsg)
			if len(assistantMsg.ToolCalls) == 0 {
				return
			}

			for _, call := range assistantMsg.ToolCalls {
				sig, sigErr := toolCallSignature(call)
				if sigErr != nil {
					yield(nil, sigErr)
					return
				}
				dupCount[sig]++
				if dupCount[sig] > 2 {
					errMsg := "duplicate tool call detected"
					toolMsg := model.Message{
						Role: model.RoleTool,
						ToolResponse: &model.ToolResponse{
							ID:     call.ID,
							Name:   call.Name,
							Result: map[string]any{"error": errMsg},
						},
					}
					ev := &session.Event{ID: newEventID(), Time: time.Now(), Message: toolMsg}
					if !yield(ev, nil) {
						return
					}
					messages = append(messages, toolMsg)
					return
				}

				capability := toolcap.Capability{Risk: toolcap.RiskUnknown}
				if toolForCap, exists := ctx.Tool(call.Name); exists {
					capability = toolcap.Of(toolForCap)
				}
				beforeIn, err := policy.ApplyBeforeTool(ctx, hooks, policy.ToolInput{
					Call:       call,
					Capability: capability,
				})
				if err != nil {
					yield(nil, err)
					return
				}
				call = beforeIn.Call
				decision := policy.NormalizeDecision(beforeIn.Decision)

				execOut := policy.ToolOutput{
					Call:       call,
					Capability: beforeIn.Capability,
					Decision:   decision,
				}
				t, ok := ctx.Tool(call.Name)
				if !ok {
					execOut.Err = fmt.Errorf("llmagent: unknown tool %q", call.Name)
					execOut.Result = map[string]any{"error": execOut.Err.Error()}
				} else if decision.Effect == policy.DecisionEffectDeny {
					reason := strings.TrimSpace(decision.Reason)
					if reason == "" {
						reason = "tool denied by policy"
					}
					execOut.Err = fmt.Errorf("llmagent: tool %q denied by policy: %s", call.Name, reason)
					execOut.Result = map[string]any{"error": execOut.Err.Error()}
				} else {
					execOut.Capability = toolcap.Of(t)
					toolCtx := context.Context(ctx)
					toolCtx = policy.WithToolDecision(toolCtx, decision)
					result, runErr := t.Run(toolCtx, call.Args)
					execOut.Err = runErr
					if runErr != nil {
						if toolexec.IsApprovalAborted(runErr) {
							yield(nil, runErr)
							return
						}
						execOut.Result = map[string]any{"error": runErr.Error()}
					} else {
						execOut.Result = result
					}
				}
				if len(execOut.Capability.Operations) == 0 && execOut.Capability.Risk == "" {
					execOut.Capability = beforeIn.Capability
				}

				afterOut, err := policy.ApplyAfterTool(ctx, hooks, execOut)
				if err != nil {
					yield(nil, err)
					return
				}
				truncatedResult, truncationInfo := tool.TruncateMap(afterOut.Result, a.cfg.ToolTruncation)
				finalResult := tool.AddTruncationMeta(truncatedResult, truncationInfo)
				finalResult = annotateToolResultMetadata(finalResult, afterOut.Err)
				modelResult := a.toolResultSanitizer(finalResult)
				toolMsg := model.Message{
					Role:         model.RoleTool,
					ToolResponse: &model.ToolResponse{ID: afterOut.Call.ID, Name: afterOut.Call.Name, Result: finalResult},
				}
				ev := &session.Event{ID: newEventID(), Time: time.Now(), Message: toolMsg}
				if !yield(ev, nil) {
					return
				}
				messages = append(messages, model.Message{
					Role: model.RoleTool,
					ToolResponse: &model.ToolResponse{
						ID:     afterOut.Call.ID,
						Name:   afterOut.Call.Name,
						Result: modelResult,
					},
				})
			}
		}

	}
}

func toMessages(events []*session.Event, systemPrompt string) []model.Message {
	return toMessagesWithSanitizer(events, systemPrompt, defaultSanitizeToolResultForModel)
}

func toMessagesWithSanitizer(
	events []*session.Event,
	systemPrompt string,
	sanitizer func(map[string]any) map[string]any,
) []model.Message {
	if sanitizer == nil {
		sanitizer = defaultSanitizeToolResultForModel
	}
	out := make([]model.Message, 0, len(events)+1)
	if systemPrompt != "" {
		out = append(out, model.Message{Role: model.RoleSystem, Text: systemPrompt})
	}
	for _, ev := range events {
		if ev == nil {
			continue
		}
		msg := ev.Message
		if msg.ToolResponse != nil {
			resp := *msg.ToolResponse
			resp.Result = sanitizer(resp.Result)
			msg.ToolResponse = &resp
		}
		out = append(out, msg)
	}
	return out
}

func defaultSanitizeToolResultForModel(result map[string]any) map[string]any {
	if len(result) == 0 {
		return result
	}
	out := make(map[string]any, len(result))
	for key, value := range result {
		if defaultIsModelHiddenToolResultKey(key) {
			continue
		}
		out[key] = sanitizeToolResultValue(value, defaultSanitizeToolResultForModel)
	}
	return out
}

func defaultIsModelHiddenToolResultKey(key string) bool {
	trimmed := strings.TrimSpace(key)
	if strings.HasPrefix(trimmed, uiOnlyResultKeyPrefix) {
		return true
	}
	return strings.EqualFold(trimmed, toolResultMetadataKey)
}

func ensureToolResultMetadata(result map[string]any) map[string]any {
	if result == nil {
		return map[string]any{toolResultMetadataKey: map[string]any{}}
	}
	if _, exists := result[toolResultMetadataKey]; !exists {
		result[toolResultMetadataKey] = map[string]any{}
		return result
	}
	if _, ok := result[toolResultMetadataKey].(map[string]any); ok {
		return result
	}
	result[toolResultMetadataKey] = map[string]any{
		"raw_value": fmt.Sprint(result[toolResultMetadataKey]),
	}
	return result
}

func annotateToolResultMetadata(result map[string]any, execErr error) map[string]any {
	result = ensureToolResultMetadata(result)
	meta, ok := result[toolResultMetadataKey].(map[string]any)
	if !ok {
		return result
	}
	if execErr == nil {
		return result
	}
	if code := toolexec.ErrorCodeOf(execErr); strings.TrimSpace(string(code)) != "" {
		meta["error_code"] = string(code)
	}
	return result
}

func sanitizeToolResultValue(value any, sanitizer func(map[string]any) map[string]any) any {
	if sanitizer == nil {
		sanitizer = defaultSanitizeToolResultForModel
	}
	switch typed := value.(type) {
	case map[string]any:
		return sanitizer(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, one := range typed {
			out = append(out, sanitizeToolResultValue(one, sanitizer))
		}
		return out
	default:
		return value
	}
}

var errYieldStopped = errors.New("llmagent: downstream yield stopped")

var (
	modelRequestMaxRetries = 5
	modelRetryBaseDelay    = 250 * time.Millisecond
	modelRetryMaxDelay     = 4 * time.Second
)

func collectLast(ctx context.Context, seq iter.Seq2[*model.Response, error], onPartial func(*model.Response) error) (*model.Response, error) {
	var last *model.Response
	for res, err := range seq {
		if err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if res != nil {
			if res.Partial && onPartial != nil {
				if err := onPartial(res); err != nil {
					return nil, err
				}
			}
			last = res
		}
	}
	return last, nil
}

func (a *Agent) generateWithRetry(
	ctx agent.InvocationContext,
	req *model.Request,
	onPartial func(*model.Response) error,
) (*model.Response, error) {
	retries := 0
	for {
		emittedPartial := false
		resp, err := collectLast(ctx, ctx.Model().Generate(ctx, req), func(partial *model.Response) error {
			if partial != nil && partial.Partial {
				emittedPartial = true
			}
			if onPartial == nil {
				return nil
			}
			return onPartial(partial)
		})
		if err == nil {
			return resp, nil
		}
		if emittedPartial {
			return nil, err
		}
		if errors.Is(err, errYieldStopped) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if retries >= modelRequestMaxRetries {
			return nil, fmt.Errorf("llmagent: model request failed after %d retries: %w", modelRequestMaxRetries, err)
		}
		delay := retryDelayForAttempt(retries)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		retries++
	}
}

func retryDelayForAttempt(retry int) time.Duration {
	if retry < 0 {
		retry = 0
	}
	delay := modelRetryBaseDelay
	for i := 0; i < retry; i++ {
		delay *= 2
		if delay >= modelRetryMaxDelay {
			return modelRetryMaxDelay
		}
	}
	if delay > modelRetryMaxDelay {
		return modelRetryMaxDelay
	}
	return delay
}

func toolCallSignature(call model.ToolCall) (string, error) {
	norm := normalize(call.Args)
	raw, err := json.Marshal(norm)
	if err != nil {
		return "", err
	}
	return call.Name + ":" + string(raw), nil
}

func normalize(input map[string]any) any {
	if input == nil {
		return nil
	}
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		out = append(out, k, input[k])
	}
	return out
}

func newEventID() string {
	return fmt.Sprintf("ev_%d", time.Now().UnixNano())
}

func responseMeta(resp *model.Response) map[string]any {
	if resp == nil {
		return nil
	}
	meta := map[string]any{}
	if value := strings.TrimSpace(resp.Provider); value != "" {
		meta["provider"] = value
	}
	if value := strings.TrimSpace(resp.Model); value != "" {
		meta["model"] = value
	}
	usage := map[string]any{}
	if resp.Usage.PromptTokens > 0 {
		usage["prompt_tokens"] = resp.Usage.PromptTokens
	}
	if resp.Usage.CompletionTokens > 0 {
		usage["completion_tokens"] = resp.Usage.CompletionTokens
	}
	if resp.Usage.TotalTokens > 0 {
		usage["total_tokens"] = resp.Usage.TotalTokens
	}
	if len(usage) > 0 {
		meta["usage"] = usage
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}
