package runtime

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
)

type overflowThenSuccessAgent struct {
	calls int
}

func (a *overflowThenSuccessAgent) Name() string { return "overflow-then-success" }

func (a *overflowThenSuccessAgent) Run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	_ = ctx
	return func(yield func(*session.Event, error) bool) {
		if a.calls == 0 {
			a.calls++
			yield(nil, errors.New("context length exceeded"))
			return
		}
		a.calls++
		yield(&session.Event{
			Message: model.Message{
				Role: model.RoleAssistant,
				Text: "final",
			},
		}, nil)
	}
}

func TestNormalizeCompactionConfig_Defaults(t *testing.T) {
	cfg := normalizeCompactionConfig(CompactionConfig{})
	if cfg.WatermarkRatio != 0.7 {
		t.Fatalf("expected default watermark=0.7, got %f", cfg.WatermarkRatio)
	}
	if cfg.DefaultContextWindowTokens != 65536 {
		t.Fatalf("expected default context window 65536, got %d", cfg.DefaultContextWindowTokens)
	}
	if cfg.PreserveRecentTurns != 2 {
		t.Fatalf("expected default preserve turns=2, got %d", cfg.PreserveRecentTurns)
	}
	if cfg.MaxModelSummaryRetries != 3 {
		t.Fatalf("expected default summary retries=3, got %d", cfg.MaxModelSummaryRetries)
	}
}

func TestRuntimeRun_AutoCompaction(t *testing.T) {
	store := inmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-auto-compact"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := seedCompactionHistory(store, sess); err != nil {
		t.Fatal(err)
	}

	rt, err := New(Config{
		Store: store,
		Compaction: CompactionConfig{
			WatermarkRatio:      0.01,
			MinWatermarkRatio:   0.01,
			PreserveRecentTurns: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	llm := newRuntimeTestLLM("fake")
	foundAutoCompaction := false
	for ev, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:             sess.AppName,
		UserID:              sess.UserID,
		SessionID:           sess.ID,
		Input:               "new turn",
		Agent:               fixedAgent{},
		Model:               llm,
		ContextWindowTokens: 2048,
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		if compactionTrigger(ev) == triggerAuto {
			foundAutoCompaction = true
		}
	}
	if !foundAutoCompaction {
		t.Fatalf("expected auto compaction event in runtime run")
	}
}

func TestRuntimeRun_OverflowCompactionRetry(t *testing.T) {
	store := inmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-overflow-compact"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := seedCompactionHistory(store, sess); err != nil {
		t.Fatal(err)
	}

	rt, err := New(Config{
		Store: store,
		Compaction: CompactionConfig{
			WatermarkRatio:      0.99,
			PreserveRecentTurns: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	agentImpl := &overflowThenSuccessAgent{}

	foundOverflowCompaction := false
	foundFinalAssistant := false
	for ev, runErr := range rt.Run(context.Background(), RunRequest{
		AppName:             sess.AppName,
		UserID:              sess.UserID,
		SessionID:           sess.ID,
		Input:               "trigger overflow branch",
		Agent:               agentImpl,
		Model:               llm,
		ContextWindowTokens: 2048,
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		if compactionTrigger(ev) == triggerOverflowRecovery {
			foundOverflowCompaction = true
		}
		if ev != nil && ev.Message.Role == model.RoleAssistant && ev.Message.Text == "final" {
			foundFinalAssistant = true
		}
	}

	if !foundOverflowCompaction {
		t.Fatalf("expected overflow recovery compaction event")
	}
	if !foundFinalAssistant {
		t.Fatalf("expected assistant event after retry")
	}
	if agentImpl.calls != 2 {
		t.Fatalf("expected agent to run twice, got %d", agentImpl.calls)
	}
}

func seedCompactionHistory(store *inmemory.Store, sess *session.Session) error {
	err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:        "seed-user-1",
		SessionID: sess.ID,
		Time:      time.Now().Add(-2 * time.Minute),
		Message: model.Message{
			Role: model.RoleUser,
			Text: "first question",
		},
	})
	if err != nil {
		return err
	}
	return store.AppendEvent(context.Background(), sess, &session.Event{
		ID:        "seed-assistant-1",
		SessionID: sess.ID,
		Time:      time.Now().Add(-1 * time.Minute),
		Message: model.Message{
			Role: model.RoleAssistant,
			Text: "first answer",
		},
	})
}

func compactionTrigger(ev *session.Event) string {
	if ev == nil || ev.Meta == nil {
		return ""
	}
	if !isCompactionEvent(ev) {
		return ""
	}
	raw, ok := ev.Meta[metaCompaction]
	if !ok {
		return ""
	}
	meta, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	trigger, _ := meta["trigger"].(string)
	return trigger
}
