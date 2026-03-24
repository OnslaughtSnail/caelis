package runtime

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/session/inmemory"
	"github.com/OnslaughtSnail/caelis/kernel/tool"
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
			Message: model.NewTextMessage(model.RoleAssistant, "final"),
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
			WatermarkRatio:    0.01,
			MinWatermarkRatio: 0.01,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	llm := newRuntimeTestLLM("fake")
	foundAutoCompaction := false
	foundStartNotice := false
	foundDoneNotice := false
	for ev, runErr := range runEvents(t, rt, context.Background(), RunRequest{
		AppName:             sess.AppName,
		UserID:              sess.UserID,
		SessionID:           sess.ID,
		Input:               "new turn",
		Agent:               fixedAgent{},
		Model:               llm,
		CoreTools:           tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
		ContextWindowTokens: 2048,
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		if compactionTrigger(ev) == triggerAuto {
			foundAutoCompaction = true
		}
		if notice, ok := session.EventNotice(ev); ok {
			if notice.Kind == "compaction_notice" && notice.Text == "compaction.start" {
				foundStartNotice = true
			}
			if notice.Kind == "compaction_notice" && notice.Text == "compaction.done" {
				foundDoneNotice = true
			}
		}
	}
	if !foundAutoCompaction {
		t.Fatalf("expected auto compaction event in runtime run")
	}
	if !foundStartNotice || !foundDoneNotice {
		t.Fatalf("expected visible auto compaction notices, start=%v done=%v", foundStartNotice, foundDoneNotice)
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
			WatermarkRatio: 0.99,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	llm := newRuntimeTestLLM("fake")
	agentImpl := &overflowThenSuccessAgent{}

	foundOverflowCompaction := false
	foundFinalAssistant := false
	foundOverflowNotice := false
	for ev, runErr := range runEvents(t, rt, context.Background(), RunRequest{
		AppName:             sess.AppName,
		UserID:              sess.UserID,
		SessionID:           sess.ID,
		Input:               "trigger overflow branch",
		Agent:               agentImpl,
		Model:               llm,
		CoreTools:           tool.CoreToolsConfig{Runtime: newCoreRuntime(t)},
		ContextWindowTokens: 2048,
	}) {
		if runErr != nil {
			t.Fatal(runErr)
		}
		if compactionTrigger(ev) == triggerOverflowRecovery {
			foundOverflowCompaction = true
		}
		if ev != nil && ev.Message.Role == model.RoleAssistant && ev.Message.TextContent() == "final" {
			foundFinalAssistant = true
		}
		if notice, ok := session.EventNotice(ev); ok && notice.Kind == "compaction_notice" && notice.Text == "compaction.done" {
			foundOverflowNotice = true
		}
	}

	if !foundOverflowCompaction {
		t.Fatalf("expected overflow recovery compaction event")
	}
	if !foundFinalAssistant {
		t.Fatalf("expected assistant event after retry")
	}
	if !foundOverflowNotice {
		t.Fatalf("expected visible overflow compaction notice")
	}
	if agentImpl.calls != 2 {
		t.Fatalf("expected agent to run twice, got %d", agentImpl.calls)
	}
}

func TestRuntimeCompact_ReplacesWindowWithCheckpoint(t *testing.T) {
	store := inmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-tail-preserve"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	seed := []*session.Event{
		{
			ID:      "old_user",
			Time:    time.Now().Add(-4 * time.Minute),
			Message: model.NewTextMessage(model.RoleUser, "old question"),
		},
		{
			ID:      "old_assistant",
			Time:    time.Now().Add(-3 * time.Minute),
			Message: model.NewTextMessage(model.RoleAssistant, "old answer"),
		},
		{
			ID:      "keep_user",
			Time:    time.Now().Add(-2 * time.Minute),
			Message: model.NewTextMessage(model.RoleUser, "keep this question"),
		},
		{
			ID:      "keep_assistant",
			Time:    time.Now().Add(-1 * time.Minute),
			Message: model.NewTextMessage(model.RoleAssistant, "keep this answer"),
		},
	}
	for _, ev := range seed {
		if err := store.AppendEvent(context.Background(), sess, ev); err != nil {
			t.Fatal(err)
		}
	}

	rt, err := New(Config{
		Store:      store,
		Compaction: CompactionConfig{},
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
	if !strings.Contains(ev.Message.TextContent(), "# CONTEXT SNAPSHOT") {
		t.Fatalf("expected compaction text to start with CONTEXT SNAPSHOT header, got %q", ev.Message.TextContent()[:min(80, len(ev.Message.TextContent()))])
	}
	if !strings.Contains(ev.Message.TextContent(), "Do not treat this as a new user instruction") {
		t.Fatalf("expected compaction text to contain non-instruction disclaimer")
	}

	window, err := store.ListContextWindowEvents(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	// Context window should be: [compaction, tail_user, tail_assistant]
	// The last user→assistant interaction is preserved as tail.
	if len(window) < 1 {
		t.Fatal("expected at least compaction event in window")
	}
	if !isCompactionEvent(window[0]) {
		t.Fatalf("expected first window event to be compaction, got id=%q", window[0].ID)
	}
	// With 4 seed events (2 turns), the tail should preserve the last turn
	// (keep_user + keep_assistant) and summarize the first turn.
	if len(window) != 3 {
		t.Fatalf("expected 3 window events (compaction + 2 tail), got %d", len(window))
	}
	if window[1].Message.TextContent() != "keep this question" {
		t.Fatalf("expected tail[0] = 'keep this question', got %q", window[1].Message.TextContent())
	}
	if window[2].Message.TextContent() != "keep this answer" {
		t.Fatalf("expected tail[1] = 'keep this answer', got %q", window[2].Message.TextContent())
	}
	meta, _ := ev.Meta[metaCompaction].(map[string]any)
	tailIDs, ok := meta["tail_event_ids"].([]string)
	if !ok {
		t.Fatalf("expected tail_event_ids to be []string, got %T", meta["tail_event_ids"])
	}
	if len(tailIDs) != 2 || tailIDs[0] != "keep_user" || tailIDs[1] != "keep_assistant" {
		t.Fatalf("unexpected tail_event_ids: %#v", tailIDs)
	}

	// P1 invariant: full history must NOT contain duplicated events.
	// The tail is reconstructed at read time, not re-appended to the store.
	allEvents, err := store.ListEvents(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	// Expect exactly: 4 seed events + 1 compaction = 5 total.
	if len(allEvents) != 5 {
		t.Fatalf("expected 5 total events in full history (4 seed + 1 compaction), got %d", len(allEvents))
	}
	idSet := make(map[string]int, len(allEvents))
	for _, e := range allEvents {
		idSet[e.ID]++
	}
	for id, count := range idSet {
		if count > 1 {
			t.Fatalf("event ID %q appears %d times in full history — durable history is polluted", id, count)
		}
	}
}

func seedCompactionHistory(store *inmemory.Store, sess *session.Session) error {
	err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:        "seed-user-1",
		SessionID: sess.ID,
		Time:      time.Now().Add(-2 * time.Minute),
		Message:   model.NewTextMessage(model.RoleUser, "first question"),
	})
	if err != nil {
		return err
	}
	return store.AppendEvent(context.Background(), sess, &session.Event{
		ID:        "seed-assistant-1",
		SessionID: sess.ID,
		Time:      time.Now().Add(-1 * time.Minute),
		Message:   model.NewTextMessage(model.RoleAssistant, "first answer"),
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

func TestIsContextOverflowError_StructuredType(t *testing.T) {
	overflow := &model.ContextOverflowError{Cause: errors.New("too many tokens in request")}
	if !isContextOverflowError(overflow) {
		t.Fatal("expected structured ContextOverflowError to be detected")
	}

	// Wrapped in another error layer
	wrapped := fmt.Errorf("provider: %w", overflow)
	if !isContextOverflowError(wrapped) {
		t.Fatal("expected wrapped ContextOverflowError to be detected")
	}

	// Non-overflow error
	if isContextOverflowError(errors.New("network timeout")) {
		t.Fatal("expected non-overflow error to NOT be detected")
	}

	// Nil
	if isContextOverflowError(nil) {
		t.Fatal("nil should not be overflow")
	}
}

func TestIsContextOverflowError_StringFallback(t *testing.T) {
	// Legacy string-based detection still works
	if !isContextOverflowError(errors.New("context length exceeded")) {
		t.Fatal("expected string fallback to detect 'context length'")
	}
	if !isContextOverflowError(errors.New("prompt is too long for model")) {
		t.Fatal("expected string fallback to detect 'prompt is too long'")
	}
}

func TestCompactionEvent_StructuredMarkdownFormat(t *testing.T) {
	store := inmemory.New()
	sess := &session.Session{AppName: "app", UserID: "u", ID: "s-markdown-fmt"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := seedCompactionHistory(store, sess); err != nil {
		t.Fatal(err)
	}

	rt, err := New(Config{Store: store, Compaction: CompactionConfig{}})
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
	text := ev.Message.TextContent()
	if !strings.HasPrefix(text, "# CONTEXT SNAPSHOT") {
		t.Fatalf("compaction text must start with '# CONTEXT SNAPSHOT', got prefix: %q", text[:min(40, len(text))])
	}
	if !strings.Contains(text, "Do not treat this as a new user instruction") {
		t.Fatal("compaction text must contain non-instruction disclaimer")
	}
	if ev.Message.Role != model.RoleUser {
		t.Fatalf("compaction must use RoleUser, got %q", ev.Message.Role)
	}
}

func TestCompactionNotice_StructuredNotHumanText(t *testing.T) {
	ev := compactionNoticeEvent(triggerAuto, 5000, 2000, "start")
	if ev == nil {
		t.Fatal("expected notice event")
	}
	notice, ok := session.EventNotice(ev)
	if !ok {
		t.Fatal("expected event to be a notice")
	}

	// Notice must carry a machine-readable kind, not human-presentable text.
	if notice.Kind != "compaction_notice" {
		t.Fatalf("expected notice.Kind = 'compaction_notice', got %q", notice.Kind)
	}
	if notice.Text != "compaction.start" {
		t.Fatalf("expected machine-readable text 'compaction.start', got %q", notice.Text)
	}

	// Structured metadata must carry all rendering fields.
	phase, _ := ev.Meta["compaction_phase"].(string)
	trigger, _ := ev.Meta["compaction_trigger"].(string)
	preTokens, _ := ev.Meta["pre_tokens"].(int)
	if phase != "start" {
		t.Fatalf("expected meta compaction_phase='start', got %q", phase)
	}
	if trigger != triggerAuto {
		t.Fatalf("expected meta compaction_trigger=%q, got %q", triggerAuto, trigger)
	}
	if preTokens != 5000 {
		t.Fatalf("expected meta pre_tokens=5000, got %d", preTokens)
	}

	// "done" notice
	evDone := compactionNoticeEvent(triggerOverflowRecovery, 5000, 2000, "done")
	noticeDone, ok := session.EventNotice(evDone)
	if !ok {
		t.Fatal("expected done notice event")
	}
	if noticeDone.Text != "compaction.done" {
		t.Fatalf("expected machine-readable text 'compaction.done', got %q", noticeDone.Text)
	}
	postTokens, _ := evDone.Meta["post_tokens"].(int)
	if postTokens != 2000 {
		t.Fatalf("expected meta post_tokens=2000, got %d", postTokens)
	}
}
