package main

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

	"github.com/OnslaughtSnail/caelis/internal/app/storage/localstore"
	"github.com/OnslaughtSnail/caelis/internal/sessionmode"
	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/runtime"
	"github.com/OnslaughtSnail/caelis/kernel/session"
	"github.com/OnslaughtSnail/caelis/pkg/idutil"
	_ "modernc.org/sqlite"
)

const (
	sessionIndexDriver = "sqlite"
	sessionIndexDSNOpt = "?_pragma=busy_timeout(3000)&_pragma=journal_mode(WAL)"
)

type sessionIndex struct {
	path   string
	db     *sql.DB
	ownsDB bool
	mu     sync.Mutex
}

func sessionIndexQueryContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

type sessionIndexRecord struct {
	SessionID       string
	AppName         string
	UserID          string
	WorkspaceCWD    string
	CreatedAt       time.Time
	LastEventAt     time.Time
	EventCount      int64
	LastUserMessage string
}

const sessionIndexVisibleFilter = "hidden = 0"

func newSessionIndex(path string) (*sessionIndex, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("session index: path is required")
	}
	if err := osMkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("session index: create dir: %w", err)
	}
	db, err := sql.Open(sessionIndexDriver, path+sessionIndexDSNOpt)
	if err != nil {
		return nil, fmt.Errorf("session index: open db: %w", err)
	}
	idx := &sessionIndex{path: path, db: db, ownsDB: true}
	if err := idx.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return idx, nil
}

func newSessionIndexWithDB(path string, db *sql.DB) (*sessionIndex, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("session index: path is required")
	}
	if db == nil {
		return nil, fmt.Errorf("session index: db is required")
	}
	idx := &sessionIndex{path: path, db: db}
	if err := idx.migrate(context.Background()); err != nil {
		return nil, err
	}
	return idx, nil
}

func (s *sessionIndex) Close() error {
	if s == nil || s.db == nil || !s.ownsDB {
		return nil
	}
	return s.db.Close()
}

func (s *sessionIndex) UpsertSession(workspace workspaceContext, appName, userID, sessionID string, at time.Time) error {
	return s.UpsertSessionContext(context.Background(), workspace, appName, userID, sessionID, at)
}

func (s *sessionIndex) UpsertSessionContext(ctx context.Context, workspace workspaceContext, appName, userID, sessionID string, at time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	ctx = sessionIndexQueryContext(ctx)
	if strings.TrimSpace(workspace.Key) == "" || strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session index: workspace and session_id are required")
	}
	if at.IsZero() {
		at = time.Now()
	}
	path := rolloutPathFallback(workspace, sessionID, at)
	ts := at.UnixMilli()
	const q = `
INSERT INTO sessions (
	scope, workspace_key, app_name, user_id, session_id, workspace_cwd, rollout_path,
	created_at, updated_at, last_event_at, event_count, last_user_message, hidden
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '', 0)
ON CONFLICT(scope, workspace_key, app_name, user_id, session_id) DO UPDATE SET
	workspace_cwd = excluded.workspace_cwd,
	app_name = excluded.app_name,
	user_id = excluded.user_id,
	updated_at = excluded.updated_at,
	last_event_at = CASE
		WHEN sessions.last_event_at > excluded.last_event_at THEN sessions.last_event_at
		ELSE excluded.last_event_at
	END`
	_, err := s.db.ExecContext(ctx, q,
		localstore.ScopeMain, workspace.Key, appName, userID, sessionID, workspace.CWD, path,
		ts, ts, ts,
	)
	return err
}

func (s *sessionIndex) TouchEvent(workspace workspaceContext, appName, userID, sessionID string, ev *session.Event, at time.Time) error {
	return s.TouchEventContext(context.Background(), workspace, appName, userID, sessionID, ev, at)
}

