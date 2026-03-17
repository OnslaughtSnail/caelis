package llmagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/eventview"
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

type agentRunState struct {
	hooks []policy.Hook
}

type legacyRunContext struct {
	agent.InvocationContext
	recorded []*session.Event
}

func (c *legacyRunContext) Events() session.Events {
	base := make([]*session.Event, 0, c.InvocationContext.Events().Len()+len(c.recorded))
	seen := make(map[string]struct{}, c.InvocationContext.Events().Len()+len(c.recorded))
	for ev := range c.InvocationContext.Events().All() {
		if ev != nil {
			base = append(base, ev)
			if id := strings.TrimSpace(ev.ID); id != "" {
				seen[id] = struct{}{}
			}
		}
	}
	for _, ev := range c.recorded {
		if ev == nil {
			continue
		}
		if session.IsOverlay(ev) {
			continue
		}
		if id := strings.TrimSpace(ev.ID); id != "" {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
		}
		base = append(base, ev)
	}
	return session.NewEvents(base)
}

func (c *legacyRunContext) appendRecordedEvent(ev *session.Event) {
	if ev == nil {
		return
	}
	cp := *ev
	c.recorded = append(c.recorded, &cp)
}

func (a *Agent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if err := validateInvocationContext(ctx); err != nil {
			yield(nil, err)
			return
		}
		runCtx := &legacyRunContext{InvocationContext: ctx}
		state := agentRunState{hooks: ctx.Policies()}
		for {
			done, nextState, err := a.step(runCtx, state, func(ev *session.Event, err error) bool {
				if !yield(ev, err) {
					return false
				}
				if err == nil && ev != nil {
					runCtx.appendRecordedEvent(ev)
				}
				return true
			})
			if errors.Is(err, errYieldStopped) || errors.Is(err, errStopAfterYieldedError) {
				return
			}
			if err != nil {
				yield(nil, err)
				return
			}
			state = nextState
			if done {
				return
			}
		}
	}
}

func (a *Agent) step(
	ctx agent.InvocationContext,
	state agentRunState,
	yield func(*session.Event, error) bool,
) (bool, agentRunState, error) {
	if err := validateInvocationContext(ctx); err != nil {
		return false, state, err
	}
	if len(state.hooks) == 0 {
		state.hooks = ctx.Policies()
	}
	done, err := a.runOneTurn(ctx, &state, yield)
	if err != nil {
		return false, state, err
	}
	return done, state, nil
}

func validateInvocationContext(ctx agent.InvocationContext) error {
	if ctx == nil {
		return fmt.Errorf("llmagent: invocation context is nil")
	}
	if ctx.Model() == nil {
		return fmt.Errorf("llmagent: model is nil")
	}
	return nil
}

func (a *Agent) runOneTurn(
	ctx agent.InvocationContext,
	state *agentRunState,
	yield func(*session.Event, error) bool,
) (bool, error) {
	resp, err := a.generateTurnResponse(ctx, state, yield)
	if err != nil {
		return false, err
	}
	assistantMsg, err := a.emitAssistantTurn(ctx, state.hooks, resp, yield)
	if err != nil {
		return false, err
	}
	if len(assistantMsg.ToolCalls) == 0 {
		return true, nil
	}
	if err := a.executeToolCalls(ctx, state, assistantMsg.ToolCalls, yield); err != nil {
		return false, err
	}
	return false, nil
}

