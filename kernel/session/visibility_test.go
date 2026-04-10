package session

import (
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
)

func TestIsTransient(t *testing.T) {
	base := &Event{
		ID:      "ev-1",
		Time:    time.Now(),
		Message: model.NewTextMessage(model.RoleAssistant, "hello"),
	}
	if IsTransient(base) {
		t.Fatal("normal conversation event must not be transient")
	}
	if !IsTransient(MarkUIOnly(&Event{ID: "ev-ui", Time: time.Now(), Message: model.NewTextMessage(model.RoleSystem, "ui")})) {
		t.Fatal("ui-only event must be transient")
	}
	if !IsTransient(MarkOverlay(&Event{ID: "ev-overlay", Time: time.Now(), Message: model.NewTextMessage(model.RoleAssistant, "overlay")})) {
		t.Fatal("overlay event must be transient")
	}
	partial := &Event{ID: "ev-partial", Time: time.Now(), Message: model.NewTextMessage(model.RoleAssistant, "partial")}
	SetEventType(partial, EventTypePartialAnswer)
	if !IsTransient(partial) {
		t.Fatal("partial event must be transient")
	}
	if !IsTransient(MarkNotice(&Event{ID: "ev-note", Time: time.Now()}, NoticeLevelWarn, "warn")) {
		t.Fatal("notice event must be transient")
	}
}

func TestIsCanonicalHistoryEvent(t *testing.T) {
	canonical := &Event{
		ID:      "ev-1",
		Time:    time.Now(),
		Message: model.NewTextMessage(model.RoleAssistant, "hello"),
	}
	if !IsCanonicalHistoryEvent(canonical) {
		t.Fatal("assistant conversation event must be canonical")
	}
	if !IsCanonicalHistoryEvent(&Event{
		ID:      "ev-tool",
		Time:    time.Now(),
		Message: model.MessageFromToolResponse(&model.ToolResponse{ID: "call-1", Name: "READ", Result: map[string]any{"ok": true}}),
	}) {
		t.Fatal("tool response event must be canonical")
	}
	if IsCanonicalHistoryEvent(nil) {
		t.Fatal("nil event must not be canonical")
	}
	if IsCanonicalHistoryEvent(MarkNotice(&Event{ID: "ev-note", Time: time.Now()}, NoticeLevelWarn, "warn")) {
		t.Fatal("notice event must not be canonical")
	}
	if IsCanonicalHistoryEvent(MarkOverlay(&Event{ID: "ev-overlay", Time: time.Now(), Message: model.NewTextMessage(model.RoleAssistant, "overlay")})) {
		t.Fatal("overlay event must not be canonical")
	}
	partial := &Event{ID: "ev-partial", Time: time.Now(), Message: model.NewTextMessage(model.RoleAssistant, "partial")}
	SetEventType(partial, EventTypePartialAnswer)
	if IsCanonicalHistoryEvent(partial) {
		t.Fatal("partial event must not be canonical")
	}
	lifecycle := &Event{ID: "ev-life", Time: time.Now(), Message: model.NewTextMessage(model.RoleSystem, "")}
	SetEventType(lifecycle, EventTypeLifecycle)
	if IsCanonicalHistoryEvent(lifecycle) {
		t.Fatal("lifecycle event must not be canonical")
	}
}

func TestIsInvocationVisibleEvent_ExcludesACPToolProtocolEvents(t *testing.T) {
	evCall := &Event{
		ID:   "ev-acp-call",
		Time: time.Now(),
		Message: model.MessageFromToolCalls(model.RoleAssistant, []model.ToolCall{{
			ID:   "call-1",
			Name: "READ",
			Args: `{"path":"main.go"}`,
		}}, ""),
		Meta: map[string]any{"controller_kind": "acp"},
	}
	if IsInvocationVisibleEvent(evCall) {
		t.Fatal("ACP tool call event must not be invocation-visible")
	}

	evResult := &Event{
		ID:      "ev-acp-result",
		Time:    time.Now(),
		Message: model.MessageFromToolResponse(&model.ToolResponse{ID: "call-1", Name: "READ", Result: map[string]any{"ok": true}}),
		Meta:    map[string]any{"controller_kind": "acp"},
	}
	if IsInvocationVisibleEvent(evResult) {
		t.Fatal("ACP tool result event must not be invocation-visible")
	}

	evText := &Event{
		ID:      "ev-acp-text",
		Time:    time.Now(),
		Message: model.NewTextMessage(model.RoleAssistant, "ACP summary"),
		Meta:    map[string]any{"controller_kind": "acp"},
	}
	if !IsInvocationVisibleEvent(evText) {
		t.Fatal("ACP assistant narrative should remain invocation-visible until handoff replacement lands")
	}
}
