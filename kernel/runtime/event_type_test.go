package runtime

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestPrepareEvent_AnnotatesEventType(t *testing.T) {
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-prepare-event-type"}
	ev := &session.Event{
		Message: model.Message{Role: model.RoleAssistant, Text: "ok"},
	}

	prepareEvent(context.Background(), sess, ev)

	if ev.SessionID != sess.ID {
		t.Fatalf("expected session id %q, got %q", sess.ID, ev.SessionID)
	}
	if got := session.EventTypeOf(ev); got != session.EventTypeConversation {
		t.Fatalf("expected conversation event type, got %q", got)
	}
	if got, _ := ev.Meta["event_type"].(string); got != string(session.EventTypeConversation) {
		t.Fatalf("expected persisted event_type metadata, got %#v", ev.Meta["event_type"])
	}
}
