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
		Message: model.Message{Role: model.RoleAssistant, Text: "hello"},
	}
	if IsTransient(base) {
		t.Fatal("normal conversation event must not be transient")
	}
	if !IsTransient(MarkUIOnly(&Event{ID: "ev-ui", Time: time.Now(), Message: model.Message{Role: model.RoleSystem, Text: "ui"}})) {
		t.Fatal("ui-only event must be transient")
	}
	if !IsTransient(MarkOverlay(&Event{ID: "ev-overlay", Time: time.Now(), Message: model.Message{Role: model.RoleAssistant, Text: "overlay"}})) {
		t.Fatal("overlay event must be transient")
	}
	partial := &Event{ID: "ev-partial", Time: time.Now(), Message: model.Message{Role: model.RoleAssistant, Text: "partial"}}
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
		Message: model.Message{Role: model.RoleAssistant, Text: "hello"},
	}
	if !IsCanonicalHistoryEvent(canonical) {
		t.Fatal("assistant conversation event must be canonical")
	}
	if !IsCanonicalHistoryEvent(&Event{
		ID:      "ev-tool",
		Time:    time.Now(),
		Message: model.Message{Role: model.RoleTool, ToolResponse: &model.ToolResponse{ID: "call-1", Name: "READ", Result: map[string]any{"ok": true}}},
	}) {
		t.Fatal("tool response event must be canonical")
	}
	if IsCanonicalHistoryEvent(nil) {
		t.Fatal("nil event must not be canonical")
	}
	if IsCanonicalHistoryEvent(MarkNotice(&Event{ID: "ev-note", Time: time.Now()}, NoticeLevelWarn, "warn")) {
		t.Fatal("notice event must not be canonical")
	}
	if IsCanonicalHistoryEvent(MarkOverlay(&Event{ID: "ev-overlay", Time: time.Now(), Message: model.Message{Role: model.RoleAssistant, Text: "overlay"}})) {
		t.Fatal("overlay event must not be canonical")
	}
	partial := &Event{ID: "ev-partial", Time: time.Now(), Message: model.Message{Role: model.RoleAssistant, Text: "partial"}}
	SetEventType(partial, EventTypePartialAnswer)
	if IsCanonicalHistoryEvent(partial) {
		t.Fatal("partial event must not be canonical")
	}
	lifecycle := &Event{ID: "ev-life", Time: time.Now(), Message: model.Message{Role: model.RoleSystem, Text: ""}}
	SetEventType(lifecycle, EventTypeLifecycle)
	if IsCanonicalHistoryEvent(lifecycle) {
		t.Fatal("lifecycle event must not be canonical")
	}
}
