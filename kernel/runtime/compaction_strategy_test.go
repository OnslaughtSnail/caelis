package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	taskinmemory "github.com/OnslaughtSnail/caelis/kernel/task/inmemory"
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
	appendEvent(&session.Event{ID: "old_user", Message: model.NewTextMessage(model.RoleUser, "old user")})
	appendEvent(&session.Event{ID: "old_assistant", Message: model.NewTextMessage(model.RoleAssistant, "old assistant")})
	appendEvent(&session.Event{
		ID:      "compact_1",
		Message: model.NewTextMessage(model.RoleSystem, "summary 1"),
		Meta: map[string]any{
			metaKind: metaKindCompaction,
		},
	})
	appendEvent(&session.Event{ID: "new_user_1", Message: model.NewTextMessage(model.RoleUser, "new user 1")})
	appendEvent(&session.Event{ID: "new_assistant_1", Message: model.NewTextMessage(model.RoleAssistant, "new assistant 1")})
	appendEvent(&session.Event{ID: "new_user_2", Message: model.NewTextMessage(model.RoleUser, "new user 2")})

	strategy := &captureCompactionStrategy{text: "custom summary"}
	rt, err := New(Config{
		Store: store,
		Compaction: CompactionConfig{
			Strategy: strategy,
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
	if ev.Message.Role != model.RoleUser {
		t.Fatalf("expected compaction role=user, got %q", ev.Message.Role)
	}
	if !strings.Contains(ev.Message.TextContent(), "custom summary") {
		t.Fatalf("expected custom summary body in compaction text, got %q", ev.Message.TextContent())
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

func TestRuntime_Compact_UsesCustomSummaryFormatter(t *testing.T) {
	store := inmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-compact-formatter"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	appendEvent := func(ev *session.Event) {
		t.Helper()
		if err := store.AppendEvent(context.Background(), sess, ev); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(&session.Event{ID: "new_user_1", Message: model.NewTextMessage(model.RoleUser, "new user 1")})
	appendEvent(&session.Event{ID: "new_assistant_1", Message: model.NewTextMessage(model.RoleAssistant, "new assistant 1")})

	strategy := &captureCompactionStrategy{text: "custom summary"}
	rt, err := New(Config{
		Store: store,
		Compaction: CompactionConfig{
			Strategy: strategy,
			SummaryFormatter: func(summary string) string {
				summary = strings.TrimSpace(summary)
				if summary == "" {
					return ""
				}
				return "CHECKPOINT:\n\n" + summary
			},
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
	if !strings.HasPrefix(ev.Message.TextContent(), "CHECKPOINT:\n\n") {
		t.Fatalf("expected formatter prefix, got %q", ev.Message.TextContent())
	}
}

func TestRuntime_Compact_InjectsRuntimeStateIntoCompactionInput(t *testing.T) {
	store := inmemory.New()
	taskStore := taskinmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-compact-runtime-state"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceState(context.Background(), sess, map[string]any{
		"plan": map[string]any{
			"version": 1,
			"entries": []any{
				map[string]any{"content": "Inspect prompt pipeline", "status": "completed"},
				map[string]any{"content": "Implement compaction runtime injection", "status": "in_progress"},
			},
		},
		runtimeLifecycleStateKey: map[string]any{
			"status":     string(RunLifecycleStatusWaitingApproval),
			"phase":      "tool",
			"error":      "approval pending for shell command",
			"error_code": "ERR_APPROVAL_REQUIRED",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := taskStore.Upsert(context.Background(), &task.Entry{
		TaskID:    "task-1",
		Kind:      task.KindSpawn,
		Title:     "delegate",
		Session:   task.SessionRef{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID},
		State:     task.StateWaitingInput,
		Running:   true,
		UpdatedAt: time.Now(),
		Result: map[string]any{
			"latest_output": "Need confirmation before continuing",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "u1",
		Message: model.NewTextMessage(model.RoleUser, "continue"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "a1",
		Message: model.NewTextMessage(model.RoleAssistant, "editing prompt files"),
	}); err != nil {
		t.Fatal(err)
	}

	strategy := &captureCompactionStrategy{text: "custom summary"}
	rt, err := New(Config{
		Store:      store,
		TaskStore:  taskStore,
		Compaction: CompactionConfig{Strategy: strategy},
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
	if !strings.Contains(strategy.last.PlanSummary, "Implement compaction runtime injection") {
		t.Fatalf("expected plan summary, got %q", strategy.last.PlanSummary)
	}
	if !strings.Contains(strategy.last.ProgressSummary, "editing prompt files") {
		t.Fatalf("expected progress summary, got %q", strategy.last.ProgressSummary)
	}
	if !strings.Contains(strategy.last.ActiveTasksSummary, "task-1") {
		t.Fatalf("expected active task summary, got %q", strategy.last.ActiveTasksSummary)
	}
	if !strings.Contains(strategy.last.ActiveTasksSummary, "Need confirmation before continuing") {
		t.Fatalf("expected active task preview in runtime summary, got %q", strategy.last.ActiveTasksSummary)
	}
	if !strings.Contains(strategy.last.LatestBlockerSummary, "approval pending for shell command") {
		t.Fatalf("expected blocker summary, got %q", strategy.last.LatestBlockerSummary)
	}
	if !strings.Contains(ev.Message.TextContent(), "<runtime_state>") {
		t.Fatalf("expected injected runtime state block in compaction text, got %q", ev.Message.TextContent())
	}
}
