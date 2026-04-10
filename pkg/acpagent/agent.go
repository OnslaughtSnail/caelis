package acpagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/acpclient"
	"github.com/OnslaughtSnail/caelis/internal/acpprojector"
	"github.com/OnslaughtSnail/caelis/kernel/agent"
	compact "github.com/OnslaughtSnail/caelis/kernel/compaction"
	toolexec "github.com/OnslaughtSnail/caelis/kernel/execenv"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/policy"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/sessionstream"
	"github.com/OnslaughtSnail/caelis/pkg/acpmeta"
)

type Config struct {
	ID                string
	Name              string
	Command           string
	Args              []string
	Env               map[string]string
	WorkDir           string
	WorkspaceRoot     string
	SessionCWD        string
	Runtime           toolexec.Runtime
	ClientInfoVersion string
	SystemPrompt      string
	SessionMeta       map[string]any
}

type Agent struct {
	cfg Config
}

const (
	freshSessionSoftTailTokens = 1600
	freshSessionHardTailTokens = 2400
	freshSessionMinTailEvents  = 4
)

type sessionClient interface {
	Initialize(context.Context) (acpclient.InitializeResponse, error)
	NewSession(context.Context, string, map[string]any) (acpclient.NewSessionResponse, error)
	LoadSession(context.Context, string, string, map[string]any) (acpclient.LoadSessionResponse, error)
	PromptParts(context.Context, string, []json.RawMessage, map[string]any) (acpclient.PromptResponse, error)
	Close() error
}

var startACPClient = func(ctx context.Context, cfg acpclient.Config) (sessionClient, error) {
	return acpclient.Start(ctx, cfg)
}

func New(cfg Config) (*Agent, error) {
	cfg.ID = strings.TrimSpace(strings.ToLower(cfg.ID))
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Command = strings.TrimSpace(cfg.Command)
	cfg.WorkDir = strings.TrimSpace(cfg.WorkDir)
	cfg.WorkspaceRoot = strings.TrimSpace(cfg.WorkspaceRoot)
	cfg.SessionCWD = strings.TrimSpace(cfg.SessionCWD)
	cfg.SystemPrompt = strings.TrimSpace(cfg.SystemPrompt)
	cfg.ClientInfoVersion = strings.TrimSpace(cfg.ClientInfoVersion)
	cfg.Args = append([]string(nil), cfg.Args...)
	cfg.Env = cloneStringMap(cfg.Env)
	cfg.SessionMeta = cloneAnyMap(cfg.SessionMeta)
	if cfg.ID == "" {
		return nil, fmt.Errorf("acpagent: id is required")
	}
	if cfg.Name == "" {
		cfg.Name = cfg.ID
	}
	if cfg.Command == "" {
		return nil, fmt.Errorf("acpagent: command is required")
	}
	return &Agent{cfg: cfg}, nil
}

func (a *Agent) Name() string {
	if a == nil {
		return ""
	}
	return a.cfg.Name
}