func (a *Agent) generateTurnResponse(
	ctx agent.InvocationContext,
	state *agentRunState,
	yield func(*session.Event, error) bool,
) (*model.Response, error) {
	toolDecls := tool.Declarations(ctx.Tools())
	in, err := policy.ApplyBeforeModel(ctx, state.hooks, policy.ModelInput{
		Messages: eventview.Messages(ctx.Events(), a.cfg.SystemPrompt, a.toolResultSanitizer),
		Tools:    toolDecls,
	})
	if err != nil {
		return nil, err
	}
	req := &model.Request{
		Messages:  in.Messages,
		Tools:     in.Tools,
		Stream:    a.cfg.StreamModel,
		Reasoning: a.cfg.Reasoning,
	}
	resp, err := a.generateWithRetry(ctx, req, func(partial *model.Response) error {
		return a.emitPartialResponse(partial, yield)
	}, func(attempt int, maxRetries int, delay time.Duration, cause error) error {
		ev := session.MarkNotice(&session.Event{
			ID:   newEventID(),
			Time: time.Now(),
		}, session.NoticeLevelWarn, retryWarningText(attempt, maxRetries, delay, cause))
		if !yield(ev, nil) {
			return errYieldStopped
		}
		return nil
	})
	if err != nil {
		if interrupted := interruptedResponseError(err); interrupted != nil && !shouldSuppressInterruptedResponseWarning(interrupted) {
			ev := session.MarkNotice(&session.Event{
				ID:   newEventID(),
				Time: time.Now(),
			}, session.NoticeLevelWarn, interruptedResponseWarning(interrupted))
			if !yield(ev, nil) {
				return nil, errYieldStopped
			}
		}
		return nil, err
	}
	return resp, nil
}

func (a *Agent) emitPartialResponse(partial *model.Response, yield func(*session.Event, error) bool) error {
	if partial == nil || !a.cfg.EmitPartialEvents || !partial.Partial {
		return nil
	}
	if partial.Message.Reasoning != "" {
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
	if partial.Message.Text != "" {
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
}

func (a *Agent) emitAssistantTurn(
	ctx agent.InvocationContext,
	hooks []policy.Hook,
	resp *model.Response,
	yield func(*session.Event, error) bool,
) (model.Message, error) {
	out, err := policy.ApplyBeforeOutput(ctx, hooks, policy.Output{Message: resp.Message})
	if err != nil {
		return model.Message{}, err
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
		return model.Message{}, errYieldStopped
	}
	return assistantMsg, nil
}

func (a *Agent) executeToolCalls(
	ctx agent.InvocationContext,
	state *agentRunState,
	toolCalls []model.ToolCall,
	yield func(*session.Event, error) bool,
) error {
	for _, call := range toolCalls {
		if err := a.executeToolCall(ctx, state, call, yield); err != nil {
			return err
		}
	}
	return nil
}

func (a *Agent) executeToolCall(
	ctx agent.InvocationContext,
	state *agentRunState,
	call model.ToolCall,
	yield func(*session.Event, error) bool,
) error {
	args, argErr := resolveToolCallArgs(call)
	if argErr != nil {
		// All arg-parse errors are fed back to the model as tool responses
		// so the model can self-correct, unless the call ID is empty (malformed).
		if strings.TrimSpace(call.ID) == "" {
			return fmt.Errorf("llmagent: invalid tool call %q arguments: %w", call.Name, argErr)
		}
		result := toolArgParseErrorResult(call, argErr)
		toolMsg := model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:     call.ID,
				Name:   call.Name,
				Result: result,
			},
		}
		ev := &session.Event{ID: newEventID(), Time: time.Now(), Message: toolMsg}
		if !yield(ev, nil) {
			return errYieldStopped
		}
		return nil
	}

	t, ok := ctx.Tool(call.Name)
	if !ok {
		execOut := policy.ToolOutput{
			Call: call,
			Args: cloneArgs(args),
			Err:  fmt.Errorf("llmagent: unknown tool %q", call.Name),
		}
		execOut.Result = map[string]any{"error": execOut.Err.Error()}
		finalResult := annotateToolResultMetadata(execOut.Result, execOut.Err)
		toolMsg := model.Message{
			Role: model.RoleTool,
			ToolResponse: &model.ToolResponse{
				ID:     call.ID,
				Name:   call.Name,
				Result: finalResult,
			},
		}
		ev := &session.Event{ID: newEventID(), Time: time.Now(), Message: toolMsg}
		if !yield(ev, nil) {
			return errYieldStopped
		}
		return nil
	}

	capability := toolcap.Of(t)
	toolCtx := toolexec.WithToolCallInfo(context.Context(ctx), call.Name, call.ID)
	beforeIn, err := policy.ApplyBeforeTool(toolCtx, state.hooks, policy.ToolInput{
		Call:       call,
		Args:       cloneArgs(args),
		Capability: capability,
	})
	if err != nil {
		return err
	}
	call = beforeIn.Call
	args = beforeIn.Args
	if args == nil {
		args = map[string]any{}
	}
	decision := policy.NormalizeDecision(beforeIn.Decision)
	toolCtx = toolexec.WithToolCallInfo(context.Context(ctx), call.Name, call.ID)
	toolCtx = policy.WithToolDecision(toolCtx, decision)

	execOut := policy.ToolOutput{
		Call:       call,
		Args:       cloneArgs(args),
		Capability: beforeIn.Capability,
		Decision:   decision,
	}
	if decision.Effect == policy.DecisionEffectDeny {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "tool denied by policy"
		}
		execOut.Err = fmt.Errorf("llmagent: tool %q denied by policy: %s", call.Name, reason)
		execOut.Result = map[string]any{"error": execOut.Err.Error()}
	} else {
		execOut.Capability = toolcap.Of(t)
		result, runErr := t.Run(toolCtx, args)
		execOut.Err = runErr
		if runErr != nil {
			if toolexec.IsApprovalAborted(runErr) {
				if !yield(nil, runErr) {
					return errYieldStopped
				}
				return errStopAfterYieldedError
			}
			execOut.Result = map[string]any{"error": runErr.Error()}
		} else {
			execOut.Result = result
		}
	}
	if len(execOut.Capability.Operations) == 0 && execOut.Capability.Risk == "" {
		execOut.Capability = beforeIn.Capability
	}

	afterOut, err := policy.ApplyAfterTool(toolCtx, state.hooks, execOut)
	if err != nil {
		return err
	}
	truncatedResult, truncationInfo := tool.TruncateMap(afterOut.Result, a.cfg.ToolTruncation)
	finalResult := tool.AddTruncationMeta(truncatedResult, truncationInfo)
	finalResult = annotateToolResultMetadata(finalResult, afterOut.Err)
	toolMsg := model.Message{
		Role:         model.RoleTool,
		ToolResponse: &model.ToolResponse{ID: afterOut.Call.ID, Name: afterOut.Call.Name, Result: finalResult},
	}
	ev := &session.Event{ID: newEventID(), Time: time.Now(), Message: toolMsg}
	if !yield(ev, nil) {
		return errYieldStopped
	}
	return nil
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
	return eventview.Messages(session.NewEvents(events), systemPrompt, sanitizer)
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

