package localstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/kernel/task"
	_ "modernc.org/sqlite"
)

const (
	ScopeMain      = "main"
	ScopeACPRemote = "acp_remote"

	sqliteMaxOpenConns = 8
	sqliteMaxIdleConns = 4

	sessionTable       = "sessions"
	sessionStatesTable = "session_states"
	taskTable          = "tasks"

	namespaceSessionMode      = "session_mode"
	namespaceRuntimeLifecycle = "runtime.lifecycle"
	namespacePlan             = "plan"
	namespaceACP              = "acp"
	namespaceExternalParts    = "external_participants_v1"
	namespaceReadBeforeWrite  = "policy.read_before_write"
	namespaceMisc             = "_misc"

	stateKeySessionMode              = "session_mode"
	stateKeyRuntimeLifecycle         = "runtime.lifecycle"
	stateKeyPlan                     = "plan"
	stateKeyACP                      = "acp"
	stateKeyExternalParticipants     = "external_participants_v1"
	stateKeyReadBeforeWriteReadPaths = "policy.read_before_write.read_paths"
	stateKeyReadBeforeWriteReady     = "policy.read_before_write.index_ready"
	stateKeyReadBeforeWriteSafeWrite = "policy.read_before_write.safe_write_paths"
)

type Workspace struct {
	Key string
	CWD string
}

type Database struct {
	db      *sql.DB
	root    string
	logMu   sync.Mutex
	writeMu sync.Mutex
}

type ScopeStore struct {
	db        *Database
	workspace Workspace
	scope     string
}

type SessionSummary struct {
	SessionID       string
	AppName         string
	UserID          string
	WorkspaceCWD    string
	CreatedAt       time.Time
	LastEventAt     time.Time
	EventCount      int64
	LastUserMessage string
	Hidden          bool
	RolloutPath     string
}

type logLine struct {
	Type    string         `json:"type"`
	Session *sessionMeta   `json:"session,omitempty"`
	Event   *session.Event `json:"event,omitempty"`
}

