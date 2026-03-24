package session

import (
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

func TestEventTypeOf_Inference(t *testing.T) {
	tests := []struct {
		name string
		ev   *Event
		want EventType
	}{
		{
			name: "conversation",
			ev:   &Event{Message: model.NewTextMessage(model.RoleUser, "hi")},
			want: EventTypeConversation,
		},
		{
			name: "system message",
			ev:   &Event{Message: model.NewTextMessage(model.RoleSystem, "internal note")},
			want: EventTypeSystemMessage,
		},
		{
			name: "partial answer",
			ev: &Event{
				Message: model.NewTextMessage(model.RoleAssistant, "hello"),
				Meta:    map[string]any{"partial": true, "channel": "answer"},
			},
			want: EventTypePartialAnswer,
		},
		{
			name: "partial reasoning",
			ev: &Event{
				Message: model.NewReasoningMessage(model.RoleAssistant, "thinking", model.ReasoningVisibilityVisible),
				Meta:    map[string]any{"partial": true, "channel": "reasoning"},
			},
			want: EventTypePartialReasoning,
		},
		{
			name: "overlay",
			ev: MarkOverlay(&Event{
				Message: model.NewTextMessage(model.RoleAssistant, "side answer"),
			}),
			want: EventTypeOverlay,
		},
		{
			name: "overlay partial answer",
			ev: MarkOverlay(&Event{
				Message: model.NewTextMessage(model.RoleAssistant, "side chunk"),
				Meta:    map[string]any{"partial": true, "channel": "answer"},
			}),
			want: EventTypeOverlayPartialAnswer,
		},
		{
			name: "overlay partial reasoning",
			ev: MarkOverlay(&Event{
				Message: model.NewReasoningMessage(model.RoleAssistant, "side thinking", model.ReasoningVisibilityVisible),
				Meta:    map[string]any{"partial": true, "channel": "reasoning"},
			}),
			want: EventTypeOverlayPartialReasoning,
		},
		{
			name: "lifecycle",
			ev: &Event{
				Message: model.Message{Role: model.RoleSystem},
				Meta:    map[string]any{"kind": "lifecycle"},
			},
			want: EventTypeLifecycle,
		},
		{
			name: "notice",
			ev:   MarkNotice(&Event{}, NoticeLevelWarn, "retrying"),
			want: EventTypeNotice,
		},
		{
			name: "compaction",
			ev: &Event{
				Message: model.NewTextMessage(model.RoleUser, "summary"),
				Meta:    map[string]any{"kind": "compaction"},
			},
			want: EventTypeCompaction,
		},
		{
			name: "compaction notice",
			ev: MarkNotice(&Event{
				Meta: map[string]any{"kind": "compaction_notice"},
			}, NoticeLevelNote, "compacted"),
			want: EventTypeCompactionNotice,
		},
		{
			name: "stream resync",
			ev: MarkUIOnly(&Event{
				Message: model.Message{Role: model.RoleSystem},
				Meta:    map[string]any{"kind": "stream_resync"},
			}),
			want: EventTypeStreamResync,
		},
		{
			name: "ui only",
			ev:   MarkUIOnly(&Event{Message: model.Message{Role: model.RoleSystem}}),
			want: EventTypeUIOnly,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EventTypeOf(tt.ev); got != tt.want {
				t.Fatalf("EventTypeOf() = %q, want %q", got, tt.want)
			}
			if tt.ev != nil {
				EnsureEventType(tt.ev)
				stored, _ := tt.ev.Meta[metaEventTypeKey].(string)
				if NormalizeEventType(stored) != tt.want {
					t.Fatalf("expected stored event_type %q, got %#v", tt.want, tt.ev.Meta[metaEventTypeKey])
				}
			}
		})
	}
}

func TestEnsureEventType_AnnotatesLegacyEvent(t *testing.T) {
	ev := &Event{
		Message: model.NewTextMessage(model.RoleAssistant, "hello"),
		Meta:    map[string]any{"partial": true, "channel": "answer"},
	}

	EnsureEventType(ev)

	if got := EventTypeOf(ev); got != EventTypePartialAnswer {
		t.Fatalf("EventTypeOf() = %q, want %q", got, EventTypePartialAnswer)
	}
	if got, _ := ev.Meta[metaEventTypeKey].(string); got != string(EventTypePartialAnswer) {
		t.Fatalf("expected explicit event_type metadata, got %#v", ev.Meta[metaEventTypeKey])
	}
}

func TestPartialChannelOf_UsesExplicitEventType(t *testing.T) {
	ev := &Event{
		Message: model.Message{Role: model.RoleAssistant},
		Meta:    map[string]any{metaEventTypeKey: string(EventTypeOverlayPartialReasoning)},
	}

	if got := PartialChannelOf(ev); got != PartialChannelReasoning {
		t.Fatalf("PartialChannelOf() = %q, want %q", got, PartialChannelReasoning)
	}
	if !IsPartial(ev) {
		t.Fatal("expected explicit overlay partial reasoning event to be partial")
	}
}