func (a *Agent) Run(inv agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		if a == nil {
			yield(nil, fmt.Errorf("acpagent: agent is nil"))
			return
		}
		if inv == nil {
			yield(nil, fmt.Errorf("acpagent: invocation context is nil"))
			return
		}

		promptCtx, err := currentPromptContext(inv)
		if err != nil {
			yield(nil, err)
			return
		}
		prompt := promptCtx.Prompt
		if len(prompt) == 0 {
			yield(nil, fmt.Errorf("acpagent: current user prompt is empty"))
			return
		}

		recorder := newTurnRecorder(a.cfg.ID)
		projector := acpprojector.NewLiveProjector()
		onUpdate := func(env acpclient.UpdateEnvelope) {
			for _, item := range projector.Project(env) {
				recorder.Observe(item)
				if live := liveProjectionEvent(item, a.cfg.ID, inv.Overlay()); live != nil {
					sessionstream.Emit(inv, sessionIDFromInvocation(inv), live)
				}
			}
		}

		client, err := startACPClient(inv, acpclient.Config{
			Command:             a.cfg.Command,
			Args:                append([]string(nil), a.cfg.Args...),
			Env:                 cloneStringMap(a.cfg.Env),
			WorkDir:             a.processWorkDir(),
			Runtime:             a.cfg.Runtime,
			Workspace:           a.workspaceRoot(),
			ClientInfo:          acpclient.DefaultClientInfo(a.cfg.ClientInfoVersion),
			OnUpdate:            onUpdate,
			OnPermissionRequest: permissionRequestHandler(inv),
		})
		if err != nil {
			yield(nil, err)
			return
		}
		defer func() { _ = client.Close() }()

		initResp, err := client.Initialize(inv)
		if err != nil {
			yield(nil, err)
			return
		}

		sessionMeta := mergeSessionMeta(a.cfg.SessionMeta, inv.ReadonlyState())
		remoteSessionID, freshSession, warning, err := a.ensureRemoteSession(inv, client, sessionMeta)
		if err != nil {
			yield(nil, err)
			return
		}
		if warning != nil && !yield(warning, nil) {
			return
		}
		if !freshSession {
			projector.SeedNarrative(previousTurnNarrative(inv, promptCtx.EventIndex, a.cfg.ID))
		}

		if !initResp.AgentCapabilities.Prompt.Image {
			prompt = filterImageBlocks(prompt)
		}
		if len(prompt) == 0 {
			yield(nil, fmt.Errorf("acpagent: prompt is empty after capability filtering"))
			return
		}
		runPrompt := prompt
		if freshSession {
			if seed := freshSessionSeed(inv, promptCtx.EventIndex); seed != "" {
				runPrompt = prependTextBlock(runPrompt, seed)
			}
		}
		if freshSession && a.cfg.SystemPrompt != "" {
			runPrompt = prependTextBlock(runPrompt, sessionPrelude(a.cfg.SystemPrompt))
		}
		if inv.Overlay() {
			runPrompt = prependTextBlock(runPrompt, overlayPromptPrefix)
		}

		if _, err := client.PromptParts(inv, remoteSessionID, runPrompt, sessionMeta); err != nil {
			yield(nil, err)
			return
		}

		recorder.Flush()
		for _, ev := range recorder.Events() {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

const overlayPromptPrefix = "This is a side-question turn. Answer from the existing conversation context only and do not invoke tools unless the answer is impossible without them."

func (a *Agent) ensureRemoteSession(ctx context.Context, client sessionClient, sessionMeta map[string]any) (string, bool, *session.Event, error) {
	stateCtx, hasState := session.StateContextFromContext(ctx)
	stored := acpmeta.ControllerSession{}
	if hasState {
		ref, err := acpmeta.ControllerSessionFromStore(ctx, stateCtx.StateStore, stateCtx.Session)
		if err != nil {
			return "", false, nil, err
		}
		stored = ref
	}

	sessionCWD := a.sessionCWD()
	if stored.AgentID == a.cfg.ID && stored.SessionID != "" {
		_, err := client.LoadSession(ctx, stored.SessionID, sessionCWD, sessionMeta)
		if err == nil {
			return stored.SessionID, false, nil, nil
		}
		newResp, newErr := client.NewSession(ctx, sessionCWD, sessionMeta)
		if newErr != nil {
			return "", false, nil, errors.Join(
				fmt.Errorf("acpagent: load controller session %q failed", stored.SessionID),
				err,
				newErr,
			)
		}
		sessionID := strings.TrimSpace(newResp.SessionID)
		if err := a.persistControllerSession(ctx, stateCtx, hasState, sessionID); err != nil {
			return "", false, nil, err
		}
		return sessionID, true, session.MarkNotice(&session.Event{
			Time: time.Now(),
		}, session.NoticeLevelNote, "external ACP controller session was restarted because the previous remote session could not be restored"), nil
	}

	newResp, err := client.NewSession(ctx, sessionCWD, sessionMeta)
	if err != nil {
		return "", false, nil, err
	}
	sessionID := strings.TrimSpace(newResp.SessionID)
	if err := a.persistControllerSession(ctx, stateCtx, hasState, sessionID); err != nil {
		return "", false, nil, err
	}
	return sessionID, true, nil, nil
}

func (a *Agent) persistControllerSession(ctx context.Context, stateCtx session.StateContext, enabled bool, sessionID string) error {
	if !enabled || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	return acpmeta.UpdateControllerSession(ctx, stateCtx.StateStore, stateCtx.Session, func(_ acpmeta.ControllerSession) acpmeta.ControllerSession {
		return acpmeta.ControllerSession{
			AgentID:   a.cfg.ID,
			SessionID: strings.TrimSpace(sessionID),
		}
	})
}

func mergeSessionMeta(base map[string]any, readonly session.ReadonlyState) map[string]any {
	meta := cloneAnyMap(base)
	stateMeta := acpmeta.SessionMetaFromState(readonlyStateSnapshot(readonly))
	if len(stateMeta) == 0 {
		return meta
	}
	if meta == nil {
		return stateMeta
	}
	for key, value := range stateMeta {
		meta[key] = value
	}
	return meta
}

func readonlyStateSnapshot(state session.ReadonlyState) map[string]any {
	if state == nil {
		return nil
	}
	out := map[string]any{}
	for key, value := range state.All() {
		out[key] = value
	}
	return out
}

type promptContext struct {
	Prompt     []json.RawMessage
	EventIndex int
}

func currentPromptContext(inv agent.InvocationContext) (promptContext, error) {
	if inv == nil {
		return promptContext{}, fmt.Errorf("acpagent: invocation context is nil")
	}
	for i := inv.Events().Len() - 1; i >= 0; i-- {
		ev := inv.Events().At(i)
		if ev == nil || ev.Message.Role != model.RoleUser {
			continue
		}
		if session.EventTypeOf(ev) == session.EventTypeCompaction {
			continue
		}
		parts := model.ContentPartsFromParts(ev.Message.Parts)
		return promptContext{
			Prompt:     marshalPromptParts(parts),
			EventIndex: i,
		}, nil
	}
	return promptContext{}, fmt.Errorf("acpagent: latest user message was not found")
}

func marshalPromptParts(parts []model.ContentPart) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case model.ContentPartText:
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			out = append(out, mustMarshalRaw(acpclient.TextContent{
				Type: "text",
				Text: part.Text,
			}))
		case model.ContentPartImage:
			if strings.TrimSpace(part.Data) == "" && strings.TrimSpace(part.MimeType) == "" && strings.TrimSpace(part.FileName) == "" {
				continue
			}
			out = append(out, mustMarshalRaw(acpclient.ImageContent{
				Type:     "image",
				MimeType: strings.TrimSpace(part.MimeType),
				Data:     part.Data,
				Name:     strings.TrimSpace(part.FileName),
			}))
		}
	}
	return out
}