func (s *sessionIndex) TouchEventContext(ctx context.Context, workspace workspaceContext, appName, userID, sessionID string, ev *session.Event, at time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	ctx = sessionIndexQueryContext(ctx)
	if strings.TrimSpace(workspace.Key) == "" || strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session index: workspace and session_id are required")
	}
	if at.IsZero() {
		at = time.Now()
	}
	lastUser := ""
	if ev != nil && ev.Message.Role == model.RoleUser && !isCompactionEventForIndex(ev) {
		lastUser = sessionIndexLastUserMessage(ev)
	}
	ts := at.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.UpsertSessionContext(ctx, workspace, appName, userID, sessionID, at); err != nil {
		return err
	}
	const q = `
UPDATE sessions SET
	updated_at = ?,
	last_event_at = ?,
	event_count = event_count + 1,
	last_user_message = CASE
		WHEN ? <> '' THEN ?
		ELSE last_user_message
	END
WHERE scope = ? AND workspace_key = ? AND app_name = ? AND user_id = ? AND session_id = ?`
	if _, err := s.db.ExecContext(ctx, q,
		ts, ts, lastUser, lastUser, localstore.ScopeMain, workspace.Key, appName, userID, sessionID,
	); err != nil {
		return err
	}
	if sessionIndexHiddenForEvent(ev, sessionID) {
		return s.setSessionHiddenContext(ctx, workspace.Key, appName, userID, sessionID, true)
	}
	return nil
}

func isCompactionEventForIndex(ev *session.Event) bool {
	return session.EventTypeOf(ev) == session.EventTypeCompaction
}

func (s *sessionIndex) ListWorkspaceSessions(workspaceKey string, limit int) ([]sessionIndexRecord, error) {
	return s.ListWorkspaceSessionsPageContext(context.Background(), workspaceKey, 1, limit)
}

func (s *sessionIndex) ListWorkspaceSessionsPage(workspaceKey string, page int, pageSize int) ([]sessionIndexRecord, error) {
	return s.ListWorkspaceSessionsPageContext(context.Background(), workspaceKey, page, pageSize)
}

