package filestore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

func TestStore_AppendAndList(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	if _, err := store.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	ev := &session.Event{ID: "e1", Message: model.Message{Role: model.RoleUser, Text: "hi"}}
	if err := store.AppendEvent(context.Background(), s, ev); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListEvents(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestStore_ListEvents_CompatibleWithConcatenatedJSONObjects(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	if _, err := store.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	eventsPath := filepath.Join(root, "app", "u", "s", "events.jsonl")
	raw := `{"ID":"e1","SessionID":"s","Message":{"Role":"user","Text":"a"}}{"ID":"e2","SessionID":"s","Message":{"Role":"assistant","Text":"b"}}`
	if err := os.WriteFile(eventsPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := store.ListEvents(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID != "e1" || events[1].ID != "e2" {
		t.Fatalf("unexpected event ids: %s, %s", events[0].ID, events[1].ID)
	}
}

func TestStore_AppendEvent_PersistsCamelCaseMessageFields(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	if _, err := store.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	ev := &session.Event{
		ID: "e1",
		Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   "call_1",
				Name: "BASH",
				Args: `{"command":"echo hi"}`,
			}},
		},
	}
	if err := store.AppendEvent(context.Background(), s, ev); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(root, "app", "u", "s", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"Message":{"Role":"assistant"`) {
		t.Fatalf("expected Role in persisted event, got %s", text)
	}
	if !strings.Contains(text, `"ToolCalls":[{"ID":"call_1"`) {
		t.Fatalf("expected ToolCalls in persisted event, got %s", text)
	}
	if strings.Contains(text, `"tool_calls"`) || strings.Contains(text, `"tool_response"`) || strings.Contains(text, `"content_parts"`) {
		t.Fatalf("expected camel-case message fields only, got %s", text)
	}
}

func TestStore_SessionOnlyLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := NewWithOptions(root, Options{Layout: LayoutSessionOnly})
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	if _, err := store.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	ev := &session.Event{ID: "e1", Message: model.Message{Role: model.RoleUser, Text: "hi"}}
	if err := store.AppendEvent(context.Background(), s, ev); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "s", "events.jsonl")); err != nil {
		t.Fatalf("expected session-only events path to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "app", "u", "s", "events.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("did not expect namespaced events path, err=%v", err)
	}
}

func TestStore_ReplaceState_RoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s-state"}
	if _, err := store.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"runtime.lifecycle": map[string]any{
			"status": "completed",
			"phase":  "run",
		},
	}
	if err := store.ReplaceState(context.Background(), s, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.SnapshotState(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, ok := got["runtime.lifecycle"].(map[string]any)
	if !ok {
		t.Fatalf("expected lifecycle map, got %+v", got)
	}
	if lifecycle["status"] != "completed" {
		t.Fatalf("unexpected lifecycle status %+v", lifecycle)
	}
}

func TestStore_ReplaceState_IsAtomicAcrossStoreInstances(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	storeA, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s-atomic"}
	if _, err := storeA.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	const writers = 32
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		i := i
		go func() {
			defer wg.Done()
			store := storeA
			if i%2 == 1 {
				store = storeB
			}
			if err := store.ReplaceState(context.Background(), s, map[string]any{
				"writer": i,
				"acp": map[string]any{
					"modeId": "default",
				},
			}); err != nil {
				t.Errorf("replace state %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	statePath := filepath.Join(root, "app", "u", "s-atomic", "state.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("expected valid json after concurrent writes, got %v: %s", err, string(raw))
	}
	if _, ok := got["writer"]; !ok {
		t.Fatalf("expected final state to include writer marker, got %+v", got)
	}
}

func TestStore_UpdateState_PreservesConcurrentIndependentKeys(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	storeA, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	storeB, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s-update"}
	if _, err := storeA.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := storeA.UpdateState(context.Background(), s, func(values map[string]any) (map[string]any, error) {
			values["session_mode"] = "full_access"
			return values, nil
		}); err != nil {
			t.Errorf("update session_mode: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := storeB.UpdateState(context.Background(), s, func(values map[string]any) (map[string]any, error) {
			values["acp"] = map[string]any{
				"configValues": map[string]any{
					"thinking_mode": "off",
				},
			}
			return values, nil
		}); err != nil {
			t.Errorf("update acp config: %v", err)
		}
	}()
	wg.Wait()

	got, err := storeA.SnapshotState(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if got["session_mode"] != "full_access" {
		t.Fatalf("expected session_mode to be preserved, got %+v", got)
	}
	acp, ok := got["acp"].(map[string]any)
	if !ok {
		t.Fatalf("expected acp map, got %+v", got["acp"])
	}
	configValues, ok := acp["configValues"].(map[string]any)
	if !ok || configValues["thinking_mode"] != "off" {
		t.Fatalf("expected thinking_mode to be preserved, got %+v", acp)
	}
}

func TestStore_RejectsPathTraversalInSessionKeys(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	bad := &session.Session{AppName: "app", UserID: "u", ID: "../escape"}
	if _, err := store.GetOrCreate(context.Background(), bad); err == nil {
		t.Fatalf("expected path traversal session id to be rejected")
	}
	if err := store.AppendEvent(context.Background(), bad, &session.Event{
		ID:      "e1",
		Message: model.Message{Role: model.RoleUser, Text: "x"},
	}); err == nil {
		t.Fatalf("expected append with path traversal session id to fail")
	}
	if _, err := store.ListEvents(context.Background(), bad); err == nil {
		t.Fatalf("expected list with path traversal session id to fail")
	}
	if _, err := store.SnapshotState(context.Background(), bad); err == nil {
		t.Fatalf("expected snapshot with path traversal session id to fail")
	}
}

func TestStore_ListContextWindowEvents(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s"}
	if _, err := store.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	events := []*session.Event{
		{ID: "old", Message: model.Message{Role: model.RoleUser, Text: "old"}},
		{
			ID:      "compact",
			Message: model.Message{Role: model.RoleSystem, Text: "summary"},
			Meta: map[string]any{
				"kind": "compaction",
			},
		},
		{ID: "new", Message: model.Message{Role: model.RoleUser, Text: "new"}},
	}
	for _, ev := range events {
		if err := store.AppendEvent(context.Background(), s, ev); err != nil {
			t.Fatal(err)
		}
	}

	window, err := store.ListContextWindowEvents(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 2 {
		t.Fatalf("expected 2 events in context window, got %d", len(window))
	}
	if window[0].ID != "compact" || window[1].ID != "new" {
		t.Fatalf("unexpected window ids: %s, %s", window[0].ID, window[1].ID)
	}
}

func TestStore_ListContextWindowEvents_UsesLatestCompactionWindow(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := &session.Session{AppName: "app", UserID: "u", ID: "s-tail"}
	if _, err := store.GetOrCreate(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	events := []*session.Event{
		{ID: "old_user", Message: model.Message{Role: model.RoleUser, Text: "old user"}},
		{ID: "old_assistant", Message: model.Message{Role: model.RoleAssistant, Text: "old assistant"}},
		{ID: "tail_user", Message: model.Message{Role: model.RoleUser, Text: "tail user"}},
		{ID: "tail_assistant", Message: model.Message{Role: model.RoleAssistant, Text: "tail assistant"}},
		{
			ID:      "compact",
			Message: model.Message{Role: model.RoleUser, Text: "summary"},
			Meta: map[string]any{
				"kind": "compaction",
			},
		},
		{ID: "new_user", Message: model.Message{Role: model.RoleUser, Text: "new user"}},
	}
	for _, ev := range events {
		if err := store.AppendEvent(context.Background(), s, ev); err != nil {
			t.Fatal(err)
		}
	}

	window, err := store.ListContextWindowEvents(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) != 2 {
		t.Fatalf("expected 2 events in context window, got %d", len(window))
	}
	if window[0].ID != "compact" || window[1].ID != "new_user" {
		t.Fatalf("unexpected window ids: %s, %s", window[0].ID, window[1].ID)
	}
}