func freshSessionSeed(inv agent.InvocationContext, promptIndex int) string {
	history := contextSeedHistory(inv, promptIndex)
	checkpoint, recent := seedCheckpointAndRecentHistory(history)
	parts := make([]string, 0, 2)
	if checkpoint.HasContent() {
		parts = append(parts, "Local continuation checkpoint:\n\n"+compact.RenderCheckpointMarkdown(checkpoint))
	}
	if transcript := strings.TrimSpace(compact.EventsToTranscript(recent)); transcript != "" {
		label := "Recent local transcript"
		if checkpoint.HasContent() {
			label += " since the last checkpoint"
		}
		parts = append(parts, label+":\n\n"+transcript)
	}
	if len(parts) == 0 {
		return ""
	}
	return "Carry forward the following local Caelis session context into this new ACP session. Treat it as prior conversation state, not as a new user instruction.\n\n" + strings.Join(parts, "\n\n")
}

func contextSeedHistory(inv agent.InvocationContext, promptIndex int) []*session.Event {
	if inv == nil || promptIndex <= 0 {
		return nil
	}
	out := make([]*session.Event, 0, promptIndex)
	for i := 0; i < promptIndex; i++ {
		ev := inv.Events().At(i)
		if ev == nil {
			continue
		}
		if session.EventTypeOf(ev) == session.EventTypeCompaction || session.IsCanonicalHistoryEvent(ev) {
			out = append(out, ev)
		}
	}
	return out
}