func (s *sessionIndex) ListWorkspaceSessionsPageContext(ctx context.Context, workspaceKey string, page int, pageSize int) ([]sessionIndexRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	ctx = sessionIndexQueryContext(ctx)
	workspaceKey = strings.TrimSpace(workspaceKey)
	if workspaceKey == "" {
		return nil, fmt.Errorf("session index: workspace_key is required")
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	const q = `
	SELECT session_id, app_name, user_id, workspace_cwd, created_at, last_event_at, event_count, last_user_message
	FROM sessions
	WHERE scope = ? AND workspace_key = ? AND ` + sessionIndexVisibleFilter + `
	ORDER BY last_event_at DESC, created_at DESC
	LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, q, localstore.ScopeMain, workspaceKey, pageSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]sessionIndexRecord, 0, pageSize)
	for rows.Next() {
		var rec sessionIndexRecord
		var createdAt, lastEventAt int64
		if err := rows.Scan(&rec.SessionID, &rec.AppName, &rec.UserID, &rec.WorkspaceCWD, &createdAt, &lastEventAt, &rec.EventCount, &rec.LastUserMessage); err != nil {
			return nil, err
		}
		rec.CreatedAt = time.UnixMilli(createdAt)
		rec.LastEventAt = time.UnixMilli(lastEventAt)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *sessionIndex) CountWorkspaceSessions(workspaceKey string) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	if workspaceKey == "" {
		return 0, fmt.Errorf("session index: workspace_key is required")
	}
	const q = `SELECT COUNT(*) FROM sessions WHERE scope = ? AND workspace_key = ? AND ` + sessionIndexVisibleFilter
	var count int
	if err := s.db.QueryRowContext(context.Background(), q, localstore.ScopeMain, workspaceKey).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *sessionIndex) MostRecentWorkspaceSession(workspaceKey string, excludeSessionID string) (sessionIndexRecord, bool, error) {
	return s.MostRecentWorkspaceSessionContext(context.Background(), workspaceKey, excludeSessionID)
}

func (s *sessionIndex) MostRecentWorkspaceSessionContext(ctx context.Context, workspaceKey string, excludeSessionID string) (sessionIndexRecord, bool, error) {
	if s == nil || s.db == nil {
		return sessionIndexRecord{}, false, nil
	}
	ctx = sessionIndexQueryContext(ctx)
	workspaceKey = strings.TrimSpace(workspaceKey)
	if workspaceKey == "" {
		return sessionIndexRecord{}, false, fmt.Errorf("session index: workspace_key is required")
	}
	excludeSessionID = strings.TrimSpace(excludeSessionID)
	const q = `
	SELECT session_id, app_name, user_id, workspace_cwd, created_at, last_event_at, event_count, last_user_message
	FROM sessions
	WHERE scope = ? AND workspace_key = ? AND (? = '' OR session_id <> ?) AND ` + sessionIndexVisibleFilter + `
	ORDER BY last_event_at DESC, created_at DESC
	LIMIT 1`
	var rec sessionIndexRecord
	var createdAt, lastEventAt int64
	if err := s.db.QueryRowContext(ctx, q, localstore.ScopeMain, workspaceKey, excludeSessionID, excludeSessionID).Scan(
		&rec.SessionID, &rec.AppName, &rec.UserID, &rec.WorkspaceCWD, &createdAt, &lastEventAt, &rec.EventCount, &rec.LastUserMessage,
	); err != nil {
		if err == sql.ErrNoRows {
			return sessionIndexRecord{}, false, nil
		}
		return sessionIndexRecord{}, false, err
	}
	rec.CreatedAt = time.UnixMilli(createdAt)
	rec.LastEventAt = time.UnixMilli(lastEventAt)
	return rec, true, nil
}

func (s *sessionIndex) HasWorkspaceSession(workspaceKey, sessionID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	sessionID = strings.TrimSpace(sessionID)
	if workspaceKey == "" || sessionID == "" {
		return false, fmt.Errorf("session index: workspace_key and session_id are required")
	}
	const q = `SELECT 1 FROM sessions WHERE scope = ? AND workspace_key = ? AND session_id = ? AND ` + sessionIndexVisibleFilter + ` LIMIT 1`
	var one int
	if err := s.db.QueryRowContext(context.Background(), q, localstore.ScopeMain, workspaceKey, sessionID).Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *sessionIndex) ResolveWorkspaceSessionID(workspaceKey, prefix string) (string, bool, error) {
	return s.ResolveWorkspaceSessionIDContext(context.Background(), workspaceKey, prefix)
}

func (s *sessionIndex) ResolveWorkspaceSessionIDContext(ctx context.Context, workspaceKey, prefix string) (string, bool, error) {
	if s == nil || s.db == nil {
		return "", false, nil
	}
	ctx = sessionIndexQueryContext(ctx)
	workspaceKey = strings.TrimSpace(workspaceKey)
	prefix = strings.TrimSpace(prefix)
	if workspaceKey == "" || prefix == "" {
		return "", false, fmt.Errorf("session index: workspace_key and session_id are required")
	}
	const q = `
	SELECT session_id
	FROM sessions
	WHERE scope = ? AND workspace_key = ? AND session_id LIKE ? AND ` + sessionIndexVisibleFilter + `
	ORDER BY last_event_at DESC, created_at DESC
	LIMIT 3`
	rows, err := s.db.QueryContext(ctx, q, localstore.ScopeMain, workspaceKey, prefix+"%")
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

func (s *sessionIndex) SyncWorkspaceFromStoreDir(workspace workspaceContext, appName, userID, storeDir string) error {
	if s == nil {
		return nil
	}
	storeDir = strings.TrimSpace(storeDir)
	if storeDir == "" {
		return nil
	}
	if _, err := os.Stat(storeDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(storeDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != storeDir && entry.Name() == localstore.ScopeACPRemote {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		switch {
		case name == "events.jsonl":
			sessionID := strings.TrimSpace(filepath.Base(filepath.Dir(path)))
			snapshot, err := readLegacySessionIndexSnapshot(path, sessionID)
			if err != nil {
				return err
			}
			return s.upsertSnapshot(workspace, appName, userID, sessionID, firstNonZeroTime(snapshot.LastEventAt, entryModTime(path)), snapshot)
		case strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl"):
			meta, snapshot, err := readRolloutIndexSnapshot(path)
			if err != nil {
				return err
			}
			if meta == nil || strings.TrimSpace(meta.SessionID) == "" {
				return nil
			}
			return s.upsertSnapshot(workspace,
				firstNonEmptyString(meta.AppName, appName),
				firstNonEmptyString(meta.UserID, userID),
				meta.SessionID,
				firstNonZeroTime(snapshot.LastEventAt, entryModTime(path)),
				snapshot,
			)
		default:
			return nil
		}
	})
}

func sessionIndexLastUserMessage(ev *session.Event) string {
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

func sessionIndexPreview(rec sessionIndexRecord, limit int) string {
	prompt := strings.TrimSpace(rec.LastUserMessage)
	if prompt == "" {
		return idutil.ShortDisplay(strings.TrimSpace(rec.SessionID))
	}
	prompt = strings.ReplaceAll(prompt, "\n", " ")
	return truncateInline(prompt, limit)
}

func (s *sessionIndex) migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	ctx = sessionIndexQueryContext(ctx)
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
ON sessions(scope, workspace_key, hidden, last_event_at DESC, created_at DESC);`
	_, err := s.db.ExecContext(ctx, ddl)
	if err != nil {
		return fmt.Errorf("session index: migrate: %w", err)
	}
	return nil
}

func (s *sessionIndex) setSessionHidden(workspaceKey, appName, userID, sessionID string, hidden bool) error {
	return s.setSessionHiddenContext(context.Background(), workspaceKey, appName, userID, sessionID, hidden)
}

func (s *sessionIndex) setSessionHiddenContext(ctx context.Context, workspaceKey, appName, userID, sessionID string, hidden bool) error {
	if s == nil || s.db == nil {
		return nil
	}
	ctx = sessionIndexQueryContext(ctx)
	workspaceKey = strings.TrimSpace(workspaceKey)
	appName = strings.TrimSpace(appName)
	userID = strings.TrimSpace(userID)
	sessionID = strings.TrimSpace(sessionID)
	if workspaceKey == "" || sessionID == "" {
		return nil
	}
	hiddenValue := 0
	if hidden {
		hiddenValue = 1
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET hidden = ? WHERE scope = ? AND workspace_key = ? AND app_name = ? AND user_id = ? AND session_id = ?`,
		hiddenValue, localstore.ScopeMain, workspaceKey, appName, userID, sessionID,
	)
	return err
}

func sessionIndexHiddenForEvent(ev *session.Event, sessionID string) bool {
	if ev == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	meta, ok := runtime.DelegationMetadataFromEvent(ev)
	if !ok || strings.TrimSpace(meta.ParentSessionID) == "" {
		return false
	}
	return strings.TrimSpace(meta.ChildSessionID) == strings.TrimSpace(sessionID)
}

func (s *sessionIndex) DeleteWorkspaceSession(workspaceKey, sessionID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	workspaceKey = strings.TrimSpace(workspaceKey)
	sessionID = strings.TrimSpace(sessionID)
	if workspaceKey == "" || sessionID == "" {
		return nil
	}
	_, err := s.db.ExecContext(context.Background(),
		`DELETE FROM sessions WHERE scope = ? AND workspace_key = ? AND session_id = ?`,
		localstore.ScopeMain, workspaceKey, sessionID,
	)
	return err
}

func rolloutPathFallback(workspace workspaceContext, sessionID string, at time.Time) string {
	if at.IsZero() {
		at = time.Now()
	}
	return filepath.Join(
		".",
		strings.TrimSpace(workspace.Key),
		at.UTC().Format("2006"),
		at.UTC().Format("01"),
		at.UTC().Format("02"),
		fmt.Sprintf("rollout-%s-%s.jsonl", at.UTC().Format("2006-01-02T15-04-05"), strings.TrimSpace(sessionID)),
	)
}

var osMkdirAll = os.MkdirAll

func (s *sessionIndex) upsertSnapshot(workspace workspaceContext, appName, userID, sessionID string, at time.Time, snapshot sessionIndexSnapshot) error {
	if err := s.UpsertSession(workspace, appName, userID, sessionID, at); err != nil {
		return err
	}
	if err := s.applySessionSnapshot(workspace, appName, userID, sessionID, snapshot); err != nil {
		return err
	}
	return s.setSessionHidden(workspace.Key, appName, userID, sessionID, snapshot.Hidden)
}

type sessionIndexSnapshot struct {
	LastEventAt     time.Time
	EventCount      int64
	LastUserMessage string
	Hidden          bool
}

func readLegacySessionIndexSnapshot(eventsPath, sessionID string) (sessionIndexSnapshot, error) {
	f, err := os.Open(eventsPath)
	if err != nil {
		return sessionIndexSnapshot{}, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var snapshot sessionIndexSnapshot
	for {
		var ev session.Event
		if err := dec.Decode(&ev); err != nil {
			if err == io.EOF {
				break
			}
			return sessionIndexSnapshot{}, err
		}
		snapshot.EventCount++
		if !ev.Time.IsZero() && ev.Time.After(snapshot.LastEventAt) {
			snapshot.LastEventAt = ev.Time
		}
		if ev.Message.Role == model.RoleUser && !isCompactionEventForIndex(&ev) {
			if text := sessionIndexLastUserMessage(&ev); text != "" {
				snapshot.LastUserMessage = text
			}
		}
		if sessionIndexHiddenForEvent(&ev, sessionID) {
			snapshot.Hidden = true
		}
	}
	return snapshot, nil
}

type indexedRolloutLine struct {
	Type    string                 `json:"type"`
	Session *indexedRolloutSession `json:"session,omitempty"`
	Event   *session.Event         `json:"event,omitempty"`
}

type indexedRolloutSession struct {
	AppName   string `json:"app_name"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
}

func readRolloutIndexSnapshot(path string) (*indexedRolloutSession, sessionIndexSnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, sessionIndexSnapshot{}, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	var (
		meta     *indexedRolloutSession
		snapshot sessionIndexSnapshot
	)
	for {
		var line indexedRolloutLine
		if err := dec.Decode(&line); err != nil {
			if err == io.EOF {
				break
			}
			return nil, sessionIndexSnapshot{}, err
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
			if line.Event.Message.Role == model.RoleUser && !isCompactionEventForIndex(line.Event) {
				if text := sessionIndexLastUserMessage(line.Event); text != "" {
					snapshot.LastUserMessage = text
				}
			}
			if meta != nil && sessionIndexHiddenForEvent(line.Event, meta.SessionID) {
				snapshot.Hidden = true
			}
		}
	}
	return meta, snapshot, nil
}

func (s *sessionIndex) applySessionSnapshot(workspace workspaceContext, appName, userID, sessionID string, snapshot sessionIndexSnapshot) error {
	if s == nil || s.db == nil {
		return nil
	}
	ts := snapshot.LastEventAt.UnixMilli()
	if snapshot.LastEventAt.IsZero() {
		ts = time.Now().UnixMilli()
	}
	const q = `
UPDATE sessions SET
	last_event_at = ?,
	updated_at = ?,
	event_count = CASE WHEN ? > 0 THEN ? ELSE event_count END,
	last_user_message = CASE WHEN ? <> '' THEN ? ELSE last_user_message END
WHERE scope = ? AND workspace_key = ? AND app_name = ? AND user_id = ? AND session_id = ?`
	_, err := s.db.ExecContext(context.Background(), q,
		ts, ts, snapshot.EventCount, snapshot.EventCount, snapshot.LastUserMessage, snapshot.LastUserMessage,
		localstore.ScopeMain, workspace.Key, appName, userID, sessionID,
	)
	return err
}

func entryModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