func resolveToolCallArgs(call model.ToolCall) (map[string]any, error) {
	raw := strings.TrimSpace(call.Args)
	if raw == "" {
		return map[string]any{}, nil
	}
	return model.ParseToolCallArgs(raw)
}

func toolArgParseErrorResult(call model.ToolCall, err error) map[string]any {
	return map[string]any{
		"error":       fmt.Sprintf("invalid tool call %q arguments: %v", call.Name, err),
		"error_type":  "invalid_tool_call_args",
		"recoverable": true,
		"hint":        fmt.Sprintf("tool call %q arguments appear truncated (incomplete JSON); try reducing the argument size or splitting into smaller operations", call.Name),
	}
}

func cloneArgs(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

var errYieldStopped = errors.New("llmagent: downstream yield stopped")
var errStopAfterYieldedError = errors.New("llmagent: stop after yielded error")

var (
	modelRequestMaxRetries     = 5
	modelRetryBaseDelay        = 1 * time.Second
	modelRetryMaxDelay         = 3 * time.Minute
	rateLimitRequestMaxRetries = 7
	rateLimitRetryBaseDelay    = 5 * time.Second
	rateLimitRetryMaxDelay     = 3 * time.Minute
)

type retryPolicy struct {
	maxRetries  int
	baseDelay   time.Duration
	maxDelay    time.Duration
	rateLimited bool
}

var errEmptyModelResponse = errors.New("llmagent: empty model response")

type interruptedModelResponseError struct {
	cause          error
	partialEmitted bool
}

func (e *interruptedModelResponseError) Error() string {
	if e == nil {
		return "llmagent: interrupted model response"
	}
	switch {
	case e.partialEmitted && e.cause != nil:
		return fmt.Sprintf("llmagent: model response interrupted after partial output: %v", e.cause)
	case e.partialEmitted:
		return "llmagent: model response interrupted after partial output"
	case e.cause != nil:
		return fmt.Sprintf("llmagent: incomplete model response: %v", e.cause)
	default:
		return "llmagent: incomplete model response"
	}
}

func (e *interruptedModelResponseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

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
	onRetry func(attempt int, maxRetries int, delay time.Duration, cause error) error,
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
			switch {
			case resp == nil:
				err = errEmptyModelResponse
			case resp.Partial || !resp.TurnComplete:
				err = &interruptedModelResponseError{
					cause:          fmt.Errorf("model returned without completing the turn"),
					partialEmitted: emittedPartial,
				}
			default:
				return resp, nil
			}
		}
		if emittedPartial {
			if interruptedResponseError(err) == nil {
				err = &interruptedModelResponseError{
					cause:          err,
					partialEmitted: true,
				}
			}
			return nil, err
		}
		if errors.Is(err, errYieldStopped) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if isNonRetryableHTTPError(err) {
			return nil, err
		}
		policy := retryPolicyForError(err)
		if retries >= policy.maxRetries {
			if policy.rateLimited {
				return nil, fmt.Errorf("llmagent: model request hit rate limits after %d retries: %w", policy.maxRetries, err)
			}
			return nil, fmt.Errorf("llmagent: model request failed after %d retries: %w", policy.maxRetries, err)
		}
		delay := retryDelayForAttemptWithBounds(retries, policy.baseDelay, policy.maxDelay)
		if onRetry != nil {
			if retryErr := onRetry(retries+1, policy.maxRetries, delay, err); retryErr != nil {
				return nil, retryErr
			}
		}
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

func formatRetryDelay(delay time.Duration) string {
	if delay <= 0 {
		return "0s"
	}
	if delay < time.Second {
		return delay.Round(100 * time.Millisecond).String()
	}
	return delay.Round(time.Second).String()
}

func retryDelayForAttempt(retry int) time.Duration {
	return retryDelayForAttemptWithBounds(retry, modelRetryBaseDelay, modelRetryMaxDelay)
}

func retryDelayForAttemptWithBounds(retry int, baseDelay, maxDelay time.Duration) time.Duration {
	if retry < 0 {
		retry = 0
	}
	if baseDelay <= 0 {
		baseDelay = time.Second
	}
	if maxDelay <= 0 {
		maxDelay = baseDelay
	}
	delay := baseDelay
	for i := 0; i < retry; i++ {
		delay *= 2
		if delay >= maxDelay {
			return maxDelay
		}
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func retryPolicyForError(err error) retryPolicy {
	if isRateLimitError(err) {
		return retryPolicy{
			maxRetries:  rateLimitRequestMaxRetries,
			baseDelay:   rateLimitRetryBaseDelay,
			maxDelay:    rateLimitRetryMaxDelay,
			rateLimited: true,
		}
	}
	return retryPolicy{
		maxRetries: modelRequestMaxRetries,
		baseDelay:  modelRetryBaseDelay,
		maxDelay:   modelRetryMaxDelay,
	}
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	if text == "" {
		return false
	}
	return strings.Contains(text, "http status 429") ||
		strings.Contains(text, "rate limit") ||
		strings.Contains(text, "ratelimit") ||
		strings.Contains(text, "too many requests")
}

func isNonRetryableHTTPError(err error) bool {
	status, ok := httpStatusCodeFromError(err)
	if !ok {
		return false
	}
	if status < 400 || status >= 500 {
		return false
	}
	switch status {
	case 408, 409, 429:
		return false
	default:
		return true
	}
}

func httpStatusCodeFromError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	text := strings.TrimSpace(err.Error())
	idx := strings.Index(strings.ToLower(text), "http status ")
	if idx < 0 {
		return 0, false
	}
	rest := text[idx+len("http status "):]
	if rest == "" {
		return 0, false
	}
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	var status int
	if _, scanErr := fmt.Sscanf(rest[:end], "%d", &status); scanErr != nil || status <= 0 {
		return 0, false
	}
	return status, true
}

func retryWarningText(attempt int, maxRetries int, delay time.Duration, cause error) string {
	if isRateLimitError(cause) {
		return fmt.Sprintf(
			"warn: llm request hit rate limits (HTTP 429 / Too Many Requests), retrying in %s (%d/%d). Waiting longer before retrying.",
			formatRetryDelay(delay),
			attempt,
			maxRetries,
		)
	}
	summary := summarizeRetryCause(cause)
	return fmt.Sprintf("warn: llm request failed, retrying in %s (%d/%d): %s", formatRetryDelay(delay), attempt, maxRetries, summary)
}

func summarizeRetryCause(err error) string {
	if err == nil {
		return "unknown error"
	}
	text := strings.TrimSpace(err.Error())
	if text == "" {
		return "unknown error"
	}
	bodyIndex := strings.Index(text, " body=")
	if bodyIndex < 0 {
		return text
	}
	prefix := strings.TrimSpace(text[:bodyIndex])
	body := strings.TrimSpace(text[bodyIndex+len(" body="):])
	bodySummary := summarizeRetryBody(body)
	switch {
	case bodySummary == "":
		return text
	case prefix == "":
		return bodySummary
	default:
		return prefix + ": " + bodySummary
	}
}

func summarizeRetryBody(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	detail, errType := extractRetryErrorDetails(payload)
	if detail == "" {
		return errType
	}
	if errType == "" || strings.Contains(strings.ToLower(detail), strings.ToLower(errType)) {
		return detail
	}
	return detail + " [" + errType + "]"
}

func extractRetryErrorDetails(payload map[string]any) (detail string, errType string) {
	if len(payload) == 0 {
		return "", ""
	}
	detail = firstString(payload["detail"], payload["message"], payload["error_description"])
	errPayload, _ := payload["error"].(map[string]any)
	metadata, _ := errPayload["metadata"].(map[string]any)
	if detail == "" {
		detail = firstString(
			errPayload["message"],
			metadata["reason"],
			metadata["raw_message"],
			metadata["raw_error"],
		)
	}
	if raw := firstString(metadata["raw"]); raw != "" {
		rawDetail := summarizeRetryBody(raw)
		if detail == "" || strings.EqualFold(detail, "provider returned error") {
			detail = rawDetail
		}
	}
	errType = firstString(
		errPayload["type"],
		errPayload["code"],
		payload["code"],
		metadata["provider_name"],
		metadata["upstream_provider"],
	)
	if provider := firstString(metadata["provider_name"], metadata["upstream_provider"]); provider != "" && detail != "" {
		lowerDetail := strings.ToLower(detail)
		lowerProvider := strings.ToLower(provider)
		if !strings.Contains(lowerDetail, lowerProvider) {
			detail += " (provider: " + provider + ")"
		}
	}
	return detail, errType
}

func firstString(values ...any) string {
	for _, value := range values {
		text, ok := value.(string)
		if ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func interruptedResponseError(err error) *interruptedModelResponseError {
	if err == nil {
		return nil
	}
	var target *interruptedModelResponseError
	if errors.As(err, &target) {
		return target
	}
	return nil
}

func interruptedResponseWarning(err *interruptedModelResponseError) string {
	if err == nil {
		return "warn: model response was interrupted before the turn completed."
	}
	cause := summarizeRetryCause(err.cause)
	if err.partialEmitted {
		return fmt.Sprintf(
			"warn: model response was interrupted before completion. Some partial output was already shown, so automatic retry was skipped to avoid duplicate content. Cause: %s. You can send /continue to resume.",
			cause,
		)
	}
	return fmt.Sprintf(
		"warn: model returned incomplete output. The request was retried automatically when safe. Last cause: %s.",
		cause,
	)
}

func shouldSuppressInterruptedResponseWarning(err *interruptedModelResponseError) bool {
	if err == nil {
		return false
	}
	return errors.Is(err.cause, context.Canceled) || errors.Is(err.cause, context.DeadlineExceeded)
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