func seedCheckpointAndRecentHistory(events []*session.Event) (compact.Checkpoint, []*session.Event) {
	if len(events) == 0 {
		return compact.Checkpoint{}, nil
	}
	var checkpoint compact.Checkpoint
	recent := make([]*session.Event, 0, len(events))
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if session.EventTypeOf(ev) == session.EventTypeCompaction {
			if cp, ok := compact.CheckpointFromEvent(ev); ok {
				checkpoint = compact.MergeCheckpoints(checkpoint, cp, compact.RuntimeState{})
				recent = recent[:0]
			}
			continue
		}
		if !session.IsCanonicalHistoryEvent(ev) {
			continue
		}
		recent = append(recent, ev)
	}
	if len(recent) == 0 {
		return checkpoint, nil
	}
	_, tail := compact.SplitTargetWithOptions(recent, compact.SplitOptions{
		SoftTailTokens: freshSessionSoftTailTokens,
		HardTailTokens: freshSessionHardTailTokens,
		MinTailEvents:  freshSessionMinTailEvents,
	})
	if len(tail) == 0 {
		tail = recent
	}
	return checkpoint, tail
}

func prependTextBlock(prompt []json.RawMessage, text string) []json.RawMessage {
	text = strings.TrimSpace(text)
	if text == "" {
		return append([]json.RawMessage(nil), prompt...)
	}
	out := []json.RawMessage{mustMarshalRaw(acpclient.TextContent{
		Type: "text",
		Text: text,
	})}
	return append(out, prompt...)
}

func filterImageBlocks(prompt []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(prompt))
	for _, block := range prompt {
		if len(block) == 0 {
			continue
		}
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(block, &header); err != nil {
			out = append(out, block)
			continue
		}
		if strings.EqualFold(strings.TrimSpace(header.Type), "image") {
			continue
		}
		out = append(out, block)
	}
	return out
}

func sessionPrelude(systemPrompt string) string {
	return "Persistent operating instructions for this workspace session:\n\n" + strings.TrimSpace(systemPrompt)
}

func liveProjectionEvent(item acpprojector.Projection, agentID string, overlay bool) *session.Event {
	if item.Event == nil {
		return nil
	}
	ev := annotateAgentEvent(session.CloneEvent(item.Event), agentID)
	switch {
	case item.Stream != "":
		if overlay {
			return session.MarkOverlay(ev)
		}
		return ev
	case item.ToolCallID != "":
		return session.MarkUIOnly(ev)
	default:
		return nil
	}
}

type turnRecorder struct {
	agentID string
	mu      sync.Mutex
	events  []*session.Event
}

func newTurnRecorder(agentID string) *turnRecorder {
	return &turnRecorder{agentID: strings.TrimSpace(agentID)}
}

func (r *turnRecorder) Observe(item acpprojector.Projection) {
	if r == nil || item.Event == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case item.Stream == "assistant" || item.Stream == "reasoning":
		r.appendNarrative(item)
	case item.ToolCallID != "" && item.ToolStatus == "":
		ev := session.CloneEvent(item.Event)
		ev.Time = time.Now()
		if ev.Meta == nil {
			ev.Meta = map[string]any{}
		}
		ev.Meta["acp_live_tool_call"] = true
		r.events = append(r.events, annotateAgentEvent(ev, r.agentID))
	case item.ToolStatus == "completed" || item.ToolStatus == "failed":
		ev := session.CloneEvent(item.Event)
		ev.Time = time.Now()
		r.events = append(r.events, annotateAgentEvent(ev, r.agentID))
	}
}