type sessionMeta struct {
	AppName      string `json:"app_name"`
	UserID       string `json:"user_id"`
	SessionID    string `json:"session_id"`
	WorkspaceKey string `json:"workspace_key,omitempty"`
	WorkspaceCWD string `json:"workspace_cwd,omitempty"`
	Scope        string `json:"scope,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
}

type stateRow struct {
	Namespace string
	Payload   string
}

func Open(root, dbPath string) (*Database, error) {
	root = strings.TrimSpace(root)
	dbPath = strings.TrimSpace(dbPath)
	if root == "" {
		return nil, fmt.Errorf("localstore: root is required")
	}
	if dbPath == "" {
		return nil, fmt.Errorf("localstore: db path is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(3000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("localstore: open db: %w", err)
	}
	db.SetMaxOpenConns(sqliteMaxOpenConns)
	db.SetMaxIdleConns(sqliteMaxIdleConns)
	store := &Database{db: db, root: root}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (d *Database) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

func (d *Database) SQLDB() *sql.DB {
	if d == nil {
		return nil
	}
	return d.db
}

func (d *Database) Scope(workspace Workspace, scope string) *ScopeStore {
	scope = normalizeScope(scope)
	return &ScopeStore{
		db: d,
		workspace: Workspace{
			Key: strings.TrimSpace(workspace.Key),
			CWD: strings.TrimSpace(workspace.CWD),
		},
		scope: scope,
	}
}

func normalizeScope(scope string) string {
	scope = strings.TrimSpace(strings.ToLower(scope))
	switch scope {
	case ScopeACPRemote:
		return ScopeACPRemote
	default:
		return ScopeMain
	}
}

func (d *Database) migrate(ctx context.Context) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("localstore: db is nil")
	}
	const ddl = `
CREATE TABLE IF NOT EXISTS sessions (
	scope TEXT NOT NULL,
	workspace_key TEXT NOT NULL,
	app_name TEXT NOT NULL,
	user_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	workspace_cwd TEXT NOT NULL DEFAULT '',
	rollout_path TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	last_event_at INTEGER NOT NULL DEFAULT 0,
	event_count INTEGER NOT NULL DEFAULT 0,
	last_user_message TEXT NOT NULL DEFAULT '',
	hidden INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (scope, workspace_key, app_name, user_id, session_id)
);
CREATE INDEX IF NOT EXISTS idx_sessions_workspace_last_event
ON sessions(scope, workspace_key, hidden, last_event_at DESC, created_at DESC);
CREATE TABLE IF NOT EXISTS session_states (
	scope TEXT NOT NULL,
	workspace_key TEXT NOT NULL,
	app_name TEXT NOT NULL,
	user_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	namespace TEXT NOT NULL,
	version INTEGER NOT NULL DEFAULT 1,
	payload_json TEXT NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (scope, workspace_key, app_name, user_id, session_id, namespace)
);
CREATE TABLE IF NOT EXISTS tasks (
	scope TEXT NOT NULL,
	workspace_key TEXT NOT NULL,
	task_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	app_name TEXT NOT NULL,
	user_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL,
	running INTEGER NOT NULL DEFAULT 0,
	supports_input INTEGER NOT NULL DEFAULT 0,
	supports_cancel INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	heartbeat_at INTEGER NOT NULL DEFAULT 0,
	stdout_cursor INTEGER NOT NULL DEFAULT 0,
	stderr_cursor INTEGER NOT NULL DEFAULT 0,
	event_cursor INTEGER NOT NULL DEFAULT 0,
	spec_json TEXT NOT NULL,
	result_json TEXT NOT NULL,
	PRIMARY KEY (scope, workspace_key, task_id)
);
CREATE INDEX IF NOT EXISTS idx_tasks_session
ON tasks(scope, workspace_key, app_name, user_id, session_id, updated_at DESC);`
	_, err := d.execWrite(ctx, ddl)
	if err != nil {
		return fmt.Errorf("localstore: migrate: %w", err)
	}
	return nil
}

func (s *ScopeStore) GetOrCreate(ctx context.Context, req *session.Session) (*session.Session, error) {
	if err := validateSession(req); err != nil {
		return nil, err
	}
	if _, err := s.lookupSession(ctx, req); err == nil {
		cp := *req
		return &cp, nil
	} else if !errors.Is(err, session.ErrSessionNotFound) {
		return nil, err
	}
	path := s.rolloutPath(req.ID, time.Now())
	now := time.Now().UnixMilli()
	const q = `
INSERT OR IGNORE INTO sessions (
	scope, workspace_key, app_name, user_id, session_id, workspace_cwd, rollout_path,
	created_at, updated_at, last_event_at, event_count, last_user_message, hidden
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, '', 0)`
	if _, err := s.db.execWrite(ctx, q,
		s.scope, s.workspace.Key, req.AppName, req.UserID, req.ID, s.workspace.CWD, path,
		now, now,
	); err != nil {
		return nil, err
	}
	cp := *req
	return &cp, nil
}

func (s *ScopeStore) SessionExists(ctx context.Context, req *session.Session) (bool, error) {
	if err := validateSession(req); err != nil {
		return false, err
	}
	_, err := s.lookupSession(ctx, req)
	if errors.Is(err, session.ErrSessionNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *ScopeStore) AppendEvent(ctx context.Context, req *session.Session, ev *session.Event) error {
	if ev == nil {
		return fmt.Errorf("localstore: event is nil")
	}
	if _, err := s.GetOrCreate(ctx, req); err != nil {
		return err
	}
	meta, err := s.lookupSession(ctx, req)
	if err != nil {
		return err
	}
	if err := s.appendLogEvent(meta, ev); err != nil {
		return err
	}
	return s.touchSession(ctx, req, ev, meta.RolloutPath)
}

func (s *ScopeStore) ListEvents(ctx context.Context, req *session.Session) ([]*session.Event, error) {
	if err := validateSession(req); err != nil {
		return nil, err
	}
	meta, err := s.lookupSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return readLogEvents(meta.RolloutPath)
}

func (s *ScopeStore) ListEventsAfter(ctx context.Context, req *session.Session, afterCursor string, limit int) ([]*session.Event, string, error) {
	events, err := s.ListEvents(ctx, req)
	if err != nil {
		return nil, "", err
	}
	start := 0
	if afterCursor != "" {
		start = len(events)
		for i, ev := range events {
			if ev != nil && ev.ID == afterCursor {
				start = i + 1
				break
			}
		}
	}
	if start > len(events) {
		start = len(events)
	}
	remaining := events[start:]
	if limit > 0 && len(remaining) > limit {
		remaining = remaining[:limit]
	}
	nextCursor := afterCursor
	if n := len(remaining); n > 0 && remaining[n-1] != nil {
		nextCursor = remaining[n-1].ID
	}
	return remaining, nextCursor, nil
}

func (s *ScopeStore) ListContextWindowEvents(ctx context.Context, req *session.Session) ([]*session.Event, error) {
	events, err := s.ListEvents(ctx, req)
	if err != nil {
		return nil, err
	}
	return session.ContextWindowEvents(events), nil
}

func (s *ScopeStore) SnapshotState(ctx context.Context, req *session.Session) (map[string]any, error) {
	if err := validateSession(req); err != nil {
		return nil, err
	}
	if _, err := s.lookupSession(ctx, req); err != nil {
		return nil, err
	}
	rows, err := s.loadStateRows(ctx, req)
	if err != nil {
		return nil, err
	}
	return assembleStateMap(rows)
}

func (s *ScopeStore) ReplaceState(ctx context.Context, req *session.Session, values map[string]any) error {
	if err := validateSession(req); err != nil {
		return err
	}
	if _, err := s.GetOrCreate(ctx, req); err != nil {
		return err
	}
	return s.replaceState(ctx, req, values)
}

func (s *ScopeStore) UpdateState(ctx context.Context, req *session.Session, update func(map[string]any) (map[string]any, error)) error {
	if update == nil {
		return nil
	}
	if err := validateSession(req); err != nil {
		return err
	}
	if _, err := s.GetOrCreate(ctx, req); err != nil {
		return err
	}
	return s.db.withWriteTx(ctx, func(tx *sql.Tx) error {
		rows, err := s.loadStateRowsTx(ctx, tx, req)
		if err != nil {
			return err
		}
		current, err := assembleStateMap(rows)
		if err != nil {
			return err
		}
		next, err := update(current)
		if err != nil {
			return err
		}
		if next == nil {
			next = map[string]any{}
		}
		return s.replaceStateTx(ctx, tx, req, next)
	})
}

func (s *ScopeStore) SessionDir(req *session.Session) (string, error) {
	if err := validateSession(req); err != nil {
		return "", err
	}
	meta, err := s.lookupSession(context.Background(), req)
	if err != nil {
		return "", err
	}
	return filepath.Dir(meta.RolloutPath), nil
}

func (s *ScopeStore) Upsert(ctx context.Context, entry *task.Entry) error {
	if entry == nil || strings.TrimSpace(entry.TaskID) == "" {
		return task.ErrTaskNotFound
	}
	specJSON, err := marshalJSON(entry.Spec)
	if err != nil {
		return err
	}
	resultJSON, err := marshalJSON(entry.Result)
	if err != nil {
		return err
	}
	const q = `
INSERT INTO tasks (
	scope, workspace_key, task_id, session_id, app_name, user_id, kind, title, state, running,
	supports_input, supports_cancel, created_at, updated_at, heartbeat_at,
	stdout_cursor, stderr_cursor, event_cursor, spec_json, result_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scope, workspace_key, task_id) DO UPDATE SET
	session_id = excluded.session_id,
	app_name = excluded.app_name,
	user_id = excluded.user_id,
	kind = excluded.kind,
	title = excluded.title,
	state = excluded.state,
	running = excluded.running,
	supports_input = excluded.supports_input,
	supports_cancel = excluded.supports_cancel,
	created_at = excluded.created_at,
	updated_at = excluded.updated_at,
	heartbeat_at = excluded.heartbeat_at,
	stdout_cursor = excluded.stdout_cursor,
	stderr_cursor = excluded.stderr_cursor,
	event_cursor = excluded.event_cursor,
	spec_json = excluded.spec_json,
	result_json = excluded.result_json`
	_, err = s.db.execWrite(ctx, q,
		s.scope, s.workspace.Key, strings.TrimSpace(entry.TaskID), strings.TrimSpace(entry.Session.SessionID),
		strings.TrimSpace(entry.Session.AppName), strings.TrimSpace(entry.Session.UserID),
		string(entry.Kind), strings.TrimSpace(entry.Title), string(entry.State), boolToInt(entry.Running), boolToInt(entry.SupportsInput), boolToInt(entry.SupportsCancel),
		entry.CreatedAt.UnixMilli(), entry.UpdatedAt.UnixMilli(), entry.HeartbeatAt.UnixMilli(),
		entry.StdoutCursor, entry.StderrCursor, entry.EventCursor, specJSON, resultJSON,
	)
	return err
}

func (s *ScopeStore) Get(ctx context.Context, taskID string) (*task.Entry, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, task.ErrTaskNotFound
	}
	const q = `
SELECT task_id, session_id, app_name, user_id, kind, title, state, running,
	supports_input, supports_cancel, created_at, updated_at, heartbeat_at,
	stdout_cursor, stderr_cursor, event_cursor, spec_json, result_json
FROM tasks WHERE scope = ? AND workspace_key = ? AND task_id = ?`
	entry, err := scanTaskEntry(s.db.db.QueryRowContext(ctx, q, s.scope, s.workspace.Key, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, task.ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return entry, nil
}

func (s *ScopeStore) ListSession(ctx context.Context, ref task.SessionRef) ([]*task.Entry, error) {
	const q = `
SELECT task_id, session_id, app_name, user_id, kind, title, state, running,
	supports_input, supports_cancel, created_at, updated_at, heartbeat_at,
	stdout_cursor, stderr_cursor, event_cursor, spec_json, result_json
FROM tasks
WHERE scope = ? AND workspace_key = ? AND app_name = ? AND user_id = ? AND session_id = ?
ORDER BY updated_at DESC`
	rows, err := s.db.db.QueryContext(ctx, q,
		s.scope, s.workspace.Key, strings.TrimSpace(ref.AppName), strings.TrimSpace(ref.UserID), strings.TrimSpace(ref.SessionID),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*task.Entry{}
	for rows.Next() {
		entry, err := scanTaskEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

type taskRowScanner interface {
	Scan(dest ...any) error
}

func scanTaskEntry(scanner taskRowScanner) (*task.Entry, error) {
	if scanner == nil {
		return nil, fmt.Errorf("localstore: task row scanner is nil")
	}
	var (
		entry          task.Entry
		kind           string
		state          string
		running        int
		supportsInput  int
		supportsCancel int
		createdAt      int64
		updatedAt      int64
		heartbeatAt    int64
		specJSON       string
		resultJSON     string
	)
	if err := scanner.Scan(
		&entry.TaskID, &entry.Session.SessionID, &entry.Session.AppName, &entry.Session.UserID,
		&kind, &entry.Title, &state, &running, &supportsInput, &supportsCancel, &createdAt, &updatedAt, &heartbeatAt,
		&entry.StdoutCursor, &entry.StderrCursor, &entry.EventCursor, &specJSON, &resultJSON,
	); err != nil {
		return nil, err
	}
	entry.Kind = task.Kind(kind)
	entry.State = task.State(state)
	entry.Running = running != 0
	entry.SupportsInput = supportsInput != 0
	entry.SupportsCancel = supportsCancel != 0
	entry.CreatedAt = unixMilli(createdAt)
	entry.UpdatedAt = unixMilli(updatedAt)
	entry.HeartbeatAt = unixMilli(heartbeatAt)
	if err := unmarshalStringJSON(specJSON, &entry.Spec); err != nil {
		return nil, err
	}
	if err := unmarshalStringJSON(resultJSON, &entry.Result); err != nil {
		return nil, err
	}
	return task.CloneEntry(&entry), nil
}

func (s *ScopeStore) Backfill(ctx context.Context) error {
	root := s.scopeRoot()
	if strings.TrimSpace(root) == "" {
		return nil
	}
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if s.scope == ScopeMain && path != root && entry.Name() == ScopeACPRemote {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		return s.backfillRollout(ctx, path)
	})
}

func (s *ScopeStore) ListSessionsPage(ctx context.Context, page, pageSize int) ([]SessionSummary, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	const q = `
SELECT session_id, app_name, user_id, workspace_cwd, created_at, last_event_at, event_count, last_user_message, hidden, rollout_path
FROM sessions
WHERE scope = ? AND workspace_key = ? AND hidden = 0
ORDER BY last_event_at DESC, created_at DESC
LIMIT ? OFFSET ?`
	rows, err := s.db.db.QueryContext(ctx, q, s.scope, s.workspace.Key, pageSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SessionSummary, 0, pageSize)
	for rows.Next() {
		var rec SessionSummary
		var createdAt, lastEventAt int64
		var hidden int
		if err := rows.Scan(&rec.SessionID, &rec.AppName, &rec.UserID, &rec.WorkspaceCWD, &createdAt, &lastEventAt, &rec.EventCount, &rec.LastUserMessage, &hidden, &rec.RolloutPath); err != nil {
			return nil, err
		}
		rec.CreatedAt = unixMilli(createdAt)
		rec.LastEventAt = unixMilli(lastEventAt)
		rec.Hidden = hidden != 0
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *ScopeStore) ResolveSessionPrefix(ctx context.Context, prefix string) (string, bool, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "", false, fmt.Errorf("localstore: session prefix is required")
	}
	const q = `
SELECT session_id
FROM sessions
WHERE scope = ? AND workspace_key = ? AND hidden = 0 AND session_id LIKE ?
ORDER BY last_event_at DESC, created_at DESC
LIMIT 3`
	rows, err := s.db.db.QueryContext(ctx, q, s.scope, s.workspace.Key, prefix+"%")
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	matches := make([]string, 0, 3)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return "", false, err
		}
		matches = append(matches, sessionID)
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	for _, match := range matches {
		if match == prefix {
			return match, true, nil
		}
	}
	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	default:
		return "", false, fmt.Errorf("session prefix %q is ambiguous in current workspace", prefix)
	}
}

func (s *ScopeStore) MostRecentSessionID(ctx context.Context, excludeSessionID string) (string, bool, error) {
	const q = `
SELECT session_id
FROM sessions
WHERE scope = ? AND workspace_key = ? AND hidden = 0 AND (? = '' OR session_id <> ?)
ORDER BY last_event_at DESC, created_at DESC
LIMIT 1`
	var sessionID string
	err := s.db.db.QueryRowContext(ctx, q, s.scope, s.workspace.Key, strings.TrimSpace(excludeSessionID), strings.TrimSpace(excludeSessionID)).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return sessionID, true, nil
}

func (s *ScopeStore) DeleteSession(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	const q = `DELETE FROM sessions WHERE scope = ? AND workspace_key = ? AND session_id = ?`
	_, err := s.db.execWrite(ctx, q, s.scope, s.workspace.Key, sessionID)
	return err
}

func (s *ScopeStore) lookupSession(ctx context.Context, req *session.Session) (SessionSummary, error) {
	const q = `
SELECT session_id, app_name, user_id, workspace_cwd, created_at, last_event_at, event_count, last_user_message, hidden, rollout_path
FROM sessions
WHERE scope = ? AND workspace_key = ? AND app_name = ? AND user_id = ? AND session_id = ?`
	var rec SessionSummary
	var createdAt, lastEventAt int64
	var hidden int
	err := s.db.db.QueryRowContext(ctx, q,
		s.scope, s.workspace.Key, req.AppName, req.UserID, req.ID,
	).Scan(&rec.SessionID, &rec.AppName, &rec.UserID, &rec.WorkspaceCWD, &createdAt, &lastEventAt, &rec.EventCount, &rec.LastUserMessage, &hidden, &rec.RolloutPath)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionSummary{}, session.ErrSessionNotFound
	}
	if err != nil {
		return SessionSummary{}, err
	}
	rec.CreatedAt = unixMilli(createdAt)
	rec.LastEventAt = unixMilli(lastEventAt)
	rec.Hidden = hidden != 0
	return rec, nil
}

func (s *ScopeStore) replaceState(ctx context.Context, req *session.Session, values map[string]any) error {
	return s.db.withWriteTx(ctx, func(tx *sql.Tx) error {
		return s.replaceStateTx(ctx, tx, req, values)
	})
}

func (s *ScopeStore) replaceStateTx(ctx context.Context, tx *sql.Tx, req *session.Session, values map[string]any) error {
	const del = `
DELETE FROM session_states
WHERE scope = ? AND workspace_key = ? AND app_name = ? AND user_id = ? AND session_id = ?`
	if _, err := tx.ExecContext(ctx, del, s.scope, s.workspace.Key, req.AppName, req.UserID, req.ID); err != nil {
		return err
	}
	namespaces, err := splitStateMap(values)
	if err != nil {
		return err
	}
	const ins = `
INSERT INTO session_states (
	scope, workspace_key, app_name, user_id, session_id, namespace, version, payload_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)`
	now := time.Now().UnixMilli()
	for namespace, payload := range namespaces {
		if _, err := tx.ExecContext(ctx, ins,
			s.scope, s.workspace.Key, req.AppName, req.UserID, req.ID, namespace, payload, now,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *ScopeStore) loadStateRows(ctx context.Context, req *session.Session) ([]stateRow, error) {
	return s.loadStateRowsTx(ctx, nil, req)
}

func (s *ScopeStore) loadStateRowsTx(ctx context.Context, tx *sql.Tx, req *session.Session) ([]stateRow, error) {
	const q = `
SELECT namespace, payload_json
FROM session_states
WHERE scope = ? AND workspace_key = ? AND app_name = ? AND user_id = ? AND session_id = ?`
	var (
		rows *sql.Rows
		err  error
	)
	if tx != nil {
		rows, err = tx.QueryContext(ctx, q, s.scope, s.workspace.Key, req.AppName, req.UserID, req.ID)
	} else {
		rows, err = s.db.db.QueryContext(ctx, q, s.scope, s.workspace.Key, req.AppName, req.UserID, req.ID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []stateRow{}
	for rows.Next() {
		var row stateRow
		if err := rows.Scan(&row.Namespace, &row.Payload); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *ScopeStore) touchSession(ctx context.Context, req *session.Session, ev *session.Event, rolloutPath string) error {
	lastUser := ""
	if ev != nil && ev.Message.Role == model.RoleUser && session.EventTypeOf(ev) != session.EventTypeCompaction {
		lastUser = visibleUserMessage(ev)
	}
	lastEventAt := time.Now()
	if ev != nil && !ev.Time.IsZero() {
		lastEventAt = ev.Time
	}
	hidden := 0
	if sessionHiddenForEvent(ev, req.ID) {
		hidden = 1
	}
	const q = `
UPDATE sessions SET
	workspace_cwd = ?,
	rollout_path = ?,
	updated_at = ?,
	last_event_at = ?,
	event_count = event_count + 1,
	last_user_message = CASE WHEN ? <> '' THEN ? ELSE last_user_message END,
	hidden = CASE WHEN ? = 1 THEN 1 ELSE hidden END
WHERE scope = ? AND workspace_key = ? AND app_name = ? AND user_id = ? AND session_id = ?`
	_, err := s.db.execWrite(ctx, q,
		s.workspace.CWD, rolloutPath, time.Now().UnixMilli(), lastEventAt.UnixMilli(), lastUser, lastUser, hidden,
		s.scope, s.workspace.Key, req.AppName, req.UserID, req.ID,
	)
	return err
}

func (s *ScopeStore) appendLogEvent(meta SessionSummary, ev *session.Event) error {
	if err := s.materializeRollout(meta); err != nil {
		return err
	}
	line, err := json.Marshal(logLine{Type: "event", Event: session.CloneEvent(ev)})
	if err != nil {
		return err
	}
	s.db.logMu.Lock()
	defer s.db.logMu.Unlock()
	f, err := os.OpenFile(meta.RolloutPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func (s *ScopeStore) materializeRollout(meta SessionSummary) error {
	s.db.logMu.Lock()
	defer s.db.logMu.Unlock()
	if _, err := os.Stat(meta.RolloutPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(meta.RolloutPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(meta.RolloutPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	metaLine, err := json.Marshal(logLine{
		Type: "session_meta",
		Session: &sessionMeta{
			AppName:      meta.AppName,
			UserID:       meta.UserID,
			SessionID:    meta.SessionID,
			WorkspaceKey: s.workspace.Key,
			WorkspaceCWD: s.workspace.CWD,
			Scope:        s.scope,
			CreatedAt:    meta.CreatedAt.UTC().Format(time.RFC3339Nano),
		},
	})
	if err != nil {
		return err
	}
	_, err = f.Write(append(metaLine, '\n'))
	return err
}

func readLogEvents(path string) ([]*session.Event, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	out := []*session.Event{}
	for {
		var line logLine
		if err := dec.Decode(&line); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if line.Type != "event" || line.Event == nil {
			continue
		}
		out = append(out, session.CloneEvent(line.Event))
	}
	return out, nil
}

func (s *ScopeStore) backfillRollout(ctx context.Context, path string) error {
	meta, snapshot, err := readRolloutSnapshot(path)
	if err != nil {
		return err
	}
	if meta == nil || strings.TrimSpace(meta.SessionID) == "" {
		return nil
	}
	req := &session.Session{
		AppName: strings.TrimSpace(meta.AppName),
		UserID:  strings.TrimSpace(meta.UserID),
		ID:      strings.TrimSpace(meta.SessionID),
	}
	if req.AppName == "" || req.UserID == "" {
		return nil
	}
	const q = `
INSERT INTO sessions (
	scope, workspace_key, app_name, user_id, session_id, workspace_cwd, rollout_path,
	created_at, updated_at, last_event_at, event_count, last_user_message, hidden
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scope, workspace_key, app_name, user_id, session_id) DO UPDATE SET
	workspace_cwd = excluded.workspace_cwd,
	rollout_path = excluded.rollout_path,
	updated_at = excluded.updated_at,
	last_event_at = excluded.last_event_at,
	event_count = excluded.event_count,
	last_user_message = excluded.last_user_message,
	hidden = excluded.hidden`
	createdAt := time.Now()
	if meta.CreatedAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, meta.CreatedAt); err == nil {
			createdAt = parsed
		}
	}
	if snapshot.LastEventAt.IsZero() {
		snapshot.LastEventAt = createdAt
	}
	_, err = s.db.execWrite(ctx, q,
		s.scope, s.workspace.Key, req.AppName, req.UserID, req.ID, firstNonEmpty(meta.WorkspaceCWD, s.workspace.CWD), path,
		createdAt.UnixMilli(), snapshot.LastEventAt.UnixMilli(), snapshot.LastEventAt.UnixMilli(), snapshot.EventCount, snapshot.LastUserMessage, boolToInt(snapshot.Hidden),
	)
	return err
}

func (d *Database) execWrite(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if d == nil || d.db == nil {
		return nil, fmt.Errorf("localstore: db is nil")
	}
	var (
		result sql.Result
		err    error
	)
	err = d.withWriteLock(ctx, func() error {
		result, err = d.db.ExecContext(ctx, query, args...)
		return err
	})
	return result, err
}

func (d *Database) withWriteTx(ctx context.Context, fn func(*sql.Tx) error) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("localstore: db is nil")
	}
	if fn == nil {
		return nil
	}
	return d.withWriteLock(ctx, func() error {
		tx, err := d.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func (d *Database) withWriteLock(ctx context.Context, fn func() error) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("localstore: db is nil")
	}
	if fn == nil {
		return nil
	}
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	return retrySQLiteBusy(ctx, fn)
}

func retrySQLiteBusy(ctx context.Context, fn func() error) error {
	delay := 10 * time.Millisecond
	for attempt := 0; ; attempt++ {
		err := fn()
		if !isSQLiteBusy(err) || attempt >= 7 {
			return err
		}
		if ctx == nil {
			time.Sleep(delay)
		} else {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "sqlite_locked") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "database schema is locked")
}

func readRolloutSnapshot(path string) (*sessionMeta, rolloutSnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, rolloutSnapshot{}, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var (
		meta     *sessionMeta
		snapshot rolloutSnapshot
	)
	for {
		var line logLine
		if err := dec.Decode(&line); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, rolloutSnapshot{}, err
		}
		switch line.Type {
		case "session_meta":
			if line.Session != nil {
				cp := *line.Session
				meta = &cp
			}
		case "event":
			if line.Event == nil {
				continue
			}
			snapshot.EventCount++
			if !line.Event.Time.IsZero() && line.Event.Time.After(snapshot.LastEventAt) {
				snapshot.LastEventAt = line.Event.Time
			}
			if line.Event.Message.Role == model.RoleUser && session.EventTypeOf(line.Event) != session.EventTypeCompaction {
				if text := visibleUserMessage(line.Event); text != "" {
					snapshot.LastUserMessage = text
				}
			}
			if meta != nil && sessionHiddenForEvent(line.Event, meta.SessionID) {
				snapshot.Hidden = true
			}
		}
	}
	return meta, snapshot, nil
}

type rolloutSnapshot struct {
	LastEventAt     time.Time
	EventCount      int64
	LastUserMessage string
	Hidden          bool
}

func (s *ScopeStore) scopeRoot() string {
	root := filepath.Join(s.db.root, strings.TrimSpace(s.workspace.Key))
	if s.scope == ScopeACPRemote {
		return filepath.Join(root, ScopeACPRemote)
	}
	return root
}

func (s *ScopeStore) rolloutPath(sessionID string, createdAt time.Time) string {
	stamp := createdAt.UTC().Format("2006-01-02T15-04-05")
	dayDir := filepath.Join(
		s.scopeRoot(),
		createdAt.UTC().Format("2006"),
		createdAt.UTC().Format("01"),
		createdAt.UTC().Format("02"),
	)
	return filepath.Join(dayDir, fmt.Sprintf("rollout-%s-%s.jsonl", stamp, strings.TrimSpace(sessionID)))
}

func validateSession(req *session.Session) error {
	if req == nil {
		return fmt.Errorf("localstore: invalid session")
	}
	if strings.TrimSpace(req.AppName) == "" || strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.ID) == "" {
		return fmt.Errorf("localstore: app_name, user_id and session_id are required")
	}
	return nil
}

func marshalJSON(value any) (string, error) {
	if value == nil {
		return "{}", nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func unmarshalStringJSON(raw string, out any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	return json.Unmarshal([]byte(raw), out)
}

func assembleStateMap(rows []stateRow) (map[string]any, error) {
	out := map[string]any{}
	for _, row := range rows {
		var payload any
		if err := json.Unmarshal([]byte(row.Payload), &payload); err != nil {
			return nil, err
		}
		switch row.Namespace {
		case namespaceSessionMode:
			switch value := payload.(type) {
			case string:
				out[stateKeySessionMode] = value
			case map[string]any:
				if mode, ok := value["mode"].(string); ok {
					out[stateKeySessionMode] = mode
				}
			}
		case namespaceRuntimeLifecycle:
			if value, ok := payload.(map[string]any); ok {
				out[stateKeyRuntimeLifecycle] = value
			}
		case namespacePlan:
			if value, ok := payload.(map[string]any); ok {
				out[stateKeyPlan] = value
			}
		case namespaceACP:
			if value, ok := payload.(map[string]any); ok {
				out[stateKeyACP] = value
			}
		case namespaceExternalParts:
			if value, ok := payload.([]any); ok {
				out[stateKeyExternalParticipants] = value
			}
		case namespaceReadBeforeWrite:
			if value, ok := payload.(map[string]any); ok {
				if readPaths, ok := value["read_paths"]; ok {
					out[stateKeyReadBeforeWriteReadPaths] = readPaths
				}
				if ready, ok := value["index_ready"]; ok {
					out[stateKeyReadBeforeWriteReady] = ready
				}
				if safeWrites, ok := value["safe_write_paths"]; ok {
					out[stateKeyReadBeforeWriteSafeWrite] = safeWrites
				}
			}
		case namespaceMisc:
			if value, ok := payload.(map[string]any); ok {
				for key, item := range value {
					out[key] = item
				}
			}
		}
	}
	return out, nil
}

func splitStateMap(values map[string]any) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	misc := map[string]any{}
	readBeforeWrite := map[string]any{}
	for key, value := range values {
		switch key {
		case stateKeySessionMode:
			raw, err := marshalJSON(value)
			if err != nil {
				return nil, err
			}
			out[namespaceSessionMode] = raw
		case stateKeyRuntimeLifecycle:
			raw, err := marshalJSON(value)
			if err != nil {
				return nil, err
			}
			out[namespaceRuntimeLifecycle] = raw
		case stateKeyPlan:
			raw, err := marshalJSON(value)
			if err != nil {
				return nil, err
			}
			out[namespacePlan] = raw
		case stateKeyACP:
			raw, err := marshalJSON(value)
			if err != nil {
				return nil, err
			}
			out[namespaceACP] = raw
		case stateKeyExternalParticipants:
			raw, err := marshalJSON(value)
			if err != nil {
				return nil, err
			}
			out[namespaceExternalParts] = raw
		case stateKeyReadBeforeWriteReadPaths:
			readBeforeWrite["read_paths"] = value
		case stateKeyReadBeforeWriteReady:
			readBeforeWrite["index_ready"] = value
		case stateKeyReadBeforeWriteSafeWrite:
			readBeforeWrite["safe_write_paths"] = value
		default:
			misc[key] = value
		}
	}
	if len(readBeforeWrite) > 0 {
		raw, err := marshalJSON(readBeforeWrite)
		if err != nil {
			return nil, err
		}
		out[namespaceReadBeforeWrite] = raw
	}
	if len(misc) > 0 {
		raw, err := marshalJSON(misc)
		if err != nil {
			return nil, err
		}
		out[namespaceMisc] = raw
	}
	return out, nil
}

func visibleUserMessage(ev *session.Event) string {
	if ev == nil {
		return ""
	}
	lastUser := sessionmode.VisibleText(strings.TrimSpace(ev.Message.TextContent()))
	contentParts := model.ContentPartsFromParts(ev.Message.Parts)
	if lastUser == "" && len(contentParts) > 0 {
		parts := make([]string, 0, len(contentParts))
		for _, part := range contentParts {
			if part.Type != model.ContentPartText {
				continue
			}
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			parts = append(parts, text)
		}
		lastUser = sessionmode.VisibleText(strings.Join(parts, "\n"))
	}
	return lastUser
}

func sessionHiddenForEvent(ev *session.Event, sessionID string) bool {
	if ev == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	meta, ok := runtime.DelegationMetadataFromEvent(ev)
	if !ok || strings.TrimSpace(meta.ParentSessionID) == "" {
		return false
	}
	return strings.TrimSpace(meta.ChildSessionID) == strings.TrimSpace(sessionID)
}

func unixMilli(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
