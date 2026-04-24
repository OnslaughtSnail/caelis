package core

import (
	"encoding/json"
	"strings"

	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdkruntime "github.com/OnslaughtSnail/caelis/sdk/runtime"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

func projectSessionEvents(ref sdksession.SessionRef, events []*sdksession.Event) []EventEnvelope {
	if len(events) == 0 {
		return nil
	}
	out := make([]EventEnvelope, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		out = append(out, EventEnvelope{
			Cursor: event.ID,
			Event: Event{
				Kind:         sessionEventKind(event),
				TurnID:       turnIDFromSessionEvent(event),
				OccurredAt:   event.Time,
				SessionRef:   ref,
				Origin:       canonicalOriginFromSessionEvent(ref, event),
				SessionEvent: event,
				Usage:        usageSnapshotFromSessionEvent(event),
				Narrative:    canonicalNarrativePayload(event),
				ToolCall:     canonicalToolCallPayload(event),
				ToolResult:   canonicalToolResultPayload(event),
				Plan:         canonicalPlanPayload(event),
				Participant:  canonicalParticipantPayload(event),
				Lifecycle:    canonicalLifecyclePayload(event),
			},
		})
	}
	return out
}

func replayAfterCursor(events []EventEnvelope, cursor string, limit int) ([]EventEnvelope, error) {
	if len(events) == 0 {
		return nil, nil
	}
	start, err := startIndexAfterCursor(events, cursor)
	if err != nil {
		return nil, err
	}
	out := events[start:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func startIndexAfterCursor(events []EventEnvelope, cursor string) (int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	for i, env := range events {
		if env.Cursor == cursor {
			return i + 1, nil
		}
	}
	return 0, &Error{
		Kind:        KindNotFound,
		Code:        CodeCursorNotFound,
		UserVisible: true,
		Message:     "gateway: cursor not found",
		Detail:      cursor,
	}
}

func turnIDFromSessionEvent(event *sdksession.Event) string {
	if event == nil || event.Scope == nil {
		return ""
	}
	return strings.TrimSpace(event.Scope.TurnID)
}

func sessionEventKind(event *sdksession.Event) EventKind {
	switch sdksession.EventTypeOf(event) {
	case sdksession.EventTypeUser:
		return EventKindUserMessage
	case sdksession.EventTypeAssistant:
		return EventKindAssistantMessage
	case sdksession.EventTypePlan:
		return EventKindPlanUpdate
	case sdksession.EventTypeToolCall:
		return EventKindToolCall
	case sdksession.EventTypeToolResult:
		return EventKindToolResult
	case sdksession.EventTypeParticipant:
		return EventKindParticipant
	case sdksession.EventTypeHandoff:
		return EventKindHandoff
	case sdksession.EventTypeCompact:
		return EventKindCompact
	case sdksession.EventTypeNotice:
		return EventKindNotice
	case sdksession.EventTypeLifecycle:
		return EventKindSessionLifecycle
	case sdksession.EventTypeSystem:
		return EventKindSystemMessage
	default:
		return EventKindSessionEvent
	}
}

func usageSnapshotFromSessionEvent(event *sdksession.Event) *UsageSnapshot {
	if event == nil || event.Meta == nil {
		return nil
	}
	raw, ok := event.Meta["usage"]
	if ok {
		payload, ok := raw.(map[string]any)
		if !ok {
			return nil
		}
		usage := &UsageSnapshot{
			PromptTokens:     intValue(payload["prompt_tokens"]),
			CompletionTokens: intValue(payload["completion_tokens"]),
			TotalTokens:      intValue(payload["total_tokens"]),
		}
		if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
			return nil
		}
		return usage
	}
	usage := &UsageSnapshot{
		PromptTokens:     intValue(event.Meta["prompt_tokens"]),
		CompletionTokens: intValue(event.Meta["completion_tokens"]),
		TotalTokens:      intValue(event.Meta["total_tokens"]),
	}
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 {
		return nil
	}
	return usage
}

func canonicalOriginFromSessionEvent(ref sdksession.SessionRef, event *sdksession.Event) *EventOrigin {
	scope := scopeFromSessionEvent(event)
	participantID := participantIDFromSessionEvent(event)
	participantKind := participantKindFromSessionEvent(event)
	participantSessionID := participantSessionIDFromSessionEvent(event)
	scopeID := canonicalScopeID(ref, scope, participantID, participantSessionID, turnIDFromSessionEvent(event))
	actor := actorIDFromSessionEvent(event)
	if scope == EventScopeMain && scopeID == "" && actor == "" && participantID == "" && participantKind == "" && participantSessionID == "" {
		return nil
	}
	return &EventOrigin{
		Scope:                scope,
		ScopeID:              scopeID,
		Actor:                actor,
		ParticipantID:        participantID,
		ParticipantKind:      participantKind,
		ParticipantSessionID: participantSessionID,
	}
}

func canonicalOriginFromApproval(req *sdkruntime.ApprovalRequest, fallbackRef sdksession.SessionRef, fallbackTurnID string) *EventOrigin {
	if req == nil {
		return nil
	}
	ref := req.SessionRef
	if strings.TrimSpace(ref.SessionID) == "" {
		ref = fallbackRef
	}
	scope := EventScopeMain
	participantID := metadataString(req.Metadata, "participant_id")
	participantKind := metadataString(req.Metadata, "participant_kind")
	participantSessionID := firstNonEmpty(
		metadataString(req.Metadata, "participant_session_id"),
		metadataString(req.Metadata, "session_id"),
	)
	turnID := firstNonEmpty(strings.TrimSpace(req.TurnID), strings.TrimSpace(fallbackTurnID))
	switch {
	case metadataBool(req.Metadata, "subagent"):
		scope = EventScopeSubagent
	case participantID != "" || participantSessionID != "":
		scope = EventScopeParticipant
	case strings.EqualFold(metadataString(req.Metadata, "scope"), string(EventScopeSubagent)):
		scope = EventScopeSubagent
	case strings.EqualFold(metadataString(req.Metadata, "scope"), string(EventScopeParticipant)):
		scope = EventScopeParticipant
	}
	scopeID := firstNonEmpty(
		metadataString(req.Metadata, "scope_id"),
	)
	if scopeID == "" {
		switch scope {
		case EventScopeSubagent:
			scopeID = firstNonEmpty(metadataString(req.Metadata, "task_id"), participantSessionID, participantID)
		case EventScopeParticipant:
			scopeID = firstNonEmpty(participantSessionID, participantID)
		default:
			scopeID = canonicalScopeID(ref, scope, participantID, participantSessionID, turnID)
		}
	}
	if scope == EventScopeMain && scopeID == "" {
		scopeID = canonicalScopeID(ref, scope, participantID, participantSessionID, turnID)
	}
	return &EventOrigin{
		Scope:                scope,
		ScopeID:              scopeID,
		Actor:                metadataString(req.Metadata, "agent"),
		ParticipantID:        participantID,
		ParticipantKind:      participantKind,
		ParticipantSessionID: participantSessionID,
	}
}

func canonicalScopeID(ref sdksession.SessionRef, scope EventScope, participantID string, participantSessionID string, turnID string) string {
	switch scope {
	case EventScopeParticipant, EventScopeSubagent:
		return firstNonEmpty(participantSessionID, participantID, strings.TrimSpace(turnID))
	default:
		return firstNonEmpty(strings.TrimSpace(ref.SessionID), strings.TrimSpace(turnID))
	}
}

func participantKindFromSessionEvent(event *sdksession.Event) string {
	if event == nil || event.Scope == nil {
		return ""
	}
	return strings.TrimSpace(string(event.Scope.Participant.Kind))
}

func participantSessionIDFromSessionEvent(event *sdksession.Event) string {
	if event == nil || event.Scope == nil {
		return ""
	}
	return strings.TrimSpace(event.Scope.ACP.SessionID)
}

func metadataString(meta map[string]any, key string) string {
	if len(meta) == 0 {
		return ""
	}
	value, ok := meta[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func metadataBool(meta map[string]any, key string) bool {
	if len(meta) == 0 {
		return false
	}
	value, ok := meta[key]
	if !ok {
		return false
	}
	flag, ok := value.(bool)
	return ok && flag
}

func AssistantText(event Event) string {
	if event.Narrative != nil && event.Narrative.Role == NarrativeRoleAssistant {
		return strings.TrimSpace(event.Narrative.Text)
	}
	sessionEvent := event.SessionEvent
	if sessionEvent == nil {
		return ""
	}
	if event.Kind != EventKindAssistantMessage && event.Kind != EventKindSessionEvent {
		return ""
	}
	if sessionEvent.Message != nil {
		if text := assistantTextFromSessionEvent(sessionEvent); text != "" {
			return text
		}
	}
	if sdksession.EventTypeOf(sessionEvent) == sdksession.EventTypeAssistant {
		return strings.TrimSpace(sessionEvent.Text)
	}
	if sessionEvent.Message != nil && sessionEvent.Message.Role == sdkmodel.RoleAssistant {
		return assistantTextFromSessionEvent(sessionEvent)
	}
	return ""
}

func PromptTokens(event Event) int {
	if event.Usage == nil {
		return 0
	}
	return event.Usage.PromptTokens
}

func canonicalNarrativePayload(event *sdksession.Event) *NarrativePayload {
	if event == nil {
		return nil
	}
	payload := &NarrativePayload{
		Actor:         actorIDFromSessionEvent(event),
		ReasoningText: reasoningTextFromSessionEvent(event),
		Visibility:    string(event.Visibility),
		UpdateType:    updateTypeFromSessionEvent(event),
		Scope:         scopeFromSessionEvent(event),
		ParticipantID: participantIDFromSessionEvent(event),
	}
	switch sdksession.EventTypeOf(event) {
	case sdksession.EventTypeUser:
		payload.Role = NarrativeRoleUser
		payload.Text = strings.TrimSpace(event.Text)
		payload.Final = true
	case sdksession.EventTypeAssistant:
		payload.Role = NarrativeRoleAssistant
		payload.Text = assistantTextFromSessionEvent(event)
		payload.Final = event.Visibility != sdksession.VisibilityUIOnly
	case sdksession.EventTypeSystem:
		payload.Role = NarrativeRoleSystem
		payload.Text = strings.TrimSpace(event.Text)
		payload.Final = true
	case sdksession.EventTypeNotice:
		payload.Role = NarrativeRoleNotice
		if notice, ok := sdksession.NoticeOf(event); ok {
			payload.Text = strings.TrimSpace(notice.Text)
		}
		if payload.Text == "" {
			payload.Text = strings.TrimSpace(event.Text)
		}
		payload.Final = true
	default:
		return nil
	}
	if strings.TrimSpace(payload.Text) == "" && strings.TrimSpace(payload.ReasoningText) == "" {
		return nil
	}
	return payload
}

func canonicalToolCallPayload(event *sdksession.Event) *ToolCallPayload {
	if event == nil || sdksession.EventTypeOf(event) != sdksession.EventTypeToolCall {
		return nil
	}
	callID, toolName, rawStatus, argsText, commandPreview := canonicalToolFields(event)
	if callID == "" && toolName == "" && argsText == "" && commandPreview == "" {
		return nil
	}
	return &ToolCallPayload{
		CallID:         callID,
		ToolName:       toolName,
		ArgsText:       argsText,
		CommandPreview: commandPreview,
		Status:         canonicalToolCallStatus(rawStatus),
		Actor:          actorIDFromSessionEvent(event),
		Scope:          scopeFromSessionEvent(event),
		ParticipantID:  participantIDFromSessionEvent(event),
	}
}

func canonicalToolResultPayload(event *sdksession.Event) *ToolResultPayload {
	if event == nil || sdksession.EventTypeOf(event) != sdksession.EventTypeToolResult {
		return nil
	}
	callID, toolName, rawStatus, _, commandPreview := canonicalToolFields(event)
	outputText, isErr := canonicalToolOutput(event)
	if callID == "" && toolName == "" && outputText == "" {
		return nil
	}
	return &ToolResultPayload{
		CallID:         callID,
		ToolName:       toolName,
		OutputText:     outputText,
		CommandPreview: commandPreview,
		Status:         canonicalToolResultStatus(rawStatus, isErr),
		Error:          isErr,
		Actor:          actorIDFromSessionEvent(event),
		Scope:          scopeFromSessionEvent(event),
		ParticipantID:  participantIDFromSessionEvent(event),
	}
}

func canonicalApprovalPayload(req *sdkruntime.ApprovalRequest) *ApprovalPayload {
	if req == nil {
		return nil
	}
	payload := &ApprovalPayload{
		ToolName: strings.TrimSpace(req.Tool.Name),
		Status:   ApprovalStatusPending,
	}
	if payload.ToolName == "" {
		payload.ToolName = strings.TrimSpace(req.Call.Name)
	}
	if req.Approval != nil {
		if toolName := strings.TrimSpace(req.Approval.ToolCall.Name); toolName != "" {
			payload.ToolName = toolName
		}
		payload.CommandPreview = compactJSONFields(req.Approval.ToolCall.RawInput)
		if len(req.Approval.Options) > 0 {
			payload.Options = make([]ApprovalOption, 0, len(req.Approval.Options))
			for _, option := range req.Approval.Options {
				payload.Options = append(payload.Options, ApprovalOption{
					ID:   strings.TrimSpace(option.ID),
					Name: strings.TrimSpace(option.Name),
					Kind: strings.TrimSpace(option.Kind),
				})
			}
		}
	}
	if payload.CommandPreview == "" {
		payload.CommandPreview = commandPreviewFromJSONString(string(req.Call.Input))
	}
	if payload.ToolName == "" && payload.CommandPreview == "" && len(payload.Options) == 0 {
		return nil
	}
	return payload
}

func canonicalPlanPayload(event *sdksession.Event) *PlanPayload {
	if event == nil || event.Protocol == nil || event.Protocol.Plan == nil {
		return nil
	}
	if len(event.Protocol.Plan.Entries) == 0 {
		return nil
	}
	payload := &PlanPayload{Entries: make([]PlanEntryPayload, 0, len(event.Protocol.Plan.Entries))}
	for _, entry := range event.Protocol.Plan.Entries {
		content := strings.TrimSpace(entry.Content)
		status := strings.TrimSpace(entry.Status)
		priority := strings.TrimSpace(entry.Priority)
		if content == "" && status == "" && priority == "" {
			continue
		}
		payload.Entries = append(payload.Entries, PlanEntryPayload{
			Content:  content,
			Status:   status,
			Priority: priority,
		})
	}
	if len(payload.Entries) == 0 {
		return nil
	}
	return payload
}

func canonicalParticipantPayload(event *sdksession.Event) *ParticipantPayload {
	if event == nil || event.Protocol == nil || event.Protocol.Participant == nil {
		return nil
	}
	action := strings.TrimSpace(event.Protocol.Participant.Action)
	if action == "" && (event.Scope == nil || strings.TrimSpace(event.Scope.Participant.ID) == "") {
		return nil
	}
	payload := &ParticipantPayload{
		ParticipantID: participantIDFromSessionEvent(event),
		Action:        ParticipantAction(strings.ToLower(action)),
		Actor:         actorIDFromSessionEvent(event),
		Scope:         scopeFromSessionEvent(event),
	}
	if event.Scope != nil {
		payload.ParticipantKind = strings.TrimSpace(string(event.Scope.Participant.Kind))
		payload.Role = strings.TrimSpace(string(event.Scope.Participant.Role))
		payload.DelegationID = strings.TrimSpace(event.Scope.Participant.DelegationID)
		payload.ParentTurnID = strings.TrimSpace(event.Scope.TurnID)
		payload.SessionID = strings.TrimSpace(event.Scope.ACP.SessionID)
	}
	return payload
}

func canonicalLifecyclePayload(event *sdksession.Event) *LifecyclePayload {
	if event == nil || event.Lifecycle == nil {
		return nil
	}
	status := strings.TrimSpace(event.Lifecycle.Status)
	reason := strings.TrimSpace(event.Lifecycle.Reason)
	if status == "" && reason == "" {
		return nil
	}
	return &LifecyclePayload{
		Status:        canonicalLifecycleStatus(status),
		Reason:        reason,
		Actor:         actorIDFromSessionEvent(event),
		Scope:         scopeFromSessionEvent(event),
		ParticipantID: participantIDFromSessionEvent(event),
	}
}

func assistantTextFromSessionEvent(event *sdksession.Event) string {
	if event == nil {
		return ""
	}
	if event.Message != nil {
		if text := strings.TrimSpace(event.Message.TextContent()); text != "" {
			return text
		}
		if strings.TrimSpace(event.Message.ReasoningText()) != "" {
			return ""
		}
	}
	if updateTypeFromSessionEvent(event) == string(sdksession.ProtocolUpdateTypeAgentThought) {
		return ""
	}
	return strings.TrimSpace(event.Text)
}

func reasoningTextFromSessionEvent(event *sdksession.Event) string {
	if event == nil || event.Message == nil {
		return ""
	}
	return strings.TrimSpace(event.Message.ReasoningText())
}

func updateTypeFromSessionEvent(event *sdksession.Event) string {
	if event == nil || event.Protocol == nil {
		return ""
	}
	return strings.TrimSpace(event.Protocol.UpdateType)
}

func actorIDFromSessionEvent(event *sdksession.Event) string {
	if event == nil {
		return ""
	}
	return strings.TrimSpace(event.Actor.ID)
}

func participantIDFromSessionEvent(event *sdksession.Event) string {
	if event == nil || event.Scope == nil {
		return ""
	}
	return strings.TrimSpace(event.Scope.Participant.ID)
}

func scopeFromSessionEvent(event *sdksession.Event) EventScope {
	if event == nil || event.Scope == nil {
		return EventScopeMain
	}
	if strings.TrimSpace(event.Scope.Participant.ID) != "" {
		if event.Scope.Participant.Kind == sdksession.ParticipantKindSubagent {
			return EventScopeSubagent
		}
		return EventScopeParticipant
	}
	return EventScopeMain
}

func canonicalToolFields(event *sdksession.Event) (callID string, toolName string, status string, argsText string, commandPreview string) {
	if event == nil {
		return "", "", "", "", ""
	}
	if event.Protocol != nil && event.Protocol.ToolCall != nil {
		tool := event.Protocol.ToolCall
		return strings.TrimSpace(tool.ID),
			strings.TrimSpace(tool.Name),
			strings.TrimSpace(tool.Status),
			compactJSONFields(tool.RawInput),
			commandPreviewFromRaw(tool.RawInput)
	}
	if event.Message != nil {
		calls := event.Message.ToolCalls()
		if len(calls) > 0 {
			call := calls[0]
			return strings.TrimSpace(call.ID),
				strings.TrimSpace(call.Name),
				"",
				strings.TrimSpace(call.Args),
				commandPreviewFromJSONString(call.Args)
		}
	}
	return "", "", "", "", ""
}

func canonicalToolOutput(event *sdksession.Event) (string, bool) {
	if event == nil {
		return "", false
	}
	if event.Protocol != nil && event.Protocol.ToolCall != nil {
		tool := event.Protocol.ToolCall
		outputText := compactJSONFields(tool.RawOutput)
		isErr := strings.EqualFold(strings.TrimSpace(tool.Status), "error") || strings.EqualFold(strings.TrimSpace(tool.Status), "failed")
		return outputText, isErr
	}
	if event.Message != nil {
		return strings.TrimSpace(event.Message.TextContent()), false
	}
	return strings.TrimSpace(event.Text), false
}

func compactJSONFields(raw map[string]any) string {
	if len(raw) == 0 {
		return ""
	}
	if cmd, ok := raw["command"].(string); ok && strings.TrimSpace(cmd) != "" {
		return compactStringValue(cmd)
	}
	if path, ok := raw["path"].(string); ok && strings.TrimSpace(path) != "" {
		return strings.TrimSpace(path)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	return compactStringValue(string(data))
}

func commandPreviewFromRaw(raw map[string]any) string {
	if len(raw) == 0 {
		return ""
	}
	if cmd, ok := raw["command"].(string); ok && strings.TrimSpace(cmd) != "" {
		return compactStringValue(cmd)
	}
	if path, ok := raw["path"].(string); ok && strings.TrimSpace(path) != "" {
		return strings.TrimSpace(path)
	}
	return ""
}

func commandPreviewFromJSONString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return compactStringValue(raw)
	}
	if preview := commandPreviewFromRaw(payload); preview != "" {
		return preview
	}
	return compactStringValue(raw)
}

func compactStringValue(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"))
	s = strings.ReplaceAll(s, "\n", " ")
	const maxCompactRunes = 120
	runes := []rune(s)
	if len(runes) > maxCompactRunes {
		return string(runes[:maxCompactRunes-3]) + "..."
	}
	return s
}

func canonicalToolCallStatus(status string) ToolStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "pending", "started":
		return ToolStatusStarted
	case "in_progress", "running":
		return ToolStatusRunning
	case "completed":
		return ToolStatusCompleted
	case "error", "failed":
		return ToolStatusFailed
	default:
		return ToolStatus(strings.TrimSpace(status))
	}
}

func canonicalToolResultStatus(status string, isErr bool) ToolStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "started":
		return ToolStatusStarted
	case "in_progress", "running":
		return ToolStatusRunning
	case "completed":
		return ToolStatusCompleted
	case "error", "failed":
		return ToolStatusFailed
	case "":
		if isErr {
			return ToolStatusFailed
		}
		return ToolStatusCompleted
	default:
		return ToolStatus(strings.TrimSpace(status))
	}
}

func canonicalLifecycleStatus(status string) LifecycleStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return LifecycleStatusRunning
	case "waiting_approval":
		return LifecycleStatusWaitingApproval
	case "interrupted":
		return LifecycleStatusInterrupted
	case "failed":
		return LifecycleStatusFailed
	case "completed":
		return LifecycleStatusCompleted
	default:
		return LifecycleStatus(strings.TrimSpace(status))
	}
}

func intValue(v any) int {
	switch value := v.(type) {
	case int:
		return value
	case int8:
		return int(value)
	case int16:
		return int(value)
	case int32:
		return int(value)
	case int64:
		return int(value)
	case uint:
		return int(value)
	case uint8:
		return int(value)
	case uint16:
		return int(value)
	case uint32:
		return int(value)
	case uint64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	default:
		return 0
	}
}