func (r *turnRecorder) appendNarrative(item acpprojector.Projection) {
	text := item.DeltaText
	if strings.TrimSpace(text) == "" {
		return
	}
	if n := len(r.events); n > 0 {
		last := r.events[n-1]
		switch item.Stream {
		case "assistant":
			if canMergeAssistantText(last) {
				last.Message = model.NewTextMessage(model.RoleAssistant, last.Message.TextContent()+text)
				return
			}
		case "reasoning":
			if canMergeReasoningText(last) {
				last.Message = model.NewReasoningMessage(model.RoleAssistant, last.Message.ReasoningText()+text, model.ReasoningVisibilityVisible)
				return
			}
		}
	}
	if ev := narrativeEventFromProjection(item); ev != nil {
		r.events = append(r.events, annotateAgentEvent(ev, r.agentID))
	}
}

func narrativeEventFromProjection(item acpprojector.Projection) *session.Event {
	switch item.Stream {
	case "assistant":
		if item.DeltaText == "" {
			return nil
		}
		return &session.Event{
			Time:    time.Now(),
			Message: model.NewTextMessage(model.RoleAssistant, item.DeltaText),
		}
	case "reasoning":
		if item.DeltaText == "" {
			return nil
		}
		return &session.Event{
			Time:    time.Now(),
			Message: model.NewReasoningMessage(model.RoleAssistant, item.DeltaText, model.ReasoningVisibilityVisible),
		}
	default:
		return nil
	}
}

func canMergeAssistantText(ev *session.Event) bool {
	if ev == nil || ev.Message.Role != model.RoleAssistant {
		return false
	}
	if len(ev.Message.ToolCalls()) > 0 || ev.Message.ToolResponse() != nil {
		return false
	}
	return ev.Message.ReasoningText() == ""
}

func canMergeReasoningText(ev *session.Event) bool {
	if ev == nil || ev.Message.Role != model.RoleAssistant {
		return false
	}
	if len(ev.Message.ToolCalls()) > 0 || ev.Message.ToolResponse() != nil {
		return false
	}
	return ev.Message.TextContent() == ""
}

func (r *turnRecorder) Flush() {
	if r == nil {
		return
	}
}

func (r *turnRecorder) Events() []*session.Event {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*session.Event, 0, len(r.events))
	for _, ev := range r.events {
		out = append(out, session.CloneEvent(ev))
	}
	return out
}

func permissionRequestHandler(inv agent.InvocationContext) func(context.Context, acpclient.RequestPermissionRequest) (acpclient.RequestPermissionResponse, error) {
	invCtx := context.WithoutCancel(inv)
	return func(reqCtx context.Context, req acpclient.RequestPermissionRequest) (acpclient.RequestPermissionResponse, error) {
		if approver, ok := toolexec.ApproverFromContext(invCtx); ok {
			reqCtx = toolexec.WithApprover(reqCtx, approver)
		}
		if authorizer, ok := policy.ToolAuthorizerFromContext(invCtx); ok {
			reqCtx = policy.WithToolAuthorizer(reqCtx, authorizer)
		}
		if requestLooksLikeToolAuthorization(req) {
			if authorizer, ok := policy.ToolAuthorizerFromContext(reqCtx); ok {
				allowed, err := authorizer.AuthorizeTool(reqCtx, authorizationRequestFromACP(req))
				if err != nil {
					return acpclient.RequestPermissionResponse{}, err
				}
				return permissionOutcome(req, allowed), nil
			}
		}
		if approver, ok := toolexec.ApproverFromContext(reqCtx); ok {
			allowed, err := approver.Approve(reqCtx, approvalRequestFromACP(req))
			if err != nil {
				return acpclient.RequestPermissionResponse{}, err
			}
			return permissionOutcome(req, allowed), nil
		}
		if authorizer, ok := policy.ToolAuthorizerFromContext(reqCtx); ok {
			allowed, err := authorizer.AuthorizeTool(reqCtx, authorizationRequestFromACP(req))
			if err != nil {
				return acpclient.RequestPermissionResponse{}, err
			}
			return permissionOutcome(req, allowed), nil
		}
		return permissionOutcome(req, true), nil
	}
}

