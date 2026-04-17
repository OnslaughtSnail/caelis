package gateway

import (
	"strings"

	sdkmodel "github.com/OnslaughtSnail/caelis/sdk/model"
	sdksession "github.com/OnslaughtSnail/caelis/sdk/session"
)

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
	if !ok {
		return nil
	}
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

func AssistantText(event Event) string {
	sessionEvent := event.SessionEvent
	if sessionEvent == nil {
		return ""
	}
	if event.Kind != EventKindAssistantMessage && event.Kind != EventKindSessionEvent {
		return ""
	}
	if sessionEvent.Message != nil {
		if text := strings.TrimSpace(sessionEvent.Message.TextContent()); text != "" {
			return text
		}
	}
	if sdksession.EventTypeOf(sessionEvent) == sdksession.EventTypeAssistant {
		return strings.TrimSpace(sessionEvent.Text)
	}
	if sessionEvent.Message != nil && sessionEvent.Message.Role == sdkmodel.RoleAssistant {
		return strings.TrimSpace(sessionEvent.Message.TextContent())
	}
	return ""
}

func PromptTokens(event Event) int {
	if event.Usage == nil {
		return 0
	}
	return event.Usage.PromptTokens
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
