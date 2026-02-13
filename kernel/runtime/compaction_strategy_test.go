package runtime

import (
	"context"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

type captureCompactionStrategy struct {
	calls int
	last  CompactionSummarizeInput
	text  string
}

func (s *captureCompactionStrategy) Summarize(
	ctx context.Context,
	llm model.LLM,
	in CompactionSummarizeInput,
) (CompactionSummarizeResult, error) {
	_ = ctx
	_ = llm
	s.calls++
	s.last = in
	return CompactionSummarizeResult{
		Text:             s.text,
		SummarizedEvents: len(in.Events),
	}, nil
}

func TestRuntime_Compact_UsesWindowEventsAndCustomStrategy(t *testing.T) {
	store := inmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-compact-window"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	appendEvent := func(ev *session.Event) {
		t.Helper()
		if err := store.AppendEvent(context.Background(), sess, ev); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(&session.Event{ID: "old_user", Message: model.Message{Role: model.RoleUser, Text: "old user"}})
	appendEvent(&session.Event{ID: "old_assistant", Message: model.Message{Role: model.RoleAssistant, Text: "old assistant"}})
	appendEvent(&session.Event{
		ID:      "compact_1",
		Message: model.Message{Role: model.RoleSystem, Text: "summary 1"},
		Meta: map[string]any{
			metaKind: metaKindCompaction,
		},
	})
	appendEvent(&session.Event{ID: "new_user_1", Message: model.Message{Role: model.RoleUser, Text: "new user 1"}})
	appendEvent(&session.Event{ID: "new_assistant_1", Message: model.Message{Role: model.RoleAssistant, Text: "new assistant 1"}})
	appendEvent(&session.Event{ID: "new_user_2", Message: model.Message{Role: model.RoleUser, Text: "new user 2"}})

	strategy := &captureCompactionStrategy{text: "custom summary"}
	rt, err := New(Config{
		Store: store,
		Compaction: CompactionConfig{
			PreserveRecentTurns: 1,
			Strategy:            strategy,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ev, err := rt.Compact(context.Background(), CompactRequest{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
		Model:     newRuntimeTestLLM("fake"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ev == nil {
		t.Fatal("expected compaction event")
	}
	if ev.Message.Text != "custom summary" {
		t.Fatalf("expected custom summary text, got %q", ev.Message.Text)
	}
	if strategy.calls != 1 {
		t.Fatalf("expected strategy called once, got %d", strategy.calls)
	}
	for _, one := range strategy.last.Events {
		if one == nil {
			continue
		}
		if one.ID == "old_user" || one.ID == "old_assistant" {
			t.Fatalf("expected old pre-compaction events excluded, got %q in strategy input", one.ID)
		}
	}
}