func approvalRequestFromACP(req acpclient.RequestPermissionRequest) toolexec.ApprovalRequest {
	command := strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "command"))
	title := strings.TrimSpace(derefString(req.ToolCall.Title))
	if title == "" {
		title = "permission"
	}
	return toolexec.ApprovalRequest{
		ToolName: title,
		Action:   strings.TrimSpace(derefString(req.ToolCall.Kind)),
		Command:  command,
		Reason:   strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "path")),
	}
}

func authorizationRequestFromACP(req acpclient.RequestPermissionRequest) policy.ToolAuthorizationRequest {
	title := strings.TrimSpace(derefString(req.ToolCall.Title))
	if title == "" {
		title = "tool"
	}
	path := strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "path"))
	target := strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "target"))
	scope := firstNonEmpty(path, target, strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "command")))
	return policy.ToolAuthorizationRequest{
		ToolName: title,
		Path:     path,
		Target:   target,
		ScopeKey: scope,
	}
}

func requestLooksLikeToolAuthorization(req acpclient.RequestPermissionRequest) bool {
	return strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "path")) != "" ||
		strings.TrimSpace(rawStringField(req.ToolCall.RawInput, "target")) != ""
}

func permissionOutcome(req acpclient.RequestPermissionRequest, allowed bool) acpclient.RequestPermissionResponse {
	return acpclient.PermissionSelectedOutcome(acpclient.SelectPermissionOptionID(req.Options, allowed))
}

func rawStringField(raw any, key string) string {
	if raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case map[string]any:
		return strings.TrimSpace(fmt.Sprint(value[key]))
	case json.RawMessage:
		var decoded map[string]any
		if err := json.Unmarshal(value, &decoded); err == nil {
			return strings.TrimSpace(fmt.Sprint(decoded[key]))
		}
	}
	return ""
}

func annotateAgentEvent(ev *session.Event, agentID string) *session.Event {
	if ev == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ev
	}
	if ev.Meta == nil {
		ev.Meta = map[string]any{}
	}
	ev.Meta["agent_id"] = agentID
	ev.Meta["_ui_agent"] = agentID
	return ev
}

func sessionIDFromInvocation(inv agent.InvocationContext) string {
	if inv == nil || inv.Session() == nil {
		return ""
	}
	return strings.TrimSpace(inv.Session().ID)
}

func (a *Agent) processWorkDir() string {
	return firstNonEmpty(a.cfg.WorkDir, a.cfg.WorkspaceRoot, a.cfg.SessionCWD)
}

func (a *Agent) workspaceRoot() string {
	return firstNonEmpty(a.cfg.WorkspaceRoot, a.cfg.SessionCWD, a.cfg.WorkDir)
}

func (a *Agent) sessionCWD() string {
	return firstNonEmpty(a.cfg.SessionCWD, a.cfg.WorkspaceRoot, a.cfg.WorkDir)
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func mustMarshalRaw(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func previousTurnNarrative(inv agent.InvocationContext, promptIndex int, agentID string) (assistant string, reasoning string) {
	if inv == nil || promptIndex <= 0 || strings.TrimSpace(agentID) == "" {
		return "", ""
	}
	for i := promptIndex - 1; i >= 0; i-- {
		ev := inv.Events().At(i)
		if ev == nil {
			continue
		}
		if ev.Message.Role == model.RoleUser {
			break
		}
		if session.EventTypeOf(ev) == session.EventTypeCompaction || session.IsTransient(ev) || session.IsPartial(ev) || session.IsUIOnly(ev) {
			continue
		}
		if strings.TrimSpace(asString(ev.Meta["agent_id"])) != strings.TrimSpace(agentID) {
			continue
		}
		if assistant == "" {
			assistant = strings.TrimSpace(ev.Message.TextContent())
		}
		if reasoning == "" {
			reasoning = strings.TrimSpace(ev.Message.ReasoningText())
		}
		if assistant != "" && reasoning != "" {
			break
		}
	}
	return assistant, reasoning
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}
