package localstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/task"
)

func TestScopeStore_AppendsEventsToRolloutAndCatalog(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	dbPath := filepath.Join(filepath.Dir(root), "state.db")
	db, err := Open(root, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := db.Scope(Workspace{Key: "ws", CWD: "/tmp/ws"}, ScopeMain)
	sess := &session.Session{AppName: "caelis", UserID: "local-user", ID: "s-123"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "e1",
		Time:    time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC),
		Message: model.NewTextMessage(model.RoleUser, "hello localstore"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "e2",
		Time:    time.Date(2026, 3, 25, 10, 1, 0, 0, time.UTC),
		Message: model.NewTextMessage(model.RoleAssistant, "done"),
	}); err != nil {
		t.Fatal(err)
	}

	dir, err := store.SessionDir(sess)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".jsonl" {
		t.Fatalf("expected one rollout jsonl file, got %#v", entries)
	}

	events, err := store.ListEvents(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	items, err := store.ListSessionsPage(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 indexed session, got %d", len(items))
	}
	if items[0].LastUserMessage != "hello localstore" {
		t.Fatalf("unexpected last user message %q", items[0].LastUserMessage)
	}
}

func TestScopeStore_StateUsesNamespacedSQLiteRows(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	dbPath := filepath.Join(filepath.Dir(root), "state.db")
	db, err := Open(root, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := db.Scope(Workspace{Key: "ws", CWD: "/tmp/ws"}, ScopeMain)
	sess := &session.Session{AppName: "caelis", UserID: "local-user", ID: "s-state"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"session_mode":                         "plan",
		"plan":                                 map[string]any{"version": 1, "entries": []any{}},
		"policy.read_before_write.index_ready": true,
		"policy.read_before_write.read_paths":  []any{"/tmp/ws/a.go"},
		"custom":                               "value",
	}
	if err := store.ReplaceState(context.Background(), sess, state); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM session_states WHERE scope = ? AND workspace_key = ? AND session_id = ?`,
		ScopeMain, "ws", "s-state",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("expected 4 namespaced rows, got %d", count)
	}

	got, err := store.SnapshotState(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if got["session_mode"] != "plan" {
		t.Fatalf("unexpected session_mode %#v", got["session_mode"])
	}
	if got["custom"] != "value" {
		t.Fatalf("unexpected custom state %#v", got["custom"])
	}
}

func TestScopeStore_BackfillsCatalogFromRolloutAfterDBReset(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "sessions")
	dbPath := filepath.Join(tmp, "state.db")

	db, err := Open(root, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	store := db.Scope(Workspace{Key: "ws", CWD: "/tmp/ws"}, ScopeMain)
	sess := &session.Session{AppName: "caelis", UserID: "local-user", ID: "s-backfill"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(context.Background(), sess, &session.Event{
		ID:      "e1",
		Time:    time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC),
		Message: model.NewTextMessage(model.RoleUser, "backfill me"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}

	db2, err := Open(root, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	store2 := db2.Scope(Workspace{Key: "ws", CWD: "/tmp/ws"}, ScopeMain)
	if err := store2.Backfill(context.Background()); err != nil {
		t.Fatal(err)
	}
	items, err := store2.ListSessionsPage(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 session after backfill, got %d", len(items))
	}
	if items[0].SessionID != "s-backfill" {
		t.Fatalf("unexpected session id %q", items[0].SessionID)
	}
}

func TestScopeStore_BackfillIgnoresMissingScopeRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	dbPath := filepath.Join(filepath.Dir(root), "state.db")
	db, err := Open(root, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	acpStore := db.Scope(Workspace{Key: "ws", CWD: "/tmp/ws"}, ScopeACPRemote)
	if err := os.RemoveAll(filepath.Join(root, ScopeACPRemote)); err != nil {
		t.Fatal(err)
	}
	if err := acpStore.Backfill(context.Background()); err != nil {
		t.Fatalf("expected missing scope root to be ignored, got %v", err)
	}
}

func TestScopeStore_BackfillKeepsACPRemoteSessionsOutOfMainScope(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "sessions")
	dbPath := filepath.Join(tmp, "state.db")

	db, err := Open(root, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	mainStore := db.Scope(Workspace{Key: "ws", CWD: "/tmp/ws"}, ScopeMain)
	acpStore := db.Scope(Workspace{Key: "ws", CWD: "/tmp/ws"}, ScopeACPRemote)

	mainSession := &session.Session{AppName: "caelis", UserID: "local-user", ID: "s-main"}
	acpSession := &session.Session{AppName: "caelis", UserID: "local-user", ID: "s-remote"}
	if _, err := mainStore.GetOrCreate(context.Background(), mainSession); err != nil {
		t.Fatal(err)
	}
	if _, err := acpStore.GetOrCreate(context.Background(), acpSession); err != nil {
		t.Fatal(err)
	}
	if err := mainStore.AppendEvent(context.Background(), mainSession, &session.Event{
		ID:      "e-main",
		Time:    time.Date(2026, 3, 25, 10, 0, 0, 0, time.UTC),
		Message: model.NewTextMessage(model.RoleUser, "main"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := acpStore.AppendEvent(context.Background(), acpSession, &session.Event{
		ID:      "e-remote",
		Time:    time.Date(2026, 3, 25, 10, 1, 0, 0, time.UTC),
		Message: model.NewTextMessage(model.RoleUser, "remote"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}

	db2, err := Open(root, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	mainStore2 := db2.Scope(Workspace{Key: "ws", CWD: "/tmp/ws"}, ScopeMain)
	acpStore2 := db2.Scope(Workspace{Key: "ws", CWD: "/tmp/ws"}, ScopeACPRemote)
	if err := mainStore2.Backfill(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := acpStore2.Backfill(context.Background()); err != nil {
		t.Fatal(err)
	}

	mainItems, err := mainStore2.ListSessionsPage(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(mainItems) != 1 || mainItems[0].SessionID != "s-main" {
		t.Fatalf("expected only main session in main scope after backfill, got %+v", mainItems)
	}

	acpItems, err := acpStore2.ListSessionsPage(context.Background(), 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(acpItems) != 1 || acpItems[0].SessionID != "s-remote" {
		t.Fatalf("expected only acp session in acp scope after backfill, got %+v", acpItems)
	}
}

func TestScopeStore_TaskSnapshotsUseSQLite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	dbPath := filepath.Join(filepath.Dir(root), "state.db")
	db, err := Open(root, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := db.Scope(Workspace{Key: "ws", CWD: "/tmp/ws"}, ScopeMain)
	entry := &task.Entry{
		TaskID:         "t-1",
		Kind:           task.KindBash,
		Session:        task.SessionRef{AppName: "caelis", UserID: "local-user", SessionID: "s-1"},
		Title:          "echo hi",
		State:          task.StateCompleted,
		Running:        false,
		SupportsInput:  true,
		SupportsCancel: true,
		CreatedAt:      time.Now().Add(-time.Second),
		UpdatedAt:      time.Now(),
		HeartbeatAt:    time.Now(),
		Spec:           map[string]any{"command": "echo hi"},
		Result:         map[string]any{"exit_code": 0},
	}
	if err := store.Upsert(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "echo hi" {
		t.Fatalf("unexpected task title %q", got.Title)
	}

	var rowCount int
	if err := db.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM tasks`).Scan(&rowCount); err != nil {
		t.Fatal(err)
	}
	if rowCount != 1 {
		t.Fatalf("expected 1 task row, got %d", rowCount)
	}
}

func TestScopeStore_ListSessionReturnsTasksWithoutNestedReadDeadlock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	dbPath := filepath.Join(filepath.Dir(root), "state.db")
	db, err := Open(root, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := db.Scope(Workspace{Key: "ws", CWD: "/tmp/ws"}, ScopeMain)
	ref := task.SessionRef{AppName: "caelis", UserID: "local-user", SessionID: "s-list"}
	for i := 0; i < 3; i++ {
		if err := store.Upsert(context.Background(), &task.Entry{
			TaskID:         fmt.Sprintf("t-list-%d", i),
			Kind:           task.KindSpawn,
			Session:        ref,
			Title:          "spawn",
			State:          task.StateRunning,
			Running:        true,
			SupportsInput:  true,
			SupportsCancel: true,
			CreatedAt:      time.Now().Add(-time.Minute),
			UpdatedAt:      time.Now(),
			HeartbeatAt:    time.Now(),
			Spec:           map[string]any{"i": i},
			Result:         map[string]any{},
		}); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	items, err := store.ListSession(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 listed tasks, got %d", len(items))
	}
}

func TestOpenCreatesSQLiteDatabase(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	dbPath := filepath.Join(filepath.Dir(root), "state.db")
	db, err := Open(root, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal(err)
	}

	var one int
	if err := db.db.QueryRowContext(context.Background(), `SELECT 1`).Scan(&one); err != nil {
		t.Fatal(err)
	}
	if one != 1 {
		t.Fatalf("unexpected sqlite probe result %d", one)
	}
}

func TestScopeStore_ConcurrentWritesDoNotFail(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	dbPath := filepath.Join(filepath.Dir(root), "state.db")
	db, err := Open(root, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := db.Scope(Workspace{Key: "ws", CWD: "/tmp/ws"}, ScopeMain)
	sess := &session.Session{AppName: "caelis", UserID: "local-user", ID: "s-concurrent"}
	if _, err := store.GetOrCreate(context.Background(), sess); err != nil {
		t.Fatal(err)
	}

	var (
		wg     sync.WaitGroup
		errMu  sync.Mutex
		errors []error
	)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		defer errMu.Unlock()
		errors = append(errors, err)
	}

	for worker := 0; worker < 8; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				recordErr(store.Upsert(context.Background(), &task.Entry{
					TaskID:         fmt.Sprintf("task-%d-%d", worker, i),
					Kind:           task.KindSpawn,
					Session:        task.SessionRef{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID},
					Title:          "spawn",
					State:          task.StateRunning,
					Running:        true,
					SupportsInput:  true,
					SupportsCancel: true,
					CreatedAt:      time.Now(),
					UpdatedAt:      time.Now(),
					HeartbeatAt:    time.Now(),
					Spec:           map[string]any{"worker": worker, "step": i},
					Result:         map[string]any{},
				}))
				recordErr(store.UpdateState(context.Background(), sess, func(values map[string]any) (map[string]any, error) {
					if values == nil {
						values = map[string]any{}
					}
					values["plan"] = map[string]any{
						"version": 1,
						"entries": []any{map[string]any{"worker": worker, "step": i}},
					}
					return values, nil
				}))
				recordErr(store.AppendEvent(context.Background(), sess, &session.Event{
					ID:      fmt.Sprintf("event-%d-%d", worker, i),
					Time:    time.Now(),
					Message: model.NewTextMessage(model.RoleAssistant, "parallel"),
				}))
			}
		}()
	}
	wg.Wait()

	if len(errors) > 0 {
		t.Fatalf("unexpected concurrent write errors: %v", errors[0])
	}
	if _, err := store.SnapshotState(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListEvents(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatalf("expected persisted events after concurrent writes")
	}
}

var _ = (*sql.DB)(nil)
