package runtime

import (
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestFinalAssistantText_UsesTrailingAssistantRun(t *testing.T) {
	events := []*session.Event{
		{
			Time:    time.Now(),
			Message: model.NewTextMessage(model.RoleUser, "start"),
		},
		{
			Time:    time.Now(),
			Message: model.NewTextMessage(model.RoleAssistant, "first chunk"),
		},
		{
			Time:    time.Now(),
			Message: model.NewTextMessage(model.RoleAssistant, "second chunk"),
		},
	}

	if got := FinalAssistantText(events); got != "first chunk\nsecond chunk" {
		t.Fatalf("FinalAssistantText() = %q, want full trailing assistant output", got)
	}
}

func TestFinalAssistantText_IgnoresTransientAssistantEvents(t *testing.T) {
	partial := &session.Event{
		Time:    time.Now(),
		Message: model.NewTextMessage(model.RoleAssistant, "partial"),
		Meta: map[string]any{
			"partial": true,
			"channel": "answer",
		},
	}
	final := &session.Event{
		Time:    time.Now(),
		Message: model.NewTextMessage(model.RoleAssistant, "final answer"),
	}

	if got := FinalAssistantText([]*session.Event{partial, final}); got != "final answer" {
		t.Fatalf("FinalAssistantText() = %q, want final canonical assistant text", got)
	}
}
